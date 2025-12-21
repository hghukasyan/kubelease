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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// ClusterTargetFinalizer blocks deletion while EnvironmentLeases reference the target.
	ClusterTargetFinalizer = "platform.kubelease.io/cluster-target-protection"

	// LocalClusterName is the status/cluster identity when clusterRef is omitted
	// (environment provisioned on the control-plane cluster).
	LocalClusterName = "local"
)

// ClusterTarget condition types.
const (
	ClusterTargetConditionReady         = "Ready"
	ClusterTargetConditionAuthenticated = "Authenticated"
	ClusterTargetConditionReachable     = "Reachable"
)

// ClusterTarget condition reasons.
const (
	ReasonClusterReachable      = "ClusterReachable"
	ReasonClusterUnreachable    = "ClusterUnreachable"
	ReasonCredentialsInvalid    = "CredentialsInvalid"
	ReasonCredentialsMissing    = "CredentialsMissing"
	ReasonClusterTargetDisabled = "TargetDisabled"
	ReasonTargetHasActiveLeases = "TargetHasActiveLeases"
	ReasonTargetDeleting        = "TargetDeleting"
	ReasonIdentityDrift         = "IdentityDrift"
)

// ClusterTargetConditionDegraded marks non-fatal health issues (e.g. soft capacity pressure).
const ClusterTargetConditionDegraded = "Degraded"

// SecretKeySelector references a key in a Secret. Credentials must never be
// embedded in the ClusterTarget itself.
type SecretKeySelector struct {
	// Name of the Secret.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the Secret. Required because ClusterTarget is cluster-scoped.
	// Prefer the KubeLease control-plane namespace.
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// Key within the Secret. Defaults to "kubeconfig".
	// +optional
	// +kubebuilder:default=kubeconfig
	Key string `json:"key,omitempty"`
}

// ClusterCredentials describes how to authenticate to a remote cluster.
//
// v1alpha1 supports kubeconfig material stored in a Secret. Future versions may
// add ServiceAccount token or workload-identity credential sources without
// embedding secrets in the CR.
type ClusterCredentials struct {
	// SecretRef points at a Secret containing a kubeconfig (client-go format).
	// +kubebuilder:validation:Required
	SecretRef SecretKeySelector `json:"secretRef"`
}

// ClusterTargetSpec defines a remote Kubernetes cluster that may host leased environments.
type ClusterTargetSpec struct {
	// Credentials references authentication material. Never embed kubeconfig here.
	// +kubebuilder:validation:Required
	Credentials ClusterCredentials `json:"credentials"`

	// Labels are optional scheduling labels merged with metadata.labels for
	// placement matching. Prefer metadata.labels with kubelease.io/* keys.
	// Not copied onto remote Namespaces automatically.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// MaxActiveLeases is a soft capacity ceiling for placement.
	// Counts are best-effort (not transactional); races may slightly exceed this.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxActiveLeases *int32 `json:"maxActiveLeases,omitempty"`

	// ClientQPS overrides the default remote client QPS for this target.
	// Safe default is applied when unset (see remote provider). Capped at 50.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	ClientQPS *int32 `json:"clientQPS,omitempty"`

	// ClientBurst overrides the default remote client burst for this target.
	// Capped at 100.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	ClientBurst *int32 `json:"clientBurst,omitempty"`

	// Enabled, when false, rejects new/ongoing provisioning against this target.
	// Defaults to true when omitted.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// IsEnabled returns whether the target accepts traffic. Default true.
func (s ClusterTargetSpec) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// ClusterCapacityStatus is lightweight lease-count capacity (not CPU/memory).
type ClusterCapacityStatus struct {
	// ActiveLeases is the soft count of EnvironmentLeases with status.cluster.name
	// equal to this target (best-effort).
	// +optional
	ActiveLeases int32 `json:"activeLeases,omitempty"`

	// MaxLeases mirrors Spec.MaxActiveLeases when set.
	// +optional
	MaxLeases *int32 `json:"maxLeases,omitempty"`
}

// ClusterTargetStatus is the observed health of a remote cluster connection.
type ClusterTargetStatus struct {
	// KubernetesVersion is the remote server version when reachable.
	// +optional
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`

	// RemoteIdentity is a sticky fingerprint of the remote API (kube-system UID).
	// Set on first successful health probe and compared thereafter; credential
	// rotation that points at a different cluster sets Ready=False (IdentityDrift).
	// +optional
	RemoteIdentity string `json:"remoteIdentity,omitempty"`

	// Capacity tracks soft lease-count usage for placement.
	// +optional
	Capacity *ClusterCapacityStatus `json:"capacity,omitempty"`

	// ObservedGeneration is the most recent generation observed.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the target.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SchedulingLabels returns labels used for placement: metadata.labels overlay
// spec.labels (metadata wins on key conflict).
func (t *ClusterTarget) SchedulingLabels() map[string]string {
	out := map[string]string{}
	for k, v := range t.Spec.Labels {
		out[k] = v
	}
	for k, v := range t.Labels {
		out[k] = v
	}
	return out
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=ctarget;cltarget
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.kubernetesVersion`
// +kubebuilder:printcolumn:name="Leases",type=integer,JSONPath=`.status.capacity.activeLeases`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterTarget registers a remote Kubernetes cluster for EnvironmentLease provisioning.
//
// Design notes:
//   - Cluster-scoped: targets are platform infrastructure shared across teams.
//   - Credentials live only in Secrets referenced by secretRef (never inlined).
//   - Auth model today is kubeconfig; the credentials struct is extensible for
//     SA tokens / workload identity later.
type ClusterTarget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterTargetSpec   `json:"spec,omitempty"`
	Status ClusterTargetStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterTargetList contains a list of ClusterTarget.
type ClusterTargetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterTarget `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterTarget{}, &ClusterTargetList{})
}
