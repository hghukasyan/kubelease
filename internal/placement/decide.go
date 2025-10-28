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

package placement

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// Decision is the outcome of cluster placement for one lease.
type Decision struct {
	// ClusterName is "local" or a ClusterTarget name.
	ClusterName string
	// Pending means no eligible target matched; retry later (not a permanent failure).
	Pending bool
	// Message explains Pending or selection context.
	Message string
	// Reselected is true when a prior sticky selection was discarded before provisioning.
	Reselected bool
}

// Options tunes Decide.
type Options struct {
	// ActiveLeaseCounts maps ClusterTarget name → soft active lease count.
	// When nil, capacity from ClusterTarget.status.capacity is used.
	ActiveLeaseCounts map[string]int32
}

// StatusClusterNameIndex is the field indexer key for status.cluster.name.
const StatusClusterNameIndex = ".status.cluster.name"

// ValidateExclusive rejects clusterRef + placement together.
func ValidateExclusive(spec platformv1alpha1.EnvironmentLeaseSpec) error {
	hasRef := spec.ClusterRef != nil && spec.ClusterRef.Name != ""
	hasPlacement := spec.Placement != nil
	if hasRef && hasPlacement {
		return fmt.Errorf("clusterRef and placement are mutually exclusive")
	}
	return nil
}

// Decide picks a stable target for the lease.
//
// Precedence:
//  1. clusterRef → exact target (policy-constrained)
//  2. sticky status.cluster when already provisioned (status.namespace set) — never migrate
//  3. sticky status.cluster when not provisioned and target still eligible
//  4. placement / policy default selector → deterministic pick among candidates
//  5. neither → local
//
// Before Namespace exists, an unhealthy/disabled sticky target may be reselected.
// After provisioning begins, selection is sticky (no automatic migration).
func Decide(
	ctx context.Context,
	hub client.Client,
	leaseObj *platformv1alpha1.EnvironmentLease,
	policy *platformv1alpha1.EnvironmentLeasePolicy,
	opts Options,
) (Decision, error) {
	if err := ValidateExclusive(leaseObj.Spec); err != nil {
		return Decision{}, err
	}

	policySel, err := selectorFromPlacement(policyPlacement(policy))
	if err != nil {
		return Decision{}, fmt.Errorf("policy placement selector: %w", err)
	}
	leaseSel, err := selectorFromPlacement(leaseObj.Spec.Placement)
	if err != nil {
		return Decision{}, fmt.Errorf("lease placement selector: %w", err)
	}

	needsPlacement := leaseObj.Spec.Placement != nil ||
		(policy != nil && policy.Spec.Placement != nil && policy.Spec.Placement.Selector != nil)

	// 1. Explicit clusterRef
	if leaseObj.Spec.ClusterRef != nil && leaseObj.Spec.ClusterRef.Name != "" {
		name := leaseObj.Spec.ClusterRef.Name
		target, err := getTarget(ctx, hub, name)
		if err != nil {
			return Decision{}, err
		}
		if err := enforcePolicyOnTarget(target, policySel); err != nil {
			return Decision{}, err
		}
		return Decision{ClusterName: name}, nil
	}

	provisioned := leaseObj.Status.Namespace != ""
	sticky := ""
	if leaseObj.Status.Cluster != nil {
		sticky = leaseObj.Status.Cluster.Name
	}

	// 2–3. Sticky selection
	if sticky != "" && sticky != platformv1alpha1.LocalClusterName {
		target, err := getTarget(ctx, hub, sticky)
		if err == nil && matchesSelectors(target, leaseSel, policySel) &&
			targetHealthyEnabled(target) {
			// Soft capacity is not re-checked for sticky leases (already assigned).
			return Decision{ClusterName: sticky}, nil
		}
		if provisioned {
			// Sticky after provisioning — surface unavailability via client resolve.
			return Decision{ClusterName: sticky}, nil
		}
		// Reselection allowed before Namespace exists.
		if needsPlacement {
			d, err := selectFromCandidates(ctx, hub, leaseObj, leaseSel, policySel, opts)
			if err != nil {
				return Decision{}, err
			}
			d.Reselected = true
			return d, nil
		}
		return Decision{ClusterName: platformv1alpha1.LocalClusterName, Reselected: true}, nil
	}
	if sticky == platformv1alpha1.LocalClusterName && provisioned {
		return Decision{ClusterName: platformv1alpha1.LocalClusterName}, nil
	}

	// 4. Placement / policy-as-default
	if needsPlacement {
		return selectFromCandidates(ctx, hub, leaseObj, leaseSel, policySel, opts)
	}

	// 5. Local default
	return Decision{ClusterName: platformv1alpha1.LocalClusterName}, nil
}

func policyPlacement(policy *platformv1alpha1.EnvironmentLeasePolicy) *platformv1alpha1.PlacementSpec {
	if policy == nil {
		return nil
	}
	return policy.Spec.Placement
}

func selectorFromPlacement(p *platformv1alpha1.PlacementSpec) (labels.Selector, error) {
	if p == nil || p.Selector == nil {
		return nil, nil
	}
	return metav1.LabelSelectorAsSelector(p.Selector)
}

func enforcePolicyOnTarget(target *platformv1alpha1.ClusterTarget, policySel labels.Selector) error {
	if policySel == nil || policySel.Empty() {
		return nil
	}
	if !policySel.Matches(labels.Set(target.SchedulingLabels())) {
		return fmt.Errorf("%s: ClusterTarget %q does not match policy placement selector",
			platformv1alpha1.ReasonPolicyViolation, target.Name)
	}
	return nil
}

func getTarget(ctx context.Context, hub client.Client, name string) (*platformv1alpha1.ClusterTarget, error) {
	target := &platformv1alpha1.ClusterTarget{}
	if err := hub.Get(ctx, types.NamespacedName{Name: name}, target); err != nil {
		return nil, fmt.Errorf("get ClusterTarget %q: %w", name, err)
	}
	return target, nil
}

func matchesSelectors(target *platformv1alpha1.ClusterTarget, leaseSel, policySel labels.Selector) bool {
	ls := labels.Set(target.SchedulingLabels())
	if leaseSel != nil && !leaseSel.Matches(ls) {
		return false
	}
	if policySel != nil && !policySel.Matches(ls) {
		return false
	}
	return true
}

func targetHealthyEnabled(target *platformv1alpha1.ClusterTarget) bool {
	if target == nil || !target.DeletionTimestamp.IsZero() || !target.Spec.IsEnabled() {
		return false
	}
	return meta.IsStatusConditionTrue(target.Status.Conditions, platformv1alpha1.ClusterTargetConditionReady)
}

func underSoftCapacity(target *platformv1alpha1.ClusterTarget, counts map[string]int32) bool {
	if target.Spec.MaxActiveLeases == nil {
		return true
	}
	active := int32(0)
	if counts != nil {
		active = counts[target.Name]
	} else if target.Status.Capacity != nil {
		active = target.Status.Capacity.ActiveLeases
	}
	return active < *target.Spec.MaxActiveLeases
}

func selectFromCandidates(
	ctx context.Context,
	hub client.Client,
	leaseObj *platformv1alpha1.EnvironmentLease,
	leaseSel, policySel labels.Selector,
	opts Options,
) (Decision, error) {
	var list platformv1alpha1.ClusterTargetList
	if err := hub.List(ctx, &list); err != nil {
		return Decision{}, fmt.Errorf("list ClusterTargets: %w", err)
	}
	counts := opts.ActiveLeaseCounts
	if counts == nil {
		counts = map[string]int32{}
		for i := range list.Items {
			if list.Items[i].Status.Capacity != nil {
				counts[list.Items[i].Name] = list.Items[i].Status.Capacity.ActiveLeases
			}
		}
	}

	var candidates []*platformv1alpha1.ClusterTarget
	for i := range list.Items {
		t := &list.Items[i]
		if !targetHealthyEnabled(t) {
			continue
		}
		if !matchesSelectors(t, leaseSel, policySel) {
			continue
		}
		if !underSoftCapacity(t, counts) {
			continue
		}
		candidates = append(candidates, t)
	}
	if len(candidates) == 0 {
		return Decision{
			Pending: true,
			Message: "no matching Ready ClusterTarget for placement selector",
		}, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Name < candidates[j].Name
	})
	return Decision{ClusterName: pickStable(leaseObj, candidates)}, nil
}

// pickStable chooses deterministically via lease UID hash among sorted candidates.
// Tradeoff vs "first sorted name": hashing spreads load across matches while
// remaining stable across reconciles for a given lease identity.
func pickStable(leaseObj *platformv1alpha1.EnvironmentLease, candidates []*platformv1alpha1.ClusterTarget) string {
	key := string(leaseObj.UID)
	if key == "" {
		key = leaseObj.Name
	}
	sum := sha256.Sum256([]byte(key))
	idx := binary.BigEndian.Uint64(sum[:8]) % uint64(len(candidates))
	return candidates[idx].Name
}

// CountActiveLeases returns soft counts of leases per status.cluster.name.
func CountActiveLeases(ctx context.Context, hub client.Client) (map[string]int32, error) {
	var list platformv1alpha1.EnvironmentLeaseList
	if err := hub.List(ctx, &list); err != nil {
		return nil, err
	}
	out := map[string]int32{}
	for i := range list.Items {
		l := &list.Items[i]
		if !l.DeletionTimestamp.IsZero() {
			continue
		}
		if l.Status.Cluster == nil || l.Status.Cluster.Name == "" ||
			l.Status.Cluster.Name == platformv1alpha1.LocalClusterName {
			continue
		}
		if l.Status.Phase == platformv1alpha1.LeasePhaseExpired {
			continue
		}
		out[l.Status.Cluster.Name]++
	}
	return out, nil
}
