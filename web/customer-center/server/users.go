package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// usersSecretKey is the key inside kai-<slug>-users where the JSON list lives.
const usersSecretKey = "users.json"

// userRecord is the on-disk shape stored in the per-customer Secret.
// passwordHash is the encoded argon2id PHC string.
type userRecord struct {
	Email              string `json:"email"`
	PasswordHash       string `json:"passwordHash"`
	CreatedAt          string `json:"createdAt"`
	PasswordUpdatedAt  string `json:"passwordUpdatedAt"`
}

// userPublic is what we expose over the API — no hash, ever.
type userPublic struct {
	Email             string `json:"email"`
	CreatedAt         string `json:"createdAt"`
	PasswordUpdatedAt string `json:"passwordUpdatedAt"`
}

// requireCenterAuth validates the JWT cookie. Returns true if the request is
// authenticated for this slug; on failure it has already written a 401.
func (s *server) requireCenterAuth(w http.ResponseWriter, r *http.Request, slug string) bool {
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return false
	}
	if s.demoMode {
		return true
	}
	if _, ok := s.authedClaims(r, slug); !ok {
		writeUnauthorized(w)
		return false
	}
	return true
}

// listUsers returns the public email list for a customer.
func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !s.requireCenterAuth(w, r, slug) {
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusOK, []userPublic{
			{Email: "anna.schmidt@acme.de", CreatedAt: time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339), PasswordUpdatedAt: time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)},
			{Email: "tobias.weber@acme.de", CreatedAt: time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339), PasswordUpdatedAt: time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)},
		})
		return
	}
	users, _, err := s.readUsersSecret(r.Context(), slug)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read failed"})
		return
	}
	out := make([]userPublic, 0, len(users))
	for _, u := range users {
		out = append(out, userPublic{Email: u.Email, CreatedAt: u.CreatedAt, PasswordUpdatedAt: u.PasswordUpdatedAt})
	}
	writeJSON(w, http.StatusOK, out)
}

// addUser inserts a new user with an initial password. The hash is computed here.
// Refuses to overwrite an existing email — use the password-reset endpoint for that.
func (s *server) addUser(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !s.requireCenterAuth(w, r, slug) {
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
	if !validEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if !validPassword(body.Password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusCreated, userPublic{Email: email, CreatedAt: time.Now().UTC().Format(time.RFC3339), PasswordUpdatedAt: time.Now().UTC().Format(time.RFC3339)})
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash failed"})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = s.updateUsersSecret(r.Context(), slug, func(users []userRecord) ([]userRecord, error) {
		for _, u := range users {
			if strings.EqualFold(u.Email, email) {
				return nil, errAlreadyExists
			}
		}
		return append(users, userRecord{
			Email:             email,
			PasswordHash:      hash,
			CreatedAt:         now,
			PasswordUpdatedAt: now,
		}), nil
	})
	switch {
	case errors.Is(err, errAlreadyExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email already exists"})
		return
	case errors.Is(err, errSecretMissing):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "users secret not yet provisioned"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	writeJSON(w, http.StatusCreated, userPublic{Email: email, CreatedAt: now, PasswordUpdatedAt: now})
}

// removeUser deletes an email entry. Idempotent: 204 even if it didn't exist.
func (s *server) removeUser(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	email := strings.TrimSpace(strings.ToLower(r.PathValue("email")))
	if !s.requireCenterAuth(w, r, slug) {
		return
	}
	if !validEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if s.demoMode {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	err := s.updateUsersSecret(r.Context(), slug, func(users []userRecord) ([]userRecord, error) {
		out := users[:0]
		for _, u := range users {
			if !strings.EqualFold(u.Email, email) {
				out = append(out, u)
			}
		}
		return out, nil
	})
	if errors.Is(err, errSecretMissing) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "users secret not yet provisioned"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resetPassword overwrites the password for an existing user.
func (s *server) resetPassword(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	email := strings.TrimSpace(strings.ToLower(r.PathValue("email")))
	if !s.requireCenterAuth(w, r, slug) {
		return
	}
	if !validEmail(email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if !validPassword(body.Password) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
		return
	}
	if s.demoMode {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	hash, err := hashPassword(body.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash failed"})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = s.updateUsersSecret(r.Context(), slug, func(users []userRecord) ([]userRecord, error) {
		found := false
		for i := range users {
			if strings.EqualFold(users[i].Email, email) {
				users[i].PasswordHash = hash
				users[i].PasswordUpdatedAt = now
				found = true
				break
			}
		}
		if !found {
			return nil, errNotFound
		}
		return users, nil
	})
	switch {
	case errors.Is(err, errNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	case errors.Is(err, errSecretMissing):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "users secret not yet provisioned"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "write failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var (
	errAlreadyExists = errors.New("user already exists")
	errNotFound      = errors.New("user not found")
	errSecretMissing = errors.New("users secret missing")
)

// readUsersSecret returns the parsed list and the live Secret (for resourceVersion-based update).
func (s *server) readUsersSecret(ctx context.Context, slug string) ([]userRecord, *corev1.Secret, error) {
	if s.core == nil {
		return nil, nil, errSecretMissing
	}
	sec, err := s.core.CoreV1().Secrets(s.namespace).Get(ctx, "kai-"+slug+"-users", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, errSecretMissing
		}
		return nil, nil, err
	}
	raw := sec.Data[usersSecretKey]
	if len(raw) == 0 {
		return nil, sec, nil
	}
	var users []userRecord
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, sec, fmt.Errorf("parse users.json: %w", err)
	}
	return users, sec, nil
}

// updateUsersSecret reads, mutates via fn, and writes back. Uses resourceVersion to detect
// concurrent writes and returns the underlying error if the K8s API rejects.
func (s *server) updateUsersSecret(ctx context.Context, slug string, fn func([]userRecord) ([]userRecord, error)) error {
	users, sec, err := s.readUsersSecret(ctx, slug)
	if err != nil {
		return err
	}
	updated, err := fn(users)
	if err != nil {
		return err
	}
	if updated == nil {
		updated = []userRecord{}
	}
	encoded, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}
	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	sec.Data[usersSecretKey] = encoded
	_, err = s.core.CoreV1().Secrets(s.namespace).Update(ctx, sec, metav1.UpdateOptions{})
	return err
}

// hashPassword runs argon2id with conservative parameters (~64 MiB, 3 iterations, 4 lanes).
// Output is the standard PHC string $argon2id$v=19$m=...$<salt>$<hash>.
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const (
		memory  uint32 = 64 * 1024
		time_   uint32 = 3
		threads uint8  = 4
		keyLen  uint32 = 32
	)
	hash := argon2.IDKey([]byte(password), salt, time_, memory, threads, keyLen)
	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory, time_, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

func validEmail(s string) bool {
	if s == "" || len(s) > 254 {
		return false
	}
	if _, err := mail.ParseAddress(s); err != nil {
		return false
	}
	return strings.Contains(s, "@")
}

func validPassword(s string) bool {
	return len(s) >= 8 && len(s) <= 1024
}

