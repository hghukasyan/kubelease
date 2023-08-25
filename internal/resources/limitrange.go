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

const LimitRangeName = "kubelease-limits"

// DesiredLimitRange builds a LimitRange from the lease limits spec.
// Returns nil when limits are unset or empty.
func DesiredLimitRange(lease *platformv1alpha1.EnvironmentLease, namespace string) *corev1.LimitRange {
	if lease.Spec.Limits == nil {
		return nil
	}

	item := corev1.LimitRangeItem{
		Type:           corev1.LimitTypeContainer,
		Default:        copyResourceList(lease.Spec.Limits.Default),
		DefaultRequest: copyResourceList(lease.Spec.Limits.DefaultRequest),
		Max:            copyResourceList(lease.Spec.Limits.Max),
		Min:            copyResourceList(lease.Spec.Limits.Min),
	}
	if len(item.Default) == 0 && len(item.DefaultRequest) == 0 &&
		len(item.Max) == 0 && len(item.Min) == 0 {
		return nil
	}

	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      LimitRangeName,
			Namespace: namespace,
			Labels:    ManagedLabels(lease.Name),
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{item},
		},
	}
}

func copyResourceList(in corev1.ResourceList) corev1.ResourceList {
	if len(in) == 0 {
		return nil
	}
	out := make(corev1.ResourceList, len(in))
	for k, v := range in {
		out[k] = v.DeepCopy()
	}
	return out
}
