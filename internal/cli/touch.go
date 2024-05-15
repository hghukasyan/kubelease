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

func newTouchCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "touch NAME",
		Short: "Record activity on an EnvironmentLease (extends idle lifetime)",
		Long: `Update status.lastActivityAt to now, extending idle expiration when idleTTL is set.

Touch never extends the hard TTL (status.expiresAt / maximumExpiresAt).
Effective expiration remains min(hardExpiration, lastActivityAt+idleTTL).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runTouch(cmd.Context(), c, args[0], time.Now().UTC())
		},
	}
}

func runTouch(ctx context.Context, c client.Client, name string, now time.Time) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		leaseObj := &platformv1alpha1.EnvironmentLease{}
		if err := c.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
			return err
		}

		idleTTL := idleTTLFromLease(leaseObj)
		beforeHard := copyMetaTime(leaseObj.Status.ExpiresAt)
		beforeIdle := copyMetaTime(leaseObj.Status.IdleExpiresAt)

		if err := lease.RecordActivity(leaseObj, now, idleTTL); err != nil {
			return err
		}

		if err := c.Status().Update(ctx, leaseObj); err != nil {
			return err
		}

		fmt.Printf("EnvironmentLease/%s activity recorded\n", name)
		if leaseObj.Status.LastActivityAt != nil {
			fmt.Printf("Last activity:      %s\n", leaseObj.Status.LastActivityAt.UTC().Format(time.RFC3339))
		}
		if leaseObj.Status.IdleExpiresAt != nil {
			fmt.Printf("Idle expires:       %s", leaseObj.Status.IdleExpiresAt.UTC().Format(time.RFC3339))
			if beforeIdle != nil && leaseObj.Status.IdleExpiresAt.After(beforeIdle.Time) {
				fmt.Printf(" (extended)")
			}
			fmt.Println()
		}
		if leaseObj.Status.EffectiveExpiresAt != nil {
			fmt.Printf("Effective expires:  %s\n", leaseObj.Status.EffectiveExpiresAt.UTC().Format(time.RFC3339))
		}
		if beforeHard != nil && leaseObj.Status.ExpiresAt != nil && !leaseObj.Status.ExpiresAt.Equal(beforeHard) {
			return fmt.Errorf("internal error: touch must not change hard expiresAt")
		}
		if leaseObj.Status.ExpiresAt != nil {
			fmt.Printf("Hard expires:       %s (unchanged)\n", leaseObj.Status.ExpiresAt.UTC().Format(time.RFC3339))
		}
		return nil
	})
}

func idleTTLFromLease(leaseObj *platformv1alpha1.EnvironmentLease) time.Duration {
	if leaseObj.Status.Effective != nil && leaseObj.Status.Effective.IdleTTL != nil {
		return leaseObj.Status.Effective.IdleTTL.Duration
	}
	if leaseObj.Spec.IdleTTL != nil {
		return leaseObj.Spec.IdleTTL.Duration
	}
	return 0
}

func copyMetaTime(t *metav1.Time) *metav1.Time {
	if t == nil {
		return nil
	}
	x := *t
	return &x
}
