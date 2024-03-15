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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/metrics"
	"github.com/hghukasyan/kubelease/internal/policy"
	"github.com/hghukasyan/kubelease/internal/resources"
)

const (
	cleanupRequeueAfter  = 5 * time.Second
	indexStatusNamespace = ".status.namespace"
	indexPolicyRefName   = ".spec.policyRef.name"

	eventEnvironmentReady = "EnvironmentProvisioned"
	eventLeaseRenewed     = "LeaseRenewed"
	eventLeaseExpiring    = "LeaseExpiring"
	eventLeaseExpired     = "LeaseExpired"
	eventCleanupStarted   = "CleanupStarted"
	eventCleanupCompleted = "CleanupCompleted"
	eventCleanupFailed    = "CleanupFailed"
)

// EnvironmentLeaseReconciler reconciles EnvironmentLease objects.
type EnvironmentLeaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Clock    lease.Clock
}

func (r *EnvironmentLeaseReconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now().UTC()
}

// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleases/finalizers,verbs=update
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=environmentleasepolicies,verbs=get;list;watch
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

	log = log.WithValues("generation", leaseObj.Generation, "phase", leaseObj.Status.Phase, "namespace", leaseObj.Status.Namespace)
	ctx = ctrl.LoggerInto(ctx, log)

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
	previousExpires := timePtr(leaseObj.Status.ExpiresAt)

	if err := lease.ValidateSpec(leaseObj); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonInvalidConfiguration, err.Error())
		lease.EnsureObservedGeneration(leaseObj)
		if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
			return ctrl.Result{}, err
		}
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, nil
	}

	pol, err := r.fetchPolicy(ctx, leaseObj)
	if err != nil {
		reason := platformv1alpha1.ReasonPolicyNotFound
		lease.MarkFailed(leaseObj, reason, err.Error())
		lease.EnsureObservedGeneration(leaseObj)
		if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, nil
	}

	resolved, err := policy.Resolve(leaseObj.Spec, pol)
	if err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonPolicyViolation, err.Error())
		lease.EnsureObservedGeneration(leaseObj)
		if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, nil
	}
	leaseObj.Status.Effective = resolved.ToEffectiveStatus()

	_, renewalRejected, err := lease.EnsureTimestamps(leaseObj, resolved.TTL, resolved.MaxTTL, resolved.Renewable, r.now())
	if err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonInvalidConfiguration, err.Error())
		lease.EnsureObservedGeneration(leaseObj)
		if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if renewalRejected {
		lease.SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionTrue,
			platformv1alpha1.ReasonRenewalRejected, "TTL increase ignored because lease is not renewable")
	}

	// Detect renewal for events/metrics (expiresAt moved later).
	if previousExpires != nil && leaseObj.Status.ExpiresAt != nil &&
		leaseObj.Status.ExpiresAt.After(*previousExpires) &&
		resolved.Renewable {
		metrics.RenewalsTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Eventf(leaseObj, corev1.EventTypeNormal, eventLeaseRenewed,
				"Lease renewed; new expiration %s", leaseObj.Status.ExpiresAt.UTC().Format(time.RFC3339))
		}
	}

	if lease.IsExpired(leaseObj, r.now()) {
		return r.reconcileExpired(ctx, leaseObj, before, previousPhase)
	}

	result, err := r.reconcileActive(ctx, leaseObj, previousPhase, resolved)
	lease.EnsureObservedGeneration(leaseObj)
	if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
		return ctrl.Result{}, patchErr
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	log.V(1).Info("reconcile complete", "phase", leaseObj.Status.Phase, "requeueAfter", result.RequeueAfter)
	return result, nil
}

func (r *EnvironmentLeaseReconciler) fetchPolicy(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (*platformv1alpha1.EnvironmentLeasePolicy, error) {
	if leaseObj.Spec.PolicyRef == nil || leaseObj.Spec.PolicyRef.Name == "" {
		return nil, nil
	}
	pol := &platformv1alpha1.EnvironmentLeasePolicy{}
	if err := r.Get(ctx, types.NamespacedName{Name: leaseObj.Spec.PolicyRef.Name}, pol); err != nil {
		return nil, fmt.Errorf("get EnvironmentLeasePolicy %q: %w", leaseObj.Spec.PolicyRef.Name, err)
	}
	return pol, nil
}

func (r *EnvironmentLeaseReconciler) reconcileActive(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	previousPhase platformv1alpha1.LeasePhase,
	resolved policy.Resolved,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	wasReady := lease.ConditionTrue(leaseObj, platformv1alpha1.ConditionReady)

	// Avoid Failed↔Provisioning flap: only mark provisioning when not already Failed
	// unless we are starting fresh.
	if !wasReady && leaseObj.Status.Phase != platformv1alpha1.LeasePhaseFailed {
		lease.MarkProvisioning(leaseObj, "Provisioning managed environment")
	}

	// Working copy carries policy-resolved NetworkPolicy for desired builders
	// without mutating the user's Spec.
	working := leaseObj.DeepCopy()
	if resolved.NetworkPolicy != nil {
		working.Spec.NetworkPolicy = resolved.NetworkPolicy
	}

	nsName, created, err := r.ensureNamespace(ctx, leaseObj)
	if err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonNamespaceCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if leaseObj.Status.Namespace != nsName {
		leaseObj.Status.Namespace = nsName
		// Persist namespace identity immediately to avoid generateName leaks.
		if err := r.Status().Update(ctx, leaseObj); err != nil {
			return ctrl.Result{}, fmt.Errorf("persist status.namespace: %w", err)
		}
		// Refresh for subsequent status writes.
		if err := r.Get(ctx, client.ObjectKeyFromObject(leaseObj), leaseObj); err != nil {
			return ctrl.Result{}, err
		}
		leaseObj.Status.Effective = resolved.ToEffectiveStatus()
	}
	if created {
		log.Info("provisioning environment", "lease", leaseObj.Name, "namespace", nsName)
	}

	if err := r.ensureResourceQuota(ctx, working, nsName); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonResourceQuotaCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if err := r.ensureLimitRange(ctx, working, nsName); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonLimitRangeCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if err := r.ensureNetworkPolicy(ctx, working, nsName); err != nil {
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonNetworkPolicyCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}

	// Emit pending expiration warnings (persisted in status.warningsDelivered).
	r.emitWarnings(ctx, leaseObj)

	expiring := lease.IsExpiringWindow(leaseObj, r.now())
	lease.MarkReady(leaseObj, expiring)

	if !wasReady && previousPhase != platformv1alpha1.LeasePhaseActive &&
		previousPhase != platformv1alpha1.LeasePhaseExpiring {
		metrics.LeasesCreatedTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Eventf(leaseObj, corev1.EventTypeNormal, eventEnvironmentReady,
				"Environment provisioned in namespace %s", nsName)
		}
	}

	until := lease.NextReconcileAfter(
		r.now(),
		leaseObj.Status.ExpiresAt.Time,
		lease.WarningDurations(leaseObj.Spec.Warnings),
		leaseObj.Status.WarningsDelivered,
	)
	return ctrl.Result{RequeueAfter: until}, nil
}

// emitWarnings fires pending LeaseExpiring events and records delivery in status.
func (r *EnvironmentLeaseReconciler) emitWarnings(_ context.Context, leaseObj *platformv1alpha1.EnvironmentLease) {
	if leaseObj.Status.ExpiresAt == nil {
		return
	}
	pending := lease.PendingWarnings(
		r.now(),
		leaseObj.Status.ExpiresAt.Time,
		lease.WarningDurations(leaseObj.Spec.Warnings),
		leaseObj.Status.WarningsDelivered,
	)
	for _, w := range pending {
		remaining := leaseObj.Status.ExpiresAt.Time.Sub(r.now())
		msg := lease.WarningMessage(leaseObj.Name, remaining)
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeWarning, eventLeaseExpiring, msg)
		}
		metrics.WarningEventsTotal.Inc()
		leaseObj.Status.WarningsDelivered = lease.MarkWarningDelivered(leaseObj.Status.WarningsDelivered, w)
	}
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
		metrics.LeasesExpiredTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventLeaseExpired,
				"Lease TTL elapsed; cleaning up environment")
			r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventCleanupStarted,
				"Cleanup started")
		}
		log.Info("lease expired", "expiresAt", leaseObj.Status.ExpiresAt)
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
		if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
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
	if firstExpiry && r.Recorder != nil {
		r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventCleanupCompleted, "Cleanup completed")
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
	if leaseObj.Status.Phase != platformv1alpha1.LeasePhaseCleaning {
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventCleanupStarted, "Cleanup started")
		}
	}
	lease.MarkCleaning(leaseObj, "Deleting managed environment")

	done, err := r.cleanupEnvironment(ctx, leaseObj)
	if err != nil {
		lease.MarkCleanupFailed(leaseObj, err.Error())
		metrics.CleanupFailuresTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeWarning, eventCleanupFailed, err.Error())
		}
		if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, err
	}
	if !done {
		if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: cleanupRequeueAfter}, nil
	}

	lease.MarkCleanupComplete(leaseObj)
	if err := r.patchStatusIfChanged(ctx, leaseObj, before); err != nil {
		return ctrl.Result{}, err
	}
	if r.Recorder != nil {
		r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventCleanupCompleted, "Cleanup completed")
	}

	// Retry finalizer removal on conflict.
	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current := &platformv1alpha1.EnvironmentLease{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(leaseObj), current); err != nil {
			return err
		}
		if !controllerutil.ContainsFinalizer(current, platformv1alpha1.FinalizerName) {
			return nil
		}
		controllerutil.RemoveFinalizer(current, platformv1alpha1.FinalizerName)
		return r.Update(ctx, current)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

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

	if !resources.OwnedByLease(ns, leaseObj.Name, string(leaseObj.UID)) {
		return false, fmt.Errorf("namespace %s is not owned by lease %s; refusing cleanup", nsName, leaseObj.Name)
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
			Annotations:  managedAnnotations(leaseObj),
		},
	}
	if err := r.Create(ctx, ns); err != nil {
		return "", false, fmt.Errorf("create namespace with generateName %q: %w",
			leaseObj.Spec.Namespace.GenerateName, err)
	}
	return ns.Name, true, nil
}

func managedAnnotations(leaseObj *platformv1alpha1.EnvironmentLease) map[string]string {
	out := map[string]string{}
	for k, v := range leaseObj.Spec.Namespace.Annotations {
		out[k] = v
	}
	out[platformv1alpha1.AnnotationLeaseUID] = string(leaseObj.UID)
	return out
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
		// Recreate same identity when status.namespace is set (drift recovery).
		if err := r.Create(ctx, desired); err != nil {
			return "", false, fmt.Errorf("create namespace %s: %w", name, err)
		}
		return name, true, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get namespace %s: %w", name, err)
	}

	if !existing.DeletionTimestamp.IsZero() {
		return "", false, fmt.Errorf("namespace %s is terminating; waiting", name)
	}

	if !resources.CanAdoptNamespace(existing, leaseObj.Name, string(leaseObj.UID)) {
		return "", false, fmt.Errorf("%s: refusing to adopt namespace %s",
			platformv1alpha1.ReasonNamespaceAdoptRefused, name)
	}

	patched := existing.DeepCopy()
	patched.Labels = resources.MergeLabels(resources.ManagedLabels(leaseObj.Name), leaseObj.Spec.Namespace.Labels)
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	for k, v := range leaseObj.Spec.Namespace.Annotations {
		patched.Annotations[k] = v
	}
	patched.Annotations[platformv1alpha1.AnnotationLeaseUID] = string(leaseObj.UID)

	if mapsEqual(existing.Labels, patched.Labels) && mapsEqual(existing.Annotations, patched.Annotations) {
		return name, false, nil
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

	if resources.ResourceListsEqual(existing.Spec.Hard, desired.Spec.Hard) &&
		mapsEqual(existing.Labels, desired.Labels) {
		return nil
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

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func timePtr(t *metav1.Time) *time.Time {
	if t == nil {
		return nil
	}
	x := t.Time
	return &x
}

// SetupWithManager sets up the controller with the Manager.
//
// Watches:
//   - EnvironmentLease (primary). No GenerationChangedPredicate: we must see
//     deletions, finalizer updates, and annotation-only changes.
//   - ResourceQuota/LimitRange/NetworkPolicy via Owns for drift recovery.
//   - Namespace via label map + status.namespace field index.
func (r *EnvironmentLeaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorderFor("environmentlease-controller")
	}
	if r.Clock == nil {
		r.Clock = lease.RealClock{}
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&platformv1alpha1.EnvironmentLease{},
		indexStatusNamespace,
		func(obj client.Object) []string {
			l := obj.(*platformv1alpha1.EnvironmentLease)
			if l.Status.Namespace == "" {
				return nil
			}
			return []string{l.Status.Namespace}
		},
	); err != nil {
		return fmt.Errorf("index status.namespace: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&platformv1alpha1.EnvironmentLease{},
		indexPolicyRefName,
		func(obj client.Object) []string {
			l := obj.(*platformv1alpha1.EnvironmentLease)
			if l.Spec.PolicyRef == nil || l.Spec.PolicyRef.Name == "" {
				return nil
			}
			return []string{l.Spec.PolicyRef.Name}
		},
	); err != nil {
		return fmt.Errorf("index spec.policyRef.name: %w", err)
	}

	mapNamespace := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		labels := obj.GetLabels()
		leaseName := labels[platformv1alpha1.LabelLease]
		if leaseName != "" && labels[platformv1alpha1.LabelManagedBy] == platformv1alpha1.ManagedByValue {
			return []reconcile.Request{{NamespacedName: types.NamespacedName{Name: leaseName}}}
		}
		var list platformv1alpha1.EnvironmentLeaseList
		if err := r.List(ctx, &list, client.MatchingFields{indexStatusNamespace: obj.GetName()}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(list.Items))
		for i := range list.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
		}
		return reqs
	})

	mapPolicy := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var list platformv1alpha1.EnvironmentLeaseList
		if err := r.List(ctx, &list, client.MatchingFields{indexPolicyRefName: obj.GetName()}); err != nil {
			return nil
		}
		reqs := make([]reconcile.Request, 0, len(list.Items))
		for i := range list.Items {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
		}
		return reqs
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
		Watches(&platformv1alpha1.EnvironmentLeasePolicy{}, mapPolicy).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Named("environmentlease").
		Complete(r)
}
