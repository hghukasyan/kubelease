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
	"errors"
	"fmt"
	"strings"
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
	"github.com/hghukasyan/kubelease/internal/identity"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/metrics"
	"github.com/hghukasyan/kubelease/internal/placement"
	"github.com/hghukasyan/kubelease/internal/policy"
	"github.com/hghukasyan/kubelease/internal/remote"
	"github.com/hghukasyan/kubelease/internal/resources"
)

const (
	cleanupRequeueAfter    = 5 * time.Second
	targetUnavailableAfter = 30 * time.Second
	remoteDriftRequeue     = 2 * time.Minute
	indexStatusNamespace   = ".status.namespace"
	indexPolicyRefName     = ".spec.policyRef.name"

	eventEnvironmentReady = "EnvironmentProvisioned"
	eventLeaseRenewed     = "LeaseRenewed"
	eventLeaseExpiring    = "LeaseExpiring"
	eventLeaseExpired     = "LeaseExpired"
	eventLeaseIdleExpired = "LeaseIdleExpired"
	eventSourceClosed     = "SourceClosed"
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
	// Provider builds remote cluster clients for ClusterTarget-backed leases.
	Provider remote.Provider
	// ControlClusterID is stamped onto managed Namespaces (kube-system UID of control plane).
	ControlClusterID string
	// Outages shares per-target backoff across leases.
	Outages *remote.OutageTracker
	// Gate limits concurrent remote ops per target.
	Gate *remote.TargetGate
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
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=clustertargets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
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
		metrics.PolicyRejectionsTotal.Inc()
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

	lease.SyncExpirationStatus(leaseObj, resolved.IdleTTL, r.now())

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

	result, err := r.reconcileActive(ctx, leaseObj, previousPhase, resolved, pol)
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
	pol *platformv1alpha1.EnvironmentLeasePolicy,
) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	wasReady := lease.ConditionTrue(leaseObj, platformv1alpha1.ConditionReady)

	sess, result, err := r.resolveTargetSessionForProvision(ctx, leaseObj, pol)
	if result != nil {
		return *result, err
	}
	if err != nil {
		return r.handleTargetError(leaseObj, err)
	}
	if r.Gate != nil {
		release := r.Gate.Acquire(sess.Name)
		defer release()
	}
	if leaseObj.Status.Cluster == nil {
		leaseObj.Status.Cluster = &platformv1alpha1.ClusterStatus{Name: sess.Name}
	} else {
		leaseObj.Status.Cluster.Name = sess.Name
	}
	lease.SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionTrue,
		platformv1alpha1.ReasonEnvironmentReady, fmt.Sprintf("Using cluster %s", sess.Name))

	// Persist sticky cluster selection before provisioning when newly decided.
	if previousPhase != platformv1alpha1.LeasePhaseActive &&
		previousPhase != platformv1alpha1.LeasePhaseExpiring &&
		leaseObj.Status.Namespace == "" {
		if err := r.Status().Update(ctx, leaseObj); err != nil {
			return ctrl.Result{}, fmt.Errorf("persist status.cluster: %w", err)
		}
		if err := r.Get(ctx, client.ObjectKeyFromObject(leaseObj), leaseObj); err != nil {
			return ctrl.Result{}, err
		}
		leaseObj.Status.Effective = resolved.ToEffectiveStatus()
		leaseObj.Status.Cluster = &platformv1alpha1.ClusterStatus{Name: sess.Name}
	}

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

	nsName, created, err := r.ensureNamespace(ctx, sess, leaseObj)
	if err != nil {
		r.observeOp(sess, "create", err)
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonNamespaceCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	r.observeOp(sess, "create", nil)
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
		leaseObj.Status.Cluster = &platformv1alpha1.ClusterStatus{Name: sess.Name}
	}
	if created {
		log.Info("provisioning environment", "lease", leaseObj.Name, "namespace", nsName, "cluster", sess.Name)
	}

	if err := r.ensureResourceQuota(ctx, sess, working, nsName); err != nil {
		r.observeOp(sess, "update", err)
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonResourceQuotaCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if err := r.ensureLimitRange(ctx, sess, working, nsName); err != nil {
		r.observeOp(sess, "update", err)
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonLimitRangeCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}
	if err := r.ensureNetworkPolicy(ctx, sess, working, nsName); err != nil {
		r.observeOp(sess, "update", err)
		lease.MarkFailed(leaseObj, platformv1alpha1.ReasonNetworkPolicyCreationFailed, err.Error())
		metrics.ProvisionFailuresTotal.Inc()
		return ctrl.Result{}, err
	}

	// Emit pending expiration warnings (persisted in status.warningsDelivered).
	r.emitWarnings(ctx, leaseObj)

	expiring := lease.IsExpiringWindow(leaseObj, r.now())
	lease.MarkReady(leaseObj, expiring)
	lease.SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionTrue,
		platformv1alpha1.ReasonEnvironmentReady, fmt.Sprintf("Using cluster %s", sess.Name))

	if !wasReady && previousPhase != platformv1alpha1.LeasePhaseActive &&
		previousPhase != platformv1alpha1.LeasePhaseExpiring {
		metrics.LeasesCreatedTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Eventf(leaseObj, corev1.EventTypeNormal, eventEnvironmentReady,
				"Environment provisioned in namespace %s on cluster %s", nsName, sess.Name)
		}
	}

	deadline := lease.EffectiveDeadline(leaseObj)
	if deadline == nil {
		if !sess.Local {
			return ctrl.Result{RequeueAfter: remoteDriftRequeue}, nil
		}
		return ctrl.Result{}, nil
	}
	until := lease.NextReconcileAfter(
		r.now(),
		*deadline,
		lease.WarningDurations(leaseObj.Spec.Warnings),
		leaseObj.Status.WarningsDelivered,
	)
	if !sess.Local && (until == 0 || until > remoteDriftRequeue) {
		until = remoteDriftRequeue
	}
	return ctrl.Result{RequeueAfter: until}, nil
}

func (r *EnvironmentLeaseReconciler) resolveTargetSessionForProvision(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
	pol *platformv1alpha1.EnvironmentLeasePolicy,
) (*remote.TargetSession, *ctrl.Result, error) {
	metrics.PlacementAttemptsTotal.Inc()
	counts, err := placement.CountActiveLeases(ctx, r.Client)
	if err != nil {
		return nil, nil, err
	}
	decision, err := placement.Decide(ctx, r.Client, leaseObj, pol, placement.Options{
		ActiveLeaseCounts: counts,
	})
	if err != nil {
		if strings.Contains(err.Error(), platformv1alpha1.ReasonPolicyViolation) {
			lease.MarkFailed(leaseObj, platformv1alpha1.ReasonPolicyViolation, err.Error())
			metrics.ProvisionFailuresTotal.Inc()
			metrics.PolicyRejectionsTotal.Inc()
			return nil, &ctrl.Result{}, nil
		}
		return nil, nil, err
	}
	if decision.Pending {
		metrics.PlacementFailuresTotal.Inc()
		lease.MarkPending(leaseObj, platformv1alpha1.ReasonNoMatchingCluster, decision.Message)
		lease.SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionFalse,
			platformv1alpha1.ReasonNoMatchingCluster, decision.Message)
		return nil, &ctrl.Result{RequeueAfter: targetUnavailableAfter}, nil
	}

	if r.Outages != nil {
		if wait := r.Outages.RequeueAfter(decision.ClusterName, r.now()); wait > 0 {
			lease.SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionFalse,
				platformv1alpha1.ReasonTargetClusterUnavailable,
				fmt.Sprintf("target %s in shared outage backoff", decision.ClusterName))
			return nil, &ctrl.Result{RequeueAfter: wait}, nil
		}
	}

	clusterStatus := &platformv1alpha1.ClusterStatus{Name: decision.ClusterName}
	if leaseObj.Status.Cluster != nil && leaseObj.Status.Cluster.Name == decision.ClusterName {
		clusterStatus.TargetUID = leaseObj.Status.Cluster.TargetUID
		clusterStatus.RemoteIdentity = leaseObj.Status.Cluster.RemoteIdentity
	}
	leaseObj.Status.Cluster = clusterStatus

	sess, err := remote.ResolveNamedTarget(ctx, r.Client, r.Client, r.Provider, decision.ClusterName)
	if err != nil {
		return nil, nil, err
	}

	if !sess.Local {
		target := &platformv1alpha1.ClusterTarget{}
		if err := r.Get(ctx, types.NamespacedName{Name: sess.Name}, target); err == nil {
			leaseObj.Status.Cluster.TargetUID = string(target.UID)
			if target.Status.RemoteIdentity != "" {
				leaseObj.Status.Cluster.RemoteIdentity = target.Status.RemoteIdentity
			}
		}
		if leaseObj.Status.Cluster.RemoteIdentity == "" {
			id, idErr := identity.ProbeRemoteIdentity(ctx, sess.Client)
			if idErr != nil {
				return nil, nil, fmt.Errorf("probe remote identity: %w", idErr)
			}
			leaseObj.Status.Cluster.RemoteIdentity = id
		}
		if r.Outages != nil {
			r.Outages.Clear(sess.Name)
		}
	}
	return sess, nil, nil
}

func (r *EnvironmentLeaseReconciler) resolveTargetSession(
	ctx context.Context,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (*remote.TargetSession, error) {
	name := platformv1alpha1.LocalClusterName
	if leaseObj.Spec.ClusterRef != nil && leaseObj.Spec.ClusterRef.Name != "" {
		name = leaseObj.Spec.ClusterRef.Name
	} else if leaseObj.Status.Cluster != nil && leaseObj.Status.Cluster.Name != "" {
		name = leaseObj.Status.Cluster.Name
	}
	return remote.ResolveNamedTarget(ctx, r.Client, r.Client, r.Provider, name)
}

func (r *EnvironmentLeaseReconciler) handleTargetError(
	leaseObj *platformv1alpha1.EnvironmentLease,
	err error,
) (ctrl.Result, error) {
	reason := platformv1alpha1.ReasonTargetClusterUnavailable
	msg := err.Error()
	cluster := ""
	if leaseObj.Status.Cluster != nil {
		cluster = leaseObj.Status.Cluster.Name
	}
	if te, ok := remote.AsTargetError(err); ok {
		reason = te.Reason
		msg = te.Message
	}
	lease.MarkFailed(leaseObj, reason, msg)
	lease.SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionFalse, reason, msg)
	metrics.ProvisionFailuresTotal.Inc()
	wait := targetUnavailableAfter
	if r.Outages != nil && cluster != "" && cluster != platformv1alpha1.LocalClusterName {
		wait = r.Outages.MarkUnavailable(cluster, r.now())
	}
	return ctrl.Result{RequeueAfter: wait}, nil
}

func (r *EnvironmentLeaseReconciler) observeOp(sess *remote.TargetSession, op string, err error) {
	r.observeClusterOp(sess, op, err, -1)
}

func (r *EnvironmentLeaseReconciler) observeClusterOp(sess *remote.TargetSession, op string, err error, seconds float64) {
	if sess == nil || sess.Local {
		return
	}
	result := metrics.ResultSuccess
	if err != nil {
		result = metrics.ResultFailure
	}
	metrics.ObserveClusterOp(sess.Name, op, result, seconds)
}

// emitWarnings fires pending LeaseExpiring events and records delivery in status.
func (r *EnvironmentLeaseReconciler) emitWarnings(_ context.Context, leaseObj *platformv1alpha1.EnvironmentLease) {
	deadline := lease.EffectiveDeadline(leaseObj)
	if deadline == nil {
		return
	}
	pending := lease.PendingWarnings(
		r.now(),
		*deadline,
		lease.WarningDurations(leaseObj.Spec.Warnings),
		leaseObj.Status.WarningsDelivered,
	)
	for _, w := range pending {
		remaining := deadline.Sub(r.now())
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

	reason := leaseObj.Status.ExpirationReason
	if reason == "" {
		var idle *time.Time
		if leaseObj.Status.IdleExpiresAt != nil {
			t := leaseObj.Status.IdleExpiresAt.Time
			idle = &t
		}
		hard := time.Time{}
		if leaseObj.Status.ExpiresAt != nil {
			hard = leaseObj.Status.ExpiresAt.Time
		}
		reason = lease.ResolveExpirationReason(r.now(), hard, idle)
	}

	if firstExpiry {
		metrics.LeasesExpiredTotal.Inc()
		switch reason {
		case platformv1alpha1.ExpirationReasonIdleTimeout:
			metrics.IdleExpirationsTotal.Inc()
		case platformv1alpha1.ExpirationReasonManualExpiration:
			metrics.ManualExpirationsTotal.Inc()
		}
		if r.Recorder != nil {
			switch reason {
			case platformv1alpha1.ExpirationReasonIdleTimeout:
				r.Recorder.Eventf(leaseObj, corev1.EventTypeNormal, eventLeaseIdleExpired,
					"Lease idle timeout elapsed; cleaning up environment")
			case platformv1alpha1.ExpirationReasonSourceClosed:
				r.Recorder.Eventf(leaseObj, corev1.EventTypeNormal, eventSourceClosed,
					"Upstream source closed; cleaning up environment")
			default:
				r.Recorder.Eventf(leaseObj, corev1.EventTypeNormal, eventLeaseExpired,
					"Lease expired (%s); cleaning up environment", reason)
			}
			r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventCleanupStarted,
				"Cleanup started")
		}
		log.Info("lease expired",
			"reason", reason,
			"effectiveExpiresAt", leaseObj.Status.EffectiveExpiresAt,
			"expiresAt", leaseObj.Status.ExpiresAt)
	}

	lease.MarkExpired(leaseObj, reason)
	done, err := r.cleanupEnvironment(ctx, leaseObj)
	if err != nil {
		var ome *identity.OwnershipMismatchError
		if errors.As(err, &ome) {
			lease.MarkCleanupFailed(leaseObj, err.Error())
			lease.SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
				platformv1alpha1.ReasonOwnershipMismatch, err.Error())
			lease.EnsureObservedGeneration(leaseObj)
			if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			// Stop hot-looping; operator must fix identity or set force-cleanup-acknowledged.
			return ctrl.Result{}, nil
		}
		lease.MarkCleanupFailed(leaseObj, err.Error())
		metrics.CleanupFailuresTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeWarning, eventCleanupFailed, err.Error())
		}
		lease.EnsureObservedGeneration(leaseObj)
		if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: targetUnavailableAfter}, nil
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
	firstDelete := leaseObj.Status.Phase != platformv1alpha1.LeasePhaseCleaning &&
		leaseObj.Status.Phase != platformv1alpha1.LeasePhaseExpired
	if leaseObj.Status.ExpirationReason != platformv1alpha1.ExpirationReasonSourceClosed {
		leaseObj.Status.ExpirationReason = platformv1alpha1.ExpirationReasonManualExpiration
	}
	if firstDelete {
		if leaseObj.Status.ExpirationReason == platformv1alpha1.ExpirationReasonManualExpiration {
			metrics.ManualExpirationsTotal.Inc()
		}
		if r.Recorder != nil {
			if leaseObj.Status.ExpirationReason == platformv1alpha1.ExpirationReasonSourceClosed {
				r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventSourceClosed,
					"Upstream source closed; cleaning up environment")
			}
			r.Recorder.Event(leaseObj, corev1.EventTypeNormal, eventCleanupStarted, "Cleanup started")
		}
	}
	lease.MarkCleaning(leaseObj, "Deleting managed environment")

	done, err := r.cleanupEnvironment(ctx, leaseObj)
	if err != nil {
		var ome *identity.OwnershipMismatchError
		if errors.As(err, &ome) {
			lease.MarkCleanupFailed(leaseObj, err.Error())
			lease.SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
				platformv1alpha1.ReasonOwnershipMismatch, err.Error())
			if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
				return ctrl.Result{}, patchErr
			}
			return ctrl.Result{}, nil
		}
		lease.MarkCleanupFailed(leaseObj, err.Error())
		metrics.CleanupFailuresTotal.Inc()
		if r.Recorder != nil {
			r.Recorder.Event(leaseObj, corev1.EventTypeWarning, eventCleanupFailed, err.Error())
		}
		if patchErr := r.patchStatusIfChanged(ctx, leaseObj, before); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		// Keep finalizer; back off while remote is unavailable (RequireRemoteCleanup).
		return ctrl.Result{RequeueAfter: targetUnavailableAfter}, nil
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

	if identity.ForceCleanupAcknowledged(leaseObj) {
		ctrl.LoggerFrom(ctx).Info("force-cleanup-acknowledged: skipping remote delete",
			"namespace", nsName, "lease", leaseObj.Name)
		return true, nil
	}

	sess, err := r.resolveTargetSession(ctx, leaseObj)
	if err != nil {
		mode := leaseObj.Spec.EffectiveCleanupMode()
		if mode == platformv1alpha1.CleanupModeBestEffort {
			ctrl.LoggerFrom(ctx).Info("best-effort cleanup: skipping remote delete",
				"namespace", nsName, "error", err.Error())
			return true, nil
		}
		lease.SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionFalse,
			platformv1alpha1.ReasonRemoteCleanupBlocked, err.Error())
		return false, fmt.Errorf("%s: %w", platformv1alpha1.ReasonRemoteCleanupBlocked, err)
	}

	release := func() {}
	if r.Gate != nil {
		release = r.Gate.Acquire(sess.Name)
	}
	defer release()

	// Prevent deleting the same Namespace name on a different cluster after credential swap.
	if !sess.Local && leaseObj.Status.Cluster != nil && leaseObj.Status.Cluster.RemoteIdentity != "" {
		liveID, idErr := identity.ProbeRemoteIdentity(ctx, sess.Client)
		if idErr != nil {
			return false, fmt.Errorf("probe target identity before cleanup: %w", idErr)
		}
		if err := identity.VerifyLiveTargetIdentity(leaseObj.Status.Cluster.RemoteIdentity, liveID); err != nil {
			lease.SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
				platformv1alpha1.ReasonOwnershipMismatch, err.Error())
			return false, err
		}
	}

	ns := &corev1.Namespace{}
	start := time.Now()
	err = sess.Client.Get(ctx, types.NamespacedName{Name: nsName}, ns)
	r.observeClusterOp(sess, "get", err, time.Since(start).Seconds())
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		mode := leaseObj.Spec.EffectiveCleanupMode()
		if mode == platformv1alpha1.CleanupModeBestEffort {
			ctrl.LoggerFrom(ctx).Info("best-effort cleanup: remote get failed",
				"namespace", nsName, "error", err.Error())
			return true, nil
		}
		return false, fmt.Errorf("get namespace %s on cluster %s: %w", nsName, sess.Name, err)
	}

	if err := identity.VerifyNamespaceOwnership(ns, leaseObj, r.ControlClusterID); err != nil {
		lease.SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
			platformv1alpha1.ReasonOwnershipMismatch, err.Error())
		return false, err
	}

	if ns.DeletionTimestamp.IsZero() {
		start := time.Now()
		err := sess.Client.Delete(ctx, ns)
		r.observeClusterOp(sess, "delete", err, time.Since(start).Seconds())
		if err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete namespace %s on cluster %s: %w", nsName, sess.Name, err)
		}
	}
	return false, nil
}

func (r *EnvironmentLeaseReconciler) ensureNamespace(
	ctx context.Context,
	sess *remote.TargetSession,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (string, bool, error) {
	if leaseObj.Status.Namespace != "" {
		return r.ensureExistingNamespace(ctx, sess, leaseObj, leaseObj.Status.Namespace)
	}
	if leaseObj.Spec.Namespace.Name != "" {
		return r.ensureExistingNamespace(ctx, sess, leaseObj, leaseObj.Spec.Namespace.Name)
	}

	remoteID := ""
	if leaseObj.Status.Cluster != nil {
		remoteID = leaseObj.Status.Cluster.RemoteIdentity
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: leaseObj.Spec.Namespace.GenerateName,
			Labels:       resources.MergeLabels(resources.ManagedLabels(leaseObj.Name), leaseObj.Spec.Namespace.Labels),
			Annotations:  managedAnnotations(leaseObj, r.ControlClusterID, remoteID),
		},
	}
	if err := sess.Client.Create(ctx, ns); err != nil {
		return "", false, fmt.Errorf("create namespace with generateName %q: %w",
			leaseObj.Spec.Namespace.GenerateName, err)
	}
	return ns.Name, true, nil
}

func managedAnnotations(leaseObj *platformv1alpha1.EnvironmentLease, controlClusterID, remoteIdentity string) map[string]string {
	out := map[string]string{}
	for k, v := range leaseObj.Spec.Namespace.Annotations {
		out[k] = v
	}
	out[platformv1alpha1.AnnotationLeaseUID] = string(leaseObj.UID)
	if controlClusterID != "" {
		out[platformv1alpha1.AnnotationControlClusterID] = controlClusterID
	}
	if remoteIdentity != "" {
		out[platformv1alpha1.AnnotationTargetIdentity] = remoteIdentity
	}
	return out
}

func (r *EnvironmentLeaseReconciler) ensureExistingNamespace(
	ctx context.Context,
	sess *remote.TargetSession,
	leaseObj *platformv1alpha1.EnvironmentLease,
	name string,
) (string, bool, error) {
	remoteID := ""
	if leaseObj.Status.Cluster != nil {
		remoteID = leaseObj.Status.Cluster.RemoteIdentity
	}
	desired, err := resources.DesiredNamespace(leaseObj, name, r.ControlClusterID, remoteID)
	if err != nil {
		return "", false, err
	}

	existing := &corev1.Namespace{}
	err = sess.Client.Get(ctx, types.NamespacedName{Name: name}, existing)
	if apierrors.IsNotFound(err) {
		// Recreate same identity when status.namespace is set (drift recovery).
		if err := sess.Client.Create(ctx, desired); err != nil {
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
	if r.ControlClusterID != "" {
		patched.Annotations[platformv1alpha1.AnnotationControlClusterID] = r.ControlClusterID
	}
	if remoteID != "" {
		patched.Annotations[platformv1alpha1.AnnotationTargetIdentity] = remoteID
	}

	if mapsEqual(existing.Labels, patched.Labels) && mapsEqual(existing.Annotations, patched.Annotations) {
		return name, false, nil
	}
	if err := sess.Client.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return "", false, fmt.Errorf("patch namespace %s: %w", name, err)
	}
	return name, false, nil
}

func (r *EnvironmentLeaseReconciler) ensureResourceQuota(
	ctx context.Context,
	sess *remote.TargetSession,
	leaseObj *platformv1alpha1.EnvironmentLease,
	namespace string,
) error {
	desired := resources.DesiredResourceQuota(leaseObj, namespace)
	if desired == nil {
		return nil
	}
	if sess.SetLeaseOwner {
		if err := controllerutil.SetControllerReference(leaseObj, desired, r.Scheme); err != nil {
			return fmt.Errorf("set owner reference on ResourceQuota: %w", err)
		}
	}

	existing := &corev1.ResourceQuota{}
	err := sess.Client.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := sess.Client.Create(ctx, desired); err != nil {
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
	if sess.SetLeaseOwner {
		if err := controllerutil.SetControllerReference(leaseObj, patched, r.Scheme); err != nil {
			return err
		}
	}
	if err := sess.Client.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return fmt.Errorf("patch ResourceQuota %s/%s: %w", namespace, desired.Name, err)
	}
	return nil
}

func (r *EnvironmentLeaseReconciler) ensureLimitRange(
	ctx context.Context,
	sess *remote.TargetSession,
	leaseObj *platformv1alpha1.EnvironmentLease,
	namespace string,
) error {
	desired := resources.DesiredLimitRange(leaseObj, namespace)
	if desired == nil {
		return nil
	}
	if sess.SetLeaseOwner {
		if err := controllerutil.SetControllerReference(leaseObj, desired, r.Scheme); err != nil {
			return fmt.Errorf("set owner reference on LimitRange: %w", err)
		}
	}

	existing := &corev1.LimitRange{}
	err := sess.Client.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := sess.Client.Create(ctx, desired); err != nil {
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
	if sess.SetLeaseOwner {
		if err := controllerutil.SetControllerReference(leaseObj, patched, r.Scheme); err != nil {
			return err
		}
	}
	if err := sess.Client.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
		return fmt.Errorf("patch LimitRange %s/%s: %w", namespace, desired.Name, err)
	}
	return nil
}

func (r *EnvironmentLeaseReconciler) ensureNetworkPolicy(
	ctx context.Context,
	sess *remote.TargetSession,
	leaseObj *platformv1alpha1.EnvironmentLease,
	namespace string,
) error {
	desired := resources.DesiredNetworkPolicy(leaseObj, namespace)
	if desired == nil {
		return nil
	}
	if sess.SetLeaseOwner {
		if err := controllerutil.SetControllerReference(leaseObj, desired, r.Scheme); err != nil {
			return fmt.Errorf("set owner reference on NetworkPolicy: %w", err)
		}
	}

	existing := &networkingv1.NetworkPolicy{}
	err := sess.Client.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		if err := sess.Client.Create(ctx, desired); err != nil {
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
	if sess.SetLeaseOwner {
		if err := controllerutil.SetControllerReference(leaseObj, patched, r.Scheme); err != nil {
			return err
		}
	}
	if err := sess.Client.Patch(ctx, patched, client.MergeFrom(existing)); err != nil {
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

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&platformv1alpha1.EnvironmentLease{},
		indexClusterRefName,
		func(obj client.Object) []string {
			l := obj.(*platformv1alpha1.EnvironmentLease)
			if l.Spec.ClusterRef == nil || l.Spec.ClusterRef.Name == "" {
				return nil
			}
			return []string{l.Spec.ClusterRef.Name}
		},
	); err != nil {
		return fmt.Errorf("index spec.clusterRef.name: %w", err)
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&platformv1alpha1.EnvironmentLease{},
		placement.StatusClusterNameIndex,
		func(obj client.Object) []string {
			l := obj.(*platformv1alpha1.EnvironmentLease)
			if l.Status.Cluster == nil || l.Status.Cluster.Name == "" {
				return nil
			}
			return []string{l.Status.Cluster.Name}
		},
	); err != nil {
		return fmt.Errorf("index status.cluster.name: %w", err)
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

	mapClusterTarget := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		name := obj.GetName()
		seen := map[string]struct{}{}
		var reqs []reconcile.Request
		add := func(n string) {
			if _, ok := seen[n]; ok {
				return
			}
			seen[n] = struct{}{}
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: n}})
		}
		var assigned platformv1alpha1.EnvironmentLeaseList
		if err := r.List(ctx, &assigned, client.MatchingFields{placement.StatusClusterNameIndex: name}); err == nil {
			for i := range assigned.Items {
				add(assigned.Items[i].Name)
			}
		}
		var refs platformv1alpha1.EnvironmentLeaseList
		if err := r.List(ctx, &refs, client.MatchingFields{indexClusterRefName: name}); err == nil {
			for i := range refs.Items {
				add(refs.Items[i].Name)
			}
		}
		// Waiting for placement: Pending with placement set (bounded by listing Pending only).
		var pending platformv1alpha1.EnvironmentLeaseList
		if err := r.List(ctx, &pending); err == nil {
			for i := range pending.Items {
				l := &pending.Items[i]
				if l.Status.Phase != platformv1alpha1.LeasePhasePending {
					continue
				}
				if l.Spec.Placement == nil && (l.Spec.ClusterRef == nil || l.Spec.ClusterRef.Name == "") {
					continue
				}
				add(l.Name)
			}
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
		Watches(&platformv1alpha1.ClusterTarget{}, mapClusterTarget).
		WithOptions(controller.Options{MaxConcurrentReconciles: 4}).
		Named("environmentlease").
		Complete(r)
}
