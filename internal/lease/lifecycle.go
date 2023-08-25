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
	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/resources"
)

// ValidateSpec performs controller-side defensive validation beyond CRD schema.
func ValidateSpec(lease *platformv1alpha1.EnvironmentLease) error {
	if err := ValidateTTL(lease.Spec.TTL); err != nil {
		return err
	}
	if err := resources.ValidateNamespaceSpec(lease.Spec.Namespace); err != nil {
		return err
	}
	if lease.Status.Namespace != "" && resources.IsProtectedNamespace(lease.Status.Namespace) {
		return resources.ValidateNamespaceSpec(platformv1alpha1.NamespaceSpec{Name: lease.Status.Namespace})
	}
	return nil
}
