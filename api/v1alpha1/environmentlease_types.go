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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// FinalizerName is added to EnvironmentLease objects so the controller can
	// clean up the managed environment before the object is removed.
	FinalizerName = "platform.kubelease.io/finalizer"

	// LabelManagedBy identifies resources managed by KubeLease.
	LabelManagedBy = "app.kubernetes.io/managed-by"
	// ManagedByValue is the value of LabelManagedBy.
	ManagedByValue = "kubelease"
	// LabelLease links a managed resource back to its EnvironmentLease name.
	LabelLease = "kubelease.io/lease"
)

// LeasePhase is a high-level lifecycle summary. Conditions are authoritative
// for machine-readable health.
// +kubebuilder:validation:Enum=Pending;Provisioning;Active;Expiring;Cleaning;Expired;Failed
type LeasePhase string

const (
	LeasePhasePending      LeasePhase = "Pending"
	LeasePhaseProvisioning LeasePhase = "Provisioning"
	LeasePhaseActive       LeasePhase = "Active"
	LeasePhaseExpiring     LeasePhase = "Expiring"
	LeasePhaseCleaning     LeasePhase = "Cleaning"
	LeasePhaseExpired      LeasePhase = "Expired"
	LeasePhaseFailed       LeasePhase = "Failed"
)

// Condition types for EnvironmentLease.
const (
	ConditionReady              = "Ready"
	ConditionEnvironmentCreated = "EnvironmentCreated"
	ConditionCleanup            = "Cleanup"
)

// Condition reasons.
const (
	ReasonProvisioning                = "Provisioning"
	ReasonEnvironmentReady            = "EnvironmentReady"
	ReasonNamespaceCreationFailed     = "NamespaceCreationFailed"
	ReasonResourceQuotaCreationFailed = "ResourceQuotaCreationFailed"
	ReasonLimitRangeCreationFailed    = "LimitRangeCreationFailed"
	ReasonNetworkPolicyCreationFailed = "NetworkPolicyCreationFailed"
	ReasonInvalidConfiguration        = "InvalidConfiguration"
	ReasonLeaseExpired                = "LeaseExpired"
	ReasonCleanupInProgress           = "CleanupInProgress"
	ReasonCleanupComplete             = "CleanupComplete"
	ReasonCleanupFailed               = "CleanupFailed"
)

// OwnerSpec identifies the human or team that owns the lease.
type OwnerSpec struct {
	// Name is the individual owner of the environment.
	// +optional
	Name string `json:"name,omitempty"`

	// Team is the owning team.
	// +optional
	Team string `json:"team,omitempty"`
}

// NamespaceSpec describes how the managed Namespace should be created.
type NamespaceSpec struct {
	// GenerateName is a prefix used with Kubernetes generateName semantics when
	// Name is empty. Example: "preview-" yields names like "preview-r82jx".
	// +optional
	GenerateName string `json:"generateName,omitempty"`

	// Name, if set, creates a Namespace with this exact name. Takes precedence
	// over GenerateName. Must not be a protected system namespace.
	// +optional
	Name string `json:"name,omitempty"`

	// Labels are applied to the managed Namespace (in addition to KubeLease
	// management labels).
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations are applied to the managed Namespace.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// QuotaSpec maps to a ResourceQuota hard limit set.
// Requests become requests.<resource>; Limits become limits.<resource>.
type QuotaSpec struct {
	// Requests are summed into ResourceQuota hard as requests.<resource>.
	// +optional
	Requests corev1.ResourceList `json:"requests,omitempty"`

	// Limits are summed into ResourceQuota hard as limits.<resource>.
	// +optional
	Limits corev1.ResourceList `json:"limits,omitempty"`
}

// LimitsSpec maps to a LimitRange for Container type.
type LimitsSpec struct {
	// Default are default limits applied to containers that omit limits.
	// +optional
	Default corev1.ResourceList `json:"default,omitempty"`

	// DefaultRequest are default requests applied to containers that omit requests.
	// +optional
	DefaultRequest corev1.ResourceList `json:"defaultRequest,omitempty"`

	// Max is the maximum amount of resources a container may request/limit.
	// +optional
	Max corev1.ResourceList `json:"max,omitempty"`

	// Min is the minimum amount of resources a container may request/limit.
	// +optional
	Min corev1.ResourceList `json:"min,omitempty"`
}

// NetworkPolicySpec configures NetworkPolicy provisioning for the environment.
type NetworkPolicySpec struct {
	// DefaultDeny, when true, creates a NetworkPolicy that denies all ingress
	// and egress traffic by default.
	// +optional
	DefaultDeny bool `json:"defaultDeny,omitempty"`
}

// EnvironmentLeaseSpec defines the desired state of EnvironmentLease.
type EnvironmentLeaseSpec struct {
	// TTL is how long the environment should exist after creation.
	// Represented as a Kubernetes duration string (e.g. "8h", "30m").
	// +kubebuilder:validation:Required
	TTL metav1.Duration `json:"ttl"`

	// Owner identifies who requested the environment.
	// +optional
	Owner OwnerSpec `json:"owner,omitempty"`

	// Namespace configures the managed Namespace.
	// +kubebuilder:validation:Required
	Namespace NamespaceSpec `json:"namespace"`

	// Quota, if set, creates a ResourceQuota in the managed Namespace.
	// +optional
	Quota *QuotaSpec `json:"quota,omitempty"`

	// Limits, if set, creates a LimitRange in the managed Namespace.
	// +optional
	Limits *LimitsSpec `json:"limits,omitempty"`

	// NetworkPolicy configures NetworkPolicy resources for the environment.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
}

// EnvironmentLeaseStatus defines the observed state of EnvironmentLease.
type EnvironmentLeaseStatus struct {
	// Phase is a human-readable summary of the lease lifecycle.
	// Prefer Conditions for programmatic decisions.
	// +optional
	Phase LeasePhase `json:"phase,omitempty"`

	// Namespace is the name of the managed Namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// CreatedAt is when the lease window started. Persisted so restarts do not
	// reset the expiration clock.
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// ExpiresAt is CreatedAt + Spec.TTL. Recalculated when Spec.TTL changes
	// relative to the sticky CreatedAt.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the lease.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=envlease;el
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// EnvironmentLease is the Schema for the environmentleases API.
// It is cluster-scoped because it manages Namespaces (cluster-scoped) and
// supporting resources inside those namespaces.
type EnvironmentLease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvironmentLeaseSpec   `json:"spec,omitempty"`
	Status EnvironmentLeaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvironmentLeaseList contains a list of EnvironmentLease.
type EnvironmentLeaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []EnvironmentLease `json:"items"`
}

func init() {
	SchemeBuilder.Register(&EnvironmentLease{}, &EnvironmentLeaseList{})
}
