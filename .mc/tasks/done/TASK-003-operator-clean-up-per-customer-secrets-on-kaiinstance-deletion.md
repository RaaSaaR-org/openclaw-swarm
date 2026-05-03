---
id: TASK-003
aliases:
- TASK-003
title: 'Operator: clean up per-customer Secrets on KaiInstance deletion'
slug: operator-clean-up-per-customer-secrets-on-kaiinstance-deletion
status: done
priority: 2
owner: ''
projects: []
customers: []
tags:
- operator
- saas
sprint: ''
depends_on: []
due_date: ''
created: 2026-05-03
updated: 2026-05-03
---




# Operator: clean up per-customer Secrets on KaiInstance deletion

## Why
The operator creates per-customer Secrets (`kai-<slug>-chat-bridge` containing JWT secret + device keys, `kai-<slug>-users` containing argon2id-hashed credentials) but the controller relies on owner references for cascade deletion. If those Secrets are created without ownerRefs (or with the wrong ones), deleting a `KaiInstance` leaves orphaned credential material and user records — a privacy/security problem (right-to-deletion under GDPR is hard to satisfy if hashes linger). For a SaaS platform, "delete tenant" must mean "delete tenant" reliably.

## What
- Audit every Secret/ConfigMap/PVC the controller creates (`operator/internal/controller/kaiinstance_controller.go`) and confirm `controllerutil.SetControllerReference` is called for each.
- Add a finalizer on `KaiInstance` that deletes the user-data Secret and (optionally) snapshots/exports the PVC before removal — useful for SaaS "30-day data recovery" promises.
- Add `status.observedGeneration` so we can tell whether a deletion is in-flight.
- Cover with a controller test that creates a KaiInstance, verifies all child resources, deletes it, and asserts the namespace is empty.

## References
- `/Users/heussers/develop/emai/swarm/operator/api/v1alpha1/kaiinstance_types.go` (spec lines 25–70; status struct currently has no `observedGeneration`)
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/kaiinstance_controller.go` (Reconcile loop)
- `/Users/heussers/develop/emai/swarm/operator/internal/controller/secrets.go` (chat-bridge + users Secret creation)
- Kubebuilder finalizers: https://book.kubebuilder.io/reference/using-finalizers
- `controllerutil.SetControllerReference`: https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/controller/controllerutil#SetControllerReference

## Acceptance Criteria
- [ ] `kubectl delete kaiinstance kai-foo` removes all `kai-foo-*` Secrets, ConfigMap, PVC, NetworkPolicy, Service, Deployment, Ingress
- [ ] Finalizer logic is idempotent (re-running deletion doesn't error)
- [ ] `status.observedGeneration` matches `metadata.generation` after each successful reconcile
- [ ] New controller test asserts post-deletion namespace state

## Notes
PVC deletion is destructive — gate behind a `spec.deletionPolicy: Retain|Delete` field if users want backup-before-delete behaviour.
