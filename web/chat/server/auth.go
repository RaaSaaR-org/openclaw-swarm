package main

import (
	"context"
	"encoding/base64"
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

// handleLogin verifies email+password, issues a session cookie.
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
		writeJSON(w, http.StatusOK, map[string]string{"email": email})
		return
	}

	users, err := s.readUsers(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}
	jwtSecret, err := s.readJWTSecret(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat not provisioned"})
		return
	}

	var match *userRecord
	for i := range users {
		if strings.EqualFold(users[i].Email, email) {
			match = &users[i]
			break
		}
	}
	if match == nil {
		// Constant-time-ish: still hash a dummy password to mask timing differences.
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
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
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

func (s *server) handleMe(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	claims, ok := s.authedClaims(r, slug)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not logged in"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": claims.Sub, "slug": claims.Slug})
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

// authedClaims validates the session cookie and returns claims if it matches the slug.
// In dev-auth mode the per-startup ephemeral secret is used; otherwise the per-customer
// chat-bridge JWT secret is read from K8s.
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
