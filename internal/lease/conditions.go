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

package lease

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// SetCondition updates or adds a condition on the lease status.
func SetCondition(
	lease *platformv1alpha1.EnvironmentLease,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&lease.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: lease.Generation,
		LastTransitionTime: metav1.NewTime(time.Now().UTC()),
	})
}

// MarkProvisioning sets conditions for an environment still being created.
func MarkProvisioning(lease *platformv1alpha1.EnvironmentLease, message string) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseProvisioning
	SetCondition(lease, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		platformv1alpha1.ReasonProvisioning, message)
	SetCondition(lease, platformv1alpha1.ConditionEnvironmentCreated, metav1.ConditionFalse,
		platformv1alpha1.ReasonProvisioning, message)
}

// MarkReady sets conditions for a fully provisioned active environment.
func MarkReady(lease *platformv1alpha1.EnvironmentLease) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseActive
	SetCondition(lease, platformv1alpha1.ConditionReady, metav1.ConditionTrue,
		platformv1alpha1.ReasonEnvironmentReady, "Environment successfully provisioned")
	SetCondition(lease, platformv1alpha1.ConditionEnvironmentCreated, metav1.ConditionTrue,
		platformv1alpha1.ReasonEnvironmentReady, "Managed environment resources are present")
}

// MarkFailed sets Failed phase and Ready=False with the given reason.
func MarkFailed(lease *platformv1alpha1.EnvironmentLease, reason, message string) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseFailed
	SetCondition(lease, platformv1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
}

// MarkExpired sets Expired/Expiring phase and Ready=False.
func MarkExpired(lease *platformv1alpha1.EnvironmentLease) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseExpiring
	SetCondition(lease, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		platformv1alpha1.ReasonLeaseExpired, "Lease TTL has elapsed")
}

// MarkCleaning sets Cleaning phase and Cleanup condition.
func MarkCleaning(lease *platformv1alpha1.EnvironmentLease, message string) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseCleaning
	SetCondition(lease, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupInProgress, message)
	SetCondition(lease, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupInProgress, message)
}

// MarkCleanupComplete marks cleanup finished (before finalizer removal).
func MarkCleanupComplete(lease *platformv1alpha1.EnvironmentLease) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseExpired
	SetCondition(lease, platformv1alpha1.ConditionCleanup, metav1.ConditionTrue,
		platformv1alpha1.ReasonCleanupComplete, "Managed environment cleaned up")
}

// MarkCleanupFailed records a cleanup failure.
func MarkCleanupFailed(lease *platformv1alpha1.EnvironmentLease, message string) {
	lease.Status.Phase = platformv1alpha1.LeasePhaseCleaning
	SetCondition(lease, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupFailed, message)
}
