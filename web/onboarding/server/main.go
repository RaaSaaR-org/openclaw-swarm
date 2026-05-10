package main

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/users"
)

//go:embed all:web
var webFS embed.FS

var (
	kaiInstanceGVR = schema.GroupVersionResource{
		Group:    "swarm.emai.io",
		Version:  "v1alpha2",
		Resource: "kaiinstances",
	}
	slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

type server struct {
	dyn       dynamic.Interface
	core      kubernetes.Interface // typed client for Secret writes (per-workspace OpenRouter key)
	namespace string
	token     string

	// SaaS signup flow (TASK-013). Optional — Enabled=false in signup keeps
	// the onboarding pod identical to its pre-signup behavior.
	users        users.Store
	email        email.Sender
	signup       signupConfig
	rl           *rateLimiter
	keyMinter    keyProvisioner     // TASK-019 Phase 2.B; nil disables per-workspace key minting
	emailWebhook emailWebhookConfig // TASK-020 Phase 4; empty Secret disables /api/email/webhook (returns 503)
}

type provisionRequest struct {
	CustomerName      string `json:"customerName"`
	ProjectName       string `json:"projectName"`
	CustomerSlug      string `json:"customerSlug"`
	Model             string `json:"model,omitempty"`
	TelegramSecretRef string `json:"telegramSecretRef,omitempty"`
	ExternalAccess    *bool  `json:"externalAccess,omitempty"`
}

type provisionResponse struct {
	Name         string `json:"name"`
	Namespace    string `json:"namespace"`
	CustomerSlug string `json:"customerSlug"`
	GatewayToken string `json:"gatewayToken"`
}

func main() {
	addr := envDefault("ADDR", ":8080")
	namespace := envDefault("SWARM_NAMESPACE", "swarm-system")
	token := os.Getenv("ADMIN_TOKEN")
	if token == "" {
		log.Fatal("ADMIN_TOKEN must be set")
	}

	s := &server{namespace: namespace, token: token}
	if cfg, err := loadKubeConfig(); err != nil {
		log.Printf("warning: no kubeconfig available (%v) — API calls will fail until creds are present", err)
	} else {
		if dyn, err := dynamic.NewForConfig(cfg); err != nil {
			log.Printf("warning: dynamic client init failed: %v", err)
		} else {
			s.dyn = dyn
		}
		if core, err := kubernetes.NewForConfig(cfg); err != nil {
			log.Printf("warning: core client init failed: %v", err)
		} else {
			s.core = core
		}
	}

	if err := s.setupSignup(); err != nil {
		log.Fatalf("signup setup: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/auth", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"namespace": s.namespace})
	}))
	mux.HandleFunc("POST /api/instances", s.requireAuth(s.createInstance))
	mux.HandleFunc("GET /api/onboarding/config", s.handleOnboardingConfig)
	mux.HandleFunc("POST /api/signup", s.handleSignup)
	mux.HandleFunc("GET /api/signup/verify", s.handleVerify)
	mux.HandleFunc("POST /api/email/webhook", s.handleEmailWebhook)

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	brandingFS, err := fs.Sub(webFS, "web/branding")
	if err != nil {
		log.Fatalf("branding fs: %v", err)
	}
	mux.Handle("GET /branding/", http.StripPrefix("/branding/", brandingHandler{
		overrideDir: os.Getenv("BRANDING_DIR"),
		defaults:    brandingFS,
	}))
	mux.Handle("/", spaHandler{root: staticFS})

	log.Printf("onboarding listening on %s (namespace=%s)", addr, namespace)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// setupSignup wires the optional public-signup flow (TASK-013). Defaults are
// dev-friendly: when KAI_SIGNUP_ENABLED=1 but no real users/email/secret env
// vars are set, the flow uses an in-memory user store + a disk email sender +
// an ephemeral HMAC secret. Production deployments must set KAI_SIGNUP_SECRET
// (so verification links survive restarts) and either KAI_USERS_DSN +
// RESEND_API_KEY or roll their own Sender/Store via a fork.
func (s *server) setupSignup() error {
	enabled := envTrue("KAI_SIGNUP_ENABLED")
	s.signup.Enabled = enabled
	if !enabled {
		return nil
	}

	// Users store: MemoryStore by default; opt in to Postgres-backed PoolStore
	// by setting `KAI_USERS_DSN`. The shared store is what lets onboarding
	// (signup) and workspace (login + dashboard) see the same user — both
	// services point at the same database in production. The public binary
	// pulls pgx as a hard dep; deployments that want pure-memory ignore the
	// env var.
	if dsn := os.Getenv("KAI_USERS_DSN"); dsn != "" {
		store, err := newPoolStore(dsn)
		if err != nil {
			return fmt.Errorf("postgres user store: %w", err)
		}
		s.users = store
		log.Printf("signup: using Postgres user store (KAI_USERS_DSN set)")
	} else {
		s.users = users.NewMemoryStore()
		log.Printf("signup: using in-memory user store (set KAI_USERS_DSN to use Postgres)")
	}

	// Email sender: DiskSender by default; switch to Resend by setting
	// EMAIL_PROVIDER=resend + RESEND_API_KEY (Phase 1 wiring — for now both
	// branches stay in pkg/email so the bind here is direct).
	emailDir := envDefault("EMAIL_DISK_DIR", "/tmp/emai-onboarding-emails")
	disk, err := email.NewDiskSender(emailDir)
	if err != nil {
		return fmt.Errorf("disk email sender: %w", err)
	}
	s.email = disk
	log.Printf("signup: email artifacts will land in %s (set EMAIL_PROVIDER=resend + RESEND_API_KEY to switch)", emailDir)

	// HMAC secret: env var preferred (verification links survive restart);
	// random fallback for dev so a forgotten env var doesn't fail startup.
	if hex := os.Getenv("KAI_SIGNUP_SECRET"); hex != "" {
		s.signup.Secret = []byte(hex)
	} else {
		secret, err := newSignupSecret()
		if err != nil {
			return fmt.Errorf("signup secret: %w", err)
		}
		s.signup.Secret = secret
		log.Printf("signup: using ephemeral HMAC secret — set KAI_SIGNUP_SECRET in production so verification links survive restarts")
	}

	s.signup.VerifyBaseURL = envDefault("KAI_VERIFY_BASE_URL", "http://localhost:8080/api/signup")
	s.signup.VerifyTTL = 24 * time.Hour
	s.signup.From = os.Getenv("EMAIL_FROM") // pkg/email falls back to its own default if empty
	s.signup.IPLimitPerHr = 5
	if secret := os.Getenv("TURNSTILE_SECRET_KEY"); secret != "" {
		s.signup.Captcha = newTurnstileCaptcha(secret)
		// The PUBLIC site key (a different Cloudflare-issued string) is
		// surfaced through /api/onboarding/config so the SPA can mount the
		// cf-turnstile widget. Both keys come from the same Turnstile
		// dashboard entry; production deploys must set both env vars.
		s.signup.TurnstilePublicSiteKey = os.Getenv("TURNSTILE_SITE_KEY")
		if s.signup.TurnstilePublicSiteKey == "" {
			log.Printf("signup: TURNSTILE_SECRET_KEY set but TURNSTILE_SITE_KEY missing — SPA widget will not render; the server will still verify tokens posted via the API")
		} else {
			log.Printf("signup: Cloudflare Turnstile CAPTCHA enabled (site-key configured for SPA widget)")
		}
	} else {
		s.signup.Captcha = noopCaptcha{}
		log.Printf("signup: no CAPTCHA configured — set TURNSTILE_SECRET_KEY + TURNSTILE_SITE_KEY to enable Cloudflare Turnstile")
	}
	s.keyMinter = resolveKeyProvisioner(os.Getenv("OPENROUTER_PROVISIONING_KEY"))

	// TASK-020 Phase 4: Resend bounce-webhook secret. Empty / unconfigured
	// → /api/email/webhook returns 503 so an enabled-but-unconfigured deploy
	// fails loudly instead of silently accepting unsigned webhooks.
	if secret, err := loadResendSecret(os.Getenv("RESEND_WEBHOOK_SECRET")); err != nil {
		log.Printf("signup: RESEND_WEBHOOK_SECRET rejected (%v); /api/email/webhook will return 503", err)
	} else if secret != nil {
		s.emailWebhook = emailWebhookConfig{Secret: secret, Tolerance: 5 * time.Minute}
		log.Printf("signup: Resend webhook receiver enabled at /api/email/webhook")
	} else {
		log.Printf("signup: no RESEND_WEBHOOK_SECRET set — /api/email/webhook will return 503")
	}
	s.rl = newRateLimiter(s.signup.IPLimitPerHr)
	return nil
}

func envTrue(k string) bool {
	v := os.Getenv(k)
	return v == "1" || strings.EqualFold(v, "true")
}

func loadKubeConfig() (*rest.Config, error) {
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			kubeconfig = home + "/.kube/config"
		}
	}
	return clientcmd.BuildConfigFromFlags("", kubeconfig)
}

func (s *server) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

func (s *server) createInstance(w http.ResponseWriter, r *http.Request) {
	var req provisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("invalid JSON: %w", err))
		return
	}

	req.CustomerName = strings.TrimSpace(req.CustomerName)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.CustomerSlug = strings.TrimSpace(req.CustomerSlug)
	req.Model = strings.TrimSpace(req.Model)
	req.TelegramSecretRef = strings.TrimSpace(req.TelegramSecretRef)

	if err := validateProvision(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}

	if s.dyn == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("kubernetes client not configured"))
		return
	}

	gatewayToken, err := generateToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("token gen: %w", err))
		return
	}

	name := "kai-" + req.CustomerSlug

	spec := map[string]any{
		"customerName": req.CustomerName,
		"projectName":  req.ProjectName,
		"customerSlug": req.CustomerSlug,
		"gatewayAuth": map[string]any{
			"mode":  "token",
			"token": gatewayToken,
		},
	}
	if req.Model != "" {
		spec["model"] = req.Model
	}
	if req.TelegramSecretRef != "" {
		spec["telegram"] = map[string]any{
			"botTokenSecretRef": req.TelegramSecretRef,
		}
	}
	if req.ExternalAccess != nil {
		spec["externalAccess"] = *req.ExternalAccess
	}

	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "swarm.emai.io/v1alpha2",
		"kind":       "KaiInstance",
		"metadata": map[string]any{
			"name":      name,
			"namespace": s.namespace,
		},
		"spec": spec,
	}}

	created, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Create(r.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			writeErr(w, http.StatusConflict, fmt.Errorf("instance %q already exists", name))
			return
		}
		writeErr(w, statusForK8sErr(err), err)
		return
	}

	writeJSON(w, http.StatusCreated, provisionResponse{
		Name:         created.GetName(),
		Namespace:    created.GetNamespace(),
		CustomerSlug: req.CustomerSlug,
		GatewayToken: gatewayToken,
	})
}

func validateProvision(r *provisionRequest) error {
	if r.CustomerName == "" {
		return errors.New("customerName is required")
	}
	if len(r.CustomerName) > 100 {
		return errors.New("customerName must be 100 characters or fewer")
	}
	if r.ProjectName == "" {
		return errors.New("projectName is required")
	}
	if len(r.ProjectName) > 200 {
		return errors.New("projectName must be 200 characters or fewer")
	}
	if r.CustomerSlug == "" {
		return errors.New("customerSlug is required")
	}
	if len(r.CustomerSlug) > 63 {
		return errors.New("customerSlug must be 63 characters or fewer")
	}
	if !slugRegex.MatchString(r.CustomerSlug) {
		return errors.New("customerSlug must be DNS-safe (lowercase letters, digits, and hyphens; must start and end with letter or digit)")
	}
	return nil
}

func generateToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func statusForK8sErr(err error) int {
	var statusErr interface{ Status() metav1.Status }
	if errors.As(err, &statusErr) {
		s := statusErr.Status()
		if s.Code != 0 {
			return int(s.Code)
		}
	}
	return http.StatusInternalServerError
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// brandingHandler serves /branding/* — override from disk if BRANDING_DIR is
// set and the requested file exists there, otherwise fall back to embedded
// defaults.
type brandingHandler struct {
	overrideDir string
	defaults    fs.FS
}

func (h brandingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if h.overrideDir != "" {
		p := filepath.Join(h.overrideDir, filepath.Clean("/"+name))
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, p)
			return
		}
	}
	if _, err := fs.Stat(h.defaults, name); err == nil {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, h.defaults, name)
		return
	}
	http.NotFound(w, r)
}

type spaHandler struct{ root fs.FS }

func (s spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(s.root, path); err != nil {
		path = "index.html"
	}
	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.root, path)
}
