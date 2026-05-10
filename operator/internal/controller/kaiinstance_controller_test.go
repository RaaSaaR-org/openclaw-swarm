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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	swarmv1alpha2 "github.com/emai-ai/swarm-operator/api/v1alpha2"
)

var _ = Describe("KaiInstance Controller", func() {
	const resourceName = "test-resource"

	ctx := context.Background()
	typeNamespacedName := types.NamespacedName{Name: resourceName, Namespace: "default"}

	newReconciler := func() *KaiInstanceReconciler {
		return &KaiInstanceReconciler{
			Client:           k8sClient,
			Scheme:           k8sClient.Scheme(),
			IngressDomain:    "kai.emai.dev",
			IngressTLSSecret: "kai-emai-dev-tls",
		}
	}

	// reconcileUntilFinalized loops until the object is gone (finalizer removed +
	// envtest GC). Bounded by attempt count so a regression doesn't hang the suite.
	reconcileUntilFinalized := func(r *KaiInstanceReconciler) {
		for i := 0; i < 5; i++ {
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			var kai swarmv1alpha2.KaiInstance
			err = k8sClient.Get(ctx, typeNamespacedName, &kai)
			if errors.IsNotFound(err) {
				return
			}
		}
	}

	BeforeEach(func() {
		By("creating the KaiInstance resource")
		resource := &swarmv1alpha2.KaiInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resourceName,
				Namespace: "default",
			},
			Spec: swarmv1alpha2.KaiInstanceSpec{
				TenantName: "Test Customer",
				ProjectName:  "Test Project",
			},
		}
		err := k8sClient.Get(ctx, typeNamespacedName, resource)
		if err != nil && errors.IsNotFound(err) {
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		}
	})

	AfterEach(func() {
		By("deleting the KaiInstance and draining the finalizer")
		var resource swarmv1alpha2.KaiInstance
		err := k8sClient.Get(ctx, typeNamespacedName, &resource)
		if err != nil && errors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, &resource)).To(Succeed())
		reconcileUntilFinalized(newReconciler())
	})

	It("adds the finalizer on the first reconcile", func() {
		r := newReconciler()
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		var kai swarmv1alpha2.KaiInstance
		Expect(k8sClient.Get(ctx, typeNamespacedName, &kai)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(&kai, swarmv1alpha2.KaiInstanceFinalizer)).To(BeTrue())
	})

	It("provisions all child resources with ownerRefs and tracks observedGeneration", func() {
		r := newReconciler()
		// First reconcile adds the finalizer and returns; second does the work.
		for i := 0; i < 2; i++ {
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
		}

		var kai swarmv1alpha2.KaiInstance
		Expect(k8sClient.Get(ctx, typeNamespacedName, &kai)).To(Succeed())
		slug := kai.Status.TenantSlug
		Expect(slug).NotTo(BeEmpty())
		Expect(kai.Status.ObservedGeneration).To(Equal(kai.Generation))

		// Each child must (a) exist and (b) carry an ownerRef pointing back to the KaiInstance.
		// That ownerRef is what makes ownerReference cascade work, which is what TASK-003 hardens.
		expectChild := func(obj client.Object, name string) {
			GinkgoHelper()
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: kai.Namespace}, obj)).To(Succeed())
			refs := obj.GetOwnerReferences()
			Expect(refs).NotTo(BeEmpty(), "child %s should carry an ownerRef", name)
			Expect(refs[0].Kind).To(Equal("KaiInstance"))
			Expect(refs[0].UID).To(Equal(kai.UID))
		}

		child := childName(slug)
		expectChild(&corev1.ConfigMap{}, child+"-identity")
		expectChild(&corev1.PersistentVolumeClaim{}, child+"-state")
		expectChild(&appsv1.Deployment{}, child)
		expectChild(&corev1.Service{}, child)
		expectChild(&networkingv1.NetworkPolicy{}, child+"-isolation")
		expectChild(&networkingv1.Ingress{}, child+"-ws")
		expectChild(&corev1.Secret{}, usersSecretName(slug))
		expectChild(&corev1.Secret{}, chatBridgeSecretName(slug))
	})

	It("only publishes ExternalURL once the Ingress is admitted by the controller (TASK-017 Phase 2)", func() {
		r := newReconciler()
		// Bring the resource up.
		for i := 0; i < 2; i++ {
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
		}

		var kai swarmv1alpha2.KaiInstance
		Expect(k8sClient.Get(ctx, typeNamespacedName, &kai)).To(Succeed())
		slug := kai.Status.TenantSlug
		Expect(slug).NotTo(BeEmpty())

		// Pre-admission: envtest doesn't run an ingress controller, so
		// `Ingress.Status.LoadBalancer.Ingress` stays empty. ExternalURL
		// must remain empty and the IngressReady condition must read False.
		Expect(kai.Status.ExternalURL).To(BeEmpty(), "ExternalURL must not publish before Ingress admission")
		var foundCond bool
		for _, c := range kai.Status.Conditions {
			if c.Type == swarmv1alpha2.ConditionIngressReady {
				Expect(c.Status).To(Equal(metav1.ConditionFalse), "IngressReady should be False pre-admission")
				Expect(c.Reason).To(Equal("Pending"))
				foundCond = true
				break
			}
		}
		Expect(foundCond).To(BeTrue(), "IngressReady condition should be set")

		// Simulate the ingress controller admitting the resource by
		// patching the Ingress' Status.LoadBalancer.Ingress.
		ingName := childName(slug) + "-ws"
		var ing networkingv1.Ingress
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ingName, Namespace: kai.Namespace}, &ing)).To(Succeed())
		ing.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{{IP: "203.0.113.10"}}
		Expect(k8sClient.Status().Update(ctx, &ing)).To(Succeed())

		// Reconcile again — now the gate flips.
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, typeNamespacedName, &kai)).To(Succeed())
		Expect(kai.Status.ExternalURL).To(Equal("https://kai.emai.dev/ws/" + slug),
			"ExternalURL should publish once the LoadBalancer ingress list is non-empty")
		for _, c := range kai.Status.Conditions {
			if c.Type == swarmv1alpha2.ConditionIngressReady {
				Expect(c.Status).To(Equal(metav1.ConditionTrue), "IngressReady should flip to True post-admission")
				Expect(c.Reason).To(Equal("Admitted"))
			}
		}
	})

	It("removes the finalizer on delete so the object can be garbage-collected", func() {
		r := newReconciler()
		// Reconcile until provisioned so the finalizer is in place.
		for i := 0; i < 2; i++ {
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
		}

		var kai swarmv1alpha2.KaiInstance
		Expect(k8sClient.Get(ctx, typeNamespacedName, &kai)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &kai)).To(Succeed())

		// After Delete, DeletionTimestamp is set but the finalizer keeps the object alive.
		// Reconcile drains it; the next Get should return NotFound.
		reconcileUntilFinalized(r)
		err := k8sClient.Get(ctx, typeNamespacedName, &kai)
		Expect(errors.IsNotFound(err)).To(BeTrue(), "KaiInstance should be gone after finalizer drain")

		// Idempotency: a reconcile against a missing object must not error.
		_, err = r.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
		Expect(err).NotTo(HaveOccurred())
	})
})
