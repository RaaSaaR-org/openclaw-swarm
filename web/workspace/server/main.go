package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/authk8s"
	"github.com/emai-ai/swarm/pkg/email"
	stripepkg "github.com/emai-ai/swarm/pkg/stripe"
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

const (
	// annotationTenantLinks is the canonical (TASK-024) key for per-tenant
	// extra app-shelf links on the KaiInstance metadata. The legacy
	// `swarm.emai.io/customer-links` key is also read for one release so
	// existing internal-tenant manifests in `swarm-emai` keep their links
	// while we migrate the annotation in-place.
	annotationTenantLinks       = "swarm.emai.io/tenant-links"
	annotationLegacyCustomLinks = "swarm.emai.io/customer-links"
	briefingConfigMapTpl        = "kai-%s-briefings"
)

type server struct {
	dyn          dynamic.Interface
	core         kubernetes.Interface
	namespace    string
	chatBase     string // e.g. "" (same origin) or "https://chat.emai.dev"
	statusBase   string
	demoMode     bool   // serves canned data, ignores K8s — for local previews and sales demos
	devJWTSecret []byte // ephemeral random secret, only set when demoMode is true; never persisted
	revoker      auth.Revoker
	// users is the central platform user store (TASK-014). Login for SaaS-managed
	// workspaces validates against it; legacy internal-managed tenants keep using
	// the per-tenant kai-<slug>-users Secret. Nil disables the SaaS path entirely
	// (every login then falls through to the legacy Secret flow).
	users users.Store

	// email + deletion config (TASK-021 Phase 1). All optional — missing
	// email Sender or empty deletionSecret leaves the account-deletion
	// endpoints returning 503 so an enabled-but-unconfigured deploy fails
	// loudly. The base URL is what the confirmation email links back to.
	email           email.Sender
	emailFrom       string
	deletionSecret  []byte
	deletionTTL     time.Duration
	deletionBaseURL string // e.g. "https://kai.emai.dev"; embeds in the confirmation email link

	// Stripe billing (TASK-016 Phase 1). Empty Client / WebhookSecret /
	// TierToPriceID disables the corresponding endpoint with 503.
	stripe stripeConfig
}

type appLink struct {
	Label       string `json:"label"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	External    bool   `json:"external,omitempty"`
}

type briefing struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Date    string `json:"date,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
	Body    string `json:"body,omitempty"`
}

type channel struct {
	Kind  string `json:"kind"`            // "webchat" | "telegram" | future
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`  // human-readable detail (e.g. "@AcmeKaiBot")
}

type teamMember struct {
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
	Company  string `json:"company,omitempty"`
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	Avatar   string `json:"avatar,omitempty"` // emoji or single character
}

type centerResponse struct {
	CustomerName string       `json:"customerName"`
	ProjectName  string       `json:"projectName"`
	Slug         string       `json:"slug"`
	Status       string       `json:"status"`        // online | setting-up | paused | issue | unknown
	StatusLabel  string       `json:"statusLabel"`   // human-friendly label
	Links        []appLink    `json:"links"`
	Channels     []channel    `json:"channels"`
	Team         []teamMember `json:"team"`
	Scope        string       `json:"scope,omitempty"`     // markdown
	Heartbeat    string       `json:"heartbeat,omitempty"` // markdown
	Briefings    []briefing   `json:"briefings"`
}

func main() {
	addr := envDefault("ADDR", ":8080")
	namespace := envDefault("SWARM_NAMESPACE", "swarm-system")

	s := &server{
		namespace:  namespace,
		chatBase:   os.Getenv("CHAT_BASE_URL"),
		statusBase: os.Getenv("STATUS_BASE_URL"),
		demoMode:   envTrue("KAI_INSECURE_DEV_AUTH"),
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
		s.revoker = auth.NewMemoryRevoker()
		log.Printf("============================================================")
		log.Printf("KAI_INSECURE_DEV_AUTH ENABLED — DO NOT USE IN PRODUCTION")
		log.Printf("Center serves canned data; JWT signed with random ephemeral secret.")
		log.Printf("Listening on loopback %s only.", addr)
		log.Printf("============================================================")
	}

	if cfg, err := loadKubeConfig(); err != nil {
		log.Printf("warning: no kubeconfig available (%v) — center lookups will fail", err)
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

	if s.revoker == nil && s.core != nil {
		s.revoker = &authk8s.SecretRevoker{Client: s.core, Namespace: namespace}
	}

	// User store: MemoryStore by default; opt in to Postgres-backed PoolStore
	// by setting `KAI_USERS_DSN`. Sharing the store across onboarding +
	// workspace is what lets a user signed up via onboarding actually log
	// in to the workspace dashboard. Both services point at the same
	// database in production. Set the same DSN on both pods.
	if dsn := os.Getenv("KAI_USERS_DSN"); dsn != "" {
		store, err := newPoolStore(dsn)
		if err != nil {
			log.Fatalf("workspace: KAI_USERS_DSN configured but failed to connect: %v", err)
		}
		s.users = store
		log.Printf("workspace: using Postgres user store (KAI_USERS_DSN set)")
	} else {
		s.users = users.NewMemoryStore()
		log.Printf("workspace: using in-memory user store (set KAI_USERS_DSN to use Postgres)")
	}

	// Account-deletion flow (TASK-021 Phase 1) — opt-in via env vars. All
	// three of RESEND_API_KEY + KAI_DELETION_SECRET + KAI_DASHBOARD_BASE_URL
	// must be set or the endpoints return 503.
	if apiKey := os.Getenv("RESEND_API_KEY"); apiKey != "" {
		if sender, err := email.NewResendSender(apiKey); err != nil {
			log.Printf("workspace: RESEND_API_KEY rejected (%v); account-deletion endpoints will return 503", err)
		} else {
			s.email = sender
			s.emailFrom = os.Getenv("EMAIL_FROM")
		}
	} else if dir := os.Getenv("EMAIL_DISK_DIR"); dir != "" {
		// Dev-mode fallback: write emails to disk instead of sending. Mirrors
		// onboarding's setupSignup behavior so the SaaS deletion flow + the
		// post-delete email can be exercised end-to-end on k3d without a
		// real Resend key. No-op in production overlays that always set
		// RESEND_API_KEY.
		if sender, err := email.NewDiskSender(dir); err != nil {
			log.Printf("workspace: EMAIL_DISK_DIR rejected (%v); account-deletion endpoints will return 503", err)
		} else {
			s.email = sender
			s.emailFrom = os.Getenv("EMAIL_FROM")
			log.Printf("workspace: dev-mode disk email sender at %s", dir)
		}
	}
	if secret := os.Getenv("KAI_DELETION_SECRET"); secret != "" {
		s.deletionSecret = []byte(secret)
	}
	s.deletionBaseURL = os.Getenv("KAI_DASHBOARD_BASE_URL")
	s.deletionTTL = 24 * time.Hour
	if s.email != nil && len(s.deletionSecret) > 0 && s.deletionBaseURL != "" {
		log.Printf("workspace: account-deletion flow enabled (base=%s)", s.deletionBaseURL)
	} else {
		log.Printf("workspace: account-deletion flow disabled — set RESEND_API_KEY + KAI_DELETION_SECRET + KAI_DASHBOARD_BASE_URL to enable")
	}

	// Stripe billing (TASK-016 Phase 1). Opt-in via env. Tier→price-ID
	// mapping is parsed from STRIPE_PRICE_STARTER + STRIPE_PRICE_GROWTH so
	// the deployment overlay can override per environment without
	// hardcoding price_… IDs in this binary.
	if stripeKey := os.Getenv("STRIPE_API_KEY"); stripeKey != "" {
		priceMap := map[string]stripepkg.Tier{}
		tierToPrice := map[users.Tier]string{}
		if pid := os.Getenv("STRIPE_PRICE_STARTER"); pid != "" {
			priceMap[pid] = stripepkg.TierStarter
			tierToPrice[users.TierStarter] = pid
		}
		if pid := os.Getenv("STRIPE_PRICE_GROWTH"); pid != "" {
			priceMap[pid] = stripepkg.TierGrowth
			tierToPrice[users.TierGrowth] = pid
		}
		client, err := stripepkg.NewClient(stripeKey, priceMap)
		if err != nil {
			log.Printf("workspace: STRIPE_API_KEY rejected (%v); billing endpoints will return 503", err)
		} else {
			s.stripe = stripeConfig{
				Client:          client,
				WebhookSecret:   os.Getenv("STRIPE_WEBHOOK_SECRET"),
				TierToPriceID:   tierToPrice,
				SuccessURL:      os.Getenv("STRIPE_SUCCESS_URL"),
				CancelURL:       os.Getenv("STRIPE_CANCEL_URL"),
				PortalReturnURL: os.Getenv("STRIPE_PORTAL_RETURN_URL"),
			}
			log.Printf("workspace: Stripe billing wired (webhook=%t, prices=%d)", s.stripe.WebhookSecret != "", len(tierToPrice))
		}
	} else {
		log.Printf("workspace: Stripe billing disabled — set STRIPE_API_KEY + STRIPE_WEBHOOK_SECRET + STRIPE_PRICE_* + STRIPE_SUCCESS_URL/CANCEL_URL/PORTAL_RETURN_URL to enable")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/workspace/{slug}", s.getCenter)
	mux.HandleFunc("GET /api/workspace/{slug}/auth", s.handleAuthInfo)
	mux.HandleFunc("POST /api/workspace/{slug}/login", s.handleLogin)
	mux.HandleFunc("POST /api/workspace/{slug}/logout", s.handleLogout)
	// Traefik forwardAuth target — gates /hq/<slug> dashboards behind the same
	// session cookie as /workspace/<slug>. GET so an unauth'd browser redirect
	// reaches the user.
	mux.HandleFunc("GET /api/workspace/{slug}/forward-auth", s.handleForwardAuth)
	mux.HandleFunc("GET /api/workspace/{slug}/users", s.listUsers)
	mux.HandleFunc("POST /api/workspace/{slug}/users", s.addUser)
	mux.HandleFunc("DELETE /api/workspace/{slug}/users/{email}", s.removeUser)
	mux.HandleFunc("POST /api/workspace/{slug}/users/{email}/password", s.resetPassword)
	mux.HandleFunc("GET /api/workspace/{slug}/agents", s.listAgents)
	mux.HandleFunc("GET /api/workspace/{slug}/owner", s.handleOwner)
	mux.HandleFunc("GET /api/workspace/{slug}/owned-workspaces", s.handleOwnedWorkspaces)
	mux.HandleFunc("GET /api/workspace/{slug}/catalog", s.handleListCatalog)
	mux.HandleFunc("PATCH /api/workspace/{slug}/app", s.handleSwitchApp)
	mux.HandleFunc("POST /api/workspace/{slug}/account/request-deletion", s.handleRequestDeletion)
	mux.HandleFunc("GET /api/workspace/{slug}/account/confirm-deletion", s.handleConfirmDeletion)
	mux.HandleFunc("GET /api/workspace/{slug}/account/export", s.handleAccountExport)
	mux.HandleFunc("POST /api/workspace/{slug}/billing/checkout", s.handleBillingCheckout)
	mux.HandleFunc("POST /api/workspace/{slug}/billing/portal", s.handleBillingPortal)
	mux.HandleFunc("POST /api/billing/webhook", s.handleBillingWebhook)

	// Legacy /center, /api/center, /center-assets paths — 301 to the /workspace*
	// equivalents for one release cycle. Drop after clients have migrated
	// (TASK-025 acceptance: 301 for one release).
	legacyRedirect := func(oldPrefix, newPrefix string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			newPath := newPrefix + strings.TrimPrefix(r.URL.Path, oldPrefix)
			if r.URL.RawQuery != "" {
				newPath += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, newPath, http.StatusMovedPermanently)
		}
	}
	mux.HandleFunc("/api/center/", legacyRedirect("/api/center/", "/api/workspace/"))
	mux.HandleFunc("/center/", legacyRedirect("/center/", "/workspace/"))
	mux.HandleFunc("/center-assets/", legacyRedirect("/center-assets/", "/workspace-assets/"))

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

	log.Printf("workspace listening on %s (namespace=%s)", addr, namespace)
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

func (s *server) getCenter(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusOK, demoData(slug))
		return
	}
	if !s.requireCenterAuth(w, r, slug) {
		return
	}
	if s.dyn == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "center backend unavailable"})
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

	customerName, _, _ := unstructured.NestedString(obj.Object, "spec", "customerName")
	projectName, _, _ := unstructured.NestedString(obj.Object, "spec", "projectName")

	status, statusLabel := translateStatus(obj)
	links := s.buildLinks(slug, obj)
	channels := s.buildChannels(obj)
	team, scope, heartbeat := s.loadProfile(r, slug)
	briefings := s.loadBriefings(r, slug)
	if team == nil {
		team = []teamMember{}
	}
	if briefings == nil {
		briefings = []briefing{}
	}

	writeJSON(w, http.StatusOK, centerResponse{
		CustomerName: customerName,
		ProjectName:  projectName,
		Slug:         slug,
		Status:       status,
		StatusLabel:  statusLabel,
		Links:        links,
		Channels:     channels,
		Team:         team,
		Scope:        scope,
		Heartbeat:    heartbeat,
		Briefings:    briefings,
	})
}

// loadProfile reads the per-customer profile ConfigMap (team.json + scope.md +
// heartbeat.md). Any missing piece is returned empty — the whole section is optional.
func (s *server) loadProfile(r *http.Request, slug string) ([]teamMember, string, string) {
	if s.core == nil {
		return nil, "", ""
	}
	cm, err := s.core.CoreV1().ConfigMaps(s.namespace).Get(r.Context(), "kai-"+slug+"-profile", metav1.GetOptions{})
	if err != nil {
		return nil, "", ""
	}
	var team []teamMember
	if raw, ok := cm.Data["team.json"]; ok {
		if err := json.Unmarshal([]byte(raw), &team); err != nil {
			team = nil
		}
	}
	scope := cm.Data["scope.md"]
	heartbeat := cm.Data["heartbeat.md"]
	return team, scope, heartbeat
}

func translateStatus(obj *unstructured.Unstructured) (status, label string) {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	ready, _, _ := unstructured.NestedBool(obj.Object, "status", "ready")
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspended")
	if suspended {
		return "paused", "Paused"
	}
	switch phase {
	case "Running":
		if ready {
			return "online", "Online"
		}
		return "setting-up", "Setting up"
	case "Provisioning":
		return "setting-up", "Setting up"
	case "Suspended":
		return "paused", "Paused"
	case "Failed":
		return "issue", "Issue detected"
	default:
		return "unknown", "Unknown"
	}
}

func (s *server) buildChannels(obj *unstructured.Unstructured) []channel {
	channels := []channel{
		{Kind: "webchat", Label: "Web chat", Hint: "Use the link in 'Your apps'."},
	}
	if telegram, found, _ := unstructured.NestedMap(obj.Object, "spec", "telegram"); found && telegram != nil {
		hint := "Configured"
		if ref, ok := telegram["botTokenSecretRef"].(string); ok && ref != "" {
			hint = "Bot configured (" + ref + ")"
		}
		channels = append(channels, channel{Kind: "telegram", Label: "Telegram", Hint: hint})
	}
	return channels
}

func (s *server) buildLinks(slug string, obj *unstructured.Unstructured) []appLink {
	encSlug := url.PathEscape(slug)

	// Both chat and status use email+password login (no per-link token).
	links := []appLink{
		{
			Label:       "Chat with Kai",
			URL:         joinURL(s.chatBase, "/chat/"+encSlug),
			Description: "Talk to your project assistant.",
			Icon:        "💬",
		},
		{
			Label:       "Status",
			URL:         joinURL(s.statusBase, "/status/"+encSlug),
			Description: "Check whether your assistant is online.",
			Icon:        "🟢",
		},
	}

	// Per-tenant extra links from the KaiInstance annotation. The new key
	// is `swarm.emai.io/tenant-links` (TASK-024); the legacy
	// `swarm.emai.io/customer-links` is read as a fallback for one release.
	annotations := obj.GetAnnotations()
	raw, ok := annotations[annotationTenantLinks]
	if !ok || raw == "" {
		raw, ok = annotations[annotationLegacyCustomLinks]
	}
	if ok && raw != "" {
		var custom []appLink
		if err := json.Unmarshal([]byte(raw), &custom); err == nil {
			for i := range custom {
				custom[i].External = true
			}
			links = append(links, custom...)
		}
	}
	return links
}

// loadBriefings reads briefings from a per-customer ConfigMap. Any error or
// missing CM yields an empty list — briefings are optional.
func (s *server) loadBriefings(r *http.Request, slug string) []briefing {
	if s.core == nil {
		return nil
	}
	cmName := "kai-" + slug + "-briefings"
	cm, err := s.core.CoreV1().ConfigMaps(s.namespace).Get(r.Context(), cmName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	raw, ok := cm.Data["briefings.json"]
	if !ok {
		return nil
	}
	var list []briefing
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	// Newest first.
	sort.SliceStable(list, func(i, j int) bool {
		return list[i].Date > list[j].Date
	})
	return list
}

func joinURL(base, path string) string {
	if base == "" {
		return path
	}
	return strings.TrimRight(base, "/") + path
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

// demoData returns a canned, fully-populated centerResponse for any slug.
// Used for local previews and sales demos when no real cluster is available.
func demoData(slug string) centerResponse {
	customer := "Acme GmbH"
	if slug != "demo" && slug != "" {
		// Light personalization for non-default slugs.
		customer = strings.Title(strings.ReplaceAll(slug, "-", " "))
	}
	now := time.Now().UTC()
	d := func(daysAgo int) string {
		return now.Add(-time.Duration(daysAgo) * 24 * time.Hour).Format(time.RFC3339)
	}
	return centerResponse{
		CustomerName: customer,
		ProjectName:  "Robotik Pilot 2026",
		Slug:         slug,
		Status:       "online",
		StatusLabel:  "Online",
		Links: []appLink{
			{Label: "Chat with Kai", URL: "/chat/" + slug, Description: "Talk to your project assistant.", Icon: "💬"},
			{Label: "Status", URL: "/status/" + slug, Description: "Check whether your assistant is online.", Icon: "🟢"},
			{Label: "MissionControl", URL: "https://mc.emai.dev/" + slug, Description: "Project board: tasks, meetings, decisions.", Icon: "📋", External: true},
			{Label: "Robot fleet", URL: "https://neodem.emai.dev/" + slug, Description: "NeoDEM dashboard for your robots.", Icon: "🤖", External: true},
		},
		Channels: []channel{
			{Kind: "webchat", Label: "Web chat", Hint: "Use the link in 'Your apps'."},
			{Kind: "telegram", Label: "Telegram", Hint: "@AcmeKaiBot"},
		},
		Team: []teamMember{
			{Name: "Anna Schmidt", Role: "Project Lead", Company: customer, Email: "anna.schmidt@acme.de", Timezone: "Europe/Berlin"},
			{Name: "Tobias Weber", Role: "Robotics Engineer", Company: customer, Email: "tobias.weber@acme.de", Timezone: "Europe/Berlin"},
			{Name: "Markus Heuser", Role: "EmAI Project Lead", Company: "EmAI", Email: "markus@emai.dev", Timezone: "Europe/Berlin"},
			{Name: "On-call", Role: "Escalation", Company: "EmAI", Phone: "+49 30 12345678", Avatar: "🚨"},
		},
		Scope: "## What Kai handles for you\n\n- **Project tracking** — open tasks, deadlines, weekly status reports.\n- **Meeting prep** — agendas + briefings before each meeting.\n- **Documentation** — research notes, decisions, action items.\n- **Operational nudges** — reminders for things you have committed to.\n\n## Out of scope\n\nKai does *not* handle billing, contracts, or anything legal. Reach out to your EmAI lead for those.",
		Heartbeat: "- **Monday 09:00** — Weekly status briefing posted here.\n- **Wednesday** — Mid-week task triage in chat.\n- **Friday 16:00** — Sprint summary + next-week preview.\n- **Daily** — Telegram nudge if there is anything urgent waiting for you.",
		Briefings: []briefing{
			{
				ID:      "demo-week-17",
				Title:   "Weekly briefing — Week 17",
				Date:    d(2),
				Excerpt: "3 tasks completed, 2 in flight, milestone review on Thursday.",
				Body:    "## Highlights\n\n- **Migrated CI to ARM64** — ~30% faster builds.\n- **Operator reconciliation** for `KaiInstance` deployed to staging.\n- **SO-101 demo** ran successfully twice this week.\n\n## In flight\n\n1. Robot agent rewrite in Rust — 60% complete.\n2. New training-worker container — pending review.\n\n## Upcoming\n\n- **Thursday 14:00** — Milestone review with team.\n- **Monday** — Next sprint planning.",
			},
			{
				ID:      "demo-meeting-retro",
				Title:   "Meeting: SO-101 demo retrospective",
				Date:    d(8),
				Excerpt: "Team reviewed the demo. Two action items captured.",
				Body:    "### Decisions\n\n- Continue with the Rust agent rewrite.\n- Defer training-worker rollout to Q3.\n\n### Action items\n\n- [ ] Customer to share latest robot config (due in 1 week).\n- [ ] EmAI to write up calibration runbook (due in 2 weeks).",
			},
			{
				ID:      "demo-week-16",
				Title:   "Weekly briefing — Week 16",
				Date:    d(9),
				Excerpt: "Quiet week. Operator scaffolded, first reconcile loop working.",
				Body:    "## Highlights\n\n- Operator scaffold landed.\n- First reconcile loop turning a `KaiInstance` into a Deployment + ConfigMap.\n\n## Notes\n\nNo blockers from your side this week.",
			},
			{
				ID:      "demo-week-15",
				Title:   "Weekly briefing — Week 15",
				Date:    d(16),
				Excerpt: "Sprint planning + new dependency review.",
			},
			{
				ID:      "demo-kickoff",
				Title:   "Project kickoff",
				Date:    d(30),
				Excerpt: "Initial scoping meeting with the engineering team.",
			},
		},
	}
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
