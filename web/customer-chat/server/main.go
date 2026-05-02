package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

//go:embed all:web
var webFS embed.FS

var slugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

type server struct {
	dyn       dynamic.Interface
	core      kubernetes.Interface
	namespace string
	demoMode  bool
	bridges   *bridgePool
}

func main() {
	addr := envDefault("ADDR", ":8080")
	namespace := envDefault("SWARM_NAMESPACE", "emai-swarm")

	s := &server{
		namespace: namespace,
		demoMode:  os.Getenv("DEMO_MODE") == "1" || os.Getenv("DEMO_MODE") == "true",
	}

	if s.demoMode {
		log.Printf("DEMO_MODE enabled — login accepts any user, no upstream gateway")
	}

	if cfg, err := loadKubeConfig(); err != nil {
		log.Printf("warning: no kubeconfig available (%v) — chat lookups will fail", err)
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

	s.bridges = newBridgePool(s)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /api/chat/{slug}/login", s.handleLogin)
	mux.HandleFunc("POST /api/chat/{slug}/logout", s.handleLogout)
	mux.HandleFunc("GET /api/chat/{slug}/me", s.handleMe)
	mux.HandleFunc("GET /chat/{slug}/ws", s.handleWS)

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("static fs: %v", err)
	}
	mux.Handle("/", spaHandler{root: staticFS})

	log.Printf("customer-chat listening on %s (namespace=%s)", addr, namespace)
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
