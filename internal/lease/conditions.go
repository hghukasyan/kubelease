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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// SetCondition updates or adds a condition. LastTransitionTime only changes
// when Status flips (handled by meta.SetStatusCondition).
func SetCondition(
	leaseObj *platformv1alpha1.EnvironmentLease,
	condType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	meta.SetStatusCondition(&leaseObj.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: leaseObj.Generation,
	})
}

// MarkProvisioning sets conditions for an environment still being created.
func MarkProvisioning(leaseObj *platformv1alpha1.EnvironmentLease, message string) {
	leaseObj.Status.Phase = platformv1alpha1.LeasePhaseProvisioning
	SetCondition(leaseObj, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		platformv1alpha1.ReasonProvisioning, message)
	SetCondition(leaseObj, platformv1alpha1.ConditionExpiring, metav1.ConditionFalse,
		platformv1alpha1.ReasonProvisioning, "Not yet active")
	SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
		platformv1alpha1.ReasonProvisioning, "Not cleaning")
	SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionFalse,
		platformv1alpha1.ReasonProvisioning, "Provisioning")
}

// MarkReady sets conditions for a fully provisioned active environment.
func MarkReady(leaseObj *platformv1alpha1.EnvironmentLease, expiring bool) {
	if expiring {
		leaseObj.Status.Phase = platformv1alpha1.LeasePhaseExpiring
		SetCondition(leaseObj, platformv1alpha1.ConditionExpiring, metav1.ConditionTrue,
			platformv1alpha1.ReasonLeaseExpiring, "Lease is approaching expiration")
	} else {
		leaseObj.Status.Phase = platformv1alpha1.LeasePhaseActive
		SetCondition(leaseObj, platformv1alpha1.ConditionExpiring, metav1.ConditionFalse,
			platformv1alpha1.ReasonEnvironmentReady, "Lease is not in warning window")
	}
	SetCondition(leaseObj, platformv1alpha1.ConditionReady, metav1.ConditionTrue,
		platformv1alpha1.ReasonEnvironmentReady, "Environment successfully provisioned")
	SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
		platformv1alpha1.ReasonEnvironmentReady, "Not cleaning")
	SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionFalse,
		platformv1alpha1.ReasonEnvironmentReady, "Healthy")
}

// MarkPending sets Pending phase while waiting for a matching ClusterTarget.
func MarkPending(leaseObj *platformv1alpha1.EnvironmentLease, reason, message string) {
	leaseObj.Status.Phase = platformv1alpha1.LeasePhasePending
	SetCondition(leaseObj, platformv1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
	SetCondition(leaseObj, platformv1alpha1.ConditionTargetClusterReady, metav1.ConditionFalse, reason, message)
	SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionFalse, reason, message)
}

// MarkFailed sets Failed phase and Ready=False with the given reason.
func MarkFailed(leaseObj *platformv1alpha1.EnvironmentLease, reason, message string) {
	leaseObj.Status.Phase = platformv1alpha1.LeasePhaseFailed
	SetCondition(leaseObj, platformv1alpha1.ConditionReady, metav1.ConditionFalse, reason, message)
	SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionTrue, reason, message)
}

// MarkExpired sets Expiring/expired ready state before cleanup.
func MarkExpired(leaseObj *platformv1alpha1.EnvironmentLease, reason platformv1alpha1.ExpirationReason) {
	if reason == "" {
		reason = platformv1alpha1.ExpirationReasonTTLExpired
	}
	leaseObj.Status.ExpirationReason = reason
	leaseObj.Status.Phase = platformv1alpha1.LeasePhaseExpiring
	msg := fmt.Sprintf("Lease expired (%s)", reason)
	SetCondition(leaseObj, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		platformv1alpha1.ReasonLeaseExpired, msg)
	SetCondition(leaseObj, platformv1alpha1.ConditionExpiring, metav1.ConditionTrue,
		platformv1alpha1.ReasonLeaseExpired, msg)
}

// MarkCleaning sets Cleaning phase and Cleanup condition.
func MarkCleaning(leaseObj *platformv1alpha1.EnvironmentLease, message string) {
	leaseObj.Status.Phase = platformv1alpha1.LeasePhaseCleaning
	SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupInProgress, message)
	SetCondition(leaseObj, platformv1alpha1.ConditionReady, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupInProgress, message)
}

// MarkCleanupComplete marks cleanup finished.
func MarkCleanupComplete(leaseObj *platformv1alpha1.EnvironmentLease) {
	leaseObj.Status.Phase = platformv1alpha1.LeasePhaseExpired
	SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionTrue,
		platformv1alpha1.ReasonCleanupComplete, "Managed environment cleaned up")
	SetCondition(leaseObj, platformv1alpha1.ConditionExpiring, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupComplete, "Expired")
	SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupComplete, "Cleanup finished")
}

// MarkCleanupFailed records a cleanup failure (keep retrying).
func MarkCleanupFailed(leaseObj *platformv1alpha1.EnvironmentLease, message string) {
	leaseObj.Status.Phase = platformv1alpha1.LeasePhaseCleaning
	SetCondition(leaseObj, platformv1alpha1.ConditionCleanup, metav1.ConditionFalse,
		platformv1alpha1.ReasonCleanupFailed, message)
	SetCondition(leaseObj, platformv1alpha1.ConditionDegraded, metav1.ConditionTrue,
		platformv1alpha1.ReasonCleanupFailed, message)
}

// ConditionTrue reports whether a condition is True.
func ConditionTrue(leaseObj *platformv1alpha1.EnvironmentLease, condType string) bool {
	return meta.IsStatusConditionTrue(leaseObj.Status.Conditions, condType)
}
