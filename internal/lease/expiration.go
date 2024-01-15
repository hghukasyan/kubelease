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
	"github.com/hghukasyan/kubelease/internal/resources"
)

// ValidateSpec performs controller-side defensive validation beyond CRD schema.
func ValidateSpec(lease *platformv1alpha1.EnvironmentLease) error {
	if err := ValidateTTL(lease.Spec.TTL); err != nil {
		return err
	}
	maxTTL := lease.Spec.EffectiveMaxTTL()
	if err := ValidateTTL(maxTTL); err != nil {
		return fmt.Errorf("maxTTL: %w", err)
	}
	if maxTTL.Duration < lease.Spec.TTL.Duration {
		return fmt.Errorf("maxTTL (%s) must be >= ttl (%s)", maxTTL.Duration, lease.Spec.TTL.Duration)
	}
	if err := ValidateWarnings(lease.Spec.Warnings); err != nil {
		return err
	}
	if err := resources.ValidateNamespaceSpec(lease.Spec.Namespace); err != nil {
		return err
	}
	if lease.Status.Namespace != "" && resources.IsProtectedNamespace(lease.Status.Namespace) {
		return fmt.Errorf("cannot manage protected namespace %q", lease.Status.Namespace)
	}
	return nil
}

// ValidateTTL ensures the TTL is a positive duration.
func ValidateTTL(ttl metav1.Duration) error {
	if ttl.Duration <= 0 {
		return fmt.Errorf("duration must be greater than zero, got %s", ttl.Duration)
	}
	return nil
}

// ValidateWarnings rejects non-positive or duplicate warning durations.
func ValidateWarnings(warnings []metav1.Duration) error {
	seen := map[time.Duration]struct{}{}
	for _, w := range warnings {
		if w.Duration <= 0 {
			return fmt.Errorf("warning duration must be greater than zero, got %s", w.Duration)
		}
		if _, ok := seen[w.Duration]; ok {
			return fmt.Errorf("duplicate warning duration %s", w.Duration)
		}
		seen[w.Duration] = struct{}{}
	}
	return nil
}

// EnsureTimestamps sets CreatedAt (once), MaximumExpiresAt, and ExpiresAt.
//
// CreatedAt is initialized from metadata.creationTimestamp when available.
// ExpiresAt = CreatedAt + TTL, clamped to MaximumExpiresAt.
// When renewable is false, ExpiresAt is not allowed to move later than the
// previously observed ExpiresAt (controller-enforced renewal guard).
//
// Returns (changed, renewalRejected, error).
func EnsureTimestamps(leaseObj *platformv1alpha1.EnvironmentLease, now time.Time) (bool, bool, error) {
	if err := ValidateTTL(leaseObj.Spec.TTL); err != nil {
		return false, false, err
	}
	maxTTL := leaseObj.Spec.EffectiveMaxTTL()
	if err := ValidateTTL(maxTTL); err != nil {
		return false, false, fmt.Errorf("maxTTL: %w", err)
	}

	changed := false
	renewalRejected := false

	if leaseObj.Status.CreatedAt == nil {
		created := now.UTC()
		if !leaseObj.CreationTimestamp.IsZero() {
			created = leaseObj.CreationTimestamp.Time.UTC()
		}
		t := metav1.NewTime(created)
		leaseObj.Status.CreatedAt = &t
		changed = true
	}

	maxExp := metav1.NewTime(leaseObj.Status.CreatedAt.Time.Add(maxTTL.Duration))
	if leaseObj.Status.MaximumExpiresAt == nil || !leaseObj.Status.MaximumExpiresAt.Equal(&maxExp) {
		leaseObj.Status.MaximumExpiresAt = &maxExp
		changed = true
	}

	ttl := leaseObj.Spec.TTL.Duration
	if ttl > maxTTL.Duration {
		ttl = maxTTL.Duration
	}
	desired := leaseObj.Status.CreatedAt.Time.Add(ttl)

	// Non-renewable: refuse moving expiresAt later than previously accepted.
	if !leaseObj.Spec.IsRenewable() && leaseObj.Status.ExpiresAt != nil && desired.After(leaseObj.Status.ExpiresAt.Time) {
		desired = leaseObj.Status.ExpiresAt.Time
		renewalRejected = true
	}

	desiredMeta := metav1.NewTime(desired)
	if leaseObj.Status.ExpiresAt == nil || !leaseObj.Status.ExpiresAt.Equal(&desiredMeta) {
		// Detect renewal (expiresAt moved later while renewable).
		leaseObj.Status.ExpiresAt = &desiredMeta
		changed = true
	}

	return changed, renewalRejected, nil
}

// IsExpired reports whether now is at or after ExpiresAt.
func IsExpired(leaseObj *platformv1alpha1.EnvironmentLease, now time.Time) bool {
	if leaseObj.Status.ExpiresAt == nil {
		return false
	}
	return !now.Before(leaseObj.Status.ExpiresAt.Time)
}

// IsExpiringWindow reports whether we are inside the earliest undelivered
// warning window (or any warning window) before expiration.
func IsExpiringWindow(leaseObj *platformv1alpha1.EnvironmentLease, now time.Time) bool {
	if leaseObj.Status.ExpiresAt == nil || IsExpired(leaseObj, now) {
		return false
	}
	warnings := WarningDurations(leaseObj.Spec.Warnings)
	if len(warnings) == 0 {
		return false
	}
	var earliest time.Duration
	for i, w := range warnings {
		if i == 0 || w > earliest {
			earliest = w
		}
	}
	threshold := leaseObj.Status.ExpiresAt.Time.Add(-earliest)
	return !now.Before(threshold)
}

// ComputeExtendedTTL returns the new Spec.TTL duration for an extend-by amount,
// capping at maxTTL. Returns an error if the lease is not renewable or the
// extension would not move expiration (already at max).
func ComputeExtendedTTL(
	createdAt time.Time,
	currentExpiresAt time.Time,
	maxExpiresAt time.Time,
	extendBy time.Duration,
	renewable bool,
) (time.Duration, time.Time, error) {
	if extendBy <= 0 {
		return 0, time.Time{}, fmt.Errorf("extension duration must be greater than zero")
	}
	if !renewable {
		return 0, time.Time{}, fmt.Errorf("lease is not renewable")
	}
	newExpires := currentExpiresAt.Add(extendBy)
	if newExpires.After(maxExpiresAt) {
		newExpires = maxExpiresAt
	}
	if !newExpires.After(currentExpiresAt) {
		return 0, time.Time{}, fmt.Errorf(
			"extension would exceed maximum lifetime (max expires at %s)",
			maxExpiresAt.UTC().Format(time.RFC3339),
		)
	}
	newTTL := newExpires.Sub(createdAt)
	return newTTL, newExpires, nil
}
