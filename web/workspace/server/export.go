package main

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emai-ai/swarm/pkg/stripe"
	"github.com/emai-ai/swarm/pkg/users"
)

// GDPR Art. 15 right-of-access export (TASK-021 Phase 4).
//
// One synchronous endpoint streams a ZIP back to the signed-in user with
// everything we hold about them that's reachable from the workspace
// binary's data plane:
//
//   - user.json        — the User row (without PasswordHash)
//   - kai-instances.json — every KaiInstance labelled `swarm.io/user-id=<uid>`
//   - stripe/invoices.json — every Stripe invoice for User.StripeCustomerID
//   - README.txt       — what's in the zip + how to interpret each file
//
// Chat history (PVC contents) and email-provider profile data are NOT
// included in this synchronous export — they require a different access
// path (init container into the per-tenant PVC, Resend API call) and
// land in a Phase 4.B follow-up that uploads to a 7-day signed URL bucket
// and emails the link.
//
// Each source is best-effort: a Stripe outage shouldn't block the export
// of the User row + KaiInstance specs. The README documents which pieces
// landed and which didn't via a per-source error block.

// exportUserView is the JSON shape we ship in user.json. PasswordHash is
// deliberately excluded — even the user's own argon2id hash is not part
// of an Art. 15 disclosure (it's not "personal data we hold about you",
// it's "the form in which we secure your authentication"). Everything
// else is fair game.
type exportUserView struct {
	ID               string  `json:"id"`
	Email            string  `json:"email"`
	Tier             string  `json:"tier"`
	StripeCustomerID string  `json:"stripeCustomerId,omitempty"`
	Language         string  `json:"language"`
	App              string  `json:"app"`
	CreatedAt        string  `json:"createdAt"`
	EmailVerifiedAt  *string `json:"emailVerifiedAt,omitempty"`
	EmailBouncedAt   *string `json:"emailBouncedAt,omitempty"`
	LastLoginAt      *string `json:"lastLoginAt,omitempty"`
}

// kaiInstanceView is the per-instance shape in kai-instances.json. We
// include the full spec + status from the unstructured object so the
// user has the complete CR they own.
type kaiInstanceView struct {
	Name        string         `json:"name"`
	Namespace   string         `json:"namespace"`
	Slug        string         `json:"slug"`
	CreatedAt   string         `json:"createdAt"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Spec        map[string]any `json:"spec,omitempty"`
	Status      map[string]any `json:"status,omitempty"`
}

// handleAccountExport streams the GDPR data export ZIP. Auth required;
// legacy internal-managed sessions get 403 (no central User row to export).
// Same auth rules as request-deletion.
func (s *server) handleAccountExport(w http.ResponseWriter, r *http.Request) {
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
	if claims.Uid == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "internal_managed_session"})
		return
	}
	if s.users == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "user store not configured"})
		return
	}
	u, err := s.users.GetByID(r.Context(), claims.Uid)
	if err != nil {
		log.Printf("export: lookup uid=%s: %v", claims.Uid, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}

	// Build the zip in-memory. A real GDPR export for an active SaaS user
	// is ~hundreds of KB at the high end (User row + a few KaiInstance
	// specs + a year of monthly invoices); streaming via memory is fine.
	// If chat history lands in a future phase, this becomes async + bucket.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kai-export-%s-%s.zip"`,
		slug, time.Now().UTC().Format("20060102-150405")))

	zw := zip.NewWriter(w)
	defer zw.Close()

	collectErrors := s.writeExportSources(r.Context(), zw, u)
	writeExportReadme(zw, slug, u, collectErrors)
}

// writeExportSources writes the three data files. Each source's error
// (if any) is collected so the README can list them.
func (s *server) writeExportSources(ctx context.Context, zw *zip.Writer, u *users.User) map[string]string {
	errs := map[string]string{}

	// 1. user.json
	if err := writeJSONEntry(zw, "user.json", buildExportUserView(u)); err != nil {
		errs["user.json"] = err.Error()
	}

	// 2. kai-instances.json
	if instances, err := s.listUserKaiInstances(ctx, u.ID); err != nil {
		errs["kai-instances.json"] = err.Error()
	} else if err := writeJSONEntry(zw, "kai-instances.json", instances); err != nil {
		errs["kai-instances.json"] = err.Error()
	}

	// 3. stripe/invoices.json — only when Stripe is wired AND the user
	// has a StripeCustomerID. No-op otherwise (a user who never upgraded
	// has nothing to disclose).
	if s.stripe.Client != nil && u.StripeCustomerID != "" {
		if invoices, err := s.stripe.Client.ListInvoices(u.StripeCustomerID); err != nil {
			errs["stripe/invoices.json"] = err.Error()
		} else if err := writeJSONEntry(zw, "stripe/invoices.json", invoices); err != nil {
			errs["stripe/invoices.json"] = err.Error()
		}
	}
	return errs
}

func (s *server) listUserKaiInstances(ctx context.Context, userID string) ([]kaiInstanceView, error) {
	if s.dyn == nil {
		return []kaiInstanceView{}, nil // no cluster wiring → empty list, not an error
	}
	list, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "swarm.io/user-id=" + userID,
	})
	if err != nil {
		return nil, fmt.Errorf("list KaiInstances: %w", err)
	}
	out := make([]kaiInstanceView, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		spec, _, _ := nestedMap(item.Object, "spec")
		status, _, _ := nestedMap(item.Object, "status")
		out = append(out, kaiInstanceView{
			Name:        item.GetName(),
			Namespace:   item.GetNamespace(),
			Slug:        strings.TrimPrefix(item.GetName(), "kai-"),
			CreatedAt:   item.GetCreationTimestamp().UTC().Format(time.RFC3339),
			Labels:      item.GetLabels(),
			Annotations: item.GetAnnotations(),
			Spec:        spec,
			Status:      status,
		})
	}
	return out, nil
}

// nestedMap is a tiny wrapper around k8s.io/apimachinery's
// `unstructured.NestedMap` that returns a default empty map (instead of
// nil) when the path is absent. Keeps the JSON shape consistent: the
// export always has `spec: {}` even when the field happens to be empty.
func nestedMap(obj map[string]any, fields ...string) (map[string]any, bool, error) {
	cur := obj
	for i, f := range fields {
		next, ok := cur[f]
		if !ok {
			return map[string]any{}, false, nil
		}
		if i == len(fields)-1 {
			if m, ok := next.(map[string]any); ok {
				return m, true, nil
			}
			return map[string]any{}, false, nil
		}
		m, ok := next.(map[string]any)
		if !ok {
			return map[string]any{}, false, nil
		}
		cur = m
	}
	return cur, true, nil
}

func writeJSONEntry(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func writeExportReadme(zw *zip.Writer, slug string, u *users.User, errs map[string]string) {
	w, err := zw.Create("README.txt")
	if err != nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Kai data export — generated %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "User:      %s (%s)\n", u.Email, u.ID)
	fmt.Fprintf(&b, "Workspace: %s\n\n", slug)
	fmt.Fprintln(&b, "Contents:")
	fmt.Fprintln(&b, "  user.json            — your User record (Email, Tier, Language, sign-up + verify timestamps)")
	fmt.Fprintln(&b, "  kai-instances.json   — every Kai workspace tied to your account (spec + status)")
	fmt.Fprintln(&b, "  stripe/invoices.json — every Stripe invoice issued to your account (only present if you have a paid plan)")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Not included in this synchronous export:")
	fmt.Fprintln(&b, "  - chat history (stored on per-workspace PVCs; available via async export — see /pricing)")
	fmt.Fprintln(&b, "  - email-provider bounce/complaint records (Resend; out-of-band on request)")
	fmt.Fprintln(&b, "  - your password hash (excluded by design — not your personal data)")
	if len(errs) > 0 {
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "Some sources could not be exported in this run:")
		for src, msg := range errs {
			fmt.Fprintf(&b, "  - %s: %s\n", src, msg)
		}
		fmt.Fprintln(&b, "Try the export again later, or contact support if the problem persists.")
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Under GDPR Art. 15 (Right of Access) you are entitled to a copy of the personal")
	fmt.Fprintln(&b, "data we process about you. This export is that copy. Under Art. 17 (Right to")
	fmt.Fprintln(&b, "Erasure) you can also delete your account from your dashboard.")
	_, _ = io.WriteString(w, b.String())
}

func buildExportUserView(u *users.User) exportUserView {
	view := exportUserView{
		ID:               u.ID,
		Email:            u.Email,
		Tier:             string(u.Tier),
		StripeCustomerID: u.StripeCustomerID,
		Language:         string(u.Language),
		App:              u.App,
		CreatedAt:        u.CreatedAt.UTC().Format(time.RFC3339),
	}
	if u.EmailVerifiedAt != nil {
		s := u.EmailVerifiedAt.UTC().Format(time.RFC3339)
		view.EmailVerifiedAt = &s
	}
	if u.EmailBouncedAt != nil {
		s := u.EmailBouncedAt.UTC().Format(time.RFC3339)
		view.EmailBouncedAt = &s
	}
	if u.LastLoginAt != nil {
		s := u.LastLoginAt.UTC().Format(time.RFC3339)
		view.LastLoginAt = &s
	}
	return view
}

// Compile-time guard: pkg/stripe.InvoiceSummary is what we serialize as
// stripe/invoices.json. If the field set ever changes, the JSON shape
// changes — keep it stable across releases or add a `version` field.
var _ stripe.InvoiceSummary = stripe.InvoiceSummary{}
