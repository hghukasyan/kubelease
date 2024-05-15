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
	"time"

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
	// AnnotationLeaseUID binds a managed Namespace to a specific EnvironmentLease UID.
	AnnotationLeaseUID = "kubelease.io/lease-uid"
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
	ConditionReady    = "Ready"
	ConditionExpiring = "Expiring"
	ConditionCleanup  = "Cleanup"
	ConditionDegraded = "Degraded"
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
	ReasonPolicyViolation             = "PolicyViolation"
	ReasonPolicyNotFound              = "PolicyNotFound"
	ReasonLeaseExpired                = "LeaseExpired"
	ReasonLeaseExpiring               = "LeaseExpiring"
	ReasonCleanupInProgress           = "CleanupInProgress"
	ReasonCleanupComplete             = "CleanupComplete"
	ReasonCleanupFailed               = "CleanupFailed"
	ReasonRenewalRejected             = "RenewalRejected"
	ReasonNamespaceAdoptRefused       = "NamespaceAdoptRefused"
)

// ExpirationReason explains why a lease reached effective expiration.
// +kubebuilder:validation:Enum=TTLExpired;IdleTimeout;ManualExpiration;SourceClosed
type ExpirationReason string

const (
	// ExpirationReasonTTLExpired means the hard TTL (createdAt+ttl) elapsed.
	ExpirationReasonTTLExpired ExpirationReason = "TTLExpired"
	// ExpirationReasonIdleTimeout means lastActivityAt+idleTTL elapsed first.
	ExpirationReasonIdleTimeout ExpirationReason = "IdleTimeout"
	// ExpirationReasonManualExpiration means the lease was explicitly expired/deleted.
	ExpirationReasonManualExpiration ExpirationReason = "ManualExpiration"
	// ExpirationReasonSourceClosed means the upstream source (e.g. PR) closed.
	ExpirationReasonSourceClosed ExpirationReason = "SourceClosed"
)

// LocalObjectReference identifies a cluster-scoped policy by name.
type LocalObjectReference struct {
	// Name of the referent.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

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
//
// Renewal model: extend a lease by increasing Spec.TTL. The controller derives
// status.expiresAt = status.createdAt + effectiveTTL and clamps it to
// status.maximumExpiresAt (= createdAt + effectiveMaxTTL).
//
// When PolicyRef is set, omitted fields take policy defaults. Values that
// violate policy hard limits are rejected (not silently clamped).
type EnvironmentLeaseSpec struct {
	// PolicyRef references a cluster-scoped EnvironmentLeasePolicy.
	// +optional
	PolicyRef *LocalObjectReference `json:"policyRef,omitempty"`

	// TTL is the requested lifetime from CreatedAt.
	// Optional when PolicyRef provides ttl.default; otherwise required.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// MaxTTL is the absolute maximum lifetime from CreatedAt.
	// If unset, defaults to effective TTL (or policy maxTTL/ttl.maximum).
	// +optional
	MaxTTL *metav1.Duration `json:"maxTTL,omitempty"`

	// IdleTTL is the inactivity window before an idle environment may expire.
	// When set, effective expiration is min(hardExpiresAt, lastActivityAt+idleTTL).
	// Heartbeats (kubectl kubelease touch) update status.lastActivityAt.
	// Activity can never bypass the hard TTL / maxTTL.
	// +optional
	IdleTTL *metav1.Duration `json:"idleTTL,omitempty"`

	// Renewable controls whether Spec.TTL may be increased to extend the lease.
	// Defaults to true when omitted (unless policy forces otherwise).
	// +optional
	Renewable *bool `json:"renewable,omitempty"`

	// Warnings are durations before expiration at which LeaseExpiring events
	// should be emitted (e.g. "1h", "15m"). Must be unique and > 0.
	// +optional
	// +listType=set
	Warnings []metav1.Duration `json:"warnings,omitempty"`

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

// IsRenewable returns whether the lease may be extended. Default true.
// Prefer ResolvedConfig.Renewable after policy resolution.
func (s EnvironmentLeaseSpec) IsRenewable() bool {
	if s.Renewable == nil {
		return true
	}
	return *s.Renewable
}

// RequestedTTL returns the explicitly requested TTL, or zero if unset.
func (s EnvironmentLeaseSpec) RequestedTTL() time.Duration {
	if s.TTL == nil {
		return 0
	}
	return s.TTL.Duration
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

	// CreatedAt is when the lease window started. Initialized once from
	// metadata.creationTimestamp and never reset on controller restart.
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// ExpiresAt is the hard expiration: CreatedAt + Spec.TTL, <= MaximumExpiresAt.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// MaximumExpiresAt is CreatedAt + effective MaxTTL. Renewals cannot exceed this.
	// +optional
	MaximumExpiresAt *metav1.Time `json:"maximumExpiresAt,omitempty"`

	// LastActivityAt is the last observed heartbeat. Initialized to CreatedAt and
	// updated by explicit activity (kubectl kubelease touch). Lives in status
	// because it is observed runtime state, not desired configuration.
	// +optional
	LastActivityAt *metav1.Time `json:"lastActivityAt,omitempty"`

	// IdleExpiresAt is LastActivityAt + effective idleTTL when idleTTL is set.
	// Capped so it never exceeds ExpiresAt / MaximumExpiresAt.
	// +optional
	IdleExpiresAt *metav1.Time `json:"idleExpiresAt,omitempty"`

	// EffectiveExpiresAt is min(ExpiresAt, IdleExpiresAt) when idle is enabled,
	// otherwise equal to ExpiresAt. Controllers schedule and expire against this.
	// +optional
	EffectiveExpiresAt *metav1.Time `json:"effectiveExpiresAt,omitempty"`

	// ExpirationReason is set when the lease has effectively expired.
	// +optional
	ExpirationReason ExpirationReason `json:"expirationReason,omitempty"`

	// WarningsDelivered lists warning durations (canonical strings like "1h")
	// for which a LeaseExpiring event has already been emitted.
	// +optional
	// +listType=set
	WarningsDelivered []string `json:"warningsDelivered,omitempty"`

	// Effective holds the policy-resolved values used for reconciliation.
	// +optional
	Effective *EffectiveLeaseSpec `json:"effective,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions represent the latest available observations of the lease.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// EffectiveLeaseSpec records the resolved lease parameters after policy application.
type EffectiveLeaseSpec struct {
	// PolicyName is the referenced policy, if any.
	// +optional
	PolicyName string `json:"policyName,omitempty"`

	// TTL is the effective lease TTL.
	TTL metav1.Duration `json:"ttl"`

	// MaxTTL is the effective maximum lifetime.
	MaxTTL metav1.Duration `json:"maxTTL"`

	// IdleTTL is the effective idle TTL when configured.
	// +optional
	IdleTTL *metav1.Duration `json:"idleTTL,omitempty"`

	// Renewable is the effective renewability.
	Renewable bool `json:"renewable"`

	// DefaultDeny is whether default-deny NetworkPolicy is effective.
	DefaultDeny bool `json:"defaultDeny"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=envlease;el
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.status.effectiveExpiresAt`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.status.expirationReason`
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
