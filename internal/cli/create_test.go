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
)

func TestBuildLease(t *testing.T) {
	t.Parallel()
	opts := &createOptions{
		ttl:           8 * time.Hour,
		maxTTL:        72 * time.Hour,
		owner:         "hayk",
		team:          "payments",
		cpuRequest:    "2",
		memoryRequest: "4Gi",
		renewable:     true,
		defaultDeny:   true,
		generateName:  "preview-",
		warnings:      []string{"1h", "15m"},
	}
	leaseObj, err := BuildLease("payment-pr", opts)
	if err != nil {
		t.Fatal(err)
	}
	if leaseObj.Name != "payment-pr" {
		t.Fatal(leaseObj.Name)
	}
	if leaseObj.Spec.TTL.Duration != 8*time.Hour {
		t.Fatal(leaseObj.Spec.TTL)
	}
	if leaseObj.Spec.MaxTTL == nil || leaseObj.Spec.MaxTTL.Duration != 72*time.Hour {
		t.Fatal(leaseObj.Spec.MaxTTL)
	}
	if leaseObj.Spec.Quota == nil {
		t.Fatal("expected quota")
	}
	if leaseObj.Spec.NetworkPolicy == nil || !leaseObj.Spec.NetworkPolicy.DefaultDeny {
		t.Fatal("expected default deny")
	}
	if len(leaseObj.Spec.Warnings) != 2 {
		t.Fatal(leaseObj.Spec.Warnings)
	}
}

func TestBuildLeaseRejectsMaxTTLLessThanTTL(t *testing.T) {
	t.Parallel()
	_, err := BuildLease("x", &createOptions{ttl: 8 * time.Hour, maxTTL: time.Hour, renewable: true, generateName: "p-"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildLeaseDefaultsMaxTTL(t *testing.T) {
	t.Parallel()
	leaseObj, err := BuildLease("x", &createOptions{ttl: 2 * time.Hour, renewable: true, generateName: "p-"})
	if err != nil {
		t.Fatal(err)
	}
	if leaseObj.Spec.MaxTTL == nil || leaseObj.Spec.MaxTTL.Duration != 2*time.Hour {
		t.Fatal(leaseObj.Spec.MaxTTL)
	}
}
