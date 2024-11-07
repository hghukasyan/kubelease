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
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TokenProvider returns the expected shared secret token.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
}

// SecretTokenProvider loads a token from a Kubernetes Secret.
type SecretTokenProvider struct {
	Client    client.Client
	Namespace string
	Name      string
	Key       string

	mu    sync.RWMutex
	cache string
}

// Token fetches (and caches) the Secret token.
func (p *SecretTokenProvider) Token(ctx context.Context) (string, error) {
	p.mu.RLock()
	if p.cache != "" {
		tok := p.cache
		p.mu.RUnlock()
		return tok, nil
	}
	p.mu.RUnlock()

	secret := &corev1.Secret{}
	if err := p.Client.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: p.Name}, secret); err != nil {
		return "", fmt.Errorf("get token secret %s/%s: %w", p.Namespace, p.Name, err)
	}
	raw, ok := secret.Data[p.Key]
	if !ok || len(raw) == 0 {
		return "", fmt.Errorf("token secret %s/%s missing key %q", p.Namespace, p.Name, p.Key)
	}
	tok := string(raw)
	p.mu.Lock()
	p.cache = tok
	p.mu.Unlock()
	return tok, nil
}

// Invalidate clears the cached token (forces reload).
func (p *SecretTokenProvider) Invalidate() {
	p.mu.Lock()
	p.cache = ""
	p.mu.Unlock()
}

// StaticTokenProvider is a fixed token for tests.
type StaticTokenProvider struct {
	Value string
}

func (p StaticTokenProvider) Token(context.Context) (string, error) {
	return p.Value, nil
}

// extractBearerToken reads Authorization: Bearer or X-KubeLease-Token.
func extractBearerToken(authorization, headerToken string) string {
	if headerToken != "" {
		return strings.TrimSpace(headerToken)
	}
	const prefix = "Bearer "
	if strings.HasPrefix(authorization, prefix) {
		return strings.TrimSpace(authorization[len(prefix):])
	}
	return ""
}

func authorize(ctx context.Context, provider TokenProvider, authorization, headerToken string) error {
	if provider == nil {
		return fmt.Errorf("token provider not configured")
	}
	provided := extractBearerToken(authorization, headerToken)
	if provided == "" {
		return fmt.Errorf("missing authentication token")
	}
	expected, err := provider.Token(ctx)
	if err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return fmt.Errorf("invalid authentication token")
	}
	return nil
}
