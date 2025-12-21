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

package identity

import (
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func TestVerifyNamespaceOwnership(t *testing.T) {
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "pay", UID: "uid-1"},
		Status: platformv1alpha1.EnvironmentLeaseStatus{
			Cluster: &platformv1alpha1.ClusterStatus{RemoteIdentity: "remote-a"},
		},
	}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "preview-1",
			Labels: map[string]string{
				platformv1alpha1.LabelManagedBy: platformv1alpha1.ManagedByValue,
				platformv1alpha1.LabelLease:     "pay",
			},
			Annotations: map[string]string{
				platformv1alpha1.AnnotationLeaseUID:         "uid-1",
				platformv1alpha1.AnnotationControlClusterID: "ctrl-1",
				platformv1alpha1.AnnotationTargetIdentity:   "remote-a",
			},
		},
	}
	if err := VerifyNamespaceOwnership(ns, leaseObj, "ctrl-1"); err != nil {
		t.Fatal(err)
	}
	ns.Annotations[platformv1alpha1.AnnotationTargetIdentity] = "remote-b"
	var ome *OwnershipMismatchError
	if err := VerifyNamespaceOwnership(ns, leaseObj, "ctrl-1"); !errors.As(err, &ome) {
		t.Fatalf("expected OwnershipMismatchError, got %v", err)
	}
}

func TestVerifyLiveTargetIdentity(t *testing.T) {
	if err := VerifyLiveTargetIdentity("a", "a"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLiveTargetIdentity("a", "b"); err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestForceCleanupAcknowledged(t *testing.T) {
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				platformv1alpha1.AnnotationForceCleanupAcknowledged: "true",
			},
		},
	}
	if !ForceCleanupAcknowledged(leaseObj) {
		t.Fatal("expected true")
	}
}
