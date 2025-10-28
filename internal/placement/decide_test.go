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

package placement

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func scheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = platformv1alpha1.AddToScheme(s)
	return s
}

func readyTarget(name string, labels map[string]string, max *int32, active int32) *platformv1alpha1.ClusterTarget {
	t := &platformv1alpha1.ClusterTarget{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: platformv1alpha1.ClusterTargetSpec{
			Credentials: platformv1alpha1.ClusterCredentials{
				SecretRef: platformv1alpha1.SecretKeySelector{Name: "k", Namespace: "ns"},
			},
			MaxActiveLeases: max,
		},
		Status: platformv1alpha1.ClusterTargetStatus{
			Capacity: &platformv1alpha1.ClusterCapacityStatus{ActiveLeases: active, MaxLeases: max},
			Conditions: []metav1.Condition{{
				Type:   platformv1alpha1.ClusterTargetConditionReady,
				Status: metav1.ConditionTrue,
				Reason: platformv1alpha1.ReasonClusterReachable,
			}},
		},
	}
	return t
}

func TestValidateExclusive(t *testing.T) {
	err := ValidateExclusive(platformv1alpha1.EnvironmentLeaseSpec{
		ClusterRef: &platformv1alpha1.LocalObjectReference{Name: "a"},
		Placement:  &platformv1alpha1.PlacementSpec{},
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDecide_ClusterRef(t *testing.T) {
	s := scheme(t)
	target := readyTarget("dev-east", map[string]string{"kubelease.io/tier": "dev"}, nil, 0)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(target).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l1", UID: "uid-1"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			ClusterRef: &platformv1alpha1.LocalObjectReference{Name: "dev-east"},
		},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || d.ClusterName != "dev-east" || d.Pending {
		t.Fatalf("got %+v err=%v", d, err)
	}
}

func TestDecide_SelectorStable(t *testing.T) {
	s := scheme(t)
	a := readyTarget("aaa-east", map[string]string{"kubelease.io/region": "us-east", "kubelease.io/tier": "dev"}, nil, 0)
	b := readyTarget("bbb-east", map[string]string{"kubelease.io/region": "us-east", "kubelease.io/tier": "dev"}, nil, 0)
	c := readyTarget("ccc-west", map[string]string{"kubelease.io/region": "us-west", "kubelease.io/tier": "dev"}, nil, 0)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(a, b, c).Build()

	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "payment-pr", UID: "stable-uid-xyz"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{
					"kubelease.io/region": "us-east",
					"kubelease.io/tier":   "dev",
				}},
			},
		},
	}
	d1, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || d1.Pending {
		t.Fatalf("%+v %v", d1, err)
	}
	d2, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || d2.ClusterName != d1.ClusterName {
		t.Fatalf("unstable: %s vs %s", d1.ClusterName, d2.ClusterName)
	}
	if d1.ClusterName != "aaa-east" && d1.ClusterName != "bbb-east" {
		t.Fatalf("unexpected pick %s", d1.ClusterName)
	}
}

func TestDecide_NoMatchPending(t *testing.T) {
	s := scheme(t)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(
		readyTarget("prod", map[string]string{"kubelease.io/tier": "prod"}, nil, 0),
	).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubelease.io/tier": "dev"}},
			},
		},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || !d.Pending {
		t.Fatalf("expected pending, got %+v err=%v", d, err)
	}
}

func TestDecide_DisabledExcluded(t *testing.T) {
	s := scheme(t)
	enabled := false
	tOff := readyTarget("off", map[string]string{"kubelease.io/tier": "dev"}, nil, 0)
	tOff.Spec.Enabled = &enabled
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(tOff).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubelease.io/tier": "dev"}},
			},
		},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || !d.Pending {
		t.Fatalf("expected pending, got %+v", d)
	}
}

func TestDecide_SoftCapacity(t *testing.T) {
	s := scheme(t)
	max := int32(1)
	full := readyTarget("full", map[string]string{"kubelease.io/tier": "dev"}, &max, 1)
	ok := readyTarget("ok", map[string]string{"kubelease.io/tier": "dev"}, &max, 0)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(full, ok).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubelease.io/tier": "dev"}},
			},
		},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{
		ActiveLeaseCounts: map[string]int32{"full": 1, "ok": 0},
	})
	if err != nil || d.ClusterName != "ok" {
		t.Fatalf("got %+v err=%v", d, err)
	}
}

func TestDecide_PolicyRestrictsClusterRef(t *testing.T) {
	s := scheme(t)
	prod := readyTarget("prod", map[string]string{"kubelease.io/tier": "prod"}, nil, 0)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(prod).Build()
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-only"},
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubelease.io/tier": "dev"}},
			},
		},
	}
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			ClusterRef: &platformv1alpha1.LocalObjectReference{Name: "prod"},
		},
	}
	_, err := Decide(t.Context(), hub, leaseObj, pol, Options{})
	if err == nil {
		t.Fatal("expected policy violation")
	}
}

func TestDecide_StickyNoMigration(t *testing.T) {
	s := scheme(t)
	// Selected target is no longer Ready, but Namespace already exists.
	bad := readyTarget("dev-east", map[string]string{"kubelease.io/tier": "dev"}, nil, 0)
	bad.Status.Conditions = []metav1.Condition{{
		Type:   platformv1alpha1.ClusterTargetConditionReady,
		Status: metav1.ConditionFalse,
		Reason: platformv1alpha1.ReasonClusterUnreachable,
	}}
	other := readyTarget("dev-west", map[string]string{"kubelease.io/tier": "dev"}, nil, 0)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(bad, other).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubelease.io/tier": "dev"}},
			},
		},
		Status: platformv1alpha1.EnvironmentLeaseStatus{
			Namespace: "preview-1",
			Cluster:   &platformv1alpha1.ClusterStatus{Name: "dev-east"},
		},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || d.ClusterName != "dev-east" {
		t.Fatalf("expected sticky dev-east, got %+v err=%v", d, err)
	}
}

func TestDecide_ReselectBeforeProvision(t *testing.T) {
	s := scheme(t)
	bad := readyTarget("dev-east", map[string]string{"kubelease.io/tier": "dev"}, nil, 0)
	bad.Status.Conditions[0].Status = metav1.ConditionFalse
	good := readyTarget("dev-west", map[string]string{"kubelease.io/tier": "dev"}, nil, 0)
	hub := fake.NewClientBuilder().WithScheme(s).WithObjects(bad, good).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Placement: &platformv1alpha1.PlacementSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubelease.io/tier": "dev"}},
			},
		},
		Status: platformv1alpha1.EnvironmentLeaseStatus{
			Cluster: &platformv1alpha1.ClusterStatus{Name: "dev-east"},
		},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || d.ClusterName != "dev-west" || !d.Reselected {
		t.Fatalf("expected reselect west, got %+v err=%v", d, err)
	}
}

func TestDecide_LocalFallback(t *testing.T) {
	s := scheme(t)
	hub := fake.NewClientBuilder().WithScheme(s).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "l", UID: "u"},
	}
	d, err := Decide(t.Context(), hub, leaseObj, nil, Options{})
	if err != nil || d.ClusterName != platformv1alpha1.LocalClusterName {
		t.Fatalf("got %+v err=%v", d, err)
	}
}
