package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/emai-ai/swarm/pkg/auth"
	"github.com/emai-ai/swarm/pkg/users"
)

// kaiBinding is what the workspace login + auth handlers need from a
// KaiInstance: the management mode and (for SaaS-managed) the user reference.
// Returned by loadKaiBinding so callers don't pass unstructured around.
type kaiBinding struct {
	Managed   string // "saas" | "internal" | "" (legacy / unset)
	UserRef   string // u_<ulid> for SaaS; empty for internal/legacy
	Suspended bool   // mirrors spec.suspended; true → idle-suspend cron (or
	// admin) scaled this workspace to zero, login should resume it
}

// IsSaaS reports whether this workspace authenticates against the central
// users.Store (true) or the legacy per-tenant Secret (false). The contract:
// `managed: saas` AND a non-empty `userRef` together flip the SaaS path on.
// Either being absent (legacy customers from before TASK-014, or tenants
// migrated to managed:internal) keeps the legacy Secret-based login active.
func (b kaiBinding) IsSaaS() bool {
	return b.Managed == "saas" && b.UserRef != ""
}

// errKaiNotFound is returned by loadKaiBinding when the KaiInstance is missing.
// Callers map it to 401 (so probing slugs reveals nothing).
var errKaiNotFound = errors.New("kai not found")

// loadKaiBinding fetches the KaiInstance and extracts the two fields the auth
// path branches on. Demo mode short-circuits to a SaaS-shaped binding so the
// dev login flow exercises the SaaS path without hitting K8s.
func (s *server) loadKaiBinding(ctx context.Context, slug string) (kaiBinding, error) {
	if s.demoMode {
		return kaiBinding{Managed: "saas", UserRef: "u_demo"}, nil
	}
	if s.dyn == nil {
		return kaiBinding{}, errors.New("no dynamic client")
	}
	obj, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Get(ctx, "kai-"+slug, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return kaiBinding{}, errKaiNotFound
		}
		return kaiBinding{}, err
	}
	managed, _, _ := unstructured.NestedString(obj.Object, "spec", "managed")
	userRef, _, _ := unstructured.NestedString(obj.Object, "spec", "userRef")
	suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspended")
	return kaiBinding{Managed: managed, UserRef: userRef, Suspended: suspended}, nil
}

// resumeWorkspace patches the named KaiInstance's spec.suspended back to false.
// TASK-015 Phase 3.B: a successful SaaS login on a suspended workspace flips
// it back on so the user doesn't have to ask support to wake it. The operator
// picks up the spec change on its next reconcile (~10s) and scales the
// Deployment from 0 → 1. Caller should treat errors as best-effort: the login
// already succeeded, the user's data isn't at risk, and the worst case is
// "user retries in a minute". A merge patch is used so we never accidentally
// clobber other spec fields the operator may have written.
func (s *server) resumeWorkspace(ctx context.Context, slug string) error {
	if s.dyn == nil {
		return errors.New("no dynamic client")
	}
	patch := []byte(`{"spec":{"suspended":false}}`)
	_, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Patch(
		ctx, "kai-"+slug, types.MergePatchType, patch, metav1.PatchOptions{},
	)
	return err
}

// loginSaaS validates an email+password against the central users.Store and,
// on success, returns the User row so the caller can issue a session cookie
// keyed to both slug and userId.
//
// Errors are intentionally one-shape: every failure path (unknown email, bad
// password, unverified email, mismatched user/workspace) returns the same
// `errInvalidLogin` so the response body is uniform — no email-enumeration
// leak, no "wait, this email exists" timing oracle.
func (s *server) loginSaaS(ctx context.Context, slug, email, password string, binding kaiBinding) (*users.User, error) {
	if s.users == nil {
		return nil, errInvalidLogin
	}
	normalized := users.NormalizeEmail(email)
	u, err := s.users.GetByEmail(ctx, normalized)
	if err != nil {
		// Constant-time-ish: still hash a dummy candidate against a known-good
		// PHC string so the wall-clock cost matches a real-user mismatch.
		_ = auth.VerifyArgon2id(password, dummyArgonHash)
		return nil, errInvalidLogin
	}
	if !auth.VerifyArgon2id(password, u.PasswordHash) {
		return nil, errInvalidLogin
	}
	if u.EmailVerifiedAt == nil {
		return nil, errInvalidLogin
	}
	if u.DeletedAt != nil {
		return nil, errInvalidLogin
	}
	// The whole point of `userRef` on the KaiInstance is the workspace-ownership
	// check. Compare against the User row from the store, not against the JWT
	// the client just sent — this is the gate, not a downstream consumer.
	if !strings.EqualFold(u.ID, binding.UserRef) {
		return nil, errInvalidLogin
	}
	return u, nil
}

// errInvalidLogin is the single failure returned by every SaaS-login path.
// Login responses always render this as `{"error":"invalid login"}` plus a 401
// — wrong email / wrong password / unverified / not your workspace all look
// identical from outside.
var errInvalidLogin = errors.New("invalid login")

// dummyArgonHash is a known-valid argon2id PHC string used to keep the failure
// path's argon2 work-factor constant when the email is unknown. The salt and
// hash bytes are zero — never matches a real password (HashPassword refuses
// empty passwords + uses a random salt) — but the verify call still pays the
// ~64 MiB / ~3-iteration cost the real path would.
const dummyArgonHash = "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// ownedWorkspace is one row in the GET /owned-workspaces response.
type ownedWorkspace struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	ProjectName string `json:"projectName,omitempty"`
	Status      string `json:"status"`      // online | setting-up | paused | issue | unknown
	StatusLabel string `json:"statusLabel"` // human-friendly
	AppRef      string `json:"appRef,omitempty"`
	Current     bool   `json:"current,omitempty"` // matches the slug in the URL
}

type ownedWorkspacesResponse struct {
	Workspaces []ownedWorkspace `json:"workspaces"`
}

// handleOwnedWorkspaces lists every KaiInstance whose `swarm.io/user-id`
// label matches the signed-in user. Slug-scoped routing: the cookie was
// issued for <slug>, so the JWT must check out against that slug's secret;
// the URL slug is just an authentication anchor, the response contains the
// user's whole workspace list (current workspace included, marked).
//
// Legacy internal-managed sessions (claims.Uid empty) get an empty list —
// they have no central User row, so there's nothing to enumerate.
func (s *server) handleOwnedWorkspaces(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	if s.demoMode {
		writeJSON(w, http.StatusOK, demoOwnedWorkspaces(slug))
		return
	}
	claims, ok := s.authedClaims(r, slug)
	if !ok {
		writeUnauthorized(w)
		return
	}
	if claims.Uid == "" {
		writeJSON(w, http.StatusOK, ownedWorkspacesResponse{Workspaces: []ownedWorkspace{}})
		return
	}
	if s.dyn == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspaces backend unavailable"})
		return
	}
	list, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).List(r.Context(), metav1.ListOptions{
		LabelSelector: "swarm.io/user-id=" + claims.Uid,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	out := make([]ownedWorkspace, 0, len(list.Items))
	for i := range list.Items {
		obj := &list.Items[i]
		name := obj.GetName() // operator-rendered: "kai-<slug>"
		ws := strings.TrimPrefix(name, "kai-")
		display, _, _ := unstructured.NestedString(obj.Object, "spec", "customerName")
		project, _, _ := unstructured.NestedString(obj.Object, "spec", "projectName")
		appRef, _, _ := unstructured.NestedString(obj.Object, "spec", "appRef")
		status, label := translateStatus(obj)
		out = append(out, ownedWorkspace{
			Slug:        ws,
			Name:        display,
			ProjectName: project,
			Status:      status,
			StatusLabel: label,
			AppRef:      appRef,
			Current:     ws == slug,
		})
	}
	writeJSON(w, http.StatusOK, ownedWorkspacesResponse{Workspaces: out})
}

func demoOwnedWorkspaces(currentSlug string) ownedWorkspacesResponse {
	return ownedWorkspacesResponse{Workspaces: []ownedWorkspace{
		{Slug: currentSlug, Name: humanizeSlug(currentSlug), ProjectName: "Robotik Pilot 2026", Status: "online", StatusLabel: "Online", AppRef: "project-assistant", Current: true},
		{Slug: "side-project", Name: "Side Project", ProjectName: "Personal sandbox", Status: "online", StatusLabel: "Online", AppRef: "personal-assistant"},
		{Slug: "study-group", Name: "Study Group", ProjectName: "Master's thesis prep", Status: "setting-up", StatusLabel: "Setting up", AppRef: "study-buddy"},
	}}
}

// humanizeSlug turns "side-project" into "Side Project". Cheap enough that
// duplicating the workspace-server's existing humanize() rule here isn't
// worth a refactor; demo data only.
func humanizeSlug(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// ownerResponse is the body of GET /api/workspace/<slug>/owner. tier is empty
// for legacy internal-managed tenants where there's no central user record.
type ownerResponse struct {
	Email   string      `json:"email"`
	UserID  string      `json:"userId,omitempty"`
	Tier    users.Tier  `json:"tier,omitempty"`
	Managed string      `json:"managed,omitempty"` // "saas" | "internal" | ""
}

// handleOwner returns the signed-in user's profile so the SPA's "your
// workspaces" view (TASK-014 Phase 3) and the sidebar's signed-in-as label
// can show real data without a second round-trip to the user store. Always
// requires an authenticated session — there is no demo bypass for the body
// here, but demoMode short-circuits via authedClaims as it does elsewhere.
func (s *server) handleOwner(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !slugRegex.MatchString(slug) || len(slug) > 63 {
		writeUnauthorized(w)
		return
	}
	claims, ok := s.authedClaims(r, slug)
	if !ok {
		writeUnauthorized(w)
		return
	}

	resp := ownerResponse{Email: claims.Sub, UserID: claims.Uid}

	// Best-effort enrichment from the binding + store. Failures fall back to
	// just the JWT claims — the SPA renders fine with email alone.
	if binding, err := s.loadKaiBinding(r.Context(), slug); err == nil {
		resp.Managed = binding.Managed
		if binding.IsSaaS() && claims.Uid != "" && s.users != nil {
			if u, err := s.users.GetByID(r.Context(), claims.Uid); err == nil {
				resp.Tier = u.Tier
			}
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

