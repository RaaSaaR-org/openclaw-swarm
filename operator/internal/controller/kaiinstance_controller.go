/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	swarmv1alpha2 "github.com/emai-ai/swarm-operator/api/v1alpha2"
)

// KaiInstanceReconciler reconciles a KaiInstance object.
type KaiInstanceReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	IngressDomain    string // from env INGRESS_DOMAIN, default "kai.emai.dev"
	IngressTLSSecret string // from env INGRESS_TLS_SECRET, default "kai-emai-dev-tls"

	// PooledOpenRouterSecret, when set, makes every reconciled KaiInstance read its
	// OPENROUTER_API_KEY from one shared Secret in the same namespace instead of
	// the per-tenant `kai-<slug>-openrouter` Secret. Empty preserves the original
	// per-tenant wiring (BYOK-shaped) for backwards-compat with existing deploys.
	// Set via env var SWARM_POOLED_OPENROUTER_SECRET — see PROP-002 (pooled-only).
	PooledOpenRouterSecret string

	// CatalogDir is the on-disk path where the agents/catalog/ tree is mounted
	// (in production, a ConfigMap projected by the swarm-cloud / swarm-emai
	// overlay; in dev, the operator binary's parent directory's `agents/catalog`
	// path). Empty disables the catalog renderer — every workspace falls back
	// to the embedded customer-template, regardless of `spec.appRef`. Set via
	// env var KAI_CATALOG_DIR; default `/etc/swarm/catalog` (TASK-018 Phase 1).
	CatalogDir string

	// PerSlugSubdomain, when true, makes the operator render per-tenant
	// Ingresses with `<slug>.<IngressDomain>` host + `/ws` path instead of
	// the legacy shared `<IngressDomain>` host + `/ws/<slug>` path. Wildcard
	// cert + wildcard DNS from TASK-017 Phase 0 cover the new shape. The
	// flip changes the URL contract for every existing tenant — opt-in via
	// env var KAI_PER_SLUG_INGRESS so deploys can roll out safely
	// (existing tenants stay on path-based; SaaS-direction overlays opt
	// in). TASK-017 Phase 1.
	PerSlugSubdomain bool
}

// +kubebuilder:rbac:groups=swarm.emai.io,resources=kaiinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=swarm.emai.io,resources=kaiinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=swarm.emai.io,resources=kaiinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *KaiInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the KaiInstance CR
	var kai swarmv1alpha2.KaiInstance
	if err := r.Get(ctx, req.NamespacedName, &kai); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil // Deleted, ownerRef cascade handles cleanup
		}
		return ctrl.Result{}, err
	}

	// 2. Handle deletion (must precede slug derivation — deletion doesn't need a slug).
	// The finalizer holds the object until the operator confirms cleanup is done.
	// Today cleanup is a no-op (ownerRef cascade does the work); the hook reserves
	// space for future SaaS pre-delete logic (GDPR DSAR snapshot, billing, audit).
	if !kai.ObjectMeta.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&kai, swarmv1alpha2.KaiInstanceFinalizer) {
			log.Info("finalizing KaiInstance", "name", kai.Name)
			controllerutil.RemoveFinalizer(&kai, swarmv1alpha2.KaiInstanceFinalizer)
			if err := r.Update(ctx, &kai); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// 3. Ensure the finalizer is present. Re-queue immediately after adding it so
	// the next reconcile sees the updated object and proceeds with provisioning.
	if !controllerutil.ContainsFinalizer(&kai, swarmv1alpha2.KaiInstanceFinalizer) {
		controllerutil.AddFinalizer(&kai, swarmv1alpha2.KaiInstanceFinalizer)
		if err := r.Update(ctx, &kai); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 4. Resolve the workspace slug. Prefers spec.tenantSlug when set
	// (TASK-024 Phase 2), falls back to spec.customerSlug, falls back to a
	// slug derived from the effective name. The EffectiveSlug/EffectiveName
	// helpers route through the tenant-* fields first so the public swarm
	// repo's code path is tenant-clean even though v1alpha1 still carries
	// the legacy customer-* fields for backwards compat.
	slug := kai.Spec.TenantSlug
	if slug == "" {
		slug = slugify(kai.Spec.TenantName)
	}

	// Persist slug in status so it's stable
	if kai.Status.TenantSlug != slug {
		kai.Status.TenantSlug = slug
	}

	log.Info("reconciling KaiInstance", "slug", slug, "suspended", kai.Spec.Suspended)

	// 3. Handle suspended state
	if kai.Spec.Suspended {
		if err := r.reconcileSuspended(ctx, &kai, slug); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// 4. Set phase to Provisioning if not yet Running
	if kai.Status.Phase != swarmv1alpha2.PhaseRunning {
		kai.Status.Phase = swarmv1alpha2.PhaseProvisioning
	}

	// 5. Render templates
	model := defaultModel
	if kai.Spec.Model != "" {
		model = kai.Spec.Model
	}
	tmpl, err := renderAllTemplates(templateVars{
		TenantName:  kai.Spec.TenantName,
		TenantSlug:  slug,
		ProjectName: kai.Spec.ProjectName,
	}, templateOpts{
		CatalogDir: r.CatalogDir,
		AppRef:     kai.Spec.AppRef,
	})
	if err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "TemplateRenderError", err)
	}
	hash := configHash(tmpl, model)
	kai.Status.ConfigHash = hash

	// 6. Reconcile child resources
	if err := r.reconcileConfigMap(ctx, &kai, slug, tmpl); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "ConfigMapError", err)
	}
	if err := r.reconcileUsersSecret(ctx, &kai, slug); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "UsersSecretError", err)
	}
	if err := r.reconcileChatBridgeSecret(ctx, &kai, slug); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "ChatBridgeSecretError", err)
	}
	if err := r.reconcilePVC(ctx, &kai, slug); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "PVCError", err)
	}
	if err := r.reconcileDeployment(ctx, &kai, slug, hash); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "DeploymentError", err)
	}
	if err := r.reconcileService(ctx, &kai, slug); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "ServiceError", err)
	}
	if err := r.reconcileNetworkPolicy(ctx, &kai, slug); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "NetworkPolicyError", err)
	}
	if err := r.reconcileIngress(ctx, &kai, slug); err != nil {
		return ctrl.Result{}, r.setFailed(ctx, &kai, "IngressError", err)
	}

	// 7. Check deployment readiness
	ready := r.isDeploymentReady(ctx, kai.Namespace, slug)
	kai.Status.Ready = ready
	kai.Status.GatewayURL = gatewayURL(kai.Namespace, slug)
	// ExternalURL is gated on Ingress admission (TASK-017 Phase 2): the
	// ingress controller has populated `status.loadBalancer.ingress` so
	// the URL we publish is one the LB can actually route. While the LB
	// is still admitting we leave ExternalURL empty so consumers don't
	// hand users an address mid-cold-start.
	externalAccessEnabled := kai.Spec.ExternalAccess == nil || *kai.Spec.ExternalAccess
	if externalAccessEnabled && r.isIngressAdmitted(ctx, kai.Namespace, slug) {
		kai.Status.ExternalURL = externalURL(r.IngressDomain, slug, ingressOpts{PerSlugSubdomain: r.PerSlugSubdomain})
		r.setCondition(&kai, swarmv1alpha2.ConditionIngressReady, metav1.ConditionTrue, "Admitted", "Ingress admitted by controller")
	} else {
		kai.Status.ExternalURL = ""
		if externalAccessEnabled {
			r.setCondition(&kai, swarmv1alpha2.ConditionIngressReady, metav1.ConditionFalse, "Pending", "Waiting for ingress controller to admit the resource")
		}
	}
	if ready {
		kai.Status.Phase = swarmv1alpha2.PhaseRunning
	}

	// 8. Mark this generation as fully observed (only on the successful path —
	// setFailed paths leave ObservedGeneration at the last good generation, so
	// callers can detect "still working on the new spec").
	kai.Status.ObservedGeneration = kai.Generation

	// 9. Update status
	if err := r.Status().Update(ctx, &kai); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconciled KaiInstance", "slug", slug, "phase", kai.Status.Phase, "ready", ready)
	return ctrl.Result{}, nil
}

// reconcileSuspended scales the deployment to 0 and updates status.
func (r *KaiInstanceReconciler) reconcileSuspended(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	// Try to scale down the deployment if it exists
	var deploy appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: childName(slug), Namespace: kai.Namespace}, &deploy)
	if err == nil {
		zero := int32(0)
		if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != zero {
			deploy.Spec.Replicas = &zero
			if err := r.Update(ctx, &deploy); err != nil {
				return err
			}
		}
	}

	kai.Status.Phase = swarmv1alpha2.PhaseSuspended
	kai.Status.Ready = false
	return r.Status().Update(ctx, kai)
}

// reconcileConfigMap creates or updates the identity ConfigMap.
func (r *KaiInstanceReconciler) reconcileConfigMap(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string, tmpl *renderedTemplates) error {
	desired := buildConfigMap(kai, slug, tmpl)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha2.ConditionConfigMapReady, metav1.ConditionTrue, "Created", "ConfigMap created")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Data = desired.Data
	existing.Labels = desired.Labels
	r.setCondition(kai, swarmv1alpha2.ConditionConfigMapReady, metav1.ConditionTrue, "Updated", "ConfigMap up to date")
	return r.Update(ctx, &existing)
}

// reconcilePVC creates the PVC if it doesn't exist (PVCs are immutable after creation).
func (r *KaiInstanceReconciler) reconcilePVC(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	desired := buildPVC(kai, slug)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.PersistentVolumeClaim
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha2.ConditionPVCBound, metav1.ConditionFalse, "Creating", "PVC created, waiting for bind")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// PVC exists, check if bound
	if existing.Status.Phase == corev1.ClaimBound {
		r.setCondition(kai, swarmv1alpha2.ConditionPVCBound, metav1.ConditionTrue, "Bound", "PVC is bound")
	}
	return nil
}

// reconcileDeployment creates or updates the agent Deployment.
func (r *KaiInstanceReconciler) reconcileDeployment(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug, hash string) error {
	desired := buildDeployment(kai, slug, hash, deploymentOpts{PooledOpenRouterSecret: r.PooledOpenRouterSecret})
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha2.ConditionDeploymentAvailable, metav1.ConditionFalse, "Creating", "Deployment created")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update the deployment spec
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, &existing)
}

// reconcileService creates or updates the ClusterIP Service.
func (r *KaiInstanceReconciler) reconcileService(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	desired := buildService(kai, slug)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.Service
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Preserve ClusterIP on update
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	return r.Update(ctx, &existing)
}

// reconcileIngress creates, updates, or deletes the external access Ingress.
func (r *KaiInstanceReconciler) reconcileIngress(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	ingressName := childName(slug) + "-ws"

	// If external access is explicitly disabled, delete existing Ingress if present
	if kai.Spec.ExternalAccess != nil && !*kai.Spec.ExternalAccess {
		var existing networkingv1.Ingress
		err := r.Get(ctx, types.NamespacedName{Name: ingressName, Namespace: kai.Namespace}, &existing)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return r.Delete(ctx, &existing)
	}

	desired := buildIngress(kai, slug, r.IngressDomain, r.IngressTLSSecret, ingressOpts{PerSlugSubdomain: r.PerSlugSubdomain})
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing networkingv1.Ingress
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		// ConditionIngressReady stays False until isIngressAdmitted() reports
		// the controller has populated `status.loadBalancer.ingress` —
		// shipping `ExternalURL` while the LB is still booting would point
		// users at an address that doesn't resolve yet (TASK-017 Phase 2).
		r.setCondition(kai, swarmv1alpha2.ConditionIngressReady, metav1.ConditionFalse, "Pending", "Ingress created, waiting for controller admission")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	return r.Update(ctx, &existing)
}

// isIngressAdmitted reports whether the per-slug Ingress has been picked
// up by the cluster's ingress controller. Standard signal: the controller
// fills in `Ingress.Status.LoadBalancer.Ingress[]` once it has assigned
// the resource a routable address. Empty list = controller hasn't seen
// it yet; non-empty = ready to serve traffic.
//
// This is the gate for `kai.Status.ExternalURL` (TASK-017 Phase 2):
// publishing a URL the LB can't yet route would link users into a
// connection-refused / NXDOMAIN window during the cold-start race.
func (r *KaiInstanceReconciler) isIngressAdmitted(ctx context.Context, namespace, slug string) bool {
	if r.IngressDomain == "" {
		// No ingress domain configured → no LB → no admission to wait for.
		// The reconcile path skips Ingress creation entirely; ExternalURL
		// stays empty, which matches the "external access disabled" branch.
		return false
	}
	var ing networkingv1.Ingress
	err := r.Get(ctx, types.NamespacedName{Name: childName(slug) + "-ws", Namespace: namespace}, &ing)
	if err != nil {
		return false
	}
	return len(ing.Status.LoadBalancer.Ingress) > 0
}

// reconcileNetworkPolicy creates or updates the isolation NetworkPolicy.
func (r *KaiInstanceReconciler) reconcileNetworkPolicy(ctx context.Context, kai *swarmv1alpha2.KaiInstance, slug string) error {
	desired := buildNetworkPolicy(kai, slug)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing networkingv1.NetworkPolicy
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha2.ConditionNetworkPolicyApplied, metav1.ConditionTrue, "Created", "NetworkPolicy created")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	r.setCondition(kai, swarmv1alpha2.ConditionNetworkPolicyApplied, metav1.ConditionTrue, "Updated", "NetworkPolicy up to date")
	return r.Update(ctx, &existing)
}

// isDeploymentReady checks if the deployment has at least one available replica.
func (r *KaiInstanceReconciler) isDeploymentReady(ctx context.Context, namespace, slug string) bool {
	var deploy appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: childName(slug), Namespace: namespace}, &deploy)
	if err != nil {
		return false
	}
	return deploy.Status.AvailableReplicas > 0
}

// setFailed updates the phase to Failed and returns the original error.
func (r *KaiInstanceReconciler) setFailed(ctx context.Context, kai *swarmv1alpha2.KaiInstance, reason string, err error) error {
	kai.Status.Phase = swarmv1alpha2.PhaseFailed
	kai.Status.Ready = false
	r.setCondition(kai, "Ready", metav1.ConditionFalse, reason, err.Error())
	if statusErr := r.Status().Update(ctx, kai); statusErr != nil {
		logf.FromContext(ctx).Error(statusErr, "failed to update status after error")
	}
	return fmt.Errorf("%s: %w", reason, err)
}

// setCondition sets a condition on the KaiInstance status.
func (r *KaiInstanceReconciler) setCondition(kai *swarmv1alpha2.KaiInstance, condType string, status metav1.ConditionStatus, reason, message string) {
	condition := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	}

	for i, c := range kai.Status.Conditions {
		if c.Type == condType {
			if c.Status != status {
				kai.Status.Conditions[i] = condition
			}
			return
		}
	}
	kai.Status.Conditions = append(kai.Status.Conditions, condition)
}

// SetupWithManager sets up the controller with the Manager.
func (r *KaiInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&swarmv1alpha2.KaiInstance{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&networkingv1.Ingress{}).
		Named("kaiinstance").
		Complete(r)
}
