package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Shared with customer-chat — see web/customer-chat/server/auth.go for the rationale.
const sessionCookieName = "kai-session"
const sessionTTL = 24 * time.Hour

type sessionClaims struct {
	Sub  string `json:"sub"`  // email
	Slug string `json:"slug"` // customer slug
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
}

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
		if err := s.issueSession(w, slug, email, s.devJWTSecret); err != nil {
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
		hash, err := hashPassword(body.Password)
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
		if err := s.issueSession(w, slug, email, jwtSecret); err != nil {
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
		_ = verifyArgon2id(body.Password, "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}
	if !verifyArgon2id(body.Password, match.PasswordHash) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid login"})
		return
	}

	if err := s.issueSession(w, slug, email, jwtSecret); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email, "setup": "false"})
}

func (s *server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
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

func (s *server) issueSession(w http.ResponseWriter, slug, email string, secret []byte) error {
	now := time.Now().UTC()
	claims := sessionClaims{
		Sub:  email,
		Slug: slug,
		Iat:  now.Unix(),
		Exp:  now.Add(sessionTTL).Unix(),
	}
	tok, err := signJWT(claims, secret)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *server) authedClaims(r *http.Request, slug string) (*sessionClaims, bool) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}

	var secret []byte
	if s.demoMode {
		secret = s.devJWTSecret
	} else {
		var err error
		secret, err = s.readJWTSecret(r.Context(), slug)
		if err != nil {
			return nil, false
		}
	}

	claims, err := parseJWT(c.Value, secret)
	if err != nil {
		return nil, false
	}
	if claims.Slug != slug {
		return nil, false
	}
	if time.Now().Unix() >= claims.Exp {
		return nil, false
	}
	return claims, true
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

// ---------- argon2id verify (mirrors customer-chat) ----------

func verifyArgon2id(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var time_ uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time_, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time_, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---------- minimal HS256 JWT (no external dep) ----------

func signJWT(c sessionClaims, secret []byte) (string, error) {
	header := `{"alg":"HS256","typ":"JWT"}`
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	h := base64.RawURLEncoding.EncodeToString([]byte(header))
	p := base64.RawURLEncoding.EncodeToString(payload)
	signing := h + "." + p
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signing + "." + sig, nil
}

func parseJWT(tok string, secret []byte) (*sessionClaims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, errors.New("bad jwt")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) != 1 {
		return nil, errors.New("bad signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var c sessionClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
