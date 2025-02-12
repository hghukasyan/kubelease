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

package main

import (
	"encoding/json"
	"flag"
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/sourcewebhook"
)

func main() {
	cfg := sourcewebhook.Config{}
	var repoPoliciesJSON string
	var enableGitHub bool

	flag.StringVar(&cfg.BindAddress, "bind-address", ":8082", "Address the webhook HTTP server binds to")
	flag.StringVar(&cfg.TokenSecretNamespace, "token-secret-namespace", "",
		"Namespace of the shared-token Secret (defaults to POD_NAMESPACE)")
	flag.StringVar(&cfg.TokenSecretName, "token-secret-name", "webhook-token", "Name of the shared-token Secret")
	flag.StringVar(&cfg.TokenSecretKey, "token-secret-key", "token", "Key within the Secret that holds the token")
	flag.StringVar(&cfg.DefaultPolicy, "default-policy", "",
		"EnvironmentLeasePolicy applied to create requests (required)")
	flag.StringVar(&cfg.NamespaceGenerateName, "namespace-generate-name", "preview-",
		"Namespace generateName prefix for created leases")
	flag.Int64Var(&cfg.MaxBodyBytes, "max-body-bytes", 64<<10, "Maximum request body size in bytes")
	flag.BoolVar(&enableGitHub, "github-enabled", false, "Enable GitHub pull_request webhook endpoint")
	flag.StringVar(&cfg.GitHubSecretName, "github-secret-name", "",
		"Secret name holding GitHub webhook HMAC secret (defaults to token-secret-name)")
	flag.StringVar(&cfg.GitHubSecretKey, "github-secret-key", "github-webhook-secret",
		"Key within the Secret for the GitHub webhook HMAC secret")
	flag.StringVar(&repoPoliciesJSON, "github-repo-policies", "",
		`JSON object mapping "owner/repo" to EnvironmentLeasePolicy names`)

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("kubelease-webhook")

	cfg.GitHubEnabled = enableGitHub
	cfg.Defaults()
	if cfg.TokenSecretNamespace == "" {
		cfg.TokenSecretNamespace = os.Getenv("POD_NAMESPACE")
	}
	if cfg.TokenSecretNamespace == "" {
		log.Error(nil, "token-secret-namespace or POD_NAMESPACE is required")
		os.Exit(1)
	}
	if cfg.DefaultPolicy == "" {
		log.Error(nil, "default-policy is required")
		os.Exit(1)
	}
	if repoPoliciesJSON != "" {
		var m map[string]string
		if err := json.Unmarshal([]byte(repoPoliciesJSON), &m); err != nil {
			log.Error(err, "invalid --github-repo-policies JSON")
			os.Exit(1)
		}
		cfg.GitHubRepoPolicies = make(map[string]string, len(m))
		for k, v := range m {
			cfg.GitHubRepoPolicies[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(platformv1alpha1.AddToScheme(scheme))

	restCfg, err := ctrl.GetConfig()
	if err != nil {
		log.Error(err, "unable to get kubeconfig")
		os.Exit(1)
	}
	c, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "unable to create kubernetes client")
		os.Exit(1)
	}

	tokenProvider := &sourcewebhook.SecretTokenProvider{
		Client:    c,
		Namespace: cfg.TokenSecretNamespace,
		Name:      cfg.TokenSecretName,
		Key:       cfg.TokenSecretKey,
	}
	srv := &sourcewebhook.Server{
		Client:        c,
		Config:        cfg,
		Log:           log,
		TokenProvider: tokenProvider,
	}
	if cfg.GitHubEnabled {
		srv.GitHubSecretProvider = &sourcewebhook.SecretTokenProvider{
			Client:    c,
			Namespace: cfg.TokenSecretNamespace,
			Name:      cfg.GitHubSecretName,
			Key:       cfg.GitHubSecretKey,
		}
	}

	ctx := ctrl.SetupSignalHandler()
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Error(err, "webhook server exited")
		os.Exit(1)
	}
}
