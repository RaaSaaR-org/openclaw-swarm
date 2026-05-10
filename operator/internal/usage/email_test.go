/*
Copyright 2026.
*/

package usage

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/emai-ai/swarm/pkg/email"
	"github.com/emai-ai/swarm/pkg/users"
)

// captureSender + fakeUserLookup are tiny in-memory fakes for the email
// branch. The Runner accepts these via its email-side seams; production
// wires `pkg/email.ResendSender` + a `pkg/users.Store` adapter.

type captureSender struct {
	last *email.Message
	err  error
}

func (c *captureSender) Send(_ context.Context, m email.Message) error {
	if c.err != nil {
		return c.err
	}
	c.last = &m
	return nil
}

type fakeUserLookup struct {
	byUID map[string]*users.User
}

func (f *fakeUserLookup) LookupByUID(_ context.Context, uid string) (*users.User, error) {
	return f.byUID[uid], nil
}

// kaiObjOwned is kaiObj + a swarm.io/user-id label so the email branch has
// a uid to look up.
func kaiObjOwned(slug, tier, uid string, suspended bool) *unstructured.Unstructured {
	o := kaiObj(slug, tier, suspended)
	labels := o.GetLabels()
	labels[LabelUserID] = uid
	o.SetLabels(labels)
	_ = unstructured.SetNestedField(o.Object, "Anna's "+slug, "spec", "tenantName")
	return o
}

func newRunnerWithEmail(t *testing.T, kais []*unstructured.Unstructured, secrets []*corev1.Secret, reader UsageReader, sender *captureSender, lookup *fakeUserLookup) *Runner {
	t.Helper()
	r := newRunner(t, kais, secrets, reader)
	r.Email = sender
	r.UserLookup = lookup
	r.UpgradeURL = "https://kai.example.org/billing"
	r.EmailFrom = "Kai <noreply@kai.example.org>"
	return r
}

func TestRun_WarnsAtEightyPercent(t *testing.T) {
	t.Parallel()
	kai := kaiObjOwned("anna", "starter", "u_anna", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	sender := &captureSender{}
	lookup := &fakeUserLookup{byUID: map[string]*users.User{
		"u_anna": {ID: "u_anna", Email: "anna@example.org", Language: users.LangDE, Tier: users.TierStarter},
	}}
	// Starter cap is $3; 80% = $2.40. Use $2.50 to land in the warn zone.
	r := newRunnerWithEmail(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 2.50}}, sender, lookup)

	results, _ := r.Run(context.Background())
	if results[0].Action != "ok" {
		t.Errorf("Action = %q, want ok (warn email is non-fatal — workspace stays running)", results[0].Action)
	}
	if sender.last == nil {
		t.Fatal("expected one email sent")
	}
	if sender.last.To != "anna@example.org" {
		t.Errorf("To = %q", sender.last.To)
	}
	// html/template auto-escapes the apostrophe → &#39; — match the rendered form.
	if want := "Anna&#39;s anna"; !contains(sender.last.HTML, want) {
		t.Errorf("HTML missing tenant name %q\n%s", want, sender.last.HTML)
	}
	if !contains(sender.last.Subject, "anna") {
		t.Errorf("Subject missing workspace context: %q", sender.last.Subject)
	}
	// Annotation should be stamped with today's UTC date.
	live, _ := r.Dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-anna", metav1.GetOptions{})
	if got := live.GetAnnotations()[AnnotationAlert]; got != "2026-05-10" {
		t.Errorf("AnnotationAlert = %q, want 2026-05-10", got)
	}
}

func TestRun_DoesNotReWarnSameDay(t *testing.T) {
	t.Parallel()
	kai := kaiObjOwned("anna", "starter", "u_anna", false)
	// Pre-stamp today's date so the second pass within the same UTC day
	// must skip the email.
	kai.SetAnnotations(map[string]string{AnnotationAlert: "2026-05-10"})

	sec := openrouterSecret("anna", "sk-or-anna")
	sender := &captureSender{}
	lookup := &fakeUserLookup{byUID: map[string]*users.User{
		"u_anna": {ID: "u_anna", Email: "anna@example.org", Language: users.LangDE, Tier: users.TierStarter},
	}}
	r := newRunnerWithEmail(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 2.80}}, sender, lookup)

	_, _ = r.Run(context.Background())
	if sender.last != nil {
		t.Errorf("must not re-email on a re-run within the same UTC day, got %+v", sender.last)
	}
}

func TestRun_EmailBranchSkipsBelowThreshold(t *testing.T) {
	t.Parallel()
	kai := kaiObjOwned("anna", "starter", "u_anna", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	sender := &captureSender{}
	lookup := &fakeUserLookup{byUID: map[string]*users.User{
		"u_anna": {ID: "u_anna", Email: "anna@example.org"},
	}}
	// Starter $3 cap; 50% = $1.50 — below threshold.
	r := newRunnerWithEmail(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 1.50}}, sender, lookup)

	_, _ = r.Run(context.Background())
	if sender.last != nil {
		t.Errorf("under-threshold workspace must not get the warning email, got %+v", sender.last)
	}
}

func TestRun_EmailBranchSkipsWhenSendersNotWired(t *testing.T) {
	t.Parallel()
	kai := kaiObjOwned("anna", "starter", "u_anna", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	// Default Runner has no email seams set — the Phase-3-only path.
	r := newRunner(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 2.50}})
	results, _ := r.Run(context.Background())
	if results[0].Action != "ok" {
		t.Errorf("Action = %q, want ok", results[0].Action)
	}
	// No annotation should have been stamped.
	live, _ := r.Dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-anna", metav1.GetOptions{})
	if got := live.GetAnnotations()[AnnotationAlert]; got != "" {
		t.Errorf("AnnotationAlert = %q, want unset (email branch not wired)", got)
	}
}

func TestRun_EmailFailureLogsButDoesNotAffectWorkspace(t *testing.T) {
	t.Parallel()
	kai := kaiObjOwned("anna", "starter", "u_anna", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	sender := &captureSender{err: errors.New("smtp 500: provider down")}
	lookup := &fakeUserLookup{byUID: map[string]*users.User{
		"u_anna": {ID: "u_anna", Email: "anna@example.org", Language: users.LangDE},
	}}
	r := newRunnerWithEmail(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 2.80}}, sender, lookup)
	results, _ := r.Run(context.Background())
	// Workspace stays "ok" — email failures don't escalate.
	if results[0].Action != "ok" {
		t.Errorf("Action = %q, want ok (email errors are non-fatal)", results[0].Action)
	}
	live, _ := r.Dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").Get(context.Background(), "kai-anna", metav1.GetOptions{})
	if got := live.GetAnnotations()[AnnotationAlert]; got != "2026-05-10" {
		// We stamp BEFORE the send to avoid double-fires on retry; that
		// means a send failure leaves the annotation set so we don't retry
		// today. The user gets the next day's email if usage is still high.
		t.Errorf("AnnotationAlert = %q, want stamped (annotation written before send to avoid double-email)", got)
	}
}

func TestRun_EmailLanguageFollowsUserPreference(t *testing.T) {
	t.Parallel()
	kai := kaiObjOwned("anna", "starter", "u_anna", false)
	sec := openrouterSecret("anna", "sk-or-anna")
	sender := &captureSender{}
	lookup := &fakeUserLookup{byUID: map[string]*users.User{
		"u_anna": {ID: "u_anna", Email: "anna@example.org", Language: users.LangEN, Tier: users.TierStarter},
	}}
	r := newRunnerWithEmail(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 2.80}}, sender, lookup)
	_, _ = r.Run(context.Background())
	if sender.last == nil {
		t.Fatal("expected email")
	}
	if !contains(sender.last.Subject, "quota") {
		t.Errorf("English subject expected, got %q", sender.last.Subject)
	}
}

func TestRun_NoOwningUserSkipsEmail(t *testing.T) {
	t.Parallel()
	// kaiObj (without Owned) → no swarm.io/user-id label → email branch
	// must skip silently. Pre-Phase-2 workspaces fall here.
	kai := kaiObj("anna", "starter", false)
	_ = unstructured.SetNestedField(kai.Object, "Anna's anna", "spec", "tenantName")
	sec := openrouterSecret("anna", "sk-or-anna")
	sender := &captureSender{}
	lookup := &fakeUserLookup{byUID: map[string]*users.User{}}
	r := newRunnerWithEmail(t, []*unstructured.Unstructured{kai}, []*corev1.Secret{sec},
		&fakeReader{usage: map[string]float64{"sk-or-anna": 2.80}}, sender, lookup)
	_, _ = r.Run(context.Background())
	if sender.last != nil {
		t.Errorf("workspace without user-id label must not trigger email, got %+v", sender.last)
	}
}

func TestResetAtFromNow_NextMidnightUTC(t *testing.T) {
	t.Parallel()
	cases := []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC), "2026-05-11 00:00 UTC"},
		{time.Date(2026, 5, 10, 23, 59, 0, 0, time.UTC), "2026-05-11 00:00 UTC"},
		{time.Date(2026, 12, 31, 22, 0, 0, 0, time.UTC), "2027-01-01 00:00 UTC"},
	}
	for _, c := range cases {
		if got := resetAtFromNow(c.now); got != c.want {
			t.Errorf("resetAtFromNow(%s) = %q, want %q", c.now, got, c.want)
		}
	}
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
