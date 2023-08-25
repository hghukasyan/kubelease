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

// DesiredNamespace builds the Namespace object for a lease.
// Name must already be resolved (either fixed or previously generated) and
// stored/passed via name. No OwnerReference is set: Namespace ownership is
// tracked via labels and cleaned up with the EnvironmentLease finalizer.
func DesiredNamespace(lease *platformv1alpha1.EnvironmentLease, name string) (*corev1.Namespace, error) {
	if name == "" {
		return nil, fmt.Errorf("namespace name must not be empty")
	}
	if IsProtectedNamespace(name) {
		return nil, fmt.Errorf("cannot manage protected namespace %q", name)
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      MergeLabels(ManagedLabels(lease.Name), lease.Spec.Namespace.Labels),
			Annotations: copyStringMap(lease.Spec.Namespace.Annotations),
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
