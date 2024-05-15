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

package cli

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
)

func TestIdleTTLFromLeasePrefersEffective(t *testing.T) {
	t.Parallel()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			IdleTTL: &metav1.Duration{Duration: time.Hour},
		},
		Status: platformv1alpha1.EnvironmentLeaseStatus{
			Effective: &platformv1alpha1.EffectiveLeaseSpec{
				IdleTTL: &metav1.Duration{Duration: 30 * time.Minute},
			},
		},
	}
	if got := idleTTLFromLease(leaseObj); got != 30*time.Minute {
		t.Fatalf("got %s", got)
	}
}

func TestRecordActivityDoesNotExtendHardTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 5, 15, 12, 0, 0, 0, time.UTC)
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	created := metav1.NewTime(now)
	hard := metav1.NewTime(now.Add(2 * time.Hour))
	leaseObj.Status.CreatedAt = &created
	leaseObj.Status.ExpiresAt = &hard
	leaseObj.Status.MaximumExpiresAt = &hard
	lease.SyncExpirationStatus(leaseObj, 30*time.Minute, now)

	// Touch while still inside the idle window.
	touchAt := now.Add(10 * time.Minute)
	if err := lease.RecordActivity(leaseObj, touchAt, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
	if !leaseObj.Status.ExpiresAt.Equal(&hard) {
		t.Fatal("hard expiresAt changed")
	}
	if leaseObj.Status.EffectiveExpiresAt.After(hard.Time) {
		t.Fatal("effective exceeded hard")
	}
}
