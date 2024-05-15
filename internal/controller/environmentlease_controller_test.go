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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
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
		clockTime = time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
		reconciler = &EnvironmentLeaseReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: record.NewFakeRecorder(64),
			Clock:    lease.FixedClock{T: clockTime},
		}
	})

	setClock := func(t time.Time) {
		clockTime = t
		reconciler.Clock = lease.FixedClock{T: clockTime}
	}

	newLease := func(name string) *platformv1alpha1.EnvironmentLease {
		return &platformv1alpha1.EnvironmentLease{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: platformv1alpha1.EnvironmentLeaseSpec{
				TTL:       &metav1.Duration{Duration: 8 * time.Hour},
				MaxTTL:    &metav1.Duration{Duration: 72 * time.Hour},
				Renewable: ptr.To(true),
				Warnings: []metav1.Duration{
					{Duration: time.Hour},
					{Duration: 15 * time.Minute},
				},
				Owner: platformv1alpha1.OwnerSpec{Name: "hayk", Team: "payments"},
				Namespace: platformv1alpha1.NamespaceSpec{
					Name: "preview-" + name,
					Labels: map[string]string{
						"environment": "preview",
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
			g.Expect(got.Status.MaximumExpiresAt).NotTo(BeNil())
			g.Expect(got.Status.ObservedGeneration).To(Equal(got.Generation))
		}, "15s", "200ms").Should(Succeed())

		got := &platformv1alpha1.EnvironmentLease{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name}, got)).To(Succeed())
		return got
	}

	It("provisions resources and status", func() {
		name := "lease-provision"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &platformv1alpha1.EnvironmentLease{ObjectMeta: metav1.ObjectMeta{Name: name}})
		})

		got := reconcileUntilActive(name)
		Expect(controllerutil.ContainsFinalizer(got, platformv1alpha1.FinalizerName)).To(BeTrue())

		ns := &corev1.Namespace{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: got.Status.Namespace}, ns)).To(Succeed())
		Expect(ns.Annotations[platformv1alpha1.AnnotationLeaseUID]).To(Equal(string(got.UID)))
		Expect(ns.OwnerReferences).To(BeEmpty())

		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.ResourceQuotaName, Namespace: got.Status.Namespace,
		}, &corev1.ResourceQuota{})).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.LimitRangeName, Namespace: got.Status.Namespace,
		}, &corev1.LimitRange{})).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.NetworkPolicyName, Namespace: got.Status.Namespace,
		}, &networkingv1.NetworkPolicy{})).To(Succeed())
	})

	It("is idempotent and keeps timestamps sticky", func() {
		name := "lease-idempotent"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &platformv1alpha1.EnvironmentLease{ObjectMeta: metav1.ObjectMeta{Name: name}})
		})

		got := reconcileUntilActive(name)
		createdAt := got.Status.CreatedAt.DeepCopy()
		expiresAt := got.Status.ExpiresAt.DeepCopy()
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
		Expect(got.Status.CreatedAt.Equal(createdAt)).To(BeTrue())
		Expect(got.Status.ExpiresAt.Equal(expiresAt)).To(BeTrue())
	})

	It("recreates deleted ResourceQuota", func() {
		name := "lease-drift-quota"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &platformv1alpha1.EnvironmentLease{ObjectMeta: metav1.ObjectMeta{Name: name}})
		})

		got := reconcileUntilActive(name)
		key := types.NamespacedName{Name: resources.ResourceQuotaName, Namespace: got.Status.Namespace}
		rq := &corev1.ResourceQuota{}
		Expect(k8sClient.Get(ctx, key, rq)).To(Succeed())
		Expect(k8sClient.Delete(ctx, rq)).To(Succeed())

		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name}})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(k8sClient.Get(ctx, key, &corev1.ResourceQuota{})).To(Succeed())
		}, "10s", "200ms").Should(Succeed())
	})

	It("renews when Spec.TTL increases", func() {
		name := "lease-renew"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &platformv1alpha1.EnvironmentLease{ObjectMeta: metav1.ObjectMeta{Name: name}})
		})

		got := reconcileUntilActive(name)
		prev := got.Status.ExpiresAt.DeepCopy()

		patched := got.DeepCopy()
		patched.Spec.TTL = &metav1.Duration{Duration: 12 * time.Hour}
		Expect(k8sClient.Patch(ctx, patched, client.MergeFrom(got))).To(Succeed())

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			current := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, current)).To(Succeed())
			g.Expect(current.Status.ExpiresAt.After(prev.Time)).To(BeTrue())
		}, "10s", "200ms").Should(Succeed())
	})

	It("clamps renewal to maxTTL", func() {
		name := "lease-maxttl"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &platformv1alpha1.EnvironmentLease{ObjectMeta: metav1.ObjectMeta{Name: name}})
		})

		got := reconcileUntilActive(name)
		// Raise MaxTTL so ValidateSpec accepts a large TTL, then lower MaxTTL via
		// status clamp: set TTL to MaxTTL (72h) and confirm expiresAt == maximum.
		patched := got.DeepCopy()
		patched.Spec.TTL = &metav1.Duration{Duration: 72 * time.Hour}
		Expect(k8sClient.Patch(ctx, patched, client.MergeFrom(got))).To(Succeed())

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			current := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, current)).To(Succeed())
			g.Expect(current.Status.ExpiresAt.Equal(current.Status.MaximumExpiresAt)).To(BeTrue())
		}, "10s", "200ms").Should(Succeed())
	})

	It("emits warning once and persists delivery", func() {
		name := "lease-warn"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &platformv1alpha1.EnvironmentLease{ObjectMeta: metav1.ObjectMeta{Name: name}})
		})

		got := reconcileUntilActive(name)
		// Move into 1h warning window (expires at created+8h).
		setClock(got.Status.ExpiresAt.Add(-30 * time.Minute))

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			current := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, current)).To(Succeed())
			g.Expect(current.Status.WarningsDelivered).NotTo(BeEmpty())
			g.Expect(current.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseExpiring))
		}, "10s", "200ms").Should(Succeed())

		Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
		delivered := append([]string{}, got.Status.WarningsDelivered...)
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
		Expect(got.Status.WarningsDelivered).To(Equal(delivered))
	})

	It("expires and cleans up the managed namespace", func() {
		name := "lease-expire"
		Expect(k8sClient.Create(ctx, newLease(name))).To(Succeed())
		got := reconcileUntilActive(name)
		nsName := got.Status.Namespace
		setClock(got.Status.ExpiresAt.Add(time.Minute))
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

	It("removes finalizer after delete cleanup", func() {
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
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, leaseObj) })

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

	It("applies EnvironmentLeasePolicy defaults and records effective status", func() {
		pol := &platformv1alpha1.EnvironmentLeasePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "preview-default"},
			Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
				TTL: &platformv1alpha1.DurationPolicy{
					Default: &metav1.Duration{Duration: 8 * time.Hour},
					Maximum: &metav1.Duration{Duration: 72 * time.Hour},
				},
				Quota: &platformv1alpha1.QuotaPolicy{
					MaxCPU:    qtyPtr("4"),
					MaxMemory: qtyPtr("8Gi"),
				},
				NetworkPolicy: &platformv1alpha1.NetworkPolicyPolicy{
					DefaultDenyRequired: true,
				},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pol) })

		name := "lease-policy-default"
		leaseObj := &platformv1alpha1.EnvironmentLease{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: platformv1alpha1.EnvironmentLeaseSpec{
				PolicyRef: &platformv1alpha1.LocalObjectReference{Name: "preview-default"},
				Namespace: platformv1alpha1.NamespaceSpec{Name: "preview-" + name},
				Quota: &platformv1alpha1.QuotaSpec{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, leaseObj) })

		got := reconcileUntilActive(name)
		Expect(got.Status.Effective).NotTo(BeNil())
		Expect(got.Status.Effective.PolicyName).To(Equal("preview-default"))
		Expect(got.Status.Effective.TTL.Duration).To(Equal(8 * time.Hour))
		Expect(got.Status.Effective.DefaultDeny).To(BeTrue())

		np := &networkingv1.NetworkPolicy{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: resources.NetworkPolicyName, Namespace: got.Status.Namespace,
		}, np)).To(Succeed())
	})

	It("rejects leases that violate policy hard limits", func() {
		pol := &platformv1alpha1.EnvironmentLeasePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "strict-policy"},
			Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
				TTL: &platformv1alpha1.DurationPolicy{
					Default: &metav1.Duration{Duration: time.Hour},
					Maximum: &metav1.Duration{Duration: 2 * time.Hour},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pol) })

		name := "lease-policy-violate"
		leaseObj := newLease(name)
		leaseObj.Spec.PolicyRef = &platformv1alpha1.LocalObjectReference{Name: "strict-policy"}
		leaseObj.Spec.TTL = &metav1.Duration{Duration: 8 * time.Hour}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, leaseObj) })

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			got := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
			g.Expect(got.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseFailed))
		}, "10s", "200ms").Should(Succeed())
	})

	It("expires on idleTTL before hard TTL and records IdleTimeout", func() {
		name := "lease-idle"
		leaseObj := newLease(name)
		leaseObj.Spec.IdleTTL = &metav1.Duration{Duration: time.Hour}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		got := reconcileUntilActive(name)

		Expect(got.Status.LastActivityAt).NotTo(BeNil())
		Expect(got.Status.IdleExpiresAt).NotTo(BeNil())
		Expect(got.Status.EffectiveExpiresAt).NotTo(BeNil())
		Expect(got.Status.EffectiveExpiresAt.Time).To(Equal(got.Status.IdleExpiresAt.Time))
		Expect(got.Status.EffectiveExpiresAt.Before(got.Status.ExpiresAt)).To(BeTrue())

		setClock(got.Status.IdleExpiresAt.Add(time.Minute))
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		nsName := got.Status.Namespace

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
			g.Expect(current.Status.ExpirationReason).To(Equal(platformv1alpha1.ExpirationReasonIdleTimeout))
			g.Expect(current.Status.Phase).To(Equal(platformv1alpha1.LeasePhaseExpired))
		}, "30s", "200ms").Should(Succeed())
	})

	It("extends idle lifetime on touch without changing hard TTL", func() {
		name := "lease-touch"
		leaseObj := newLease(name)
		leaseObj.Spec.IdleTTL = &metav1.Duration{Duration: 30 * time.Minute}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		got := reconcileUntilActive(name)
		hardBefore := got.Status.ExpiresAt.DeepCopy()
		idleBefore := got.Status.IdleExpiresAt.DeepCopy()

		setClock(got.Status.CreatedAt.Add(10 * time.Minute))
		Expect(lease.RecordActivity(got, clockTime, 30*time.Minute)).To(Succeed())
		Expect(k8sClient.Status().Update(ctx, got)).To(Succeed())

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		_, err := reconciler.Reconcile(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, req.NamespacedName, got)).To(Succeed())
		Expect(got.Status.ExpiresAt.Equal(hardBefore)).To(BeTrue())
		Expect(got.Status.IdleExpiresAt.After(idleBefore.Time)).To(BeTrue())
		Expect(got.Status.EffectiveExpiresAt.After(idleBefore.Time)).To(BeTrue())
		Expect(got.Status.EffectiveExpiresAt.After(got.Status.ExpiresAt.Time)).To(BeFalse())
	})

	It("expires immediately when already past idle deadline at startup", func() {
		name := "lease-idle-startup"
		leaseObj := newLease(name)
		leaseObj.Spec.TTL = &metav1.Duration{Duration: 8 * time.Hour}
		leaseObj.Spec.IdleTTL = &metav1.Duration{Duration: 15 * time.Minute}
		Expect(k8sClient.Create(ctx, leaseObj)).To(Succeed())
		got := reconcileUntilActive(name)
		nsName := got.Status.Namespace

		// Jump well past idle window but before hard TTL.
		setClock(got.Status.CreatedAt.Add(time.Hour))
		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: name}}
		Eventually(func(g Gomega) {
			_, err := reconciler.Reconcile(ctx, req)
			g.Expect(err).NotTo(HaveOccurred())
			finalizeNamespace(g, nsName)
			if apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Name: nsName}, &corev1.Namespace{})) {
				_, err = reconciler.Reconcile(ctx, req)
				g.Expect(err).NotTo(HaveOccurred())
			}
			current := &platformv1alpha1.EnvironmentLease{}
			g.Expect(k8sClient.Get(ctx, req.NamespacedName, current)).To(Succeed())
			g.Expect(current.Status.ExpirationReason).To(Equal(platformv1alpha1.ExpirationReasonIdleTimeout))
		}, "30s", "200ms").Should(Succeed())
	})
})

func qtyPtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
