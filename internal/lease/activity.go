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

// ExpirationWindow is the deterministic result of hard TTL vs idle TTL.
type ExpirationWindow struct {
	HardExpiresAt      time.Time
	IdleExpiresAt      *time.Time // nil when idleTTL is unset
	EffectiveExpiresAt time.Time
}

// ComputeExpirationWindow returns:
//
//	effectiveExpiration = min(hardExpiration, lastActivityAt + idleTTL)
//
// When idleTTL <= 0, idle expiration is disabled and effective == hard.
// Idle expiration is always capped so activity cannot bypass the hard TTL.
func ComputeExpirationWindow(hardExpiresAt, lastActivityAt time.Time, idleTTL time.Duration) ExpirationWindow {
	hard := hardExpiresAt.UTC()
	out := ExpirationWindow{
		HardExpiresAt:      hard,
		EffectiveExpiresAt: hard,
	}
	if idleTTL <= 0 {
		return out
	}
	idle := lastActivityAt.UTC().Add(idleTTL)
	if idle.After(hard) {
		idle = hard
	}
	out.IdleExpiresAt = &idle
	if idle.Before(hard) {
		out.EffectiveExpiresAt = idle
	}
	return out
}

// ResolveExpirationReason picks TTLExpired vs IdleTimeout for an expired lease.
// ManualExpiration / SourceClosed are set by callers (delete / integrations).
func ResolveExpirationReason(now, hardExpiresAt time.Time, idleExpiresAt *time.Time) platformv1alpha1.ExpirationReason {
	now = now.UTC()
	hard := hardExpiresAt.UTC()
	if idleExpiresAt != nil {
		idle := idleExpiresAt.UTC()
		if !now.Before(idle) && idle.Before(hard) {
			return platformv1alpha1.ExpirationReasonIdleTimeout
		}
	}
	if !now.Before(hard) {
		return platformv1alpha1.ExpirationReasonTTLExpired
	}
	return ""
}

// SyncExpirationStatus initializes lastActivityAt (once) and derives idle /
// effective expiration fields from the hard ExpiresAt and idleTTL.
// Preserves ManualExpiration / SourceClosed reasons once set.
func SyncExpirationStatus(leaseObj *platformv1alpha1.EnvironmentLease, idleTTL time.Duration, now time.Time) {
	if leaseObj.Status.CreatedAt == nil || leaseObj.Status.ExpiresAt == nil {
		return
	}

	if leaseObj.Status.LastActivityAt == nil {
		t := *leaseObj.Status.CreatedAt
		leaseObj.Status.LastActivityAt = &t
	}

	hard := leaseObj.Status.ExpiresAt.Time
	if leaseObj.Status.MaximumExpiresAt != nil && hard.After(leaseObj.Status.MaximumExpiresAt.Time) {
		hard = leaseObj.Status.MaximumExpiresAt.Time
	}

	win := ComputeExpirationWindow(hard, leaseObj.Status.LastActivityAt.Time, idleTTL)
	if win.IdleExpiresAt != nil {
		t := metav1.NewTime(*win.IdleExpiresAt)
		leaseObj.Status.IdleExpiresAt = &t
	} else {
		leaseObj.Status.IdleExpiresAt = nil
	}
	eff := metav1.NewTime(win.EffectiveExpiresAt)
	leaseObj.Status.EffectiveExpiresAt = &eff

	switch leaseObj.Status.ExpirationReason {
	case platformv1alpha1.ExpirationReasonManualExpiration,
		platformv1alpha1.ExpirationReasonSourceClosed:
		// Preserve explicit reasons.
	default:
		if now.Before(win.EffectiveExpiresAt) {
			leaseObj.Status.ExpirationReason = ""
		} else {
			leaseObj.Status.ExpirationReason = ResolveExpirationReason(now, win.HardExpiresAt, win.IdleExpiresAt)
		}
	}
}

// RecordActivity updates lastActivityAt to now and recomputes derived expiration.
// Activity never extends past the hard TTL. Requires idleTTL > 0.
func RecordActivity(leaseObj *platformv1alpha1.EnvironmentLease, now time.Time, idleTTL time.Duration) error {
	if leaseObj.Status.CreatedAt == nil || leaseObj.Status.ExpiresAt == nil {
		return fmt.Errorf("lease has not been initialized by the controller yet")
	}
	if idleTTL <= 0 {
		return fmt.Errorf("idleTTL is not configured; touch has no effect without idle expiration")
	}

	now = now.UTC()
	hard := leaseObj.Status.ExpiresAt.Time
	if leaseObj.Status.MaximumExpiresAt != nil && hard.After(leaseObj.Status.MaximumExpiresAt.Time) {
		hard = leaseObj.Status.MaximumExpiresAt.Time
	}
	if !now.Before(hard) {
		return fmt.Errorf("cannot touch: hard TTL already elapsed at %s", hard.Format(time.RFC3339))
	}

	// Reject heartbeats after the lease has already effectively expired.
	SyncExpirationStatus(leaseObj, idleTTL, now)
	if leaseObj.Status.EffectiveExpiresAt != nil && !now.Before(leaseObj.Status.EffectiveExpiresAt.Time) {
		reason := leaseObj.Status.ExpirationReason
		if reason == "" {
			reason = platformv1alpha1.ExpirationReasonIdleTimeout
		}
		return fmt.Errorf("cannot touch: lease already expired (%s)", reason)
	}

	t := metav1.NewTime(now)
	leaseObj.Status.LastActivityAt = &t
	SyncExpirationStatus(leaseObj, idleTTL, now)
	return nil
}

// EffectiveDeadline returns the time the controller should treat as expiration,
// preferring EffectiveExpiresAt when present.
func EffectiveDeadline(leaseObj *platformv1alpha1.EnvironmentLease) *time.Time {
	if leaseObj.Status.EffectiveExpiresAt != nil {
		t := leaseObj.Status.EffectiveExpiresAt.Time
		return &t
	}
	if leaseObj.Status.ExpiresAt != nil {
		t := leaseObj.Status.ExpiresAt.Time
		return &t
	}
	return nil
}
