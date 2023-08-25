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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// ValidateTTL ensures the TTL is a positive duration.
func ValidateTTL(ttl metav1.Duration) error {
	if ttl.Duration <= 0 {
		return fmt.Errorf("ttl must be greater than zero, got %s", ttl.Duration)
	}
	return nil
}

// EnsureTimestamps sets CreatedAt (once) and ExpiresAt (= CreatedAt + TTL).
// CreatedAt is sticky across reconciles and controller restarts.
// ExpiresAt is recalculated from CreatedAt whenever Spec.TTL changes.
// Returns true if status timestamps were modified.
func EnsureTimestamps(lease *platformv1alpha1.EnvironmentLease, now time.Time) (bool, error) {
	if err := ValidateTTL(lease.Spec.TTL); err != nil {
		return false, err
	}

	changed := false
	if lease.Status.CreatedAt == nil {
		t := metav1.NewTime(now.UTC())
		lease.Status.CreatedAt = &t
		changed = true
	}

	desiredExpiry := metav1.NewTime(lease.Status.CreatedAt.Time.Add(lease.Spec.TTL.Duration))
	if lease.Status.ExpiresAt == nil || !lease.Status.ExpiresAt.Equal(&desiredExpiry) {
		lease.Status.ExpiresAt = &desiredExpiry
		changed = true
	}
	return changed, nil
}

// IsExpired reports whether now is at or after ExpiresAt.
func IsExpired(lease *platformv1alpha1.EnvironmentLease, now time.Time) bool {
	if lease.Status.ExpiresAt == nil {
		return false
	}
	return !now.Before(lease.Status.ExpiresAt.Time)
}

// TimeUntilExpiration returns the duration until expiry. Negative if already expired.
func TimeUntilExpiration(lease *platformv1alpha1.EnvironmentLease, now time.Time) time.Duration {
	if lease.Status.ExpiresAt == nil {
		return 0
	}
	return lease.Status.ExpiresAt.Time.Sub(now)
}
