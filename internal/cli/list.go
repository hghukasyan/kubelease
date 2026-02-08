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
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func newListCommand(gf *GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List EnvironmentLeases",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runList(cmd.Context(), c, gf)
		},
	}
	return cmd
}

func runList(ctx context.Context, c client.Client, gf *GlobalFlags) error {
	list := &platformv1alpha1.EnvironmentLeaseList{}
	if err := c.List(ctx, list); err != nil {
		return fmt.Errorf("list EnvironmentLeases: %w", err)
	}

	if isMachineOutput(gf.Output) {
		return printObject(list, gf.Output)
	}

	if len(list.Items) == 0 {
		fmt.Fprintln(os.Stdout, "No active EnvironmentLeases found.")
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "Create one with:")
		fmt.Fprintln(os.Stdout, "  kubectl kubelease create demo --ttl 30m")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tCLUSTER\tNAMESPACE\tOWNER\tEXPIRES IN\tSTATUS")
	now := time.Now()
	for i := range list.Items {
		item := &list.Items[i]
		owner := item.Spec.Owner.Name
		if owner == "" {
			owner = item.Spec.Owner.Team
		}
		expires := "-"
		if item.Status.ExpiresAt != nil {
			expires = formatDuration(item.Status.ExpiresAt.Time.Sub(now))
		}
		phase := string(item.Status.Phase)
		if phase == "" {
			phase = "Pending"
		}
		cluster := "-"
		if item.Status.Cluster != nil && item.Status.Cluster.Name != "" {
			cluster = item.Status.Cluster.Name
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Name, cluster, item.Status.Namespace, owner, expires, phase)
	}
	return w.Flush()
}
