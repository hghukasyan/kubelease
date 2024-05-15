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
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func newGetCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "get NAME",
		Short: "Get EnvironmentLease details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runGet(cmd.Context(), c, gf, args[0])
		},
	}
}

func runGet(ctx context.Context, c client.Client, gf *GlobalFlags, name string) error {
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
		return err
	}

	if isMachineOutput(gf.Output) {
		return printObject(leaseObj, gf.Output)
	}

	now := time.Now()
	fmt.Printf("Name:               %s\n", leaseObj.Name)
	fmt.Printf("Namespace:          %s\n", leaseObj.Status.Namespace)
	fmt.Printf("Owner:              %s\n", leaseObj.Spec.Owner.Name)
	fmt.Printf("Team:               %s\n", leaseObj.Spec.Owner.Team)
	fmt.Printf("Status:             %s\n", leaseObj.Status.Phase)
	if leaseObj.Status.CreatedAt != nil {
		fmt.Printf("Created:            %s ago\n", formatDuration(now.Sub(leaseObj.Status.CreatedAt.Time)))
	}
	if leaseObj.Status.ExpiresAt != nil {
		fmt.Printf("Hard expires:       in %s\n", formatDuration(leaseObj.Status.ExpiresAt.Time.Sub(now)))
	}
	if leaseObj.Status.IdleExpiresAt != nil {
		fmt.Printf("Idle expires:       in %s\n", formatDuration(leaseObj.Status.IdleExpiresAt.Time.Sub(now)))
	}
	if leaseObj.Status.EffectiveExpiresAt != nil {
		fmt.Printf("Effective expires:  in %s\n", formatDuration(leaseObj.Status.EffectiveExpiresAt.Time.Sub(now)))
	}
	if leaseObj.Status.LastActivityAt != nil {
		fmt.Printf("Last activity:      %s ago\n", formatDuration(now.Sub(leaseObj.Status.LastActivityAt.Time)))
	}
	if leaseObj.Status.ExpirationReason != "" {
		fmt.Printf("Expiration reason:  %s\n", leaseObj.Status.ExpirationReason)
	}
	if leaseObj.Status.MaximumExpiresAt != nil {
		fmt.Printf("Maximum expiration: in %s\n", formatDuration(leaseObj.Status.MaximumExpiresAt.Time.Sub(now)))
	}
	renewable := "yes"
	if !leaseObj.Spec.IsRenewable() {
		renewable = "no"
	}
	fmt.Printf("Renewable:          %s\n", renewable)
	return nil
}
