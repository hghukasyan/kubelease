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
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

const NetworkPolicyName = "kubelease-default-deny"

// DesiredNetworkPolicy builds a default-deny NetworkPolicy when configured.
// Returns nil when defaultDeny is false or unset.
func DesiredNetworkPolicy(lease *platformv1alpha1.EnvironmentLease, namespace string) *networkingv1.NetworkPolicy {
	if lease.Spec.NetworkPolicy == nil || !lease.Spec.NetworkPolicy.DefaultDeny {
		return nil
	}

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NetworkPolicyName,
			Namespace: namespace,
			Labels:    ManagedLabels(lease.Name),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
			// Empty Ingress/Egress lists = deny all for selected pods (all pods).
		},
	}
}
