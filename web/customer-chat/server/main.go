package main

import (
	"crypto/rand"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
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
	dyn          dynamic.Interface
	core         kubernetes.Interface
	namespace    string
	demoMode     bool
	devJWTSecret []byte // ephemeral random secret, only set when demoMode is true; never persisted
	bridges      *bridgePool
}

func main() {
	addr := envDefault("ADDR", ":8080")
	namespace := envDefault("SWARM_NAMESPACE", "emai-swarm")

	s := &server{
		namespace: namespace,
		demoMode:  envTrue("KAI_INSECURE_DEV_AUTH"),
	}

	if s.demoMode {
		if err := requireLoopback(addr); err != nil {
			log.Fatalf("%v", err)
		}
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			log.Fatalf("KAI_INSECURE_DEV_AUTH: failed to seed dev JWT secret: %v", err)
		}
		s.devJWTSecret = secret
		log.Printf("============================================================")
		log.Printf("KAI_INSECURE_DEV_AUTH ENABLED — DO NOT USE IN PRODUCTION")
		log.Printf("Login accepts any user; JWT signed with random ephemeral secret.")
		log.Printf("Listening on loopback %s only.", addr)
		log.Printf("============================================================")
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

func envTrue(k string) bool {
	v := os.Getenv(k)
	return v == "1" || strings.EqualFold(v, "true")
}

// requireLoopback refuses to start when an "insecure dev auth" mode binds to
// anything reachable off-host. Empty host (e.g. ":8080") binds all interfaces
// and is rejected — the dev must opt in to a loopback address explicitly.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("KAI_INSECURE_DEV_AUTH requires loopback ADDR (e.g. 127.0.0.1:8080), got %q: %v", addr, err)
	}
	if host == "" {
		return fmt.Errorf("KAI_INSECURE_DEV_AUTH requires explicit loopback host (e.g. 127.0.0.1:8080), got %q", addr)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("KAI_INSECURE_DEV_AUTH refuses non-loopback host %q — bind to 127.0.0.1, ::1, or localhost", host)
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
