package auth

import (
	"crypto/rand"
	"net/http"
	"strings"
	"testing"
	"time"
)

func mustSecret(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return b
}

func TestHashAndVerifyArgon2idRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, password string
	}{
		{"ascii", "correct horse battery staple"},
		{"unicode", "p@$$wörd münchen"},
		{"long", strings.Repeat("a", 200)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h, err := HashPassword(c.password)
			if err != nil {
				t.Fatalf("hash: %v", err)
			}
			if !strings.HasPrefix(h, "$argon2id$v=19$") {
				t.Fatalf("unexpected PHC: %q", h)
			}
			if !VerifyArgon2id(c.password, h) {
				t.Fatal("VerifyArgon2id should accept the correct password")
			}
			if VerifyArgon2id(c.password+"x", h) {
				t.Fatal("VerifyArgon2id must reject the wrong password")
			}
		})
	}
}

func TestVerifyArgon2idRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	bad := []string{
		"",
		"plain text",
		"$argon2i$v=19$m=64$AAAA$AAAA",                      // wrong algorithm
		"$argon2id$v=99$m=64,t=3,p=4$AAAA$AAAA",             // wrong version
		"$argon2id$v=19$nope$AAAA$AAAA",                     // bad params
		"$argon2id$v=19$m=64,t=3,p=4$!!!$AAAA",              // bad salt b64
		"$argon2id$v=19$m=64,t=3,p=4$AAAA$!!!",              // bad hash b64
	}
	for _, enc := range bad {
		if VerifyArgon2id("anything", enc) {
			t.Errorf("expected reject for %q", enc)
		}
	}
}

func TestSignAndParseJWTRoundTrip(t *testing.T) {
	t.Parallel()
	secret := mustSecret(t)
	now := time.Unix(1_700_000_000, 0)
	claims := SessionClaims{
		Sub:  "alice@example.com",
		Slug: "acme",
		Iat:  now.Unix(),
		Exp:  now.Add(time.Hour).Unix(),
	}
	tok, err := SignJWT(claims, secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if strings.Count(tok, ".") != 2 {
		t.Fatalf("not a JWT: %q", tok)
	}
	got, err := ParseJWT(tok, secret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *got != claims {
		t.Fatalf("roundtrip mismatch: got %+v want %+v", *got, claims)
	}
}

func TestParseJWTRejectsBadSignature(t *testing.T) {
	t.Parallel()
	secret := mustSecret(t)
	other := mustSecret(t)
	tok, err := SignJWT(SessionClaims{Sub: "a", Slug: "s", Exp: 1}, secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := ParseJWT(tok, other); err == nil {
		t.Fatal("ParseJWT must reject a token signed with a different secret")
	}
	if _, err := ParseJWT("not.a.jwt", secret); err == nil {
		t.Fatal("ParseJWT must reject a malformed token")
	}
}

func TestAuthenticateChecksSlugAndExpiry(t *testing.T) {
	t.Parallel()
	secret := mustSecret(t)
	now := time.Unix(1_700_000_000, 0)
	cookie, err := IssueSession("acme", "alice@example.com", secret, now)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if cookie.Name != SessionCookieName {
		t.Fatalf("wrong cookie name: %q", cookie.Name)
	}
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatal("session cookie must be HttpOnly + Secure")
	}

	if _, ok := Authenticate(cookie.Value, "acme", secret, now); !ok {
		t.Fatal("happy path should authenticate")
	}
	if _, ok := Authenticate(cookie.Value, "betaco", secret, now); ok {
		t.Fatal("must reject when slug binding does not match")
	}
	if _, ok := Authenticate(cookie.Value, "acme", secret, now.Add(SessionTTL+time.Second)); ok {
		t.Fatal("must reject after expiry")
	}
	rotated := mustSecret(t)
	if _, ok := Authenticate(cookie.Value, "acme", rotated, now); ok {
		t.Fatal("must reject after secret rotation")
	}
	if _, ok := Authenticate("", "acme", secret, now); ok {
		t.Fatal("empty token must not authenticate")
	}
	if _, ok := Authenticate(cookie.Value, "acme", nil, now); ok {
		t.Fatal("empty secret must not authenticate")
	}
}

func TestMakeClearCookieEndsTheSession(t *testing.T) {
	t.Parallel()
	c := MakeClearCookie()
	if c.Name != SessionCookieName {
		t.Fatalf("wrong name: %q", c.Name)
	}
	if c.MaxAge != -1 {
		t.Fatalf("expected MaxAge=-1, got %d", c.MaxAge)
	}
	if c.Value != "" {
		t.Fatalf("expected empty value, got %q", c.Value)
	}
	// Sanity-check: the session cookie and the clear cookie share the security flags
	// so a browser overwrites the existing cookie cleanly.
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("clear cookie must mirror session cookie security flags")
	}
}
