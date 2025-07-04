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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// StatusEqual reports whether two statuses are semantically equal for skipping
// unnecessary status writes. LastTransitionTime is ignored.
func StatusEqual(a, b platformv1alpha1.EnvironmentLeaseStatus) bool {
	if a.Phase != b.Phase || a.Namespace != b.Namespace || a.ObservedGeneration != b.ObservedGeneration {
		return false
	}
	if !clusterStatusEqual(a.Cluster, b.Cluster) {
		return false
	}
	if !timePtrEqual(a.CreatedAt, b.CreatedAt) ||
		!timePtrEqual(a.ExpiresAt, b.ExpiresAt) ||
		!timePtrEqual(a.MaximumExpiresAt, b.MaximumExpiresAt) ||
		!timePtrEqual(a.LastActivityAt, b.LastActivityAt) ||
		!timePtrEqual(a.IdleExpiresAt, b.IdleExpiresAt) ||
		!timePtrEqual(a.EffectiveExpiresAt, b.EffectiveExpiresAt) {
		return false
	}
	if a.ExpirationReason != b.ExpirationReason {
		return false
	}
	if !stringSliceEqual(a.WarningsDelivered, b.WarningsDelivered) {
		return false
	}
	if !effectiveEqual(a.Effective, b.Effective) {
		return false
	}
	return conditionsEqual(a.Conditions, b.Conditions)
}

func clusterStatusEqual(a, b *platformv1alpha1.ClusterStatus) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Name == b.Name
}

func effectiveEqual(a, b *platformv1alpha1.EffectiveLeaseSpec) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.PolicyName != b.PolicyName || a.Renewable != b.Renewable || a.DefaultDeny != b.DefaultDeny {
		return false
	}
	if a.TTL.Duration != b.TTL.Duration || a.MaxTTL.Duration != b.MaxTTL.Duration {
		return false
	}
	switch {
	case a.IdleTTL == nil && b.IdleTTL == nil:
		return true
	case a.IdleTTL == nil || b.IdleTTL == nil:
		return false
	default:
		return a.IdleTTL.Duration == b.IdleTTL.Duration
	}
}

func timePtrEqual(a, b *metav1.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(b)
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func conditionsEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	byType := make(map[string]metav1.Condition, len(b))
	for _, c := range b {
		byType[c.Type] = c
	}
	for _, ac := range a {
		bc, ok := byType[ac.Type]
		if !ok {
			return false
		}
		if ac.Status != bc.Status || ac.Reason != bc.Reason ||
			ac.Message != bc.Message || ac.ObservedGeneration != bc.ObservedGeneration {
			return false
		}
	}
	return true
}

// DeepCopyStatus returns a deep copy of status for before/after comparison.
func DeepCopyStatus(s platformv1alpha1.EnvironmentLeaseStatus) platformv1alpha1.EnvironmentLeaseStatus {
	out := s
	if s.CreatedAt != nil {
		t := *s.CreatedAt
		out.CreatedAt = &t
	}
	if s.ExpiresAt != nil {
		t := *s.ExpiresAt
		out.ExpiresAt = &t
	}
	if s.MaximumExpiresAt != nil {
		t := *s.MaximumExpiresAt
		out.MaximumExpiresAt = &t
	}
	if s.LastActivityAt != nil {
		t := *s.LastActivityAt
		out.LastActivityAt = &t
	}
	if s.IdleExpiresAt != nil {
		t := *s.IdleExpiresAt
		out.IdleExpiresAt = &t
	}
	if s.EffectiveExpiresAt != nil {
		t := *s.EffectiveExpiresAt
		out.EffectiveExpiresAt = &t
	}
	if s.Cluster != nil {
		c := *s.Cluster
		out.Cluster = &c
	}
	if s.WarningsDelivered != nil {
		out.WarningsDelivered = append([]string{}, s.WarningsDelivered...)
	}
	if s.Effective != nil {
		e := *s.Effective
		if s.Effective.IdleTTL != nil {
			d := *s.Effective.IdleTTL
			e.IdleTTL = &d
		}
		out.Effective = &e
	}
	if s.Conditions != nil {
		out.Conditions = make([]metav1.Condition, len(s.Conditions))
		copy(out.Conditions, s.Conditions)
	}
	return out
}

// EnsureObservedGeneration sets ObservedGeneration to the object's generation.
func EnsureObservedGeneration(leaseObj *platformv1alpha1.EnvironmentLease) {
	leaseObj.Status.ObservedGeneration = leaseObj.Generation
}
