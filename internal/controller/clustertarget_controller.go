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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/identity"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/metrics"
	"github.com/hghukasyan/kubelease/internal/placement"
	"github.com/hghukasyan/kubelease/internal/remote"
)

const (
	indexClusterRefName      = ".spec.clusterRef.name"
	clusterTargetHealthAfter = 5 * time.Minute
	clusterTargetBackoff     = 30 * time.Second
)

// ClusterTargetReconciler performs lightweight health checks and blocks
// ClusterTarget deletion while EnvironmentLeases still reference the target.
type ClusterTargetReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Provider remote.Provider
	Clock    lease.Clock
	// ProbeVersion optionally overrides ServerVersion probing (tests).
	ProbeVersion func(ctx context.Context, cfg *rest.Config) (string, error)
}

func (r *ClusterTargetReconciler) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now().UTC()
}

func (r *ClusterTargetReconciler) probeVersion(ctx context.Context, cfg *rest.Config) (string, error) {
	if r.ProbeVersion != nil {
		return r.ProbeVersion(ctx, cfg)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", err
	}
	info, err := cs.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	return info.GitVersion, nil
}

// +kubebuilder:rbac:groups=platform.kubelease.io,resources=clustertargets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=clustertargets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.kubelease.io,resources=clustertargets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ClusterTargetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	target := &platformv1alpha1.ClusterTarget{}
	if err := r.Get(ctx, req.NamespacedName, target); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !target.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, target)
	}

	if !controllerutil.ContainsFinalizer(target, platformv1alpha1.ClusterTargetFinalizer) {
		controllerutil.AddFinalizer(target, platformv1alpha1.ClusterTargetFinalizer)
		if err := r.Update(ctx, target); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return r.reconcileHealth(ctx, target)
}

func (r *ClusterTargetReconciler) reconcileDelete(ctx context.Context, target *platformv1alpha1.ClusterTarget) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(target, platformv1alpha1.ClusterTargetFinalizer) {
		return ctrl.Result{}, nil
	}

	active, err := r.countReferencingLeases(ctx, target.Name)
	if err != nil {
		return ctrl.Result{}, err
	}
	if active > 0 {
		meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
			Type:               platformv1alpha1.ClusterTargetConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             platformv1alpha1.ReasonTargetHasActiveLeases,
			Message:            fmt.Sprintf("%d EnvironmentLease(s) still reference this target", active),
			ObservedGeneration: target.Generation,
		})
		target.Status.ObservedGeneration = target.Generation
		_ = r.Status().Update(ctx, target)
		return ctrl.Result{RequeueAfter: clusterTargetBackoff}, nil
	}

	if r.Provider != nil {
		r.Provider.Invalidate(target.Name)
	}

	err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current := &platformv1alpha1.ClusterTarget{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(target), current); err != nil {
			return err
		}
		if !controllerutil.ContainsFinalizer(current, platformv1alpha1.ClusterTargetFinalizer) {
			return nil
		}
		controllerutil.RemoveFinalizer(current, platformv1alpha1.ClusterTargetFinalizer)
		return r.Update(ctx, current)
	})
	return ctrl.Result{}, err
}

func (r *ClusterTargetReconciler) countReferencingLeases(ctx context.Context, targetName string) (int, error) {
	var list platformv1alpha1.EnvironmentLeaseList
	if err := r.List(ctx, &list, client.MatchingFields{indexClusterRefName: targetName}); err != nil {
		if err := r.List(ctx, &list); err != nil {
			return 0, err
		}
		n := 0
		for i := range list.Items {
			ref := list.Items[i].Spec.ClusterRef
			if ref != nil && ref.Name == targetName {
				n++
			}
		}
		return n, nil
	}
	return len(list.Items), nil
}

func (r *ClusterTargetReconciler) reconcileHealth(ctx context.Context, target *platformv1alpha1.ClusterTarget) (ctrl.Result, error) {
	defer refreshClusterTargetMetrics(ctx, r.Client)

	if !target.Spec.IsEnabled() {
		r.setTargetConditions(target, false, false, false,
			platformv1alpha1.ReasonClusterTargetDisabled, "ClusterTarget is disabled")
		target.Status.ObservedGeneration = target.Generation
		if err := r.Status().Update(ctx, target); err != nil {
			return ctrl.Result{}, err
		}
		if r.Provider != nil {
			r.Provider.Invalidate(target.Name)
		}
		return ctrl.Result{RequeueAfter: clusterTargetHealthAfter}, nil
	}

	cfg, err := r.Provider.RESTConfigFor(ctx, target)
	if err != nil {
		r.setTargetConditions(target, false, false, false,
			platformv1alpha1.ReasonCredentialsInvalid, err.Error())
		target.Status.ObservedGeneration = target.Generation
		_ = r.Status().Update(ctx, target)
		return ctrl.Result{RequeueAfter: clusterTargetBackoff}, nil
	}

	ver, err := r.probeVersion(ctx, cfg)
	if err != nil {
		r.setTargetConditions(target, false, true, false,
			platformv1alpha1.ReasonClusterUnreachable, err.Error())
		meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
			Type:               platformv1alpha1.ClusterTargetConditionDegraded,
			Status:             metav1.ConditionTrue,
			Reason:             platformv1alpha1.ReasonClusterUnreachable,
			Message:            err.Error(),
			ObservedGeneration: target.Generation,
		})
		target.Status.ObservedGeneration = target.Generation
		_ = r.Status().Update(ctx, target)
		metrics.ClusterConnectionFailuresTotal.Inc()
		metrics.ClusterHealth.WithLabelValues(target.Name).Set(0)
		return ctrl.Result{RequeueAfter: clusterTargetBackoff}, nil
	}

	cl, err := r.Provider.ClientFor(ctx, target)
	if err != nil {
		r.setTargetConditions(target, false, false, false,
			platformv1alpha1.ReasonCredentialsInvalid, err.Error())
		target.Status.ObservedGeneration = target.Generation
		_ = r.Status().Update(ctx, target)
		metrics.ClusterHealth.WithLabelValues(target.Name).Set(0)
		return ctrl.Result{RequeueAfter: clusterTargetBackoff}, nil
	}
	liveID, err := identity.ProbeRemoteIdentity(ctx, cl)
	if err != nil {
		r.setTargetConditions(target, false, true, false,
			platformv1alpha1.ReasonClusterUnreachable, err.Error())
		target.Status.ObservedGeneration = target.Generation
		_ = r.Status().Update(ctx, target)
		metrics.ClusterHealth.WithLabelValues(target.Name).Set(0)
		return ctrl.Result{RequeueAfter: clusterTargetBackoff}, nil
	}
	if target.Status.RemoteIdentity == "" {
		target.Status.RemoteIdentity = liveID
	} else if target.Status.RemoteIdentity != liveID {
		msg := fmt.Sprintf("remote identity changed (sticky=%s live=%s); refusing to follow credential swap",
			target.Status.RemoteIdentity, liveID)
		r.setTargetConditions(target, false, true, true,
			platformv1alpha1.ReasonIdentityDrift, msg)
		meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
			Type:               platformv1alpha1.ClusterTargetConditionDegraded,
			Status:             metav1.ConditionTrue,
			Reason:             platformv1alpha1.ReasonIdentityDrift,
			Message:            msg,
			ObservedGeneration: target.Generation,
		})
		target.Status.ObservedGeneration = target.Generation
		_ = r.Status().Update(ctx, target)
		metrics.ClusterHealth.WithLabelValues(target.Name).Set(0)
		if r.Provider != nil {
			r.Provider.Invalidate(target.Name)
		}
		return ctrl.Result{RequeueAfter: clusterTargetBackoff}, nil
	}

	target.Status.KubernetesVersion = ver
	r.setTargetConditions(target, true, true, true,
		platformv1alpha1.ReasonClusterReachable, "Remote API reachable")
	meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
		Type:               platformv1alpha1.ClusterTargetConditionDegraded,
		Status:             metav1.ConditionFalse,
		Reason:             platformv1alpha1.ReasonClusterReachable,
		Message:            "Healthy",
		ObservedGeneration: target.Generation,
	})
	r.refreshCapacity(ctx, target)
	target.Status.ObservedGeneration = target.Generation
	if err := r.Status().Update(ctx, target); err != nil {
		return ctrl.Result{}, err
	}
	metrics.ClusterHealth.WithLabelValues(target.Name).Set(1)
	return ctrl.Result{RequeueAfter: clusterTargetHealthAfter}, nil
}

func (r *ClusterTargetReconciler) refreshCapacity(ctx context.Context, target *platformv1alpha1.ClusterTarget) {
	active := int32(0)
	var list platformv1alpha1.EnvironmentLeaseList
	if err := r.List(ctx, &list, client.MatchingFields{placement.StatusClusterNameIndex: target.Name}); err != nil {
		// Fallback soft count without index.
		counts, err := placement.CountActiveLeases(ctx, r.Client)
		if err == nil {
			active = counts[target.Name]
		}
	} else {
		for i := range list.Items {
			l := &list.Items[i]
			if l.DeletionTimestamp.IsZero() && l.Status.Phase != platformv1alpha1.LeasePhaseExpired {
				active++
			}
		}
	}
	cap := &platformv1alpha1.ClusterCapacityStatus{ActiveLeases: active}
	if target.Spec.MaxActiveLeases != nil {
		cap.MaxLeases = target.Spec.MaxActiveLeases
	}
	target.Status.Capacity = cap
}

func refreshClusterTargetMetrics(ctx context.Context, c client.Client) {
	var list platformv1alpha1.ClusterTargetList
	if err := c.List(ctx, &list); err != nil {
		return
	}
	var ready, notReady float64
	for i := range list.Items {
		if meta.IsStatusConditionTrue(list.Items[i].Status.Conditions, platformv1alpha1.ClusterTargetConditionReady) {
			ready++
		} else {
			notReady++
		}
	}
	metrics.ClusterTargets.WithLabelValues("true").Set(ready)
	metrics.ClusterTargets.WithLabelValues("false").Set(notReady)
}

func (r *ClusterTargetReconciler) setTargetConditions(
	target *platformv1alpha1.ClusterTarget,
	ready, authenticated, reachable bool,
	reason, message string,
) {
	boolStatus := func(v bool) metav1.ConditionStatus {
		if v {
			return metav1.ConditionTrue
		}
		return metav1.ConditionFalse
	}
	meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
		Type:               platformv1alpha1.ClusterTargetConditionAuthenticated,
		Status:             boolStatus(authenticated),
		Reason:             reason,
		Message:            message,
		ObservedGeneration: target.Generation,
	})
	meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
		Type:               platformv1alpha1.ClusterTargetConditionReachable,
		Status:             boolStatus(reachable),
		Reason:             reason,
		Message:            message,
		ObservedGeneration: target.Generation,
	})
	meta.SetStatusCondition(&target.Status.Conditions, metav1.Condition{
		Type:               platformv1alpha1.ClusterTargetConditionReady,
		Status:             boolStatus(ready),
		Reason:             reason,
		Message:            message,
		ObservedGeneration: target.Generation,
	})
}

// SetupWithManager registers the ClusterTarget controller.
func (r *ClusterTargetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = lease.RealClock{}
	}
	if r.Provider == nil {
		return fmt.Errorf("ClusterTargetReconciler.Provider is required")
	}

	mapSecret := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		var list platformv1alpha1.ClusterTargetList
		if err := r.List(ctx, &list); err != nil {
			return nil
		}
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			return nil
		}
		var reqs []reconcile.Request
		for i := range list.Items {
			ref := list.Items[i].Spec.Credentials.SecretRef
			if ref.Name == secret.Name && ref.Namespace == secret.Namespace {
				if r.Provider != nil {
					r.Provider.Invalidate(list.Items[i].Name)
				}
				reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Name: list.Items[i].Name}})
			}
		}
		return reqs
	})

	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ClusterTarget{}).
		Watches(&corev1.Secret{}, mapSecret).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Named("clustertarget").
		Complete(r)
}
