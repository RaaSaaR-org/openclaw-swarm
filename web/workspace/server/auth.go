package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emai-ai/swarm/pkg/auth"
)

// handleLogin verifies email+password against the per-customer users Secret and
// issues a session cookie. Bootstrap path: when the users list is empty, the
// caller's submission becomes the first admin user (one-time, no auth required).
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	email := strings.TrimSpace(strings.ToLower(body.Email))
	if email == "" || body.Password == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}

	if s.demoMode {
		log.Printf("WARN [insecure-dev-auth] login bypass slug=%s email=%s remote=%s", slug, email, r.RemoteAddr)
		if err := s.issueAndSetSessionWithUID(w, slug, email, "u_demo", s.devJWTSecret); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"email": email, "setup": "false"})
		return
	}

	// Branch on KaiInstance.spec.managed. SaaS-managed workspaces authenticate
	// against the central users.Store; legacy internal-managed (and pre-TASK-014
	// tenants without the field set) fall through to the per-tenant Secret +
	// bootstrap-admin flow that has shipped since v0.
	binding, err := s.loadKaiBinding(r.Context(), slug)
	if err != nil {
		if errors.Is(err, errKaiNotFound) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
			return
		}
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "center not provisioned"})
		return
	}

	jwtSecret, err := s.readJWTSecret(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "center not provisioned"})
		return
	}

	if binding.IsSaaS() {
		u, err := s.loginSaaS(r.Context(), slug, email, body.Password, binding)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
			return
		}
		// TASK-015 Phase 3.B: a successful login on a suspended free-tier
		// workspace flips spec.suspended=false so the operator can scale the
		// Deployment back to 1. Best-effort — login still succeeds even if the
		// patch fails; the user retries / asks support in the rare failure
		// case. Cookie is issued either way so the SPA can render and a
		// follow-up request will pick up the resumed pod within ~10s.
		if binding.Suspended {
			if err := s.resumeWorkspace(r.Context(), slug); err != nil {
				log.Printf("login: resume suspended workspace slug=%s user=%s: %v", slug, u.ID, err)
			} else {
				log.Printf("login: resumed suspended workspace slug=%s user=%s", slug, u.ID)
			}
		}
		if err := s.issueAndSetSessionWithUID(w, slug, u.Email, u.ID, jwtSecret); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"email": u.Email, "setup": "false"})
		return
	}

	users, _, err := s.readUsersSecret(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "users secret unavailable"})
		return
	}

	// Bootstrap: empty list means initial setup. The submitter becomes the first user.
	if len(users) == 0 {
		if !validPassword(body.Password) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
			return
		}
		hash, err := auth.HashPassword(body.Password)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash failed"})
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		err = s.updateUsersSecret(r.Context(), slug, func(existing []userRecord) ([]userRecord, error) {
			if len(existing) > 0 {
				// Race: someone added a user between our read and write. Refuse and let
				// the client retry through the regular login path.
				return nil, errAlreadyExists
			}
			return []userRecord{{
				Email:             email,
				PasswordHash:      hash,
				CreatedAt:         now,
				PasswordUpdatedAt: now,
			}}, nil
		})
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "another admin was just created — please retry sign-in"})
			return
		}
		if err := s.issueAndSetSession(w, slug, email, jwtSecret); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"email": email, "setup": "true"})
		return
	}

	// Normal login path.
	var match *userRecord
	for i := range users {
		if strings.EqualFold(users[i].Email, email) {
			match = &users[i]
			break
		}
	}
	if match == nil {
		// Constant-time-ish: still hash a dummy to mask timing differences.
		_ = auth.VerifyArgon2id(body.Password, "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}
	if !auth.VerifyArgon2id(body.Password, match.PasswordHash) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}

	if err := s.issueAndSetSession(w, slug, email, jwtSecret); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email, "setup": "false"})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Best-effort server-side revocation: parse the cookie's claims (without
	// requiring a valid signature — the caller is leaving the session anyway),
	// record the jti so concurrent reuse of a stolen cookie is shut down before
	// natural expiry. Failures are logged but do not block the client logout.
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" && s.revoker != nil {
		if claims := unsafeParseClaims(c.Value); claims != nil && claims.Jti != "" {
			exp := time.Unix(claims.Exp, 0)
			if err := s.revoker.Revoke(r.Context(), claims.Slug, claims.Jti, exp); err != nil {
				log.Printf("revoke jti=%s slug=%s: %v", claims.Jti, claims.Slug, err)
			}
		}
	}
	http.SetCookie(w, auth.MakeClearCookie())
	w.WriteHeader(http.StatusNoContent)
}

// unsafeParseClaims decodes a JWT payload without verifying the signature.
// Only used at logout: the caller is throwing the session away, and we just
// want the slug + jti to populate the revocation list. Never trust the result
// for authorization.
func unsafeParseClaims(token string) *auth.SessionClaims {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var c auth.SessionClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil
	}
	return &c
}

// handleAuthInfo reports auth state to the frontend. Always 200 — the body tells
// the SPA whether to render the login form, the setup form, or the hub.
func (s *server) handleAuthInfo(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "needsSetup": false})
		return
	}

	if s.demoMode {
		claims, ok := s.authedClaims(r, slug)
		if ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"authenticated": true,
				"email":         claims.Sub,
				"slug":          claims.Slug,
				"needsSetup":    false,
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "needsSetup": false})
		return
	}

	claims, ok := s.authedClaims(r, slug)
	if ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": true,
			"email":         claims.Sub,
			"slug":          claims.Slug,
			"needsSetup":    false,
		})
		return
	}

	// Not authenticated. The frontend wants to know whether to render the
	// bootstrap-admin form. That only applies to internal-managed tenants —
	// SaaS users sign up via the onboarding flow (TASK-013), so for them
	// needsSetup is always false even if no per-tenant Secret exists.
	needsSetup := false
	if s.core != nil {
		binding, bErr := s.loadKaiBinding(r.Context(), slug)
		if bErr == nil && !binding.IsSaaS() {
			users, _, err := s.readUsersSecret(r.Context(), slug)
			if err == nil && len(users) == 0 {
				needsSetup = true
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "needsSetup": needsSetup})
}

// handleForwardAuth is the upstream for Traefik's forwardAuth middleware. It
// verifies the workspace session cookie against the requested slug and either
// allows the original request through (204 + X-Auth-Email) or redirects the
// browser to the login page. Used to gate the read-only `mc serve` dashboards
// at /hq/<slug> with the same login customers already use for /workspace/<slug>.
func (s *server) handleForwardAuth(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		http.Error(w, "invalid slug", http.StatusBadRequest)
		return
	}

	if claims, ok := s.authedClaims(r, slug); ok {
		w.Header().Set("X-Auth-Email", claims.Sub)
		if claims.Uid != "" {
			w.Header().Set("X-Auth-Uid", claims.Uid)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Not authenticated. Redirect the browser to the workspace login. Traefik
	// forwards the auth-server response body to the client, so the redirect
	// reaches the user. We pass the original URI as `?return=` so the SPA can
	// optionally bounce back after login.
	loginPath := "/workspace/" + slug + "/"
	if returnURI := r.Header.Get("X-Forwarded-Uri"); returnURI != "" {
		loginPath += "?return=" + url.QueryEscape(returnURI)
	}
	http.Redirect(w, r, loginPath, http.StatusFound)
}

// issueAndSetSession is a thin wrapper that signs a JWT cookie via pkg/auth and writes it.
func (s *server) issueAndSetSession(w http.ResponseWriter, slug, email string, secret []byte) error {
	return s.issueAndSetSessionWithUID(w, slug, email, "", secret)
}

// issueAndSetSessionWithUID is the SaaS variant — same as issueAndSetSession
// but stamps the platform User ID onto the JWT claims so downstream handlers
// (and the /owner endpoint) can read it without re-querying the user store.
func (s *server) issueAndSetSessionWithUID(w http.ResponseWriter, slug, email, uid string, secret []byte) error {
	cookie, err := auth.IssueSessionWithUID(slug, email, uid, secret, time.Now())
	if err != nil {
		return err
	}
	http.SetCookie(w, cookie)
	return nil
}

func (s *server) authedClaims(r *http.Request, slug string) (*auth.SessionClaims, bool) {
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	secret, err := s.resolveJWTSecret(r.Context(), slug)
	if err != nil {
		return nil, false
	}
	claims, ok := auth.Authenticate(c.Value, slug, secret, time.Now())
	if !ok {
		return nil, false
	}
	if s.revoker != nil {
		revoked, err := s.revoker.IsRevoked(r.Context(), slug, claims.Jti)
		if err != nil {
			log.Printf("revocation check jti=%s slug=%s: %v", claims.Jti, slug, err)
			return nil, false
		}
		if revoked {
			return nil, false
		}
	}
	return claims, true
}

func (s *server) resolveJWTSecret(ctx context.Context, slug string) ([]byte, error) {
	if s.demoMode {
		return s.devJWTSecret, nil
	}
	return s.readJWTSecret(ctx, slug)
}

func (s *server) readJWTSecret(ctx context.Context, slug string) ([]byte, error) {
	if s.core == nil {
		return nil, errors.New("no kube client")
	}
	sec, err := s.core.CoreV1().Secrets(s.namespace).Get(ctx, "kai-"+slug+"-chat-bridge", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("chat-bridge secret missing for %s", slug)
		}
		return nil, err
	}
	jwt, ok := sec.Data["jwt-secret"]
	if !ok || len(jwt) == 0 {
		return nil, errors.New("jwt-secret missing in chat-bridge secret")
	}
	return jwt, nil
}
