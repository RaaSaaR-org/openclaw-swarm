package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"
)

// catalogEntry is one row of the GET /catalog response — the subset of
// metadata.yaml the dashboard needs to render a "switch persona" picker.
// Description fields are localized; the SPA picks DE/EN at render time.
type catalogEntry struct {
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	NameDe            string `json:"nameDe,omitempty"`
	Tier              string `json:"tier"`              // free | plus | pro
	Category          string `json:"category"`          // lifestyle | productivity | learning | creative | development
	ToolsProfile      string `json:"toolsProfile"`      // messaging | coding
	ShortDescription  string `json:"shortDescription,omitempty"`
	ShortDescriptionDe string `json:"shortDescriptionDe,omitempty"`
	RecommendedModel  string `json:"recommendedModel,omitempty"`
}

type catalogResponse struct {
	Apps []catalogEntry `json:"apps"`
}

// catalogFileShape is the metadata.yaml schema (subset). Marshalled into
// JSON tags so sigs.k8s.io/yaml (which routes through encoding/json) reads
// the camelCase keys exactly as written in the catalog files.
type catalogFileShape struct {
	Name               string `json:"name"`
	NameDe             string `json:"nameDe,omitempty"`
	Slug               string `json:"slug,omitempty"` // redundant with dirname; we trust the dir
	Category           string `json:"category"`
	ShortDescription   string `json:"shortDescription,omitempty"`
	ShortDescriptionDe string `json:"shortDescriptionDe,omitempty"`
	RecommendedModel   string `json:"recommendedModel,omitempty"`
	ToolsProfile       string `json:"toolsProfile,omitempty"`
	Tier               string `json:"tier,omitempty"`
}

var validAppSlug = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// catalogDir returns the directory the workspace pod reads catalog metadata
// from. Defaults to `/etc/swarm/catalog` (matches the operator's
// `KAI_CATALOG_DIR` default and the deployment-overlay ConfigMap mount). In
// dev / tests, set the env var to a working tree path like
// `agents/catalog`.
func (s *server) catalogDir() string {
	if dir := os.Getenv("KAI_CATALOG_DIR"); dir != "" {
		return dir
	}
	return "/etc/swarm/catalog"
}

// loadCatalog walks the catalog dir and returns one entry per subdirectory
// that contains a parseable metadata.yaml. Sorted by slug for stable
// rendering. Missing dir → empty slice (the deploy may not have mounted the
// catalog ConfigMap; better to return an empty list than 500 the dashboard).
func loadCatalog(root string) ([]catalogEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read catalog dir: %w", err)
	}
	out := make([]catalogEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, e.Name(), "metadata.yaml"))
		if err != nil {
			// Subdir without metadata.yaml is not a published app (e.g. a
			// README sibling). Skip silently rather than fail the whole list.
			continue
		}
		var meta catalogFileShape
		if err := yaml.Unmarshal(raw, &meta); err != nil {
			log.Printf("catalog: parse %s/metadata.yaml: %v", e.Name(), err)
			continue
		}
		slug := e.Name()
		if !validAppSlug.MatchString(slug) {
			log.Printf("catalog: skip non-DNS-safe dir name %q", slug)
			continue
		}
		out = append(out, catalogEntry{
			Slug:               slug,
			Name:               firstNonEmpty(meta.Name, slug),
			NameDe:             firstNonEmpty(meta.NameDe, meta.Name),
			Tier:               firstNonEmpty(meta.Tier, "free"),
			Category:           firstNonEmpty(meta.Category, "lifestyle"),
			ToolsProfile:       firstNonEmpty(meta.ToolsProfile, "messaging"),
			ShortDescription:   meta.ShortDescription,
			ShortDescriptionDe: firstNonEmpty(meta.ShortDescriptionDe, meta.ShortDescription),
			RecommendedModel:   meta.RecommendedModel,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// handleListCatalog is GET /api/workspace/{slug}/catalog. Slug-scoped (the
// cookie is bound to the URL slug); the response is the same catalog for
// every slug since the catalog is platform-wide. Empty slice when the
// catalog ConfigMap isn't mounted — the dashboard hides the picker in that
// case (no apps to switch to).
func (s *server) handleListCatalog(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusOK, demoCatalog())
		return
	}
	if _, ok := s.authedClaims(r, slug); !ok {
		writeUnauthorized(w)
		return
	}
	apps, err := loadCatalog(s.catalogDir())
	if err != nil {
		log.Printf("catalog list: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog read failed"})
		return
	}
	writeJSON(w, http.StatusOK, catalogResponse{Apps: apps})
}

// switchAppRequest is the body of PATCH /api/workspace/{slug}/app.
type switchAppRequest struct {
	AppRef string `json:"appRef"`
}

type switchAppResponse struct {
	Slug   string `json:"slug"`
	AppRef string `json:"appRef"`
}

// handleSwitchApp is PATCH /api/workspace/{slug}/app. Validates the new
// appRef against the catalog, then merge-patches `spec.appRef` and the
// `swarm.io/app` label on the KaiInstance. The operator picks up the change
// on the next reconcile and re-renders the workspace SOUL.md from the new
// catalog persona. Existing chat memory and PVC contents are untouched —
// the persona reset takes effect on the next session start (see
// TASK-018 Phase 4 acceptance criterion: "User can switch apps from
// customer-center, with confirmation prompt").
//
// Authorization: the workspace cookie must be valid for the URL slug AND
// the authenticated user must own this workspace (claims.Uid matches the
// CR's `swarm.io/user-id` label). Legacy internal-managed sessions
// (claims.Uid empty) are denied — switching personas on hand-managed
// internal tenants is an admin operation, not a user one.
func (s *server) handleSwitchApp(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusOK, switchAppResponse{Slug: slug, AppRef: "personal-assistant"})
		return
	}
	claims, ok := s.authedClaims(r, slug)
	if !ok {
		writeUnauthorized(w)
		return
	}
	if claims.Uid == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "internal_managed_workspace"})
		return
	}
	if s.dyn == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace backend unavailable"})
		return
	}

	var req switchAppRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	req.AppRef = strings.TrimSpace(req.AppRef)
	if !validAppSlug.MatchString(req.AppRef) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "appRef must be a DNS-safe slug"})
		return
	}
	apps, err := loadCatalog(s.catalogDir())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog read failed"})
		return
	}
	if !catalogHas(apps, req.AppRef) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "appRef not in catalog"})
		return
	}

	if err := s.patchKaiInstanceApp(r.Context(), slug, claims.Uid, req.AppRef); err != nil {
		if errors.Is(err, errWorkspaceNotOwned) {
			writeUnauthorized(w)
			return
		}
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
			return
		}
		log.Printf("switch app: patch %s -> %s: %v", slug, req.AppRef, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "patch failed"})
		return
	}
	writeJSON(w, http.StatusOK, switchAppResponse{Slug: slug, AppRef: req.AppRef})
}

// errWorkspaceNotOwned is returned by patchKaiInstanceApp when the
// authenticated user's Uid doesn't match the CR's user-id label. We map it
// to 401 (uniform with cookie-missing) so probing can't enumerate which
// slugs exist for which users.
var errWorkspaceNotOwned = errors.New("workspace not owned by this user")

func catalogHas(apps []catalogEntry, slug string) bool {
	for _, a := range apps {
		if a.Slug == slug {
			return true
		}
	}
	return false
}

// patchKaiInstanceApp validates ownership, then merge-patches spec.appRef +
// the swarm.io/app label. The operator's reconcile loop watches the CR for
// generation changes; the next loop re-renders the workspace SOUL.md from
// the new catalog persona.
func (s *server) patchKaiInstanceApp(ctx context.Context, slug, userUID, appRef string) error {
	obj, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Get(ctx, "kai-"+slug, metav1.GetOptions{})
	if err != nil {
		return err
	}
	labels := obj.GetLabels()
	if labels["swarm.io/user-id"] != userUID {
		return errWorkspaceNotOwned
	}
	patch := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]any{
				"swarm.io/app": appRef,
			},
		},
		"spec": map[string]any{
			"appRef": appRef,
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal patch: %w", err)
	}
	_, err = s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Patch(ctx, "kai-"+slug, types.MergePatchType, body, metav1.PatchOptions{})
	return err
}

// demoCatalog is the demoMode response — three canned entries so local dev
// (without a mounted catalog ConfigMap) can still render the picker.
func demoCatalog() catalogResponse {
	return catalogResponse{Apps: []catalogEntry{
		{Slug: "personal-assistant", Name: "Personal Assistant", NameDe: "Persönlicher Assistent", Tier: "free", Category: "lifestyle", ToolsProfile: "messaging", ShortDescription: "Day-to-day helper.", ShortDescriptionDe: "Alltagshelfer."},
		{Slug: "coding-helper", Name: "Coding Helper", NameDe: "Code-Helfer", Tier: "free", Category: "development", ToolsProfile: "coding", ShortDescription: "Reads code, suggests fixes.", ShortDescriptionDe: "Liest Code, schlaegt Fixes vor."},
		{Slug: "writing-coach", Name: "Writing Coach", NameDe: "Schreibcoach", Tier: "free", Category: "creative", ToolsProfile: "messaging", ShortDescription: "Edits drafts, gives feedback.", ShortDescriptionDe: "Bearbeitet Drafts, gibt Feedback."},
	}}
}
