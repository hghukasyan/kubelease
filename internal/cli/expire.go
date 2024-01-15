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

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func newExpireCommand(gf *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "expire NAME",
		Short: "Expire an EnvironmentLease (delete CR; controller cleans up)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := newClient(gf)
			if err != nil {
				return err
			}
			return runExpire(cmd.Context(), c, args[0])
		},
	}
}

func runExpire(ctx context.Context, c client.Client, name string) error {
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	if err := c.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
		return err
	}
	if err := c.Delete(ctx, leaseObj); err != nil {
		return fmt.Errorf("delete EnvironmentLease: %w", err)
	}
	fmt.Printf("EnvironmentLease/%s expiration requested\n", name)
	fmt.Println("Controller will clean up the managed Namespace via the finalizer.")
	return nil
}
