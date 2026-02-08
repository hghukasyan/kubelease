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
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

// GlobalFlags holds kubeconfig-related flags shared by all commands.
type GlobalFlags struct {
	Kubeconfig string
	Context    string
	Output     string
}

// NewRootCommand builds the kubectl-kubelease command tree.
func NewRootCommand() *cobra.Command {
	gf := &GlobalFlags{}

	root := &cobra.Command{
		Use:          "kubectl-kubelease",
		Short:        "Manage ephemeral Kubernetes environments (EnvironmentLeases)",
		SilenceUsage: true,
		Long: `kubectl kubelease manages temporary Kubernetes environments with TTLs.

Install the binary on PATH as kubectl-kubelease so kubectl can discover it:

  go install github.com/hghukasyan/kubelease/cmd/kubectl-kubelease@latest
  kubectl kubelease --help

Common commands:
  create   Create a lease
  list     List leases
  get      Show lease details
  extend   Renew TTL (when renewable)
  touch    Heartbeat for idle TTL
  expire   End a lease and clean up
  cluster  Inspect ClusterTargets
`,
	}

	root.PersistentFlags().StringVar(&gf.Kubeconfig, "kubeconfig", os.Getenv("KUBECONFIG"), "Path to kubeconfig file")
	root.PersistentFlags().StringVar(&gf.Context, "context", "", "Kubeconfig context to use")
	root.PersistentFlags().StringVarP(&gf.Output, "output", "o", "", "Output format: json|yaml (default human-readable)")

	root.AddCommand(newCreateCommand(gf))
	root.AddCommand(newListCommand(gf))
	root.AddCommand(newGetCommand(gf))
	root.AddCommand(newClusterCommand(gf))
	root.AddCommand(newExtendCommand(gf))
	root.AddCommand(newTouchCommand(gf))
	root.AddCommand(newExpireCommand(gf))

	return root
}

func newClient(gf *GlobalFlags) (client.Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if gf.Kubeconfig != "" {
		loadingRules.ExplicitPath = gf.Kubeconfig
	}
	overrides := &clientcmd.ConfigOverrides{}
	if gf.Context != "" {
		overrides.CurrentContext = gf.Context
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(platformv1alpha1.AddToScheme(scheme))

	return client.New(cfg, client.Options{Scheme: scheme})
}
