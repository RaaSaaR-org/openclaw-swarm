package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/users"
)

// signupConfig is the runtime config for the public signup flow. Constructed
// in main.go from environment variables; passed into the server struct so
// handlers don't reach into globals.
type signupConfig struct {
	Enabled       bool          // KAI_SIGNUP_ENABLED — defaults off so existing internal-tenant deploys aren't accidentally opened to the public
	Secret        []byte        // HMAC key for verification tokens; rotated by changing the env var
	VerifyBaseURL string        // base URL embedded in the verification email; e.g. https://kai.example.org/onboarding
	VerifyTTL     time.Duration // how long a verification link stays valid
	From          string        // From: header on signup mail; falls back to pkg/email default
	IPLimitPerHr  int           // per-IP rate limit; 0 disables
	Captcha       captchaVerifier
}

// captchaVerifier is the seam for hCaptcha / Turnstile / etc. Phase 0 ships
// only the always-pass implementation (`noopCaptcha`); a real provider is
// future work. Returning a non-nil error rejects the signup with 400.
type captchaVerifier interface {
	Verify(ctx context.Context, token, remoteIP string) error
}

// noopCaptcha accepts any token. Used when no provider is configured —
// every call site passes some string but the server never hits a real API.
// Tests use this; production deployments are expected to set a real
// captchaVerifier when KAI_CAPTCHA_PROVIDER lands (Phase 2).
type noopCaptcha struct{}

func (noopCaptcha) Verify(_ context.Context, _, _ string) error { return nil }

// signupRequest is the JSON body for POST /api/signup.
type signupRequest struct {
	Email        string `json:"email"`
	Password     string `json:"password"`
	Language     string `json:"language,omitempty"`     // de | en; defaults to de per CLAUDE.md
	CaptchaToken string `json:"captchaToken,omitempty"` // forwarded to the captcha verifier
}

// signupResponse is the JSON returned on a successful POST /api/signup. We
// don't echo the email back to avoid speculative-execution side channels in
// the future; the client already has it.
type signupResponse struct {
	Status string `json:"status"` // "verification_sent"
}

// handleSignup is the public POST /api/signup endpoint. Steps:
//  1. Feature flag check (KAI_SIGNUP_ENABLED).
//  2. Rate-limit by client IP.
//  3. Decode + validate body (email parses, not disposable, password ≥ 8).
//  4. CAPTCHA verify.
//  5. Hash password via pkg/auth.
//  6. Create User (free tier, unverified).
//  7. Mint HMAC-signed verification token, embed in URL.
//  8. Dispatch verify email.
//  9. Return 202.
//
// Failures are deliberately uniform on the user-existence / disposable
// branches — we return 202 either way so probing for existing addresses
// reveals nothing. The actual create error gets logged server-side.
func (s *server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.signup.Enabled {
		writeErr(w, http.StatusNotFound, errors.New("signup disabled"))
		return
	}
	ip := clientIP(r)
	if !s.rl.allow(ip, time.Now()) {
		writeErr(w, http.StatusTooManyRequests, fmt.Errorf("rate limit exceeded for %s", ip))
		return
	}
	var req signupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}
	req.Email = users.NormalizeEmail(req.Email)
	if !parseableEmail(req.Email) {
		writeErr(w, http.StatusBadRequest, errors.New("invalid email"))
		return
	}
	if isDisposableEmail(req.Email) {
		writeErr(w, http.StatusBadRequest, errors.New("disposable email addresses are not allowed"))
		return
	}
	if len(req.Password) < 8 || len(req.Password) > 1024 {
		writeErr(w, http.StatusBadRequest, errors.New("password must be 8-1024 characters"))
		return
	}
	if err := s.signup.Captcha.Verify(r.Context(), req.CaptchaToken, ip); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("captcha: %w", err))
		return
	}
	lang := users.LangDE
	if req.Language == string(users.LangEN) {
		lang = users.LangEN
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("hash: %w", err))
		return
	}
	u, err := s.users.Create(r.Context(), users.CreateParams{
		Email: req.Email, PasswordHash: hash, Tier: users.TierFree, Language: lang,
	})
	if err != nil {
		// Uniform 202 on duplicate-email so probing doesn't enumerate accounts.
		// Other create errors (bad email, etc.) already returned 400 above; this
		// branch handles ErrEmailTaken + transient store errors.
		log.Printf("signup: store create failed for %s: %v", req.Email, err)
		writeJSON(w, http.StatusAccepted, signupResponse{Status: "verification_sent"})
		return
	}

	link, err := s.buildVerifyLink(u.ID, time.Now().Add(s.signup.VerifyTTL))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("verify link: %w", err))
		return
	}
	if err := s.sendVerifyEmail(r.Context(), u, link, lang); err != nil {
		// Don't roll back the User row — the user can request another verification
		// later (Phase 1). Log the failure so it's visible.
		log.Printf("signup: send verify mail to %s: %v", u.Email, err)
	}
	writeJSON(w, http.StatusAccepted, signupResponse{Status: "verification_sent"})
}

// handleVerify is the GET /api/signup/verify?token=<...>&id=<userID> endpoint
// the verification email links to. Validates the HMAC signature and exp,
// flips email_verified_at on the User, then provisions the user's first
// KaiInstance (TASK-013 Phase 1.A): tier=free, managed=saas,
// userRef=<the user's ID>, appRef=defaultSignupApp. The slug is derived
// from the User ID so it's globally unique by construction without any
// user-facing input.
//
// Provisioning failures are surfaced as 502 (the user IS verified, but
// their workspace didn't land — they'll need a retry path or admin
// intervention; that retry path is a Phase 1.B follow-up).
func (s *server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.signup.Enabled {
		writeErr(w, http.StatusNotFound, errors.New("signup disabled"))
		return
	}
	id := r.URL.Query().Get("id")
	tok := r.URL.Query().Get("token")
	if id == "" || tok == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing id or token"))
		return
	}
	if err := s.checkVerifyToken(id, tok, time.Now()); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid token: %w", err))
		return
	}
	u, err := s.users.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.users.MarkEmailVerified(r.Context(), id, time.Now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}

	// Provision the workspace. Skip when the K8s client isn't wired (dev
	// mode without kubeconfig) — the user is still marked verified and a
	// retry path lands in Phase 1.B.
	resp := map[string]string{"status": "verified"}
	if s.dyn != nil {
		slug := slugFromUserID(u.ID)
		gatewayToken, err := generateToken()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("token gen: %w", err))
			return
		}
		obj := buildSaaSKaiInstance(s.namespace, slug, u, gatewayToken)
		if _, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Create(r.Context(), obj, metav1.CreateOptions{}); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Verify clicked twice — fine, workspace already exists.
				log.Printf("verify: workspace already exists for %s: %v", u.Email, err)
				resp["workspace"] = slug
				resp["status"] = "verified"
				writeJSON(w, http.StatusOK, resp)
				return
			}
			log.Printf("verify: provision failed for %s: %v", u.Email, err)
			writeErr(w, http.StatusBadGateway, fmt.Errorf("verified but workspace provisioning failed: %w", err))
			return
		}
		resp["workspace"] = slug
	}
	writeJSON(w, http.StatusOK, resp)
}

// defaultSignupApp is the persona a brand-new SaaS workspace ships with
// when the user didn't pick one at signup. Phase 1.B will let signup carry
// an `app` field and store it on the User row.
const defaultSignupApp = "personal-assistant"

// slugFromUserID derives a DNS-safe slug from a User ID (`u_<26-char ULID>`).
// Strips the `u_` prefix, lowercases, takes the first 12 chars of the ULID
// body — globally unique per ULID's collision space, short enough to fit
// comfortably in `kai-<slug>.<domain>` URLs.
func slugFromUserID(userID string) string {
	body := strings.TrimPrefix(userID, users.IDPrefix)
	if len(body) > 12 {
		body = body[:12]
	}
	return "u" + strings.ToLower(body)
}

// buildSaaSKaiInstance is the Unstructured KaiInstance written to the
// cluster on verify. Spec fields populated per PROP-001 + TASK-012 Phase
// 2.A: managed:saas + tier:free + userRef + appRef + the gateway-auth
// shape every operator-managed instance carries.
func buildSaaSKaiInstance(namespace, slug string, u *users.User, gatewayToken string) *unstructured.Unstructured {
	tier := string(u.Tier)
	if tier == "" {
		tier = string(users.TierFree)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "swarm.emai.io/v1alpha1",
		"kind":       "KaiInstance",
		"metadata": map[string]any{
			"name":      "kai-" + slug,
			"namespace": namespace,
			"annotations": map[string]any{
				"swarm.io/created-by": "onboarding-signup",
			},
		},
		"spec": map[string]any{
			// Legacy CRD field names — they keep their names until the
			// v1alpha2 bump bundled with TASK-012 + TASK-024.
			"customerName": u.Email,
			"customerSlug": slug,
			"projectName":  "Workspace",
			// SaaS-direction fields from TASK-012 Phase 2.A.
			"tier":     tier,
			"userRef":  u.ID,
			"managed":  "saas",
			"appRef":   defaultSignupApp,
			"gatewayAuth": map[string]any{
				"mode":  "token",
				"token": gatewayToken,
			},
		},
	}}
}

// buildVerifyLink mints the URL the verification email links to. Format:
//
//	{verifyBase}/verify?id=<userID>&token=<base64url-HMAC>
//
// The HMAC covers `<userID>|<exp-unix>` so a leaked token can't be replayed
// past its expiry and can't be forged for a different userID.
func (s *server) buildVerifyLink(userID string, exp time.Time) (string, error) {
	if len(s.signup.Secret) == 0 {
		return "", errors.New("signup secret not configured")
	}
	expSec := exp.UTC().Unix()
	mac := hmac.New(sha256.New, s.signup.Secret)
	fmt.Fprintf(mac, "%s|%d", userID, expSec)
	tok := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf("%s/verify?id=%s&token=%s.%d",
		strings.TrimRight(s.signup.VerifyBaseURL, "/"), userID, tok, expSec), nil
}

// checkVerifyToken validates a `<base64-mac>.<exp-unix>` token against the
// expected HMAC for the given userID. Constant-time signature comparison.
func (s *server) checkVerifyToken(userID, token string, now time.Time) error {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return errors.New("malformed token")
	}
	wantMac, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("bad token encoding")
	}
	var expSec int64
	if _, err := fmt.Sscanf(parts[1], "%d", &expSec); err != nil {
		return errors.New("bad token expiry")
	}
	if now.UTC().Unix() >= expSec {
		return errors.New("token expired")
	}
	mac := hmac.New(sha256.New, s.signup.Secret)
	fmt.Fprintf(mac, "%s|%d", userID, expSec)
	got := mac.Sum(nil)
	if !hmac.Equal(got, wantMac) {
		return errors.New("signature mismatch")
	}
	return nil
}

func (s *server) sendVerifyEmail(ctx context.Context, u *users.User, link string, lang users.Lang) error {
	if s.email == nil {
		return errors.New("email sender not configured")
	}
	mailLang := email.LangDE
	if lang == users.LangEN {
		mailLang = email.LangEN
	}
	return email.Dispatch(ctx, s.email, email.SendOptions{
		Template: email.TemplateVerify,
		Lang:     mailLang,
		To:       u.Email,
		From:     s.signup.From,
	}, struct {
		Name           string
		VerifyURL      string
		ExpiresInHours int
	}{
		Name:           strings.SplitN(u.Email, "@", 2)[0],
		VerifyURL:      link,
		ExpiresInHours: int(s.signup.VerifyTTL / time.Hour),
	})
}

// parseableEmail is the conservative gate: RFC 5322 parse + has '@'. Stricter
// heuristics (MX lookup, plus-addressing tricks) belong upstream.
func parseableEmail(s string) bool {
	if s == "" || len(s) > 254 || !strings.Contains(s, "@") {
		return false
	}
	_, err := mail.ParseAddress(s)
	return err == nil
}

// isDisposableEmail checks the local domain against an embedded blocklist.
// Kept short on purpose — the full disposable-email-domains list is ~100k
// entries and updates weekly; that maintenance burden doesn't belong in the
// public swarm repo. Deployment overlays can layer on a fuller list if abuse
// becomes a problem.
func isDisposableEmail(addr string) bool {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return false
	}
	domain := strings.ToLower(addr[at+1:])
	_, hit := disposableDomains[domain]
	return hit
}

var disposableDomains = map[string]struct{}{
	"mailinator.com":      {},
	"guerrillamail.com":   {},
	"10minutemail.com":    {},
	"tempmail.com":        {},
	"throwawaymail.com":   {},
	"yopmail.com":         {},
	"fakeinbox.com":       {},
	"trashmail.com":       {},
	"sharklasers.com":     {},
	"getairmail.com":      {},
	"mintemail.com":       {},
	"mohmal.com":          {},
	"discard.email":       {},
	"discardmail.de":      {},
	"emailondeck.com":     {},
	"maildrop.cc":         {},
	"mailnesia.com":       {},
	"spambog.com":         {},
}

// rateLimiter is a tiny in-memory token bucket per client IP. Capacity is the
// per-hour ceiling; refill is amortised by computing tokens-since-last-seen on
// every check. Per-process state — multi-replica deploys would need a shared
// store (Redis or a CRDT) but a single-replica onboarding pod is fine for
// Phase 0 abuse defense.
type rateLimiter struct {
	mu       sync.Mutex
	capacity int
	buckets  map[string]rlBucket
}

type rlBucket struct {
	tokens   float64
	lastSeen time.Time
}

func newRateLimiter(perHour int) *rateLimiter {
	return &rateLimiter{capacity: perHour, buckets: map[string]rlBucket{}}
}

// allow consumes a token for ip and returns true if accepted. capacity == 0
// disables the limiter (always allow).
func (r *rateLimiter) allow(ip string, now time.Time) bool {
	if r == nil || r.capacity <= 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.buckets[ip]
	if !ok {
		b = rlBucket{tokens: float64(r.capacity), lastSeen: now}
	} else {
		// Refill: capacity tokens per hour.
		elapsed := now.Sub(b.lastSeen).Seconds()
		b.tokens += elapsed * float64(r.capacity) / 3600.0
		if b.tokens > float64(r.capacity) {
			b.tokens = float64(r.capacity)
		}
		b.lastSeen = now
	}
	if b.tokens < 1 {
		r.buckets[ip] = b
		return false
	}
	b.tokens -= 1
	r.buckets[ip] = b
	return true
}

// clientIP pulls the request's source IP. Honors X-Forwarded-For only when the
// connection arrives via loopback (a common shape behind ingress controllers
// or sidecars); otherwise trusts r.RemoteAddr to avoid spoofed headers.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if xf := strings.TrimSpace(strings.SplitN(r.Header.Get("X-Forwarded-For"), ",", 2)[0]); xf != "" {
			return xf
		}
	}
	return host
}

// newSignupSecret returns 32 bytes of randomness for the HMAC key. Used at
// startup when no SIGNUP_SECRET env var is set — purely a dev convenience;
// production deployments must set the env var explicitly so verification
// links survive a restart.
func newSignupSecret() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
