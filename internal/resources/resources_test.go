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

package resources

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func sampleLease() *platformv1alpha1.EnvironmentLease {
	return &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "payment-api-pr-1842"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			TTL: &metav1.Duration{Duration: 8 * time.Hour}, // unused in builders
			Namespace: platformv1alpha1.NamespaceSpec{
				GenerateName: "preview-",
				Labels: map[string]string{
					"environment": "preview",
					"application": "payments",
				},
			},
			Quota: &platformv1alpha1.QuotaSpec{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("4"),
					corev1.ResourceMemory: resource.MustParse("8Gi"),
				},
			},
			Limits: &platformv1alpha1.LimitsSpec{
				Default: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("512Mi"),
				},
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("100m"),
					corev1.ResourceMemory: resource.MustParse("128Mi"),
				},
			},
			NetworkPolicy: &platformv1alpha1.NetworkPolicySpec{DefaultDeny: true},
		},
	}
}

func TestIsProtectedNamespace(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"default", "kube-system", "kube-public", "kube-node-lease"} {
		if !IsProtectedNamespace(name) {
			t.Fatalf("%s should be protected", name)
		}
	}
	if IsProtectedNamespace("preview-abc") {
		t.Fatal("preview-abc should not be protected")
	}
}

func TestValidateNamespaceSpec(t *testing.T) {
	t.Parallel()
	if err := ValidateNamespaceSpec(platformv1alpha1.NamespaceSpec{}); err == nil {
		t.Fatal("expected error when name and generateName empty")
	}
	if err := ValidateNamespaceSpec(platformv1alpha1.NamespaceSpec{Name: "kube-system"}); err == nil {
		t.Fatal("expected error for protected name")
	}
	if err := ValidateNamespaceSpec(platformv1alpha1.NamespaceSpec{GenerateName: "preview-"}); err != nil {
		t.Fatal(err)
	}
}

func TestDesiredNamespace(t *testing.T) {
	t.Parallel()
	leaseObj := sampleLease()
	ns, err := DesiredNamespace(leaseObj, "preview-r82jx", "ctrl", "remote")
	if err != nil {
		t.Fatal(err)
	}
	if ns.Annotations[platformv1alpha1.AnnotationControlClusterID] != "ctrl" {
		t.Fatal("missing control-cluster-id")
	}
	if ns.Annotations[platformv1alpha1.AnnotationTargetIdentity] != "remote" {
		t.Fatal("missing target-identity")
	}
	if ns.Name != "preview-r82jx" {
		t.Fatalf("name=%s", ns.Name)
	}
	if ns.Labels[platformv1alpha1.LabelManagedBy] != platformv1alpha1.ManagedByValue {
		t.Fatal("missing managed-by label")
	}
	if ns.Labels[platformv1alpha1.LabelLease] != leaseObj.Name {
		t.Fatal("missing lease label")
	}
	if ns.Labels["environment"] != "preview" {
		t.Fatal("user labels not merged")
	}
	if len(ns.OwnerReferences) != 0 {
		t.Fatal("Namespace must not have OwnerReferences")
	}
	if _, err := DesiredNamespace(leaseObj, "default", "", ""); err == nil {
		t.Fatal("expected error for protected namespace")
	}
}

func TestDesiredResourceQuota(t *testing.T) {
	t.Parallel()
	leaseObj := sampleLease()
	rq := DesiredResourceQuota(leaseObj, "preview-r82jx")
	if rq == nil {
		t.Fatal("expected ResourceQuota")
	}
	if rq.Namespace != "preview-r82jx" {
		t.Fatalf("ns=%s", rq.Namespace)
	}
	cpuReq := rq.Spec.Hard[corev1.ResourceName("requests.cpu")]
	if cpuReq.Cmp(resource.MustParse("2")) != 0 {
		t.Fatalf("requests.cpu=%v", cpuReq)
	}
	memLim := rq.Spec.Hard[corev1.ResourceName("limits.memory")]
	if memLim.Cmp(resource.MustParse("8Gi")) != 0 {
		t.Fatalf("limits.memory=%v", memLim)
	}
}

func TestDesiredLimitRange(t *testing.T) {
	t.Parallel()
	leaseObj := sampleLease()
	lr := DesiredLimitRange(leaseObj, "preview-r82jx")
	if lr == nil || len(lr.Spec.Limits) != 1 {
		t.Fatal("expected LimitRange with one item")
	}
	item := lr.Spec.Limits[0]
	if item.Type != corev1.LimitTypeContainer {
		t.Fatalf("type=%s", item.Type)
	}
	defaultCPU := item.Default[corev1.ResourceCPU]
	if defaultCPU.Cmp(resource.MustParse("500m")) != 0 {
		t.Fatal("default cpu mismatch")
	}
}

func TestDesiredNetworkPolicy(t *testing.T) {
	t.Parallel()
	leaseObj := sampleLease()
	np := DesiredNetworkPolicy(leaseObj, "preview-r82jx")
	if np == nil {
		t.Fatal("expected NetworkPolicy")
	}
	if len(np.Spec.Ingress) != 0 || len(np.Spec.Egress) != 0 {
		t.Fatal("default-deny should have empty ingress/egress")
	}
	if len(np.Spec.PolicyTypes) != 2 {
		t.Fatal("expected Ingress and Egress policy types")
	}
	for _, pt := range np.Spec.PolicyTypes {
		if pt != networkingv1.PolicyTypeIngress && pt != networkingv1.PolicyTypeEgress {
			t.Fatalf("unexpected policy type %s", pt)
		}
	}

	leaseObj.Spec.NetworkPolicy.DefaultDeny = false
	if DesiredNetworkPolicy(leaseObj, "preview-r82jx") != nil {
		t.Fatal("expected nil when defaultDeny=false")
	}
}

func TestMergeLabelsManagementWins(t *testing.T) {
	t.Parallel()
	base := ManagedLabels("lease-a")
	extra := map[string]string{
		platformv1alpha1.LabelManagedBy: "someone-else",
		"custom":                        "yes",
	}
	merged := MergeLabels(base, extra)
	if merged[platformv1alpha1.LabelManagedBy] != platformv1alpha1.ManagedByValue {
		t.Fatal("management labels must win")
	}
	if merged["custom"] != "yes" {
		t.Fatal("custom label missing")
	}
}
