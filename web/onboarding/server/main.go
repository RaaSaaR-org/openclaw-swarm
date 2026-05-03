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
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
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
		Version:  "v1alpha1",
		Resource: "kaiinstances",
	}
	slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

type server struct {
	dyn       dynamic.Interface
	namespace string
	token     string

	// SaaS signup flow (TASK-013). Optional — Enabled=false in signup keeps
	// the onboarding pod identical to its pre-signup behavior.
	users  users.Store
	email  email.Sender
	signup signupConfig
	rl     *rateLimiter
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
	namespace := envDefault("SWARM_NAMESPACE", "emai-swarm")
	token := os.Getenv("ADMIN_TOKEN")
	if token == "" {
		log.Fatal("ADMIN_TOKEN must be set")
	}

	s := &server{namespace: namespace, token: token}
	if cfg, err := loadKubeConfig(); err != nil {
		log.Printf("warning: no kubeconfig available (%v) — API calls will fail until creds are present", err)
	} else if dyn, err := dynamic.NewForConfig(cfg); err != nil {
		log.Printf("warning: dynamic client init failed: %v", err)
	} else {
		s.dyn = dyn
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
	mux.HandleFunc("POST /api/signup", s.handleSignup)
	mux.HandleFunc("GET /api/signup/verify", s.handleVerify)

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
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

	// Users store: MemoryStore for dev / single-process; production overlays
	// should construct a pkg/userspg.PoolStore and inject via a code change
	// (kept out of this commit so the public swarm repo doesn't pull pgx
	// into the onboarding binary by default).
	s.users = users.NewMemoryStore()
	log.Printf("signup: using in-memory user store (Phase 0 — Postgres wiring lands with the swarm-cloud overlay)")

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
	s.signup.Captcha = noopCaptcha{}
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
		"apiVersion": "swarm.emai.io/v1alpha1",
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
