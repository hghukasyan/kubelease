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

package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

type createOptions struct {
	ttl           time.Duration
	maxTTL        time.Duration
	idleTTL       time.Duration
	policy        string
	owner         string
	team          string
	cpuRequest    string
	memoryRequest string
	cpuLimit      string
	memoryLimit   string
	renewable     bool
	defaultDeny   bool
	generateName  string
	namespaceName string
	wait          bool
	timeout       time.Duration
	warnings      []string
	clusterRef    string
	selectors     []string
}

func newCreateCommand(gf *GlobalFlags) *cobra.Command {
	opts := &createOptions{
		ttl:          8 * time.Hour,
		renewable:    true,
		generateName: "preview-",
		wait:         true,
		timeout:      2 * time.Minute,
		defaultDeny:  false,
	}

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an EnvironmentLease",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runCreate(cmd.Context(), c, args[0], opts)
		},
	}

	cmd.Flags().DurationVar(&opts.ttl, "ttl", opts.ttl, "Lease TTL")
	cmd.Flags().DurationVar(&opts.maxTTL, "max-ttl", 0, "Maximum lifetime from creation (defaults to ttl)")
	cmd.Flags().DurationVar(&opts.idleTTL, "idle-ttl", 0, "Idle expiration window (requires heartbeats via touch)")
	cmd.Flags().StringVar(&opts.policy, "policy", "", "EnvironmentLeasePolicy name to reference")
	cmd.Flags().StringVar(&opts.owner, "owner", "", "Owner name")
	cmd.Flags().StringVar(&opts.team, "team", "", "Owner team")
	cmd.Flags().StringVar(&opts.cpuRequest, "cpu-request", "", "CPU request quota (e.g. 2)")
	cmd.Flags().StringVar(&opts.memoryRequest, "memory-request", "", "Memory request quota (e.g. 4Gi)")
	cmd.Flags().StringVar(&opts.cpuLimit, "cpu-limit", "", "CPU limit quota")
	cmd.Flags().StringVar(&opts.memoryLimit, "memory-limit", "", "Memory limit quota")
	cmd.Flags().BoolVar(&opts.renewable, "renewable", true, "Allow lease renewal")
	cmd.Flags().BoolVar(&opts.defaultDeny, "default-deny", false, "Create default-deny NetworkPolicy")
	cmd.Flags().StringVar(&opts.generateName, "generate-name", "preview-", "Namespace generateName prefix")
	cmd.Flags().StringVar(&opts.namespaceName, "namespace-name", "", "Exact namespace name (optional)")
	cmd.Flags().BoolVar(&opts.wait, "wait", true, "Wait for environment to become Active")
	cmd.Flags().DurationVar(&opts.timeout, "timeout", opts.timeout, "Wait timeout")
	cmd.Flags().StringSliceVar(&opts.warnings, "warning", nil, "Warning durations before expiry (e.g. 1h,15m)")
	cmd.Flags().StringVar(&opts.clusterRef, "cluster-ref", "", "Exact ClusterTarget name (mutually exclusive with --selector)")
	cmd.Flags().StringSliceVar(&opts.selectors, "selector", nil, "Placement matchLabels key=value (repeatable; exclusive with --cluster-ref)")

	return cmd
}

// BuildLease constructs an EnvironmentLease from create options (testable).
func BuildLease(name string, opts *createOptions) (*platformv1alpha1.EnvironmentLease, error) {
	if opts.ttl <= 0 && opts.policy == "" {
		return nil, fmt.Errorf("--ttl must be greater than zero (or set --policy with a default)")
	}
	maxTTL := opts.maxTTL
	if maxTTL == 0 && opts.ttl > 0 {
		maxTTL = opts.ttl
	}
	if opts.ttl > 0 && maxTTL > 0 && maxTTL < opts.ttl {
		return nil, fmt.Errorf("--max-ttl must be >= --ttl")
	}

	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Renewable: ptr.To(opts.renewable),
			Owner: platformv1alpha1.OwnerSpec{
				Name: opts.owner,
				Team: opts.team,
			},
			Namespace: platformv1alpha1.NamespaceSpec{
				GenerateName: opts.generateName,
				Name:         opts.namespaceName,
				Labels: map[string]string{
					"environment": "preview",
				},
			},
		},
	}
	if opts.policy != "" {
		leaseObj.Spec.PolicyRef = &platformv1alpha1.LocalObjectReference{Name: opts.policy}
	}
	if opts.ttl > 0 {
		leaseObj.Spec.TTL = &metav1.Duration{Duration: opts.ttl}
	}
	if maxTTL > 0 {
		leaseObj.Spec.MaxTTL = &metav1.Duration{Duration: maxTTL}
	}
	if opts.idleTTL > 0 {
		leaseObj.Spec.IdleTTL = &metav1.Duration{Duration: opts.idleTTL}
	}

	for _, w := range opts.warnings {
		d, err := time.ParseDuration(w)
		if err != nil {
			return nil, fmt.Errorf("invalid --warning %q: %w", w, err)
		}
		leaseObj.Spec.Warnings = append(leaseObj.Spec.Warnings, metav1.Duration{Duration: d})
	}

	if opts.cpuRequest != "" || opts.memoryRequest != "" || opts.cpuLimit != "" || opts.memoryLimit != "" {
		q := &platformv1alpha1.QuotaSpec{
			Requests: corev1.ResourceList{},
			Limits:   corev1.ResourceList{},
		}
		if opts.cpuRequest != "" {
			q.Requests[corev1.ResourceCPU] = resource.MustParse(opts.cpuRequest)
		}
		if opts.memoryRequest != "" {
			q.Requests[corev1.ResourceMemory] = resource.MustParse(opts.memoryRequest)
		}
		if opts.cpuLimit != "" {
			q.Limits[corev1.ResourceCPU] = resource.MustParse(opts.cpuLimit)
		}
		if opts.memoryLimit != "" {
			q.Limits[corev1.ResourceMemory] = resource.MustParse(opts.memoryLimit)
		}
		leaseObj.Spec.Quota = q
	}

	if opts.defaultDeny {
		leaseObj.Spec.NetworkPolicy = &platformv1alpha1.NetworkPolicySpec{DefaultDeny: true}
	}

	if opts.clusterRef != "" && len(opts.selectors) > 0 {
		return nil, fmt.Errorf("--cluster-ref and --selector are mutually exclusive")
	}
	if opts.clusterRef != "" {
		leaseObj.Spec.ClusterRef = &platformv1alpha1.LocalObjectReference{Name: opts.clusterRef}
	}
	if len(opts.selectors) > 0 {
		match := map[string]string{}
		for _, s := range opts.selectors {
			k, v, ok := strings.Cut(s, "=")
			if !ok || k == "" {
				return nil, fmt.Errorf("invalid --selector %q (want key=value)", s)
			}
			match[k] = v
		}
		leaseObj.Spec.Placement = &platformv1alpha1.PlacementSpec{
			Selector: &metav1.LabelSelector{MatchLabels: match},
		}
	}

	return leaseObj, nil
}

func runCreate(ctx context.Context, c client.Client, name string, opts *createOptions) error {
	leaseObj, err := BuildLease(name, opts)
	if err != nil {
		return err
	}
	if err := c.Create(ctx, leaseObj); err != nil {
		return fmt.Errorf("create EnvironmentLease: %w", err)
	}
	fmt.Printf("EnvironmentLease/%s created\n", name)

	if !opts.wait {
		return nil
	}

	fmt.Println("\nWaiting for environment...")
	deadline := time.Now().Add(opts.timeout)
	for time.Now().Before(deadline) {
		current := &platformv1alpha1.EnvironmentLease{}
		if err := c.Get(ctx, types.NamespacedName{Name: name}, current); err != nil {
			return err
		}
		if current.Status.Phase == platformv1alpha1.LeasePhaseActive ||
			current.Status.Phase == platformv1alpha1.LeasePhaseExpiring {
			fmt.Printf("\nNamespace: %s\n", current.Status.Namespace)
			fmt.Printf("Status:    %s\n", current.Status.Phase)
			if current.Status.ExpiresAt != nil {
				fmt.Printf("Expires:   in %s\n", formatDuration(time.Until(current.Status.ExpiresAt.Time)))
			}
			return nil
		}
		if current.Status.Phase == platformv1alpha1.LeasePhaseFailed {
			return fmt.Errorf("lease failed: %s", conditionMessage(current, platformv1alpha1.ConditionReady))
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("timed out waiting for EnvironmentLease/%s to become Active", name)
}

func conditionMessage(leaseObj *platformv1alpha1.EnvironmentLease, condType string) string {
	for _, c := range leaseObj.Status.Conditions {
		if c.Type == condType {
			return c.Message
		}
	}
	return "unknown failure"
}
