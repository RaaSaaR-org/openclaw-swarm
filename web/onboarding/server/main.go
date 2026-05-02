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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/auth", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"namespace": s.namespace})
	}))
	mux.HandleFunc("POST /api/instances", s.requireAuth(s.createInstance))

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	mux.Handle("/", spaHandler{root: staticFS})

	log.Printf("onboarding listening on %s (namespace=%s)", addr, namespace)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
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
