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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// ProtectedNamespaces must never be managed by KubeLease.
var ProtectedNamespaces = map[string]struct{}{
	"default":         {},
	"kube-system":     {},
	"kube-public":     {},
	"kube-node-lease": {},
}

// IsProtectedNamespace reports whether name is a protected system namespace.
func IsProtectedNamespace(name string) bool {
	_, ok := ProtectedNamespaces[name]
	return ok
}

// ValidateNamespaceSpec ensures the namespace configuration is safe to apply.
func ValidateNamespaceSpec(spec platformv1alpha1.NamespaceSpec) error {
	if spec.Name != "" {
		if IsProtectedNamespace(spec.Name) {
			return fmt.Errorf("cannot manage protected namespace %q", spec.Name)
		}
		return nil
	}
	if spec.GenerateName == "" {
		return fmt.Errorf("namespace.name or namespace.generateName must be set")
	}
	return nil
}

// OwnedByLease reports whether the Namespace is owned by the given lease UID.
func OwnedByLease(ns *corev1.Namespace, leaseName, leaseUID string) bool {
	if ns == nil {
		return false
	}
	if !IsManagedByKubeLease(ns.Labels, leaseName) {
		return false
	}
	if leaseUID == "" {
		return true
	}
	return ns.Annotations[platformv1alpha1.AnnotationLeaseUID] == leaseUID
}

// CanAdoptNamespace reports whether an existing Namespace may be adopted.
// Empty/unmanaged namespaces with matching name may be claimed; foreign
// managed namespaces must not.
func CanAdoptNamespace(ns *corev1.Namespace, leaseName, leaseUID string) bool {
	if ns == nil {
		return false
	}
	if IsProtectedNamespace(ns.Name) {
		return false
	}
	if OwnedByLease(ns, leaseName, leaseUID) {
		return true
	}
	// Refuse if managed by a different lease.
	if ns.Labels[platformv1alpha1.LabelManagedBy] == platformv1alpha1.ManagedByValue {
		other := ns.Labels[platformv1alpha1.LabelLease]
		if other != "" && other != leaseName {
			return false
		}
		otherUID := ns.Annotations[platformv1alpha1.AnnotationLeaseUID]
		if otherUID != "" && otherUID != leaseUID {
			return false
		}
	}
	// Unmanaged namespace with the desired name: allow claim.
	return ns.Labels[platformv1alpha1.LabelManagedBy] != platformv1alpha1.ManagedByValue ||
		ns.Labels[platformv1alpha1.LabelLease] == leaseName
}

// DesiredNamespace builds the Namespace object for a lease.
// No OwnerReference is set on Namespace.
func DesiredNamespace(leaseObj *platformv1alpha1.EnvironmentLease, name string) (*corev1.Namespace, error) {
	if name == "" {
		return nil, fmt.Errorf("namespace name must not be empty")
	}
	if IsProtectedNamespace(name) {
		return nil, fmt.Errorf("cannot manage protected namespace %q", name)
	}

	annotations := copyStringMap(leaseObj.Spec.Namespace.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[platformv1alpha1.AnnotationLeaseUID] = string(leaseObj.UID)

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      MergeLabels(ManagedLabels(leaseObj.Name), leaseObj.Spec.Namespace.Labels),
			Annotations: annotations,
		},
	}
	return ns, nil
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
