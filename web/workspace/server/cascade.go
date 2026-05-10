package main

import (
	"context"
	"log"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/emai-ai/swarm/pkg/users"
)

// runDeletionCascade does the best-effort steps that must run BEFORE a
// User row is soft-deleted (TASK-021 Phase 3):
//
//  1. Cancel every billable Stripe subscription for the user's customer
//     ID. Runs first so we'd rather lose a half-deleted user record than
//     leave a recurring charge active.
//  2. Delete every KaiInstance labelled `swarm.io/user-id=<userID>`. The
//     operator's finalizer (TASK-003) cascades to child Deployments,
//     Services, ConfigMaps, PVCs; the cascade just requests deletion of
//     the parent CR.
//
// Errors on either step are logged but do NOT block the SoftDelete that
// follows in the caller. A user who clicked "delete my account" must
// always end up soft-deleted; lingering Stripe / cluster state is a
// follow-up for the daily reconciler — not a reason to refuse the user's
// right to erasure.
func (s *server) runDeletionCascade(ctx context.Context, u *users.User) {
	if u == nil {
		return
	}
	s.cancelUserStripeSubscriptions(u)
	s.deleteUserKaiInstances(ctx, u.ID)
}

// cancelUserStripeSubscriptions cancels every billable Stripe subscription
// for the user. No-op when Stripe isn't wired or the user never checked
// out (StripeCustomerID empty).
func (s *server) cancelUserStripeSubscriptions(u *users.User) {
	if s.stripe.Client == nil || u.StripeCustomerID == "" {
		return
	}
	ids, err := s.stripe.Client.ListActiveSubscriptions(u.StripeCustomerID)
	if err != nil {
		log.Printf("cascade: list subscriptions user=%s cust=%s: %v",
			u.ID, u.StripeCustomerID, err)
		return
	}
	for _, id := range ids {
		if _, err := s.stripe.Client.CancelSubscription(id); err != nil {
			log.Printf("cascade: cancel subscription user=%s sub=%s: %v",
				u.ID, id, err)
			continue
		}
		log.Printf("cascade: cancelled subscription user=%s sub=%s", u.ID, id)
	}
}

// deleteUserKaiInstances deletes every KaiInstance labelled with this
// user's ID. Best-effort per-CR — one failure doesn't block the rest.
func (s *server) deleteUserKaiInstances(ctx context.Context, userID string) {
	if s.dyn == nil || userID == "" {
		return
	}
	list, err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "swarm.io/user-id=" + userID,
	})
	if err != nil {
		log.Printf("cascade: list KaiInstances user=%s: %v", userID, err)
		return
	}
	for i := range list.Items {
		name := list.Items[i].GetName()
		if err := s.dyn.Resource(kaiInstanceGVR).Namespace(s.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			log.Printf("cascade: delete KaiInstance user=%s name=%s: %v",
				userID, name, err)
			continue
		}
		log.Printf("cascade: deleted KaiInstance user=%s name=%s", userID, name)
	}
}
