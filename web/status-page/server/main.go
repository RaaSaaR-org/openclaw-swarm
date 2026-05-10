package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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
	namespace string
}

// publicStatus is the customer-facing view of an instance — no spec internals,
// no operator implementation details, only what the customer cares about.
type publicStatus struct {
	CustomerName string `json:"customerName"`
	ProjectName  string `json:"projectName"`
	Slug         string `json:"slug"`
	Status       string `json:"status"`              // online | setting-up | maintenance | issue | unknown
	Ready        bool   `json:"ready"`
	Message      string `json:"message"`             // human-readable line
	LastUpdate   string `json:"lastUpdate"`
	GatewayURL   string `json:"gatewayURL,omitempty"`
}

func main() {
	addr := envDefault("ADDR", ":8080")
	namespace := envDefault("SWARM_NAMESPACE", "swarm-system")

	s := &server{namespace: namespace}
	if cfg, err := loadKubeConfig(); err != nil {
		log.Printf("warning: no kubeconfig available (%v) — status lookups will fail until creds are present", err)
	} else if dyn, err := dynamic.NewForConfig(cfg); err != nil {
		log.Printf("warning: dynamic client init failed: %v", err)
	} else {
		s.dyn = dyn
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/status/{slug}", s.getStatus)

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

	log.Printf("status-page listening on %s (namespace=%s)", addr, namespace)
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

func (s *server) getStatus(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	token := tokenFromRequest(r)

	// Always require a token; respond with the same 401 whether the slug is
	// missing, malformed, the instance does not exist, or the token is wrong —
	// so probing slugs reveals nothing.
	if token == "" {
		writeUnauthorized(w)
		return
	}
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	if s.dyn == nil {
		// We can't validate the token without a cluster; treat as service-unavailable
		// so monitoring distinguishes "down" from "auth fail."
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "status backend unavailable"})
		return
	}

	name := "kai-" + slug
	obj, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeUnauthorized(w)
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}

	expected, _, _ := unstructured.NestedString(obj.Object, "spec", "gatewayAuth", "token")
	if expected == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(token)) != 1 {
		writeUnauthorized(w)
		return
	}

	writeJSON(w, http.StatusOK, summarize(obj, slug))
}

func tokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

func summarize(u *unstructured.Unstructured, slug string) publicStatus {
	get := func(path ...string) string {
		v, _, _ := unstructured.NestedString(u.Object, path...)
		return v
	}
	getBool := func(path ...string) bool {
		v, _, _ := unstructured.NestedBool(u.Object, path...)
		return v
	}

	phase := get("status", "phase")
	ready := getBool("status", "ready")
	suspended := getBool("spec", "suspended")

	status, message := translatePhase(phase, ready, suspended)
	lastUpdate := lastConditionTime(u)
	if lastUpdate == "" {
		lastUpdate = u.GetCreationTimestamp().Format(time.RFC3339)
	}

	return publicStatus{
		CustomerName: get("spec", "customerName"),
		ProjectName:  get("spec", "projectName"),
		Slug:         slug,
		Status:       status,
		Ready:        ready,
		Message:      message,
		LastUpdate:   lastUpdate,
		GatewayURL:   get("status", "externalURL"),
	}
}

func translatePhase(phase string, ready, suspended bool) (status, message string) {
	if suspended {
		return "maintenance", "Your assistant is paused. Contact your EmAI team to resume."
	}
	switch phase {
	case "Running":
		if ready {
			return "online", "All systems operational."
		}
		return "setting-up", "Starting up — almost ready."
	case "Provisioning":
		return "setting-up", "Setting up your assistant for the first time."
	case "Suspended":
		return "maintenance", "Your assistant is paused."
	case "Failed":
		return "issue", "We're investigating an issue. Your EmAI team has been notified."
	default:
		return "unknown", "Status unavailable."
	}
}

func lastConditionTime(u *unstructured.Unstructured) string {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return ""
	}
	var latest string
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := m["lastTransitionTime"].(string); ok && t > latest {
			latest = t
		}
	}
	return latest
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeUnauthorized(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
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

