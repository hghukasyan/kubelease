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

package identity

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// ProbeRemoteIdentity returns a stable fingerprint for a Kubernetes cluster.
// Uses the kube-system Namespace UID, which is unique per cluster installation
// and survives most control-plane upgrades (unlike API server URL/host).
func ProbeRemoteIdentity(ctx context.Context, c client.Client) (string, error) {
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, types.NamespacedName{Name: "kube-system"}, ns); err != nil {
		return "", fmt.Errorf("probe remote identity (kube-system): %w", err)
	}
	if ns.UID == "" {
		return "", fmt.Errorf("kube-system UID is empty")
	}
	return string(ns.UID), nil
}

// OwnershipMismatchError stops destructive cleanup when identity checks fail.
type OwnershipMismatchError struct {
	Message string
}

func (e *OwnershipMismatchError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return platformv1alpha1.ReasonOwnershipMismatch
}

// VerifyNamespaceOwnership ensures Namespace metadata matches the lease and
// captured control/target identities before deletion.
func VerifyNamespaceOwnership(
	ns *corev1.Namespace,
	leaseObj *platformv1alpha1.EnvironmentLease,
	controlClusterID string,
) error {
	if ns == nil {
		return &OwnershipMismatchError{Message: "namespace is nil"}
	}
	if ns.Labels[platformv1alpha1.LabelManagedBy] != platformv1alpha1.ManagedByValue {
		return &OwnershipMismatchError{Message: "namespace missing kubelease managed-by label"}
	}
	if ns.Labels[platformv1alpha1.LabelLease] != leaseObj.Name {
		return &OwnershipMismatchError{
			Message: fmt.Sprintf("namespace lease label %q != %q", ns.Labels[platformv1alpha1.LabelLease], leaseObj.Name),
		}
	}
	if ns.Annotations[platformv1alpha1.AnnotationLeaseUID] != string(leaseObj.UID) {
		return &OwnershipMismatchError{
			Message: fmt.Sprintf("namespace lease-uid annotation mismatch (got %q)", ns.Annotations[platformv1alpha1.AnnotationLeaseUID]),
		}
	}
	if controlClusterID != "" {
		got := ns.Annotations[platformv1alpha1.AnnotationControlClusterID]
		if got != "" && got != controlClusterID {
			return &OwnershipMismatchError{
				Message: fmt.Sprintf("control-cluster-id mismatch: ns=%q expected=%q", got, controlClusterID),
			}
		}
	}
	if leaseObj.Status.Cluster != nil && leaseObj.Status.Cluster.RemoteIdentity != "" {
		got := ns.Annotations[platformv1alpha1.AnnotationTargetIdentity]
		if got != "" && got != leaseObj.Status.Cluster.RemoteIdentity {
			return &OwnershipMismatchError{
				Message: fmt.Sprintf("target-identity annotation mismatch: ns=%q expected=%q",
					got, leaseObj.Status.Cluster.RemoteIdentity),
			}
		}
	}
	return nil
}

// VerifyLiveTargetIdentity compares the live remote identity to the one
// captured when the environment was provisioned.
func VerifyLiveTargetIdentity(expected, live string) error {
	if expected == "" || live == "" {
		return nil // cannot verify yet / local
	}
	if expected != live {
		return &OwnershipMismatchError{
			Message: fmt.Sprintf("%s: expected remote identity %q, live target is %q",
				platformv1alpha1.ReasonTargetIdentityMismatch, expected, live),
		}
	}
	return nil
}

// ForceCleanupAcknowledged reports whether the operator explicitly opted into
// skipping remote cleanup / identity checks.
func ForceCleanupAcknowledged(leaseObj *platformv1alpha1.EnvironmentLease) bool {
	if leaseObj == nil || leaseObj.Annotations == nil {
		return false
	}
	return leaseObj.Annotations[platformv1alpha1.AnnotationForceCleanupAcknowledged] == "true"
}
