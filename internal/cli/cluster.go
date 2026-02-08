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
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func newClusterCommand(gf *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cluster",
		Short: "Inspect ClusterTarget placement targets",
	}
	cmd.AddCommand(newClusterListCommand(gf))
	cmd.AddCommand(newClusterGetCommand(gf))
	cmd.AddCommand(newClusterDisableCommand(gf))
	cmd.AddCommand(newClusterDrainCommand(gf))
	return cmd
}

func newClusterListCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List ClusterTargets",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runClusterList(cmd.Context(), c, gf)
		},
	}
}

func newClusterGetCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Get ClusterTarget details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runClusterGet(cmd.Context(), c, gf, args[0])
		},
	}
}

func runClusterList(ctx context.Context, c client.Client, gf *GlobalFlags) error {
	var list platformv1alpha1.ClusterTargetList
	if err := c.List(ctx, &list); err != nil {
		return err
	}
	if isMachineOutput(gf.Output) {
		return printObject(&list, gf.Output)
	}

	if len(list.Items) == 0 {
		fmt.Fprintln(os.Stdout, "No ClusterTargets found.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Register a remote cluster, or omit placement to use the local cluster.")
		fmt.Fprintln(os.Stdout, "See docs/multicluster.md")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tREADY\tENABLED\tREGION\tACTIVE LEASES")
	for i := range list.Items {
		t := &list.Items[i]
		ready := "False"
		if meta.IsStatusConditionTrue(t.Status.Conditions, platformv1alpha1.ClusterTargetConditionReady) {
			ready = "True"
		}
		region := t.SchedulingLabels()["kubelease.io/region"]
		if region == "" {
			region = t.SchedulingLabels()["region"]
		}
		active := int32(0)
		if t.Status.Capacity != nil {
			active = t.Status.Capacity.ActiveLeases
		}
		fmt.Fprintf(w, "%s\t%s\t%t\t%s\t%d\n", t.Name, ready, t.Spec.IsEnabled(), region, active)
	}
	return w.Flush()
}

func runClusterGet(ctx context.Context, c client.Client, gf *GlobalFlags, name string) error {
	target := &platformv1alpha1.ClusterTarget{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, target); err != nil {
		return err
	}
	if isMachineOutput(gf.Output) {
		return printObject(target, gf.Output)
	}

	ready := "False"
	if meta.IsStatusConditionTrue(target.Status.Conditions, platformv1alpha1.ClusterTargetConditionReady) {
		ready = "True"
	}
	fmt.Printf("Name:               %s\n", target.Name)
	fmt.Printf("Ready:              %s\n", ready)
	fmt.Printf("Enabled:            %t\n", target.Spec.IsEnabled())
	fmt.Printf("Kubernetes version: %s\n", target.Status.KubernetesVersion)
	if target.Status.Capacity != nil {
		fmt.Printf("Active leases:      %d\n", target.Status.Capacity.ActiveLeases)
		if target.Status.Capacity.MaxLeases != nil {
			fmt.Printf("Max leases:         %d (soft)\n", *target.Status.Capacity.MaxLeases)
		}
	}
	labels := target.SchedulingLabels()
	if len(labels) > 0 {
		fmt.Println("Scheduling labels:")
		for k, v := range labels {
			fmt.Printf("  %s=%s\n", k, v)
		}
	}
	return nil
}

func newClusterDisableCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "disable NAME",
		Short: "Disable a ClusterTarget (blocks new placement)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runClusterSetEnabled(cmd.Context(), c, args[0], false)
		},
	}
}

func newClusterDrainCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "drain NAME",
		Short: "Disable ClusterTarget and list remaining active leases (no auto-delete)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			if err := runClusterSetEnabled(cmd.Context(), c, args[0], false); err != nil {
				return err
			}
			var list platformv1alpha1.EnvironmentLeaseList
			if err := c.List(cmd.Context(), &list); err != nil {
				return err
			}
			active := 0
			fmt.Printf("ClusterTarget %q disabled for new placement.\nActive leases (not deleted):\n", args[0])
			for i := range list.Items {
				l := &list.Items[i]
				if l.Status.Cluster == nil || l.Status.Cluster.Name != args[0] {
					continue
				}
				if l.Status.Phase == platformv1alpha1.LeasePhaseExpired {
					continue
				}
				active++
				fmt.Printf("  - %s  phase=%s  namespace=%s\n", l.Name, l.Status.Phase, l.Status.Namespace)
			}
			fmt.Printf("%d active lease(s) remain until normal expiry.\n", active)
			return nil
		},
	}
}

func runClusterSetEnabled(ctx context.Context, c client.Client, name string, enabled bool) error {
	target := &platformv1alpha1.ClusterTarget{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, target); err != nil {
		return err
	}
	target.Spec.Enabled = &enabled
	return c.Update(ctx, target)
}
