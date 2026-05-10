package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

//go:embed all:web
var webFS embed.FS

var kaiInstanceGVR = schema.GroupVersionResource{
	Group:    "swarm.emai.io",
	Version:  "v1alpha2",
	Resource: "kaiinstances",
}

type server struct {
	dyn       dynamic.Interface
	namespace string
	token     string
}

type instanceSummary struct {
	Name              string `json:"name"`
	TenantName        string `json:"tenantName"`
	ProjectName       string `json:"projectName"`
	TenantSlug        string `json:"tenantSlug"`
	Model             string `json:"model,omitempty"`
	Phase             string `json:"phase"`
	Ready             bool   `json:"ready"`
	Suspended         bool   `json:"suspended"`
	GatewayURL        string `json:"gatewayURL,omitempty"`
	ExternalURL       string `json:"externalURL,omitempty"`
	CreationTimestamp string `json:"creationTimestamp"`
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
	} else if dyn, err := dynamic.NewForConfig(cfg); err != nil {
		log.Printf("warning: dynamic client init failed: %v", err)
	} else {
		s.dyn = dyn
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/instances", s.requireAuth(s.listInstances))
	mux.HandleFunc("GET /api/instances/{name}", s.requireAuth(s.getInstance))
	mux.HandleFunc("POST /api/instances/{name}/suspend", s.requireAuth(s.suspendInstance))
	mux.HandleFunc("POST /api/instances/{name}/resume", s.requireAuth(s.resumeInstance))

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

	log.Printf("admin-console listening on %s (namespace=%s)", addr, namespace)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
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

func (s *server) listInstances(w http.ResponseWriter, r *http.Request) {
	if s.dyn == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("kubernetes client not configured"))
		return
	}
	list, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).List(r.Context(), metav1.ListOptions{})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out := make([]instanceSummary, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, summarize(&list.Items[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getInstance(w http.ResponseWriter, r *http.Request) {
	if s.dyn == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("kubernetes client not configured"))
		return
	}
	name := r.PathValue("name")
	obj, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Get(r.Context(), name, metav1.GetOptions{})
	if err != nil {
		writeErr(w, statusForK8sErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, obj.Object)
}

func (s *server) suspendInstance(w http.ResponseWriter, r *http.Request) {
	s.patchSuspended(w, r, true)
}

func (s *server) resumeInstance(w http.ResponseWriter, r *http.Request) {
	s.patchSuspended(w, r, false)
}

func (s *server) patchSuspended(w http.ResponseWriter, r *http.Request, suspended bool) {
	if s.dyn == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("kubernetes client not configured"))
		return
	}
	name := r.PathValue("name")
	patch := []byte(`{"spec":{"suspended":` + boolStr(suspended) + `}}`)
	_, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Patch(
		r.Context(), name, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	if err != nil {
		writeErr(w, statusForK8sErr(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "suspended": suspended})
}

func summarize(u *unstructured.Unstructured) instanceSummary {
	get := func(path ...string) string {
		v, _, _ := unstructured.NestedString(u.Object, path...)
		return v
	}
	getBool := func(path ...string) bool {
		v, _, _ := unstructured.NestedBool(u.Object, path...)
		return v
	}
	// v1alpha2 renamed customerName/customerSlug → tenantName/tenantSlug
	// (TASK-024 Phase 5). The legacy customer* paths are still tried so an
	// admin-console talking to a cluster that still has v1alpha1 leftovers
	// (e.g. a swarm-emai overlay mid-migration) renders correctly. The
	// conversion webhook normalizes most reads, but the fallback keeps the
	// dashboard honest if a cluster ever serves the legacy version directly.
	return instanceSummary{
		Name:              u.GetName(),
		TenantName:        firstNonEmpty(get("spec", "tenantName"), get("spec", "customerName")),
		ProjectName:       get("spec", "projectName"),
		TenantSlug:        firstNonEmpty(get("status", "tenantSlug"), get("status", "customerSlug"), get("spec", "tenantSlug"), get("spec", "customerSlug")),
		Model:             get("spec", "model"),
		Phase:             get("status", "phase"),
		Ready:             getBool("status", "ready"),
		Suspended:         getBool("spec", "suspended"),
		GatewayURL:        get("status", "gatewayURL"),
		ExternalURL:       get("status", "externalURL"),
		CreationTimestamp: u.GetCreationTimestamp().Format(time.RFC3339),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
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
	if meta.IsNoMatchError(err) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// spaHandler serves the embedded static site with SPA fallback to index.html.
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
		// SPA fallback
		path = "index.html"
	}

	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(w, r, s.root, path)
}
