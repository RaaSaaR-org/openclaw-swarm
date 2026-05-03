// Package auth holds the JWT + argon2id primitives shared by every EmAI
// swarm web server that issues a session cookie. The public API is small on
// purpose: pure data in/out, no *http.Request leaks. Callers wire the
// returned cookie / parsed claims into their own handlers.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Cookie + TTL chosen so customer-chat and customer-center can share an origin
// and a single login. Slug is part of the JWT claim and validated per-request.
const (
	SessionCookieName = "kai-session"
	SessionTTL        = 24 * time.Hour
)

// SessionClaims is the minimal JWT payload we issue. Sub is the user email,
// Slug is the customer slug the session belongs to, Iat/Exp are unix seconds.
type SessionClaims struct {
	Sub  string `json:"sub"`
	Slug string `json:"slug"`
	Exp  int64  `json:"exp"`
	Iat  int64  `json:"iat"`
}

// Argon2id parameters tuned for ~64 MiB working set per call. Bumping these
// is a deployment concern: the per-pod memory limit must allow the working
// set per concurrent login.
const (
	argonMemory  uint32 = 64 * 1024
	argonTime    uint32 = 3
	argonThreads uint8  = 4
	argonKeyLen  uint32 = 32
)

// HashPassword returns a $argon2id$… PHC string. Never log the result.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyArgon2id parses a $argon2id$… PHC string and checks the candidate
// password in constant time. Returns false on any decode / mismatch error so
// the caller can't accidentally treat malformed input as a success.
func VerifyArgon2id(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, time_ uint32
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

// SignJWT produces an HS256 JWT (header.payload.signature) signed with secret.
// No external JWT library — the format is small, the dep surface stays smaller.
func SignJWT(c SessionClaims, secret []byte) (string, error) {
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
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

// ParseJWT validates HMAC-SHA256 against secret and returns decoded claims.
// Does NOT check expiry / slug — that's the caller's job via Authenticate.
func ParseJWT(token string, secret []byte) (*SessionClaims, error) {
	parts := strings.Split(token, ".")
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
	var c SessionClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// IssueSession builds claims, signs the JWT, and returns the cookie struct.
// Caller does http.SetCookie(w, …). `now` is parameterized so tests can pin time.
func IssueSession(slug, email string, secret []byte, now time.Time) (*http.Cookie, error) {
	claims := SessionClaims{
		Sub:  email,
		Slug: slug,
		Iat:  now.UTC().Unix(),
		Exp:  now.UTC().Add(SessionTTL).Unix(),
	}
	tok, err := SignJWT(claims, secret)
	if err != nil {
		return nil, err
	}
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}, nil
}

// MakeClearCookie returns the cookie that terminates a session (used by /logout).
func MakeClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
}

// Authenticate validates the JWT signature, expiry, and slug binding in one
// step. Returns claims on success; (nil, false) on any failure. Does not log.
func Authenticate(token, slug string, secret []byte, now time.Time) (*SessionClaims, bool) {
	if token == "" || len(secret) == 0 {
		return nil, false
	}
	claims, err := ParseJWT(token, secret)
	if err != nil {
		return nil, false
	}
	if claims.Slug != slug {
		return nil, false
	}
	if now.UTC().Unix() >= claims.Exp {
		return nil, false
	}
	return claims, true
}
