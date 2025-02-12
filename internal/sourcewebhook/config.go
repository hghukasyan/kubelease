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

package sourcewebhook

import (
	"time"

	"github.com/go-logr/logr"

	"github.com/hghukasyan/kubelease/internal/lease"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// AnnotationRequestID records the webhook idempotency key on the lease.
	AnnotationRequestID = "kubelease.io/webhook-request-id"
	// AnnotationSource marks leases created by the generic webhook source.
	AnnotationSource = "kubelease.io/source"
	// SourceValue is the AnnotationSource value for this component.
	SourceValue = "webhook"

	defaultMaxBodyBytes   = 64 << 10 // 64 KiB
	defaultReadTimeout    = 10 * time.Second
	defaultWriteTimeout   = 10 * time.Second
	defaultIdleTimeout    = 60 * time.Second
	defaultRequestTimeout = 10 * time.Second
)

// Config configures the generic lease webhook HTTP server.
type Config struct {
	// BindAddress is the listen address (e.g. ":8082").
	BindAddress string

	// TokenSecretNamespace/Name/Key identify the Secret that holds the shared token.
	TokenSecretNamespace string
	TokenSecretName      string
	TokenSecretKey       string

	// DefaultPolicy is the EnvironmentLeasePolicy name applied to create requests.
	// Callers cannot override policy hard limits; TTL/quota/network come from policy.
	DefaultPolicy string

	// NamespaceGenerateName is the Namespace generateName prefix for created leases.
	NamespaceGenerateName string

	// GitHubEnabled turns on POST /v1/github/hooks.
	GitHubEnabled bool
	// GitHubSecretName/Key identify the Secret holding the GitHub webhook HMAC secret.
	// Never stored on EnvironmentLease specs.
	GitHubSecretName string
	GitHubSecretKey  string
	// GitHubRepoPolicies maps "owner/repo" (lowercase) to EnvironmentLeasePolicy names.
	GitHubRepoPolicies map[string]string
	// GitHubMaxBodyBytes overrides MaxBodyBytes for GitHub payloads when > 0.
	GitHubMaxBodyBytes int64

	MaxBodyBytes   int64
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	RequestTimeout time.Duration
}

// Defaults fills zero-valued Config fields.
func (c *Config) Defaults() {
	if c.BindAddress == "" {
		c.BindAddress = ":8082"
	}
	if c.TokenSecretKey == "" {
		c.TokenSecretKey = "token"
	}
	if c.NamespaceGenerateName == "" {
		c.NamespaceGenerateName = "preview-"
	}
	if c.GitHubSecretKey == "" {
		c.GitHubSecretKey = "github-webhook-secret"
	}
	if c.GitHubSecretName == "" {
		if c.TokenSecretName != "" {
			c.GitHubSecretName = c.TokenSecretName
		} else {
			c.GitHubSecretName = "webhook-token"
		}
	}
	if c.GitHubMaxBodyBytes <= 0 {
		c.GitHubMaxBodyBytes = 1 << 20 // 1 MiB
	}
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = defaultMaxBodyBytes
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = defaultReadTimeout
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = defaultWriteTimeout
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = defaultRequestTimeout
	}
}

// Server is the HTTP webhook integration component (outside the reconciler).
type Server struct {
	Client client.Client
	Config Config
	Log    logr.Logger
	Clock  lease.Clock
	// TokenProvider supplies the expected bearer token (tests inject static tokens).
	TokenProvider TokenProvider
	// GitHubSecretProvider supplies the GitHub webhook HMAC secret.
	GitHubSecretProvider TokenProvider
}

func (s *Server) clock() lease.Clock {
	if s.Clock != nil {
		return s.Clock
	}
	return lease.RealClock{}
}
