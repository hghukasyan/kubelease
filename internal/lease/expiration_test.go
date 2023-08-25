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

func TestValidateTTL(t *testing.T) {
	t.Parallel()
	if err := ValidateTTL(metav1.Duration{Duration: 0}); err == nil {
		t.Fatal("expected error for zero ttl")
	}
	if err := ValidateTTL(metav1.Duration{Duration: -time.Hour}); err == nil {
		t.Fatal("expected error for negative ttl")
	}
	if err := ValidateTTL(metav1.Duration{Duration: time.Hour}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureTimestampsStickyCreatedAt(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			TTL: metav1.Duration{Duration: 8 * time.Hour},
		},
	}

	changed, err := EnsureTimestamps(leaseObj, now)
	if err != nil {
		t.Fatalf("EnsureTimestamps: %v", err)
	}
	if !changed {
		t.Fatal("expected status change on first call")
	}
	created := *leaseObj.Status.CreatedAt
	expires := *leaseObj.Status.ExpiresAt

	later := now.Add(2 * time.Hour)
	changed, err = EnsureTimestamps(leaseObj, later)
	if err != nil {
		t.Fatalf("EnsureTimestamps restart: %v", err)
	}
	if changed {
		t.Fatal("timestamps should be sticky across restarts when TTL unchanged")
	}
	if !leaseObj.Status.CreatedAt.Equal(&created) {
		t.Fatalf("CreatedAt reset: got %v want %v", leaseObj.Status.CreatedAt, created)
	}
	if !leaseObj.Status.ExpiresAt.Equal(&expires) {
		t.Fatalf("ExpiresAt changed: got %v want %v", leaseObj.Status.ExpiresAt, expires)
	}
}

func TestEnsureTimestampsRecalculatesOnTTLChange(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			TTL: metav1.Duration{Duration: 2 * time.Hour},
		},
	}
	if _, err := EnsureTimestamps(leaseObj, now); err != nil {
		t.Fatal(err)
	}
	created := *leaseObj.Status.CreatedAt

	leaseObj.Spec.TTL = metav1.Duration{Duration: 10 * time.Hour}
	changed, err := EnsureTimestamps(leaseObj, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected ExpiresAt change when TTL changes")
	}
	if !leaseObj.Status.CreatedAt.Equal(&created) {
		t.Fatal("CreatedAt must remain sticky when TTL changes")
	}
	want := metav1.NewTime(created.Add(10 * time.Hour))
	if !leaseObj.Status.ExpiresAt.Equal(&want) {
		t.Fatalf("ExpiresAt=%v want %v", leaseObj.Status.ExpiresAt, want)
	}
}

func TestIsExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	exp := metav1.NewTime(now.Add(-time.Second))
	leaseObj := &platformv1alpha1.EnvironmentLease{
		Status: platformv1alpha1.EnvironmentLeaseStatus{ExpiresAt: &exp},
	}
	if !IsExpired(leaseObj, now) {
		t.Fatal("expected expired")
	}
	future := metav1.NewTime(now.Add(time.Hour))
	leaseObj.Status.ExpiresAt = &future
	if IsExpired(leaseObj, now) {
		t.Fatal("expected not expired")
	}
}

func TestMarkReadyConditions(t *testing.T) {
	t.Parallel()
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	leaseObj.Generation = 3
	MarkReady(leaseObj)
	if leaseObj.Status.Phase != platformv1alpha1.LeasePhaseActive {
		t.Fatalf("phase=%s", leaseObj.Status.Phase)
	}
	found := false
	for _, c := range leaseObj.Status.Conditions {
		if c.Type == platformv1alpha1.ConditionReady && c.Status == metav1.ConditionTrue {
			found = true
			if c.ObservedGeneration != 3 {
				t.Fatalf("ObservedGeneration=%d", c.ObservedGeneration)
			}
		}
	}
	if !found {
		t.Fatal("Ready condition missing")
	}
}
