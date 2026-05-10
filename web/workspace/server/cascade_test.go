package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/emai-ai/swarm/pkg/users"
)

// TestCascade_NilStripeNilDyn — runDeletionCascade is a no-op when nothing's
// wired. Proves the cascade can't crash a workspace running without a
// cluster connection (dev-mode binary).
func TestCascade_NilStripeNilDyn(t *testing.T) {
	t.Parallel()
	s := &server{namespace: "swarm-system"}
	u := &users.User{ID: "u_alice", StripeCustomerID: "cus_test"}
	// Should not panic.
	s.runDeletionCascade(context.Background(), u)
}

// TestCascade_NilUser — defensive guard: a nil User pointer must not panic
// the handler if SoftDelete races us.
func TestCascade_NilUser(t *testing.T) {
	t.Parallel()
	s := &server{namespace: "swarm-system"}
	s.runDeletionCascade(context.Background(), nil)
}

// TestCascade_DeletesOnlyOwnerKaiInstances — the cascade lists by label
// `swarm.io/user-id=<userID>` so a multi-tenant cluster can't have one
// user's deletion take out another user's workspaces.
func TestCascade_DeletesOnlyOwnerKaiInstances(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "project-assistant", true),
		kaiObj("side", "Side", "Personal", userID, "personal-assistant", true),
		kaiObj("not-mine", "Not Mine", "Other", "u_bob", "writing-coach", true),
	})

	f.server.runDeletionCascade(context.Background(), &users.User{ID: userID})

	// alice's KaiInstances should be gone.
	for _, gone := range []string{"kai-primary", "kai-side"} {
		_, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").
			Get(context.Background(), gone, metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Errorf("expected %s deleted, got err=%v", gone, err)
		}
	}
	// bob's KaiInstance must still be there.
	if _, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").
		Get(context.Background(), "kai-not-mine", metav1.GetOptions{}); err != nil {
		t.Errorf("expected kai-not-mine preserved, got err=%v", err)
	}
}

// TestCascade_NoMatchingInstances — user has a Stripe customer but no
// KaiInstances (signed up, never provisioned). Cascade is a no-op on the
// K8s side; no error.
func TestCascade_NoMatchingInstances(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("not-mine", "Not Mine", "Other", "u_bob", "writing-coach", true),
	})
	// Cascade for a user with no instances — no-op.
	f.server.runDeletionCascade(context.Background(), &users.User{ID: userID})

	// bob's instance still there.
	if _, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").
		Get(context.Background(), "kai-not-mine", metav1.GetOptions{}); err != nil {
		t.Errorf("kai-not-mine should be preserved, got err=%v", err)
	}
}

// TestCascade_EmptyStripeCustomerID — user never checked out (no Stripe
// customer). Stripe branch is skipped; KaiInstance branch still runs.
func TestCascade_EmptyStripeCustomerID(t *testing.T) {
	t.Parallel()
	const userID = "u_alice"
	f := newFixtureWithKaiObjects(t, "primary", userID, []*unstructured.Unstructured{
		kaiObj("primary", "Primary", "Robotik", userID, "project-assistant", true),
	})
	// stripe.Client is nil → first branch skipped; user.StripeCustomerID = ""
	// → would also be skipped if Client were set. K8s branch still runs.
	f.server.runDeletionCascade(context.Background(), &users.User{ID: userID, StripeCustomerID: ""})

	_, err := f.server.dyn.Resource(kaiInstanceGVR).Namespace("swarm-system").
		Get(context.Background(), "kai-primary", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected kai-primary deleted, got err=%v", err)
	}
}

// TestConfirmDeletion_RunsCascadeAndSendsEmail — end-to-end: confirm-deletion
// dispatches the post-delete `account-deleted` email and soft-deletes the
// user. The K8s cascade is exercised in TestCascade_* directly; this test
// covers the wire-up from handleConfirmDeletion → runDeletionCascade →
// SoftDelete → email.
func TestConfirmDeletion_RunsCascadeAndSendsEmail(t *testing.T) {
	t.Parallel()
	f, alice, cap := newDeletionFixture(t, "primary")

	tok, err := f.server.signDeletionToken("primary", alice.ID, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet,
		"/api/workspace/primary/account/confirm-deletion?id="+alice.ID+"&token="+url.QueryEscape(tok), nil)
	req.SetPathValue("slug", "primary")
	rr := httptest.NewRecorder()
	f.server.handleConfirmDeletion(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d (%s)", rr.Code, rr.Body.String())
	}

	// Phase 1.B improvement (TASK-021 Phase 3 wire-up): the post-delete
	// email now reliably fires because the pre-delete User snapshot is
	// captured before SoftDelete strips it from MemoryStore.GetByID.
	if len(cap.all) != 1 {
		t.Fatalf("expected 1 post-delete email, got %d", len(cap.all))
	}
	if cap.all[0].To != "alice@example.org" {
		t.Errorf("To = %q", cap.all[0].To)
	}

	// SoftDelete happened — alice no longer reachable via active-only
	// GetByID. (PoolStore audit row would still exist via LookupDeletion.)
	if _, err := f.server.users.GetByID(context.Background(), alice.ID); err == nil {
		t.Errorf("expected alice GetByID to return ErrNotFound after SoftDelete")
	}
}
