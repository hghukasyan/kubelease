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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/metrics"
	"github.com/hghukasyan/kubelease/internal/resources"
)

const (
	cleanupRequeueAfter   = 5 * time.Second
	eventEnvironmentReady = "EnvironmentProvisioned"
	eventLeaseExpired     = "LeaseExpired"
	eventCleanupFailed    = "CleanupFailed"
)

// EnvironmentLeaseReconciler reconciles EnvironmentLease objects.
type EnvironmentLeaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	// Clock allows tests to inject a fixed time. Defaults to time.Now.
	Clock func() time.Time
}

func (r *EnvironmentLeaseReconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now().UTC()
}

// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleases/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile converges cluster state toward the EnvironmentLease desired state.
func (r *EnvironmentLeaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("environmentlease", req.Name)

	leaseObj := &platformv1alpha1.EnvironmentLease{}
	if err := r.Get(ctx, req.NamespacedName, leaseObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get EnvironmentLease %s: %w", req.Name, err)
	}

	if !leaseObj.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, leaseObj)
	}

	if !controllerutil.ContainsFinalizer(leaseObj, platformv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(leaseObj, platformv1alpha1.FinalizerName)
		if err := r.Update(ctx, leaseObj); err != nil {
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	before := lease.DeepCopyStatus(leaseObj.Status)
	previousPhase := leaseObj.Status.Phase

	if err := lease.ValidateSpec(leaseObj); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonInvalidConfiguration, err.Error())
		lease.EnsureObservedGeneration(leaseObj)
		if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
			return ctrl.Result{}, err
		}
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, nil
	}

	if _, err := lease.EnsureTimestamps(leaseObj, r.now()); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonInvalidConfiguration, err.Error())
		lease.EnsureObservedGeneration(leaseObj)
		if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if lease.IsExpired(leaseObj, r.now()) {
		return r.reconcileExpired(ctx, leaseObj, before, previousPhase)
	}

	result, err := r.reconcileActive(ctx, leaseObj, previousPhase)
	lease.EnsureObservedGeneration(leaseObj)
	if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
		return ctrl.Result{}, patchErr
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile complete", "phase", leaseObj.Status.Phase, "namespace", leaseObj.Status.Namespace)
	return result, nil
}

func (r *EnvironmentLeaseReconciler) reconcileActive(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	previousPhase platformv1alpha1.LeasePhase,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	wasReady := metaConditionTrue(leaseObj, platformv1alpha1.ConditionReady)
	if !wasReady {
		lease.MarkProvisioning(leaseObj, "Provisioning managed environment")
	}

	nsName, created, err := r.ensureNamespace(ctx, leaseObj)
	if err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonNamespaceCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	leaseObj.Status.Namespace = nsName
	if created {
		log.Info("provisioning environment", "lease", leaseObj.Name, "namespace", nsName)
	}

	if err := r.ensureResourceQuota(ctx, leaseObj, nsName); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonResourceQuotaCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if err := r.ensureLimitRange(ctx, leaseObj, nsName); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonLimitRangeCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if err := r.ensureNetworkPolicy(ctx, leaseObj, nsName); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonNetworkPolicyCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}

	lease.MarkReady(leaseObj)
	if previousPhase != platformv1alpha1.LeasePhaseActive {
		metrics.ActiveLeases.Inc()
	}
	if !wasReady && r.Recorder != nil {
		r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventEnvironmentReady,
			fmt.Sprintf("Environment provisioned in namespace %s", nsName))
	}

	until := lease.TimeUntilExpiration(leaseObj, r.now())
	if until < 0 {
		until = 0
	}
	return ctrl.Result{RequeueAfter: until}, nil
}

func (r *EnvironmentLeaseReconciler) reconcileExpired(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	before platformv1alpha1.EnvironmentLeaseStatus,
	previousPhase platformv1alpha1.LeasePhase,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	firstExpiry := previousPhase != platformv1alpha1.LeasePhaseExpired &&
		previousPhase != platformv1alpha1.LeasePhaseCleaning &&
		previousPhase != platformv1alpha1.LeasePhaseExpiring

	if firstExpiry {
		metrics.ExpiredLeasesTotal.Inc()
		if previousPhase == platformv1alpha1.LeasePhaseActive {
			metrics.ActiveLeases.Dec()
		}
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventLeaseExpired,
				"Lease TTL elapsed; cleaning up environment")
		}
		log.Info("lease expired", "lease", leaseObj.Name, "expiresAt", leaseObj.Status.ExpiresAt)
	}

	lease.MarkExpired(leaseObj)
	done, err := r.cleanupEnvironment(ctx, leaseObj)
	if err != nil {
		lease.MarkCleanupFailed(leaseObj, err.Error())
		metrics.CleanupFailuresTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeWarning, eventCleanupFailed, err.Error())
		}
		lease.EnsureObservedGeneration(leaseObj)
		_ = r.patchStatusIfChanged(ctx, leaseObj, before)
		return ctrl.Result{}, err
	}
	if !done {
		lease.MarkCleaning(leaseObj, "Waiting for namespace deletion")
		lease.EnsureObservedGeneration(leaseObj)
		if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: cleanupRequeueAfter}, nil
	}

	lease.MarkCleanupComplete(leaseObj)
	lease.EnsureObservedGeneration(leaseObj)
	if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *EnvironmentLeaseReconciler) reconcileDelete(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(leaseObj, platformv1alpha1.FinalizerName) {
		return ctrl.Result{}, nil
	}

	before := lease.DeepCopyStatus(leaseObj.Status)
	if leaseObj.Status.Phase == platformv1alpha1.LeasePhaseActive {
		metrics.ActiveLeases.Dec()
	}
	lease.MarkCleaning(leaseObj, "Deleting managed environment")

	done, err := r.cleanupEnvironment(ctx, leaseObj)
	if err != nil {
		lease.MarkCleanupFailed(leaseObj, err.Error())
		metrics.CleanupFailuresTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeWarning, eventCleanupFailed, err.Error())
		}
		_ = r.patchStatusIfChanged(ctx, leaseObj, before)
		return ctrl.Result{}, err
	}
	if !done {
		_ = r.patchStatusIfChanged(ctx, leaseObj, before)
		return ctrl.Result{RequeueAfter: cleanupRequeueAfter}, nil
	}

	lease.MarkCleanupComplete(leaseObj)
	_ = r.patchStatusIfChanged(ctx, leaseObj, before)

	controllerutil.RemoveFinalizer(leaseObj, platformv1alpha1.FinalizerName)
	if err := r.Update(ctx, leaseObj); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

// cleanupEnvironment deletes the managed Namespace. NotFound is success.
// Returns done=true when the namespace is gone.
func (r *EnvironmentLeaseReconciler) cleanupEnvironment(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (bool, error) {
	nsName := leaseObj.Status.Namespace
	if nsName == "" {
		return true, nil
	}
	if resources.IsProtectedNamespace(nsName) {
		return false, fmt.Errorf("refusing to delete protected namespace %q", nsName)
	}

	ns := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: nsName}, ns)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get namespace %s: %w", nsName, err)
	}

	if !resources.IsManagedByKubeLease(ns.Labels, leaseObj.Name) {
		return false, fmt.Errorf("namespace %s is not managed by lease %s; refusing cleanup", nsName, leaseObj.Name)
	}

	if ns.DeletionTimestamp.IsZero() {
		if err := r.Delete(ctx, ns); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete namespace %s: %w", nsName, err)
		}
	}
	return false, nil
}

func (r *EnvironmentLeaseReconciler) ensureNamespace(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (string, bool, error) {
	if leaseObj.Status.Namespace != "" {
		return r.ensureExistingNamespace(ctx, leaseObj, leaseObj.Status.Namespace)
	}

	if leaseObj.Spec.Namespace.Name != "" {
		return r.ensureExistingNamespace(ctx, leaseObj, leaseObj.Spec.Namespace.Name)
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: leaseObj.Spec.Namespace.GenerateName,
			Labels:       resources.MergeLabels(resources.ManagedLabels(leaseObj.Name), leaseObj.Spec.Namespace.Labels),
			Annotations:  copyAnnotations(leaseObj.Spec.Namespace.Annotations),
		},
	}
	if err := r.Create(ctx, ns); err != nil {
		return "", false, fmt.Errorf("create namespace with generateName %q: %w",
			leaseObj.Spec.Namespace.GenerateName, err)
	}
	return ns.Name, true, nil
}

func (r *EnvironmentLeaseReconciler) ensureExistingNamespace(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	name string,
) (string, bool, error) {
	desired, err := resources.DesiredNamespace(leaseObj, name)
	if err != nil {
		return "", false, err
	}

	existing := &corev1.Namespace{}
	err = r.Get(ctx, types.NamespacedName{Name: name}, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return "", false, fmt.Errorf("create namespace %s: %w", name, err)
		}
		return name, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get namespace %s: %w", name, err)
	}

	patched := existing.DeepCopy()
	patched.Labels = resources.MergeLabels(resources.ManagedLabels(leaseObj.Name), leaseObj.Spec.Namespace.Labels)
	if leaseObj.Spec.Namespace.Annotations != nil {
		if patched.Annotations == nil {
			patched.Annotations = map[string]string{}
		}
		for k, v := range leaseObj.Spec.Namespace.Annotations {
			patched.Annotations[k] = v
		}
	}
	if err := r.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return "", false, fmt.Errorf("patch namespace %s: %w", name, err)
	}
	return name, false, nil
}

func (r *EnvironmentLeaseReconciler) ensureResourceQuota(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	namespace string,
) error {
	desired := resources.DesiredResourceQuota(leaseObj, namespace)
	if desired == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(leaseObj, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on ResourceQuota: %w", err)
	}

	existing := &corev1.ResourceQuota{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("create ResourceQuota %s/%s: %w", namespace, desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get ResourceQuota %s/%s: %w", namespace, desired.Name, err)
	}

	patched := existing.DeepCopy()
	patched.Labels = desired.Labels
	patched.Spec.Hard = desired.Spec.Hard
	if err := controllerutil.SetControllerReference(leaseObj, patched, r.Scheme); err != nil {
		return err
	}
	if err := r.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return fmt.Errorf("patch ResourceQuota %s/%s: %w", namespace, desired.Name, err)
	}
	return nil
}

func (r *EnvironmentLeaseReconciler) ensureLimitRange(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	namespace string,
) error {
	desired := resources.DesiredLimitRange(leaseObj, namespace)
	if desired == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(leaseObj, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on LimitRange: %w", err)
	}

	existing := &corev1.LimitRange{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("create LimitRange %s/%s: %w", namespace, desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get LimitRange %s/%s: %w", namespace, desired.Name, err)
	}

	patched := existing.DeepCopy()
	patched.Labels = desired.Labels
	patched.Spec = desired.Spec
	if err := controllerutil.SetControllerReference(leaseObj, patched, r.Scheme); err != nil {
		return err
	}
	if err := r.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return fmt.Errorf("patch LimitRange %s/%s: %w", namespace, desired.Name, err)
	}
	return nil
}

func (r *EnvironmentLeaseReconciler) ensureNetworkPolicy(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	namespace string,
) error {
	desired := resources.DesiredNetworkPolicy(leaseObj, namespace)
	if desired == nil {
		return nil
	}
	if err := controllerutil.SetControllerReference(leaseObj, desired, r.Scheme); err != nil {
		return fmt.Errorf("set owner reference on NetworkPolicy: %w", err)
	}

	existing := &networkingv1.NetworkPolicy{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("create NetworkPolicy %s/%s: %w", namespace, desired.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get NetworkPolicy %s/%s: %w", namespace, desired.Name, err)
	}

	patched := existing.DeepCopy()
	patched.Labels = desired.Labels
	patched.Spec = desired.Spec
	if err := controllerutil.SetControllerReference(leaseObj, patched, r.Scheme); err != nil {
		return err
	}
	if err := r.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return fmt.Errorf("patch NetworkPolicy %s/%s: %w", namespace, desired.Name, err)
	}
	return nil
}

func (r *EnvironmentLeaseReconciler) patchStatusIfChanged(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	before platformv1alpha1.EnvironmentLeaseStatus,
) error {
	if lease.StatusEqual(before, leaseObj.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, leaseObj); err != nil {
		return fmt.Errorf("update status for %s: %w", leaseObj.Name, err)
	}
	return nil
}

func metaConditionTrue(leaseObj *platformv1alpha1.EnvironmentLease, condType string) bool {
	for _, c := range leaseObj.Status.Conditions {
		if c.Type == condType {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func copyAnnotations(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SetupWithManager sets up the controller with the Manager.
//
// Watches:
//   - EnvironmentLease (primary)
//   - ResourceQuota, LimitRange, NetworkPolicy via Owns — drift / deletion of
//     owned children requeues the parent lease
//   - Namespace via label map — Namespace has no OwnerReference; label
//     kubelease.io/lease maps changes back to the EnvironmentLease
func (r *EnvironmentLeaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("environmentlease-controller")
	}

	mapNamespace := handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
		labels := obj.GetLabels()
		leaseName := labels[platformv1alpha1.LabelLease]
		if leaseName == "" {
			return nil
		}
		if labels[platformv1alpha1.LabelManagedBy] != platformv1alpha1.ManagedByValue {
			return nil
		}
		return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: leaseName}}}
	})

	nsPred := predicate.NewPredicateFuncs(func(obj client.Object) bool {
		labels := obj.GetLabels()
		return labels[platformv1alpha1.LabelManagedBy] == platformv1alpha1.ManagedByValue &&
			labels[platformv1alpha1.LabelLease] != ""
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.EnvironmentLease{}).
		Owns(&corev1.ResourceQuota{}).
		Owns(&corev1.LimitRange{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Watches(&corev1.Namespace{}, mapNamespace, builder.WithPredicates(nsPred)).
		Named("environmentlease").
		Complete(r)
}
