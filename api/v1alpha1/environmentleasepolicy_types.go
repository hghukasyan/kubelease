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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DurationPolicy configures a default and hard maximum for a duration field.
type DurationPolicy struct {
	// Default is applied when the lease omits the corresponding field.
	// +optional
	Default *metav1.Duration `json:"default,omitempty"`

	// Maximum is a hard upper bound. Lease values above this are rejected.
	// +optional
	Maximum *metav1.Duration `json:"maximum,omitempty"`

	// Minimum is a hard lower bound. Lease values below this are rejected.
	// +optional
	Minimum *metav1.Duration `json:"minimum,omitempty"`
}

// BoolPolicy configures default and optional forced boolean values.
type BoolPolicy struct {
	// Default is applied when the lease omits the field.
	// +optional
	Default *bool `json:"default,omitempty"`

	// Force, when set, requires the effective value to equal this.
	// Mismatches are rejected (not silently overridden beyond applying when unset).
	// +optional
	Force *bool `json:"force,omitempty"`
}

// QuotaPolicy defines hard ceiling values for ResourceQuota requests/limits.
type QuotaPolicy struct {
	// MaxCPU is the maximum allowed CPU for quota requests and limits.
	// +optional
	MaxCPU *resource.Quantity `json:"maxCPU,omitempty"`

	// MaxMemory is the maximum allowed memory for quota requests and limits.
	// +optional
	MaxMemory *resource.Quantity `json:"maxMemory,omitempty"`
}

// NetworkPolicyPolicy configures NetworkPolicy defaults and requirements.
type NetworkPolicyPolicy struct {
	// DefaultDenyRequired, when true, requires default-deny NetworkPolicy.
	// Leases that explicitly set defaultDeny=false are rejected.
	// +optional
	DefaultDenyRequired bool `json:"defaultDenyRequired,omitempty"`

	// DefaultDenyDefault is applied when the lease omits NetworkPolicy.DefaultDeny.
	// +optional
	DefaultDenyDefault *bool `json:"defaultDenyDefault,omitempty"`
}

// EnvironmentLeasePolicySpec defines reusable defaults and hard limits.
type EnvironmentLeasePolicySpec struct {
	// TTL configures defaults/limits for EnvironmentLease.spec.ttl.
	// +optional
	TTL *DurationPolicy `json:"ttl,omitempty"`

	// MaxTTL configures defaults/limits for EnvironmentLease.spec.maxTTL.
	// When unset, ttl.maximum (if present) is also used as the maxTTL ceiling.
	// +optional
	MaxTTL *DurationPolicy `json:"maxTTL,omitempty"`

	// IdleTTL configures defaults/limits for EnvironmentLease.spec.idleTTL.
	// Idle detection is enforced in a later phase; values are resolved and validated now.
	// +optional
	IdleTTL *DurationPolicy `json:"idleTTL,omitempty"`

	// Renewable configures default/force for EnvironmentLease.spec.renewable.
	// +optional
	Renewable *BoolPolicy `json:"renewable,omitempty"`

	// Quota configures hard ceilings for lease quota.
	// +optional
	Quota *QuotaPolicy `json:"quota,omitempty"`

	// NetworkPolicy configures NetworkPolicy defaults/requirements.
	// +optional
	NetworkPolicy *NetworkPolicyPolicy `json:"networkPolicy,omitempty"`
}

// EnvironmentLeasePolicyStatus is reserved for observed policy state.
type EnvironmentLeasePolicyStatus struct {
	// ObservedGeneration is the most recent generation observed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=envleasepolicy;elp
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EnvironmentLeasePolicy is a reusable cluster-scoped policy for EnvironmentLeases.
// Policies provide defaults for omitted lease fields and hard limits that are
// rejected (never silently clamped) when violated.
type EnvironmentLeasePolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvironmentLeasePolicySpec   `json:"spec,omitempty"`
	Status EnvironmentLeasePolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvironmentLeasePolicyList contains a list of EnvironmentLeasePolicy.
type EnvironmentLeasePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EnvironmentLeasePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EnvironmentLeasePolicy{}, &EnvironmentLeasePolicyList{})
}
