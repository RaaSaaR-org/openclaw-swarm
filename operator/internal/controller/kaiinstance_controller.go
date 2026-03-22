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

	swarmv1alpha1 "github.com/emai-ai/swarm-operator/api/v1alpha1"
)

// KaiInstanceReconciler reconciles a KaiInstance object.
type KaiInstanceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=swarm.emai.io,resources=kaiinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=swarm.emai.io,resources=kaiinstances/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=swarm.emai.io,resources=kaiinstances/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *KaiInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the KaiInstance CR
	var kai swarmv1alpha1.KaiInstance
	if err := r.Get(ctx, req.NamespacedName, &kai); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil // Deleted, ownerRef cascade handles cleanup
		}
		return ctrl.Result{}, err
	}

	// 2. Resolve the customer slug
	slug := kai.Spec.CustomerSlug
	if slug == "" {
		slug = slugify(kai.Spec.CustomerName)
	}

	// Persist slug in status so it's stable
	if kai.Status.CustomerSlug != slug {
		kai.Status.CustomerSlug = slug
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
	if kai.Status.Phase != swarmv1alpha1.PhaseRunning {
		kai.Status.Phase = swarmv1alpha1.PhaseProvisioning
	}

	// 5. Render templates
	model := defaultModel
	if kai.Spec.Model != "" {
		model = kai.Spec.Model
	}
	tmpl, err := renderAllTemplates(templateVars{
		CustomerName: kai.Spec.CustomerName,
		CustomerSlug: slug,
		ProjectName:  kai.Spec.ProjectName,
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

	// 7. Check deployment readiness
	ready := r.isDeploymentReady(ctx, kai.Namespace, slug)
	kai.Status.Ready = ready
	kai.Status.GatewayURL = gatewayURL(kai.Namespace, slug)
	if ready {
		kai.Status.Phase = swarmv1alpha1.PhaseRunning
	}

	// 8. Update status
	if err := r.Status().Update(ctx, &kai); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("reconciled KaiInstance", "slug", slug, "phase", kai.Status.Phase, "ready", ready)
	return ctrl.Result{}, nil
}

// reconcileSuspended scales the deployment to 0 and updates status.
func (r *KaiInstanceReconciler) reconcileSuspended(ctx context.Context, kai *swarmv1alpha1.KaiInstance, slug string) error {
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

	kai.Status.Phase = swarmv1alpha1.PhaseSuspended
	kai.Status.Ready = false
	return r.Status().Update(ctx, kai)
}

// reconcileConfigMap creates or updates the identity ConfigMap.
func (r *KaiInstanceReconciler) reconcileConfigMap(ctx context.Context, kai *swarmv1alpha1.KaiInstance, slug string, tmpl *renderedTemplates) error {
	desired := buildConfigMap(kai, slug, tmpl)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.ConfigMap
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha1.ConditionConfigMapReady, metav1.ConditionTrue, "Created", "ConfigMap created")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Data = desired.Data
	existing.Labels = desired.Labels
	r.setCondition(kai, swarmv1alpha1.ConditionConfigMapReady, metav1.ConditionTrue, "Updated", "ConfigMap up to date")
	return r.Update(ctx, &existing)
}

// reconcilePVC creates the PVC if it doesn't exist (PVCs are immutable after creation).
func (r *KaiInstanceReconciler) reconcilePVC(ctx context.Context, kai *swarmv1alpha1.KaiInstance, slug string) error {
	desired := buildPVC(kai, slug)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing corev1.PersistentVolumeClaim
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha1.ConditionPVCBound, metav1.ConditionFalse, "Creating", "PVC created, waiting for bind")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// PVC exists, check if bound
	if existing.Status.Phase == corev1.ClaimBound {
		r.setCondition(kai, swarmv1alpha1.ConditionPVCBound, metav1.ConditionTrue, "Bound", "PVC is bound")
	}
	return nil
}

// reconcileDeployment creates or updates the agent Deployment.
func (r *KaiInstanceReconciler) reconcileDeployment(ctx context.Context, kai *swarmv1alpha1.KaiInstance, slug, hash string) error {
	desired := buildDeployment(kai, slug, hash)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing appsv1.Deployment
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha1.ConditionDeploymentAvailable, metav1.ConditionFalse, "Creating", "Deployment created")
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
func (r *KaiInstanceReconciler) reconcileService(ctx context.Context, kai *swarmv1alpha1.KaiInstance, slug string) error {
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

// reconcileNetworkPolicy creates or updates the isolation NetworkPolicy.
func (r *KaiInstanceReconciler) reconcileNetworkPolicy(ctx context.Context, kai *swarmv1alpha1.KaiInstance, slug string) error {
	desired := buildNetworkPolicy(kai, slug)
	if err := controllerutil.SetControllerReference(kai, desired, r.Scheme); err != nil {
		return err
	}

	var existing networkingv1.NetworkPolicy
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, &existing)
	if apierrors.IsNotFound(err) {
		r.setCondition(kai, swarmv1alpha1.ConditionNetworkPolicyApplied, metav1.ConditionTrue, "Created", "NetworkPolicy created")
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	r.setCondition(kai, swarmv1alpha1.ConditionNetworkPolicyApplied, metav1.ConditionTrue, "Updated", "NetworkPolicy up to date")
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
func (r *KaiInstanceReconciler) setFailed(ctx context.Context, kai *swarmv1alpha1.KaiInstance, reason string, err error) error {
	kai.Status.Phase = swarmv1alpha1.PhaseFailed
	kai.Status.Ready = false
	r.setCondition(kai, "Ready", metav1.ConditionFalse, reason, err.Error())
	if statusErr := r.Status().Update(ctx, kai); statusErr != nil {
		logf.FromContext(ctx).Error(statusErr, "failed to update status after error")
	}
	return fmt.Errorf("%s: %w", reason, err)
}

// setCondition sets a condition on the KaiInstance status.
func (r *KaiInstanceReconciler) setCondition(kai *swarmv1alpha1.KaiInstance, condType string, status metav1.ConditionStatus, reason, message string) {
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
		For(&swarmv1alpha1.KaiInstance{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Named("kaiinstance").
		Complete(r)
}
