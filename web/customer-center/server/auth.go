package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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
		if err := s.issueAndSetSession(w, slug, email, s.devJWTSecret); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"email": email, "setup": "false"})
		return
	}

	jwtSecret, err := s.readJWTSecret(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "center not provisioned"})
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
	http.SetCookie(w, auth.MakeClearCookie())
	w.WriteHeader(http.StatusNoContent)
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

	// Not authenticated. Tell the frontend whether this is the initial setup case.
	needsSetup := false
	if s.core != nil {
		users, _, err := s.readUsersSecret(r.Context(), slug)
		if err == nil && len(users) == 0 {
			needsSetup = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false, "needsSetup": needsSetup})
}

// issueAndSetSession is a thin wrapper that signs a JWT cookie via pkg/auth and writes it.
func (s *server) issueAndSetSession(w http.ResponseWriter, slug, email string, secret []byte) error {
	cookie, err := auth.IssueSession(slug, email, secret, time.Now())
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
	return auth.Authenticate(c.Value, slug, secret, time.Now())
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
