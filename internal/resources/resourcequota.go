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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

const ResourceQuotaName = "kubelease-quota"

// DesiredResourceQuota builds a ResourceQuota from the lease quota spec.
// Returns nil when quota is unset or empty.
func DesiredResourceQuota(lease *platformv1alpha1.EnvironmentLease, namespace string) *corev1.ResourceQuota {
	if lease.Spec.Quota == nil {
		return nil
	}

	hard := corev1.ResourceList{}
	for name, qty := range lease.Spec.Quota.Requests {
		hard[corev1.ResourceName("requests."+string(name))] = qty.DeepCopy()
	}
	for name, qty := range lease.Spec.Quota.Limits {
		hard[corev1.ResourceName("limits."+string(name))] = qty.DeepCopy()
	}
	if len(hard) == 0 {
		return nil
	}

	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ResourceQuotaName,
			Namespace: namespace,
			Labels:    ManagedLabels(lease.Name),
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: hard,
		},
	}
}

// ResourceListsEqual compares two ResourceLists for equality.
func ResourceListsEqual(a, b corev1.ResourceList) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		bv, ok := b[k]
		if !ok {
			return false
		}
		if av.Cmp(bv) != 0 {
			return false
		}
	}
	return true
}
