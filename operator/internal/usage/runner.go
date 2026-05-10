/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package usage runs the daily usage-monitor pass that suspends
// SaaS-enrolled workspaces whose per-workspace OpenRouter usage has
// exceeded the tier's `DailyDollars` cap (TASK-019 Phase 3).
//
// Architecture: a thin Runner that's small enough to test as plain Go,
// driven by interfaces for the K8s + OpenRouter sides so tests don't need
// real clients. The cmd/usage-monitor entrypoint wires real clientset +
// dynamic + REST clients into the Runner. The CronJob in
// `operator/config/cronjob/` schedules it once a day at 00:30 UTC (just
// after OpenRouter's daily reset at 00:00 UTC, so the previous day's
// usage is still readable).
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/openrouter"
	"github.com/emai-ai/swarm/pkg/quotas"
	"github.com/emai-ai/swarm/pkg/users"
)

// kaiInstanceGVR is the v1alpha2 GVR. Same as the operator main; duplicated
// here to keep `internal/usage` independent of the controller package.
var kaiInstanceGVR = schema.GroupVersionResource{
	Group:    "swarm.emai.io",
	Version:  "v1alpha2",
	Resource: "kaiinstances",
}

// LabelManaged tags SaaS-enrolled workspaces. Only those participate in the
// auto-suspend pass; legacy + internal-managed tenants are sized by hand and
// out of scope here.
const (
	LabelManaged   = "swarm.io/managed"
	LabelUserID    = "swarm.io/user-id"
	ManagedSaaS    = "saas"
	AnnotationLast  = "swarm.io/last-usage-check"   // RFC3339 timestamp of last successful poll
	AnnotationOver  = "swarm.io/usage-suspended-at" // RFC3339 timestamp of suspend
	AnnotationAlert = "swarm.io/last-usage-alert"   // YYYY-MM-DD UTC date of last 80%-of-cap email
)

// WarnThreshold is the fraction of `quotas.For(tier).DailyDollars` at which
// the Runner sends the `usage-warning` email. 0.8 (80%) gives the user a
// signal before the suspend lands at 100%.
const WarnThreshold = 0.8

// UsageReader is the OpenRouter side of the Runner — abstracted so tests
// can drive deterministic numbers without httptest fixtures. The real
// implementation wraps `pkg/openrouter.Client.GetKey` and remembers the
// per-workspace api-key it was constructed with.
type UsageReader interface {
	UsageDailyUSD(ctx context.Context, apiKey string) (float64, error)
}

// realUsageReader is the production implementation. Each call constructs a
// fresh `openrouter.Client` because each workspace has a different api-key —
// the read endpoint authenticates as the key it's reading.
type realUsageReader struct{}

func (realUsageReader) UsageDailyUSD(ctx context.Context, apiKey string) (float64, error) {
	c, err := openrouter.NewClient(apiKey)
	if err != nil {
		return 0, err
	}
	info, err := c.GetKey(ctx)
	if err != nil {
		return 0, err
	}
	return info.UsageDaily, nil
}

// NewRealUsageReader returns the wire-real reader for the operator entry point.
func NewRealUsageReader() UsageReader { return realUsageReader{} }

// UserLookup is the seam for resolving a workspace owner's email + language
// preference from the `swarm.io/user-id` label. Optional — leave nil to
// disable the email-warn branch entirely. The real implementation in the
// swarm-cloud overlay wraps `pkg/users.Store`.
type UserLookup interface {
	LookupByUID(ctx context.Context, uid string) (*users.User, error)
}

// Runner is the testable unit. K8s side is split: dynamic client for
// listing/patching KaiInstances; typed client for reading the per-tenant
// Secret. Namespace is the namespace SaaS workspaces live in.
//
// The four optional Email* / UserLookup / UpgradeURL fields together enable
// the 80%-of-cap warning email branch (TASK-019 Phase 5). All four must be
// non-nil/non-empty for the branch to fire; otherwise the suspend-at-cap
// path runs alone (matches Phase 3 behavior).
type Runner struct {
	Dyn       dynamic.Interface
	Core      kubernetes.Interface
	Namespace string
	Reader    UsageReader
	Now       func() time.Time // overridable for tests

	Email      email.Sender
	UserLookup UserLookup
	UpgradeURL string // e.g. "https://kai.example.org/billing"
	EmailFrom  string // optional From header override; empty falls back to pkg/email default
}

// Result captures one workspace's pass outcome — what happened, why. The
// Runner aggregates results so the cron pod's logs have one line per
// workspace and ops can grep for "suspended" / "skipped".
type Result struct {
	Slug        string
	Tier        string
	UsageDaily  float64
	CapDollars  float64
	Action      string // "ok" | "suspended" | "skipped" | "error"
	Reason      string
}

// Run does one pass over every saas-managed KaiInstance. Errors on a single
// workspace are recorded in the Result and don't abort the pass — one bad
// key shouldn't keep the rest from getting their suspends.
func (r *Runner) Run(ctx context.Context) ([]Result, error) {
	now := r.now()
	list, err := r.Dyn.Resource(kaiInstanceGVR).Namespace(r.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelManaged + "=" + ManagedSaaS,
	})
	if err != nil {
		return nil, fmt.Errorf("list KaiInstances: %w", err)
	}
	out := make([]Result, 0, len(list.Items))
	for i := range list.Items {
		obj := &list.Items[i]
		out = append(out, r.runOne(ctx, obj, now))
	}
	return out, nil
}

func (r *Runner) runOne(ctx context.Context, obj *unstructured.Unstructured, now time.Time) Result {
	slug, _ := obj.GetLabels()["swarm.io/tenant"]
	if slug == "" {
		// Fall back to deriving from the object name (`kai-<slug>`).
		slug = strings.TrimPrefix(obj.GetName(), "kai-")
	}
	tier, _, _ := unstructured.NestedString(obj.Object, "spec", "tier")
	cap := quotas.For(quotas.Tier(tier)).DailyDollars
	res := Result{Slug: slug, Tier: tier, CapDollars: cap}

	if cap <= 0 {
		res.Action = "skipped"
		res.Reason = "tier has no DailyDollars cap (enterprise / unbounded)"
		return res
	}

	// Already suspended? Skip — operator's reconcile loop will react to
	// status, and we don't want to ping OpenRouter for paused workspaces.
	if suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspended"); suspended {
		res.Action = "skipped"
		res.Reason = "already suspended"
		return res
	}

	apiKey, err := r.readWorkspaceAPIKey(ctx, slug)
	if err != nil {
		res.Action = "error"
		res.Reason = "read api-key: " + err.Error()
		return res
	}
	if apiKey == "" {
		res.Action = "skipped"
		res.Reason = "no per-workspace api-key configured (pooled-key fallback in effect)"
		return res
	}

	usage, err := r.Reader.UsageDailyUSD(ctx, apiKey)
	if err != nil {
		res.Action = "error"
		res.Reason = "read usage: " + err.Error()
		return res
	}
	res.UsageDaily = usage

	if usage >= cap {
		if err := r.suspend(ctx, obj, now); err != nil {
			res.Action = "error"
			res.Reason = "patch suspended: " + err.Error()
			return res
		}
		res.Action = "suspended"
		res.Reason = fmt.Sprintf("usage $%.4f >= cap $%.2f", usage, cap)
		return res
	}

	// 80%-of-cap warning email (TASK-019 Phase 5). Only fires when all four
	// email-side seams are wired AND the workspace is within the
	// [WarnThreshold*cap, cap) zone AND we haven't already alerted today.
	if usage >= WarnThreshold*cap && r.canEmail() {
		if err := r.maybeWarnAtThreshold(ctx, obj, usage, cap, now); err != nil {
			// Email failures are non-fatal — log and continue to the regular
			// "ok" path. Workspace remains operational; the user just doesn't
			// get the heads-up this run.
			log.Printf("usage-monitor warn email for slug=%s failed (continuing): %v", res.Slug, err)
		}
	}

	if err := r.stampLastChecked(ctx, obj, now); err != nil {
		// Non-fatal — surface as a warning in the log, but the workspace is fine.
		res.Action = "ok"
		res.Reason = fmt.Sprintf("usage $%.4f < cap $%.2f (annotation patch failed: %v)", usage, cap, err)
		return res
	}
	res.Action = "ok"
	res.Reason = fmt.Sprintf("usage $%.4f < cap $%.2f", usage, cap)
	return res
}

// readWorkspaceAPIKey pulls the api-key field from the per-tenant
// `kai-<slug>-openrouter` Secret. Empty string + nil error means the Secret
// is missing — workspace is on the pooled-key fallback and out of scope for
// per-workspace tracking.
func (r *Runner) readWorkspaceAPIKey(ctx context.Context, slug string) (string, error) {
	name := "kai-" + slug + "-openrouter"
	sec, err := r.Core.CoreV1().Secrets(r.Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if v, ok := sec.Data[corev1.ServiceAccountTokenKey]; ok && len(v) > 0 {
		_ = v // unused; placeholder
	}
	if v, ok := sec.Data["api-key"]; ok && len(v) > 0 {
		return string(v), nil
	}
	if v, ok := sec.StringData["api-key"]; ok && v != "" {
		return v, nil
	}
	return "", nil
}

// suspend merge-patches `spec.suspended=true` + the timestamp annotation in
// one round-trip. The timestamp anchors the future "warn at 80%" email so
// we don't re-suspend (and re-email) within the same UTC day.
func (r *Runner) suspend(ctx context.Context, obj *unstructured.Unstructured, now time.Time) error {
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				AnnotationOver: now.UTC().Format(time.RFC3339),
				AnnotationLast: now.UTC().Format(time.RFC3339),
			},
		},
		"spec": map[string]any{"suspended": true},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = r.Dyn.Resource(kaiInstanceGVR).Namespace(obj.GetNamespace()).
		Patch(ctx, obj.GetName(), types.MergePatchType, body, metav1.PatchOptions{})
	return err
}

func (r *Runner) stampLastChecked(ctx context.Context, obj *unstructured.Unstructured, now time.Time) error {
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				AnnotationLast: now.UTC().Format(time.RFC3339),
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = r.Dyn.Resource(kaiInstanceGVR).Namespace(obj.GetNamespace()).
		Patch(ctx, obj.GetName(), types.MergePatchType, body, metav1.PatchOptions{})
	return err
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// canEmail reports whether the optional email-warn branch is fully wired.
// All four seams must be non-nil/non-empty — partial wiring (sender but no
// user lookup, etc.) is treated as "not configured" so a deploy that only
// wants the suspend-at-cap behavior doesn't accidentally start sending half-
// resolved emails.
func (r *Runner) canEmail() bool {
	return r.Email != nil && r.UserLookup != nil && r.UpgradeURL != ""
}

// maybeWarnAtThreshold sends the usage-warning email for one workspace IF
// today's UTC date doesn't already match the `swarm.io/last-usage-alert`
// annotation. Idempotency is at-most-once per UTC day per workspace —
// re-running the cron later in the same day (failure retry, etc.) doesn't
// re-spam the user.
//
// Order of operations: stamp the annotation FIRST, then send the email.
// That way a sender that succeeds-then-times-out won't double-fire on
// retry. (The reverse — send first, stamp after — risks a duplicate email
// if the stamp patch fails after a successful send.)
func (r *Runner) maybeWarnAtThreshold(ctx context.Context, obj *unstructured.Unstructured, usage, cap float64, now time.Time) error {
	today := now.UTC().Format("2006-01-02")
	if last, _ := obj.GetAnnotations()[AnnotationAlert]; last == today {
		return nil // already warned today
	}

	uid, _ := obj.GetLabels()[LabelUserID]
	if uid == "" {
		// Pre-Phase-2 workspaces (no user-id label) don't have a User row to
		// look up. Skip silently — they're managed by hand anyway.
		return nil
	}
	user, err := r.UserLookup.LookupByUID(ctx, uid)
	if err != nil {
		return fmt.Errorf("user lookup uid=%s: %w", uid, err)
	}
	if user == nil || user.Email == "" {
		return nil // unknown user — no recipient
	}

	tenantName, _, _ := unstructured.NestedString(obj.Object, "spec", "tenantName")
	if tenantName == "" {
		tenantName = strings.TrimPrefix(obj.GetName(), "kai-")
	}

	pct := int(usage / cap * 100)
	if pct < 80 {
		pct = 80 // floor at 80% so the subject line never reads "79% used"
	}
	if pct > 99 {
		pct = 99 // we only fire below the 100% suspend threshold
	}

	if err := r.stampAlertAnnotation(ctx, obj, today); err != nil {
		return fmt.Errorf("stamp annotation: %w", err)
	}

	lang := email.LangDE
	if user.Language == users.LangEN {
		lang = email.LangEN
	}
	return email.Dispatch(ctx, r.Email, email.SendOptions{
		Template: email.TemplateUsageWarning,
		Lang:     lang,
		To:       user.Email,
		From:     r.EmailFrom,
	}, struct {
		Name          string
		WorkspaceName string
		UsedPct       int
		ResetAt       string
		UpgradeURL    string
	}{
		Name:          strings.SplitN(user.Email, "@", 2)[0],
		WorkspaceName: tenantName,
		UsedPct:       pct,
		ResetAt:       resetAtFromNow(now),
		UpgradeURL:    r.UpgradeURL,
	})
}

// resetAtFromNow returns the human-readable next-reset string for the
// usage-warning email. OpenRouter resets daily at 00:00 UTC; we compute the
// "next 00:00 UTC" relative to `now` so the email always shows a concrete
// time within ~24 hours of receipt.
func resetAtFromNow(now time.Time) string {
	t := now.UTC()
	next := time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
	return next.Format("2006-01-02 15:04 UTC")
}

func (r *Runner) stampAlertAnnotation(ctx context.Context, obj *unstructured.Unstructured, date string) error {
	patch := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				AnnotationAlert: date,
			},
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = r.Dyn.Resource(kaiInstanceGVR).Namespace(obj.GetNamespace()).
		Patch(ctx, obj.GetName(), types.MergePatchType, body, metav1.PatchOptions{})
	return err
}
