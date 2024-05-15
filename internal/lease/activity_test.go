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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func TestComputeExpirationWindowHardVsIdle(t *testing.T) {
	t.Parallel()
	created := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	hard := created.Add(8 * time.Hour)
	activity := created

	win := ComputeExpirationWindow(hard, activity, 30*time.Minute)
	if win.IdleExpiresAt == nil {
		t.Fatal("expected idleExpiresAt")
	}
	wantIdle := activity.Add(30 * time.Minute)
	if !win.IdleExpiresAt.Equal(wantIdle) {
		t.Fatalf("idle=%v want %v", win.IdleExpiresAt, wantIdle)
	}
	if !win.EffectiveExpiresAt.Equal(wantIdle) {
		t.Fatalf("effective=%v want idle", win.EffectiveExpiresAt)
	}

	// Idle window beyond hard TTL is capped.
	win = ComputeExpirationWindow(hard, activity, 24*time.Hour)
	if !win.EffectiveExpiresAt.Equal(hard) {
		t.Fatalf("effective should clamp to hard, got %v", win.EffectiveExpiresAt)
	}
	if win.IdleExpiresAt == nil || !win.IdleExpiresAt.Equal(hard) {
		t.Fatalf("idle should clamp to hard, got %v", win.IdleExpiresAt)
	}

	// No idleTTL → effective == hard.
	win = ComputeExpirationWindow(hard, activity, 0)
	if win.IdleExpiresAt != nil {
		t.Fatal("expected nil idleExpiresAt")
	}
	if !win.EffectiveExpiresAt.Equal(hard) {
		t.Fatal("effective should equal hard")
	}
}

func TestRecordActivityHeartbeatAndRepeatedTouch(t *testing.T) {
	t.Parallel()
	clock := NewFakeClock(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(clock.Now())
	hard := metav1.NewTime(clock.Now().Add(8 * time.Hour))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard
	leaseObj.Status.MaximumExpiresAt = &hard

	idleTTL := 30 * time.Minute
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	firstIdle := *leaseObj.Status.IdleExpiresAt

	clock.Advance(10 * time.Minute)
	if err := RecordActivity(leaseObj, clock.Now(), idleTTL); err != nil {
		t.Fatal(err)
	}
	if !leaseObj.Status.LastActivityAt.Equal(&metav1.Time{Time: clock.Now()}) {
		t.Fatalf("lastActivityAt=%v", leaseObj.Status.LastActivityAt)
	}
	if !leaseObj.Status.IdleExpiresAt.After(firstIdle.Time) {
		t.Fatal("idleExpiresAt should move later after touch")
	}
	if leaseObj.Status.EffectiveExpiresAt.After(leaseObj.Status.ExpiresAt.Time) {
		t.Fatal("effective must never exceed hard expiresAt")
	}

	// Repeated touch near hard TTL: keep activity fresh, then verify idle caps to hard.
	clock.Set(hard.Add(-5 * time.Minute))
	// Simulate prior heartbeats by seeding lastActivity just before the near-end touch.
	seed := metav1.NewTime(clock.Now().Add(-time.Minute))
	leaseObj.Status.LastActivityAt = &seed
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	if err := RecordActivity(leaseObj, clock.Now(), idleTTL); err != nil {
		t.Fatal(err)
	}
	if !leaseObj.Status.IdleExpiresAt.Equal(leaseObj.Status.ExpiresAt) {
		t.Fatalf("idle should clamp to hard near end, idle=%v hard=%v",
			leaseObj.Status.IdleExpiresAt, leaseObj.Status.ExpiresAt)
	}
	if !leaseObj.Status.EffectiveExpiresAt.Equal(leaseObj.Status.ExpiresAt) {
		t.Fatal("effective should equal hard when idle is capped")
	}
}

func TestIdleExpirationBeforeHardTTL(t *testing.T) {
	t.Parallel()
	clock := NewFakeClock(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(clock.Now())
	hard := metav1.NewTime(clock.Now().Add(8 * time.Hour))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard

	idleTTL := time.Hour
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	clock.Advance(time.Hour + time.Minute)

	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	if !IsExpired(leaseObj, clock.Now()) {
		t.Fatal("expected idle expiration")
	}
	if leaseObj.Status.ExpirationReason != platformv1alpha1.ExpirationReasonIdleTimeout {
		t.Fatalf("reason=%s", leaseObj.Status.ExpirationReason)
	}
}

func TestHardTTLBeatsIdleWhenEarlier(t *testing.T) {
	t.Parallel()
	clock := NewFakeClock(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(clock.Now())
	hard := metav1.NewTime(clock.Now().Add(30 * time.Minute))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard
	leaseObj.Status.MaximumExpiresAt = &hard

	// Large idle window; hard TTL is the effective limit.
	SyncExpirationStatus(leaseObj, 24*time.Hour, clock.Now())
	if !leaseObj.Status.EffectiveExpiresAt.Equal(leaseObj.Status.ExpiresAt) {
		t.Fatal("effective should be hard TTL")
	}
	clock.Advance(31 * time.Minute)
	SyncExpirationStatus(leaseObj, 24*time.Hour, clock.Now())
	if leaseObj.Status.ExpirationReason != platformv1alpha1.ExpirationReasonTTLExpired {
		t.Fatalf("reason=%s", leaseObj.Status.ExpirationReason)
	}
}

func TestActivityCannotBypassMaxTTL(t *testing.T) {
	t.Parallel()
	clock := NewFakeClock(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(clock.Now())
	hard := metav1.NewTime(clock.Now().Add(2 * time.Hour))
	maxExp := metav1.NewTime(clock.Now().Add(2 * time.Hour))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard
	leaseObj.Status.MaximumExpiresAt = &maxExp

	idleTTL := time.Hour
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())

	// Stay within the idle window, near the hard ceiling.
	clock.Advance(90 * time.Minute)
	seed := metav1.NewTime(clock.Now().Add(-5 * time.Minute))
	leaseObj.Status.LastActivityAt = &seed
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	if err := RecordActivity(leaseObj, clock.Now(), idleTTL); err != nil {
		t.Fatal(err)
	}
	if leaseObj.Status.EffectiveExpiresAt.After(leaseObj.Status.MaximumExpiresAt.Time) {
		t.Fatal("effectiveExpiresAt exceeded maximumExpiresAt")
	}
	if !leaseObj.Status.EffectiveExpiresAt.Equal(leaseObj.Status.ExpiresAt) {
		t.Fatalf("expected clamp to hard/max, got %v", leaseObj.Status.EffectiveExpiresAt)
	}
}

func TestControllerRestartPreservesLastActivity(t *testing.T) {
	t.Parallel()
	clock := NewFakeClock(time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC))
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(clock.Now())
	hard := metav1.NewTime(clock.Now().Add(8 * time.Hour))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard

	idleTTL := time.Hour
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	clock.Advance(20 * time.Minute)
	if err := RecordActivity(leaseObj, clock.Now(), idleTTL); err != nil {
		t.Fatal(err)
	}
	savedActivity := *leaseObj.Status.LastActivityAt

	// Simulate restart: status reloaded, Sync again without resetting activity.
	clock.Advance(5 * time.Minute)
	SyncExpirationStatus(leaseObj, idleTTL, clock.Now())
	if !leaseObj.Status.LastActivityAt.Equal(&savedActivity) {
		t.Fatal("lastActivityAt must survive controller restart / re-sync")
	}
}

func TestAlreadyExpiredAtStartup(t *testing.T) {
	t.Parallel()
	created := time.Date(2024, 5, 1, 10, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	c := metav1.NewTime(created)
	hard := metav1.NewTime(created.Add(8 * time.Hour))
	activity := metav1.NewTime(created)
	leaseObj.Status.CreatedAt = &c
	leaseObj.Status.ExpiresAt = &hard
	leaseObj.Status.LastActivityAt = &activity

	// Controller starts after idle window already elapsed.
	now := created.Add(2 * time.Hour)
	SyncExpirationStatus(leaseObj, time.Hour, now)
	if !IsExpired(leaseObj, now) {
		t.Fatal("expected already expired at startup")
	}
	if leaseObj.Status.ExpirationReason != platformv1alpha1.ExpirationReasonIdleTimeout {
		t.Fatalf("reason=%s", leaseObj.Status.ExpirationReason)
	}
}

func TestRecordActivityRejectsWithoutIdleTTLAndAfterExpiry(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(now)
	hard := metav1.NewTime(now.Add(time.Hour))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard
	SyncExpirationStatus(leaseObj, 0, now)

	if err := RecordActivity(leaseObj, now, 0); err == nil {
		t.Fatal("expected error without idleTTL")
	}

	SyncExpirationStatus(leaseObj, 10*time.Minute, now)
	if err := RecordActivity(leaseObj, now.Add(11*time.Minute), 10*time.Minute); err == nil {
		t.Fatal("expected error after idle expiry")
	}
	if err := RecordActivity(leaseObj, hard.Add(time.Minute), time.Hour); err == nil {
		t.Fatal("expected error after hard TTL")
	}
}

func TestNextReconcileUsesEffectiveDeadline(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	idleDeadline := now.Add(30 * time.Minute)
	got := NextReconcileAfter(now, idleDeadline, nil, nil)
	if got != 30*time.Minute {
		t.Fatalf("requeue=%s", got)
	}
}

func TestResolveExpirationReason(t *testing.T) {
	t.Parallel()
	hard := time.Date(2024, 5, 1, 20, 0, 0, 0, time.UTC)
	idle := hard.Add(-4 * time.Hour)

	if got := ResolveExpirationReason(idle.Add(time.Minute), hard, &idle); got != platformv1alpha1.ExpirationReasonIdleTimeout {
		t.Fatalf("idle first: got %s", got)
	}
	if got := ResolveExpirationReason(hard.Add(time.Minute), hard, nil); got != platformv1alpha1.ExpirationReasonTTLExpired {
		t.Fatalf("hard only: got %s", got)
	}
	// When idle was capped to hard (same instant), report TTLExpired.
	same := hard
	if got := ResolveExpirationReason(hard.Add(time.Minute), hard, &same); got != platformv1alpha1.ExpirationReasonTTLExpired {
		t.Fatalf("tied: got %s", got)
	}
}
