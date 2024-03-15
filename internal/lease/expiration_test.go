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
	"k8s.io/utils/ptr"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func TestValidateTTL(t *testing.T) {
	t.Parallel()
	if err := ValidateTTL(metav1.Duration{Duration: 0}); err == nil {
		t.Fatal("expected error for zero ttl")
	}
	if err := ValidateTTL(metav1.Duration{Duration: time.Hour}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureTimestampsStickyAndMaxTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
		},
	}
	changed, rejected, err := EnsureTimestamps(leaseObj, 8*time.Hour, 72*time.Hour, true, now)
	if err != nil || rejected || !changed {
		t.Fatalf("changed=%v rejected=%v err=%v", changed, rejected, err)
	}
	if !leaseObj.Status.CreatedAt.Equal(&leaseObj.CreationTimestamp) {
		t.Fatal("CreatedAt should come from creationTimestamp")
	}
	wantMax := metav1.NewTime(leaseObj.Status.CreatedAt.Add(72 * time.Hour))
	if !leaseObj.Status.MaximumExpiresAt.Equal(&wantMax) {
		t.Fatalf("max=%v want %v", leaseObj.Status.MaximumExpiresAt, wantMax)
	}

	_, _, err = EnsureTimestamps(leaseObj, 8*time.Hour, 72*time.Hour, true, now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !leaseObj.Status.CreatedAt.Equal(&leaseObj.CreationTimestamp) {
		t.Fatal("CreatedAt reset")
	}
}

func TestEnsureTimestampsRejectsTTLAboveMaxTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)},
	}
	_, _, err := EnsureTimestamps(leaseObj, 100*time.Hour, 72*time.Hour, true, now)
	if err == nil {
		t.Fatal("expected error when ttl exceeds maxTTL")
	}
}

func TestEnsureTimestampsNonRenewableRejectsExtension(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{CreationTimestamp: metav1.NewTime(now)},
		Spec:       platformv1alpha1.EnvironmentLeaseSpec{Renewable: ptr.To(false)},
	}
	_, _, err := EnsureTimestamps(leaseObj, 8*time.Hour, 72*time.Hour, false, now)
	if err != nil {
		t.Fatal(err)
	}
	prev := *leaseObj.Status.ExpiresAt
	_, rejected, err := EnsureTimestamps(leaseObj, 12*time.Hour, 72*time.Hour, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if !rejected {
		t.Fatal("expected renewal rejected")
	}
	if !leaseObj.Status.ExpiresAt.Equal(&prev) {
		t.Fatal("expiresAt should not move later when not renewable")
	}
}

func TestComputeExtendedTTL(t *testing.T) {
	t.Parallel()
	created := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	expires := created.Add(8 * time.Hour)
	maxExp := created.Add(72 * time.Hour)

	newTTL, newExp, err := ComputeExtendedTTL(created, expires, maxExp, 4*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if newTTL != 12*time.Hour {
		t.Fatalf("ttl=%s", newTTL)
	}
	if !newExp.Equal(expires.Add(4 * time.Hour)) {
		t.Fatalf("exp=%v", newExp)
	}

	_, _, err = ComputeExtendedTTL(created, expires, maxExp, 4*time.Hour, false)
	if err == nil {
		t.Fatal("expected not renewable error")
	}

	nearMax := maxExp.Add(-time.Hour)
	_, capped, err := ComputeExtendedTTL(created, nearMax, maxExp, 4*time.Hour, true)
	if err != nil {
		t.Fatal(err)
	}
	if !capped.Equal(maxExp) {
		t.Fatalf("capped=%v", capped)
	}

	_, _, err = ComputeExtendedTTL(created, maxExp, maxExp, time.Hour, true)
	if err == nil {
		t.Fatal("expected error when already at max")
	}
}

func TestPendingWarningsAndSchedule(t *testing.T) {
	t.Parallel()
	expires := time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)
	warnings := []time.Duration{time.Hour, 15 * time.Minute}

	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	if got := NextReconcileAfter(now, expires, warnings, nil); got != 7*time.Hour {
		t.Fatalf("requeue=%s want 7h", got)
	}

	now = time.Date(2024, 1, 15, 17, 0, 0, 0, time.UTC)
	pending := PendingWarnings(now, expires, warnings, nil)
	if len(pending) != 1 || pending[0] != time.Hour {
		t.Fatalf("pending=%v", pending)
	}
	delivered := MarkWarningDelivered(nil, time.Hour)
	if got := NextReconcileAfter(now, expires, warnings, delivered); got != 45*time.Minute {
		t.Fatalf("requeue=%s want 45m", got)
	}

	now = time.Date(2024, 1, 15, 17, 45, 0, 0, time.UTC)
	pending = PendingWarnings(now, expires, warnings, delivered)
	if len(pending) != 1 || pending[0] != 15*time.Minute {
		t.Fatalf("pending=%v", pending)
	}
	delivered = MarkWarningDelivered(delivered, 15*time.Minute)
	if got := NextReconcileAfter(now, expires, warnings, delivered); got != 15*time.Minute {
		t.Fatalf("requeue=%s want 15m", got)
	}

	pending = PendingWarnings(now, expires, warnings, delivered)
	if len(pending) != 0 {
		t.Fatalf("expected no pending, got %v", pending)
	}
}

func TestValidateWarnings(t *testing.T) {
	t.Parallel()
	err := ValidateWarnings([]metav1.Duration{{Duration: time.Hour}, {Duration: time.Hour}})
	if err == nil {
		t.Fatal("expected duplicate error")
	}
}
