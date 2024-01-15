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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
)

func newExtendCommand(gf *GlobalFlags) *cobra.Command {
	var forDuration time.Duration

	cmd := &cobra.Command{
		Use:   "extend NAME",
		Short: "Extend an EnvironmentLease by increasing Spec.TTL",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runExtend(cmd.Context(), c, args[0], forDuration)
		},
	}
	cmd.Flags().DurationVar(&forDuration, "for", 0, "Duration to extend the current expiration by (required)")
	_ = cmd.MarkFlagRequired("for")
	return cmd
}

func runExtend(ctx context.Context, c client.Client, name string, forDuration time.Duration) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		leaseObj := &platformv1alpha1.EnvironmentLease{}
		if err := c.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
			return err
		}
		if leaseObj.Status.CreatedAt == nil || leaseObj.Status.ExpiresAt == nil || leaseObj.Status.MaximumExpiresAt == nil {
			return fmt.Errorf("lease %s has not been initialized by the controller yet", name)
		}

		newTTL, newExpires, err := lease.ComputeExtendedTTL(
			leaseObj.Status.CreatedAt.Time,
			leaseObj.Status.ExpiresAt.Time,
			leaseObj.Status.MaximumExpiresAt.Time,
			forDuration,
			leaseObj.Spec.IsRenewable(),
		)
		if err != nil {
			return err
		}

		patched := leaseObj.DeepCopy()
		patched.Spec.TTL = metav1.Duration{Duration: newTTL}
		if err := c.Patch(ctx, patched, client.MergeFrom(leaseObj)); err != nil {
			return err
		}
		fmt.Printf("EnvironmentLease/%s extended\n", name)
		fmt.Printf("New expiration: %s (in %s)\n",
			newExpires.UTC().Format(time.RFC3339),
			formatDuration(time.Until(newExpires)))
		return nil
	})
}
