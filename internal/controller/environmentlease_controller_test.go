/*
Copyright 2026 KubeLease Authors.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/resources"
)

func finalizeNamespace(g Gomega, name string) {
	ns := &corev1.Namespace{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, ns)
	if apierrors.IsNotFound(err) {
		return
	}
	g.Expect(err).NotTo(HaveOccurred())
	if ns.DeletionTimestamp.IsZero() {
		return
	}
	// envtest has no namespace controller. Use the Finalize subresource to clear
	// the protected "kubernetes" spec finalizer so the Namespace can disappear.
	ns.Spec.Finalizers = []corev1.FinalizerName{}
	_, err = k8sCS.CoreV1().Namespaces().Finalize(context.Background(), ns, metav1.UpdateOptions{})
	g.Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("EnvironmentLease Controller", func() {
	var (
		ctx        context.Context
		reconciler *EnvironmentLeaseReconciler
		clockTime  time.Time
	)

	BeforeEach(func() {
		ctx = context.Background()
		clockTime = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		reconciler = &EnvironmentLeaseReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(32),
			Clock:    func() time.Time { return clockTime },
		}
	})

	newLease := func(name string) *platformv1alpha1.EnvironmentLease {
		return &platformv1alpha1.EnvironmentLease{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: platformv1alpha1.EnvironmentLeaseSpec{
				TTL: metav1.Duration{Duration: 8 * time.Hour},
				Owner: platformv1alpha1.OwnerSpec{
					Name: "hayk",
					Team: "payments",
				},
				Namespace: platformv1alpha1.NamespaceSpec{
					Name: "preview-" + name,
					Labels: map[string]string{
						"environment": "preview",
						"application": "payments",
					},
				},
				Quota: &platformv1alpha1.QuotaSpec{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
					},
				},
				Limits: &platformv1alpha1.LimitsSpec{
					Default: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
					DefaultRequest: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
				},
				NetworkPolicy: &platformv1alpha1.NetworkPolicySpec{DefaultDeny: true},
			},
		}
	}

	reconcileUntilActive := func(name string) *platformv1alpha1.EnvironmentLease {
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			got := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseActive))
			g.Expect(got.Status.Namespace).NotTo(BeEmpty())
			g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		}, "15s", "200ms").Should(Succeed())

		got := &platformv1alpha1.EnvironmentLease{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		return got
	}

	AfterEach(func() {
		// Best-effort cleanup of leases created in each test.
	})

	It("provisions Namespace, ResourceQuota, LimitRange, NetworkPolicy and status", func() {
		name := "lease-provision"
		leaseObj := newLease(name)
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, leaseObj)
		})

		got := reconcileUntilActive(name)
		Expect(got.Status.CreatedAt).NotTo(BeNil())
		Expect(got.Status.ExpiresAt).NotTo(BeNil())
		Expect(controllerutil.ContainsFinalizer(got, platformv1alpha1.FinalizerName)).To(BeTrue())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: got.Status.Namespace}, ns)).To(Succeed())
		Expect(ns.Labels[platformv1alpha1.LabelManagedBy]).To(Equal(platformv1alpha1.ManagedByValue))
		Expect(ns.Labels["environment"]).To(Equal("preview"))
		Expect(ns.OwnerReferences).To(BeEmpty())

		rq := &corev1.ResourceQuota{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.ResourceQuotaName, Namespace: got.Status.Namespace,
		}, rq)).To(Succeed())
		Expect(rq.OwnerReferences).NotTo(BeEmpty())

		lr := &corev1.LimitRange{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.LimitRangeName, Namespace: got.Status.Namespace,
		}, lr)).To(Succeed())

		np := &networkingv1.NetworkPolicy{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.NetworkPolicyName, Namespace: got.Status.Namespace,
		}, np)).To(Succeed())
		Expect(np.Spec.Ingress).To(BeEmpty())
		Expect(np.Spec.Egress).To(BeEmpty())
	})

	It("is idempotent across repeated reconciles", func() {
		name := "lease-idempotent"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			obj := &platformv1alpha1.EnvironmentLease{}
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: name}, obj)
			_ = k8sClient.Delete(ctx, obj)
		})

		got := reconcileUntilActive(name)
		nsName := got.Status.Namespace
		createdAt := got.Status.CreatedAt.DeepCopy()
		expiresAt := got.Status.ExpiresAt.DeepCopy()

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		Expect(got.Status.Namespace).To(Equal(nsName))
		Expect(got.Status.CreatedAt.Equal(createdAt)).To(BeTrue())
		Expect(got.Status.ExpiresAt.Equal(expiresAt)).To(BeTrue())
		Expect(got.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseActive))
	})

	It("recreates deleted managed ResourceQuota", func() {
		name := "lease-drift-quota"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			obj := &platformv1alpha1.EnvironmentLease{}
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: name}, obj)
			_ = k8sClient.Delete(ctx, obj)
		})

		got := reconcileUntilActive(name)
		rq := &corev1.ResourceQuota{}
		key := types.NamespacedName{Name: resources.ResourceQuotaName, Namespace: got.Status.Namespace}
		Expect(k8sClient.Get(ctx, key, rq)).To(Succeed())
		Expect(k8sClient.Delete(ctx, rq)).To(Succeed())

		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(k8sClient.Get(ctx, key, &corev1.ResourceQuota{})).To(Succeed())
		}, "10s", "200ms").Should(Succeed())
	})

	It("repairs manually modified ResourceQuota hard limits", func() {
		name := "lease-drift-patch"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			obj := &platformv1alpha1.EnvironmentLease{}
			_ = k8sClient.Get(ctx, types.NamespacedName{Name: name}, obj)
			_ = k8sClient.Delete(ctx, obj)
		})

		got := reconcileUntilActive(name)
		rq := &corev1.ResourceQuota{}
		key := types.NamespacedName{Name: resources.ResourceQuotaName, Namespace: got.Status.Namespace}
		Expect(k8sClient.Get(ctx, key, rq)).To(Succeed())
		patched := rq.DeepCopy()
		patched.Spec.Hard[corev1.ResourceName("requests.cpu")] = resource.MustParse("1")
		Expect(k8sClient.Update(ctx, patched)).To(Succeed())

		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
			g.Expect(err).NotTo(HaveOccurred())
			current := &corev1.ResourceQuota{}
			g.Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
			cpuReq := current.Spec.Hard[corev1.ResourceName("requests.cpu")]
			g.Expect(cpuReq.Cmp(resource.MustParse("2"))).To(Equal(0))
		}, "10s", "200ms").Should(Succeed())
	})

	It("expires and cleans up the managed namespace", func() {
		name := "lease-expire"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())

		got := reconcileUntilActive(name)
		nsName := got.Status.Namespace

		clockTime = clockTime.Add(9 * time.Hour)
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}

		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			finalizeNamespace(g, nsName)

			nsErr := k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, &corev1.Namespace{})
			if apierrors.IsNotFound(nsErr) {
				_, err = reconciler.Reconcile(ctx, req)
				g.Expect(err).NotTo(HaveOccurred())
			}

			current := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, current)).To(Succeed())
			g.Expect(apierrors.IsNotFound(
				k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, &corev1.Namespace{}),
			)).To(BeTrue())
			g.Expect(current.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseExpired))
		}, "30s", "200ms").Should(Succeed())
	})

	It("removes finalizer after delete cleanup (NotFound is success)", func() {
		name := "lease-finalizer"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		got := reconcileUntilActive(name)
		nsName := got.Status.Namespace

		Expect(k8sClient.Delete(ctx, got)).To(Succeed())
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, req.NamespacedName, &platformv1alpha1.EnvironmentLease{})
			if apierrors.IsNotFound(err) {
				return
			}
			g.Expect(err).NotTo(HaveOccurred())

			_, reconErr := reconciler.Reconcile(ctx, req)
			g.Expect(reconErr).NotTo(HaveOccurred())
			finalizeNamespace(g, nsName)
			g.Expect(apierrors.IsNotFound(
				k8sClient.Get(ctx, req.NamespacedName, &platformv1alpha1.EnvironmentLease{}),
			)).To(BeTrue())
		}, "30s", "200ms").Should(Succeed())
	})

	It("fails closed on protected namespace names", func() {
		name := "lease-protected"
		leaseObj := newLease(name)
		leaseObj.Spec.Namespace.Name = "kube-system"
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, leaseObj)
		})

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			got := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseFailed))
		}, "10s", "200ms").Should(Succeed())
	})

	It("handles NotFound for missing EnvironmentLease", func() {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "does-not-exist"},
		})
		Expect(err).NotTo(HaveOccurred())
	})
})
