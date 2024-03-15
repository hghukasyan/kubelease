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

package policy

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func TestResolveDefaultsFromPolicy(t *testing.T) {
	t.Parallel()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-default"},
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{
				Default: &metav1.Duration{Duration: 8 * time.Hour},
				Maximum: &metav1.Duration{Duration: 72 * time.Hour},
			},
			NetworkPolicy: &platformv1alpha1.NetworkPolicyPolicy{
				DefaultDenyRequired: true,
			},
		},
	}
	got, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if got.TTL != 8*time.Hour || got.MaxTTL != 72*time.Hour {
		t.Fatalf("ttl=%s max=%s", got.TTL, got.MaxTTL)
	}
	if !got.DefaultDeny || got.PolicyName != "preview-default" {
		t.Fatalf("got=%+v", got)
	}
}

func TestResolveRejectsTTLAboveMaximum(t *testing.T) {
	t.Parallel()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{
				Maximum: &metav1.Duration{Duration: 8 * time.Hour},
			},
		},
	}
	_, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{
		TTL: &metav1.Duration{Duration: 24 * time.Hour},
	}, pol)
	if err == nil {
		t.Fatal("expected rejection")
	}
}

func TestResolveRejectsQuotaAboveMax(t *testing.T) {
	t.Parallel()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{Default: &metav1.Duration{Duration: time.Hour}},
			Quota: &platformv1alpha1.QuotaPolicy{
				MaxCPU:    ptrQuantity("4"),
				MaxMemory: ptrQuantity("8Gi"),
			},
		},
	}
	_, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{
		Quota: &platformv1alpha1.QuotaSpec{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8")},
		},
	}, pol)
	if err == nil {
		t.Fatal("expected quota rejection")
	}
}

func TestResolveRejectsExplicitDefaultDenyFalse(t *testing.T) {
	t.Parallel()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{Default: &metav1.Duration{Duration: time.Hour}},
			NetworkPolicy: &platformv1alpha1.NetworkPolicyPolicy{
				DefaultDenyRequired: true,
			},
		},
	}
	_, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{
		NetworkPolicy: &platformv1alpha1.NetworkPolicySpec{DefaultDeny: false},
	}, pol)
	if err == nil {
		t.Fatal("expected defaultDeny rejection")
	}
}

func TestResolveWithoutPolicyRequiresTTL(t *testing.T) {
	t.Parallel()
	_, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{}, nil)
	if err == nil {
		t.Fatal("expected ttl required")
	}
	got, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{
		TTL: &metav1.Duration{Duration: 2 * time.Hour},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.TTL != 2*time.Hour || got.MaxTTL != 2*time.Hour || !got.Renewable {
		t.Fatalf("%+v", got)
	}
}

func TestResolveRenewableForce(t *testing.T) {
	t.Parallel()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{Default: &metav1.Duration{Duration: time.Hour}},
			Renewable: &platformv1alpha1.BoolPolicy{
				Default: ptr.To(false),
				Force:   ptr.To(false),
			},
		},
	}
	_, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{
		Renewable: ptr.To(true),
	}, pol)
	if err == nil {
		t.Fatal("expected force rejection")
	}
}

func TestResolveLeaseOverridesDefault(t *testing.T) {
	t.Parallel()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{
				Default: &metav1.Duration{Duration: 8 * time.Hour},
				Maximum: &metav1.Duration{Duration: 72 * time.Hour},
			},
		},
	}
	got, err := Resolve(platformv1alpha1.EnvironmentLeaseSpec{
		TTL: &metav1.Duration{Duration: 4 * time.Hour},
	}, pol)
	if err != nil {
		t.Fatal(err)
	}
	if got.TTL != 4*time.Hour {
		t.Fatalf("ttl=%s", got.TTL)
	}
}

func ptrQuantity(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}
