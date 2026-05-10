// mc-gateway is a thin HTTP proxy that sits in front of `mc api serve`,
// holding per-tenant bearer tokens and rewriting requests so each tenant only
// sees its own customer subtree of the HQ repo.
//
// The gateway is the only inbound caller of mc api. mc binds to 127.0.0.1 in
// the same pod; this process holds the cluster-facing :8080 port.
//
// See swarm/docs/api/gateway-design.md for the contract and scoping rules.
package main

import (
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	addr := envDefault("ADDR", ":8080")

	upstreamURL := envDefault("MC_UPSTREAM_URL", "http://127.0.0.1:5100")
	upstream, err := url.Parse(upstreamURL)
	if err != nil {
		log.Fatalf("invalid MC_UPSTREAM_URL %q: %v", upstreamURL, err)
	}
	if upstream.Scheme == "" || upstream.Host == "" {
		log.Fatalf("MC_UPSTREAM_URL must be an absolute URL, got %q", upstreamURL)
	}

	upstreamToken := strings.TrimSpace(os.Getenv("MC_UPSTREAM_TOKEN"))
	if upstreamToken == "" {
		log.Fatal("MC_UPSTREAM_TOKEN is required (the bearer mc-gateway uses to call mc api)")
	}

	tokensPath := envDefault("GATEWAY_TOKENS_PATH", "/etc/mc-gateway/tokens.yml")
	tokens, err := LoadTokenStore(tokensPath)
	if err != nil {
		log.Fatalf("load tokens %s: %v", tokensPath, err)
	}
	log.Printf("mc-gateway: loaded %d tokens from %s", tokens.Len(), tokensPath)

	s := &server{
		tokens:        tokens,
		upstream:      upstream,
		upstreamToken: upstreamToken,
		client:        &http.Client{Timeout: 30 * time.Second},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	// Everything else — public mc routes (openapi.json, /v1/docs, healthz/readyz
	// on mc) and the authenticated /v1/* surface — flows through the same
	// dispatcher. The dispatcher decides per-request whether to require a
	// bearer and which scoping rules apply.
	mux.HandleFunc("/", s.handleRequest)

	log.Printf("mc-gateway: listening on %s, upstream=%s", addr, upstream)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

type server struct {
	tokens        *TokenStore
	upstream      *url.URL
	upstreamToken string
	client        *http.Client
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReady reports that the gateway can reach mc upstream. We do a fast
// HEAD on /healthz; failures bubble up as 503 so a Kubernetes readiness probe
// can take the gateway out of rotation while mc is still booting.
func (s *server) handleReady(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, s.upstream.String()+"/healthz", nil)
	if err != nil {
		http.Error(w, "build probe request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := s.client.Do(req)
	if err != nil {
		http.Error(w, "upstream unreachable: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "upstream not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready"))
}

func envDefault(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
