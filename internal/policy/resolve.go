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

package policy

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// Resolved is the deterministic result of applying a policy to a lease spec.
type Resolved struct {
	PolicyName string

	TTL       time.Duration
	MaxTTL    time.Duration
	IdleTTL   time.Duration // 0 means unset
	Renewable bool

	Quota         *platformv1alpha1.QuotaSpec
	NetworkPolicy *platformv1alpha1.NetworkPolicySpec
	DefaultDeny   bool
}

// ToEffectiveStatus converts Resolved into API status form.
func (r Resolved) ToEffectiveStatus() *platformv1alpha1.EffectiveLeaseSpec {
	out := &platformv1alpha1.EffectiveLeaseSpec{
		PolicyName:  r.PolicyName,
		TTL:         metav1.Duration{Duration: r.TTL},
		MaxTTL:      metav1.Duration{Duration: r.MaxTTL},
		Renewable:   r.Renewable,
		DefaultDeny: r.DefaultDeny,
	}
	if r.IdleTTL > 0 {
		d := metav1.Duration{Duration: r.IdleTTL}
		out.IdleTTL = &d
	}
	return out
}

// Resolve merges lease spec with an optional policy.
// Lease-set values win over defaults. Hard-limit violations return an error
// (never silently clamped).
//
// policy may be nil when the lease has no policyRef.
func Resolve(spec platformv1alpha1.EnvironmentLeaseSpec, policy *platformv1alpha1.EnvironmentLeasePolicy) (Resolved, error) {
	var polSpec *platformv1alpha1.EnvironmentLeasePolicySpec
	policyName := ""
	if policy != nil {
		polSpec = &policy.Spec
		policyName = policy.Name
		if err := validatePolicySpec(polSpec); err != nil {
			return Resolved{}, err
		}
	}

	ttl, err := resolveDuration("ttl", spec.TTL, durationPolicy(polSpec, "ttl"), true)
	if err != nil {
		return Resolved{}, err
	}

	maxTTLPolicy := durationPolicy(polSpec, "maxTTL")
	// Convenience: when maxTTL policy is absent, ttl.maximum acts as maxTTL ceiling/default.
	if maxTTLPolicy == nil && polSpec != nil && polSpec.TTL != nil && polSpec.TTL.Maximum != nil {
		maxTTLPolicy = &platformv1alpha1.DurationPolicy{
			Default: polSpec.TTL.Maximum,
			Maximum: polSpec.TTL.Maximum,
		}
	}
	maxTTL, err := resolveDuration("maxTTL", spec.MaxTTL, maxTTLPolicy, false)
	if err != nil {
		return Resolved{}, err
	}
	if maxTTL == 0 {
		maxTTL = ttl
	}
	if maxTTL < ttl {
		return Resolved{}, fmt.Errorf("maxTTL (%s) must be >= ttl (%s)", maxTTL, ttl)
	}
	// Also enforce ttl.maximum as a hard ceiling on maxTTL when using the convenience mapping.
	if polSpec != nil && polSpec.TTL != nil && polSpec.TTL.Maximum != nil && maxTTL > polSpec.TTL.Maximum.Duration {
		if spec.MaxTTL != nil {
			return Resolved{}, fmt.Errorf("maxTTL %s exceeds policy ttl.maximum %s", maxTTL, polSpec.TTL.Maximum.Duration)
		}
		// maxTTL came from default path already bounded.
	}

	idleTTL, err := resolveDuration("idleTTL", spec.IdleTTL, durationPolicy(polSpec, "idleTTL"), false)
	if err != nil {
		return Resolved{}, err
	}

	renewable, err := resolveBool(spec.Renewable, boolPolicy(polSpec))
	if err != nil {
		return Resolved{}, err
	}

	quota := spec.Quota
	if err := validateQuota(quota, quotaPolicy(polSpec)); err != nil {
		return Resolved{}, err
	}

	np, defaultDeny, err := resolveNetworkPolicy(spec.NetworkPolicy, networkPolicy(polSpec))
	if err != nil {
		return Resolved{}, err
	}

	return Resolved{
		PolicyName:    policyName,
		TTL:           ttl,
		MaxTTL:        maxTTL,
		IdleTTL:       idleTTL,
		Renewable:     renewable,
		Quota:         quota,
		NetworkPolicy: np,
		DefaultDeny:   defaultDeny,
	}, nil
}

func validatePolicySpec(pol *platformv1alpha1.EnvironmentLeasePolicySpec) error {
	check := func(name string, d *platformv1alpha1.DurationPolicy) error {
		if d == nil {
			return nil
		}
		if d.Default != nil && d.Maximum != nil && d.Default.Duration > d.Maximum.Duration {
			return fmt.Errorf("policy %s.default %s exceeds %s.maximum %s",
				name, d.Default.Duration, name, d.Maximum.Duration)
		}
		if d.Minimum != nil && d.Maximum != nil && d.Minimum.Duration > d.Maximum.Duration {
			return fmt.Errorf("policy %s.minimum %s exceeds %s.maximum %s",
				name, d.Minimum.Duration, name, d.Maximum.Duration)
		}
		return nil
	}
	if err := check("ttl", pol.TTL); err != nil {
		return err
	}
	if err := check("maxTTL", pol.MaxTTL); err != nil {
		return err
	}
	return check("idleTTL", pol.IdleTTL)
}

func durationPolicy(pol *platformv1alpha1.EnvironmentLeasePolicySpec, field string) *platformv1alpha1.DurationPolicy {
	if pol == nil {
		return nil
	}
	switch field {
	case "ttl":
		return pol.TTL
	case "maxTTL":
		return pol.MaxTTL
	case "idleTTL":
		return pol.IdleTTL
	default:
		return nil
	}
}

func boolPolicy(pol *platformv1alpha1.EnvironmentLeasePolicySpec) *platformv1alpha1.BoolPolicy {
	if pol == nil {
		return nil
	}
	return pol.Renewable
}

func quotaPolicy(pol *platformv1alpha1.EnvironmentLeasePolicySpec) *platformv1alpha1.QuotaPolicy {
	if pol == nil {
		return nil
	}
	return pol.Quota
}

func networkPolicy(pol *platformv1alpha1.EnvironmentLeasePolicySpec) *platformv1alpha1.NetworkPolicyPolicy {
	if pol == nil {
		return nil
	}
	return pol.NetworkPolicy
}

func resolveDuration(name string, leaseVal *metav1.Duration, pol *platformv1alpha1.DurationPolicy, required bool) (time.Duration, error) {
	var d time.Duration
	switch {
	case leaseVal != nil:
		d = leaseVal.Duration
	case pol != nil && pol.Default != nil:
		d = pol.Default.Duration
	case required:
		return 0, fmt.Errorf("%s is required (set spec.%s or reference a policy with %s.default)", name, name, name)
	default:
		return 0, nil
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero, got %s", name, d)
	}
	if pol != nil {
		if pol.Minimum != nil && d < pol.Minimum.Duration {
			return 0, fmt.Errorf("%s %s is below policy minimum %s", name, d, pol.Minimum.Duration)
		}
		if pol.Maximum != nil && d > pol.Maximum.Duration {
			return 0, fmt.Errorf("%s %s exceeds policy maximum %s", name, d, pol.Maximum.Duration)
		}
	}
	return d, nil
}

func resolveBool(leaseVal *bool, pol *platformv1alpha1.BoolPolicy) (bool, error) {
	var v bool
	switch {
	case leaseVal != nil:
		v = *leaseVal
	case pol != nil && pol.Default != nil:
		v = *pol.Default
	default:
		v = true
	}
	if pol != nil && pol.Force != nil && v != *pol.Force {
		return false, fmt.Errorf("renewable=%v violates policy force=%v", v, *pol.Force)
	}
	return v, nil
}

func validateQuota(quota *platformv1alpha1.QuotaSpec, pol *platformv1alpha1.QuotaPolicy) error {
	if pol == nil || quota == nil {
		return nil
	}
	check := func(list corev1.ResourceList, kind string) error {
		if list == nil {
			return nil
		}
		if pol.MaxCPU != nil {
			if cpu, ok := list[corev1.ResourceCPU]; ok && cpu.Cmp(*pol.MaxCPU) > 0 {
				return fmt.Errorf("quota.%s.cpu %s exceeds policy maxCPU %s", kind, cpu.String(), pol.MaxCPU.String())
			}
		}
		if pol.MaxMemory != nil {
			if mem, ok := list[corev1.ResourceMemory]; ok && mem.Cmp(*pol.MaxMemory) > 0 {
				return fmt.Errorf("quota.%s.memory %s exceeds policy maxMemory %s", kind, mem.String(), pol.MaxMemory.String())
			}
		}
		return nil
	}
	if err := check(quota.Requests, "requests"); err != nil {
		return err
	}
	return check(quota.Limits, "limits")
}

func resolveNetworkPolicy(
	leaseNP *platformv1alpha1.NetworkPolicySpec,
	pol *platformv1alpha1.NetworkPolicyPolicy,
) (*platformv1alpha1.NetworkPolicySpec, bool, error) {
	defaultDeny := false
	explicitFalse := false

	switch {
	case leaseNP != nil:
		defaultDeny = leaseNP.DefaultDeny
		if !leaseNP.DefaultDeny {
			explicitFalse = true
		}
	case pol != nil && pol.DefaultDenyDefault != nil:
		defaultDeny = *pol.DefaultDenyDefault
	}

	if pol != nil && pol.DefaultDenyRequired {
		if explicitFalse {
			return nil, false, fmt.Errorf("networkPolicy.defaultDeny=false violates policy defaultDenyRequired")
		}
		defaultDeny = true
	}

	if !defaultDeny {
		return leaseNP, false, nil
	}
	return &platformv1alpha1.NetworkPolicySpec{DefaultDeny: true}, true, nil
}
