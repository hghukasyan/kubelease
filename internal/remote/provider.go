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

package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/metrics"
)

const (
	defaultSecretKey = "kubeconfig"
	defaultMaxCache  = 32
)

// ErrTargetDisabled is returned when ClusterTarget.spec.enabled is false.
var ErrTargetDisabled = fmt.Errorf("cluster target is disabled")

// Provider builds controller-runtime clients for ClusterTargets.
//
// Flow: ClusterTarget → Secret credentials → rest.Config → client.Client
type Provider interface {
	ClientFor(ctx context.Context, target *platformv1alpha1.ClusterTarget) (client.Client, error)
	RESTConfigFor(ctx context.Context, target *platformv1alpha1.ClusterTarget) (*rest.Config, error)
	Invalidate(targetName string)
}

// Options configures the Provider.
type Options struct {
	// Hub is used to read credential Secrets on the control cluster.
	Hub client.Client
	// Scheme is applied to remote clients.
	Scheme *runtime.Scheme
	// MaxCacheEntries bounds cached remote clients (LRU eviction). Default 32.
	MaxCacheEntries int
	// NewClient allows tests to inject client construction.
	NewClient func(config *rest.Config, options client.Options) (client.Client, error)
}

type cacheEntry struct {
	fingerprint string
	cl          client.Client
	cfg         *rest.Config
	lastUsed    time.Time
}

type provider struct {
	hub       client.Client
	scheme    *runtime.Scheme
	maxCache  int
	newClient func(config *rest.Config, options client.Options) (client.Client, error)

	mu    sync.Mutex
	cache map[string]*cacheEntry // keyed by ClusterTarget name
}

// NewProvider returns a bounded caching ClusterClientProvider.
func NewProvider(opts Options) Provider {
	max := opts.MaxCacheEntries
	if max <= 0 {
		max = defaultMaxCache
	}
	nc := opts.NewClient
	if nc == nil {
		nc = client.New
	}
	return &provider{
		hub:       opts.Hub,
		scheme:    opts.Scheme,
		maxCache:  max,
		newClient: nc,
		cache:     make(map[string]*cacheEntry),
	}
}

func (p *provider) Invalidate(targetName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cache, targetName)
}

func (p *provider) ClientFor(ctx context.Context, target *platformv1alpha1.ClusterTarget) (client.Client, error) {
	cfg, cl, err := p.configAndClient(ctx, target)
	_ = cfg
	return cl, err
}

func (p *provider) RESTConfigFor(ctx context.Context, target *platformv1alpha1.ClusterTarget) (*rest.Config, error) {
	cfg, _, err := p.configAndClient(ctx, target)
	return cfg, err
}

func (p *provider) configAndClient(ctx context.Context, target *platformv1alpha1.ClusterTarget) (*rest.Config, client.Client, error) {
	if target == nil {
		return nil, nil, fmt.Errorf("cluster target is nil")
	}
	if !target.Spec.IsEnabled() {
		return nil, nil, ErrTargetDisabled
	}

	secret, data, err := p.loadKubeconfig(ctx, target)
	if err != nil {
		metrics.ClusterConnectionFailuresTotal.Inc()
		return nil, nil, err
	}
	fp := fingerprint(target, secret, data)

	p.mu.Lock()
	if ent, ok := p.cache[target.Name]; ok && ent.fingerprint == fp {
		ent.lastUsed = time.Now()
		cfg, cl := ent.cfg, ent.cl
		p.mu.Unlock()
		return cfg, cl, nil
	}
	p.mu.Unlock()

	cfg, err := RESTConfigFromKubeconfig(data)
	if err != nil {
		metrics.ClusterConnectionFailuresTotal.Inc()
		return nil, nil, fmt.Errorf("parse kubeconfig for target %s: %w", target.Name, err)
	}

	cl, err := p.newClient(cfg, client.Options{Scheme: p.scheme})
	if err != nil {
		metrics.ClusterConnectionFailuresTotal.Inc()
		return nil, nil, fmt.Errorf("build remote client for target %s: %w", target.Name, err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.evictIfNeededLocked()
	p.cache[target.Name] = &cacheEntry{
		fingerprint: fp,
		cl:          cl,
		cfg:         cfg,
		lastUsed:    time.Now(),
	}
	return cfg, cl, nil
}

func (p *provider) evictIfNeededLocked() {
	for len(p.cache) >= p.maxCache {
		var oldestName string
		var oldestTime time.Time
		first := true
		for name, ent := range p.cache {
			if first || ent.lastUsed.Before(oldestTime) {
				oldestName = name
				oldestTime = ent.lastUsed
				first = false
			}
		}
		if oldestName == "" {
			return
		}
		delete(p.cache, oldestName)
	}
}

func (p *provider) loadKubeconfig(ctx context.Context, target *platformv1alpha1.ClusterTarget) (*corev1.Secret, []byte, error) {
	ref := target.Spec.Credentials.SecretRef
	if ref.Name == "" || ref.Namespace == "" {
		return nil, nil, fmt.Errorf("credentials.secretRef.name and namespace are required")
	}
	key := ref.Key
	if key == "" {
		key = defaultSecretKey
	}

	secret := &corev1.Secret{}
	if err := p.hub.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil, fmt.Errorf("credential secret %s/%s not found: %w", ref.Namespace, ref.Name, err)
		}
		return nil, nil, fmt.Errorf("get credential secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	data, ok := secret.Data[key]
	if !ok || len(data) == 0 {
		return secret, nil, fmt.Errorf("credential secret %s/%s missing key %q", ref.Namespace, ref.Name, key)
	}
	return secret, data, nil
}

// RESTConfigFromKubeconfig builds a rest.Config from kubeconfig bytes.
func RESTConfigFromKubeconfig(data []byte) (*rest.Config, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, err
	}
	// Bound remote client behaviour.
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.QPS == 0 {
		cfg.QPS = 20
	}
	if cfg.Burst == 0 {
		cfg.Burst = 40
	}
	return cfg, nil
}

func fingerprint(target *platformv1alpha1.ClusterTarget, secret *corev1.Secret, data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s|gen=%d|enabled=%t|secretRV=%s|sha=%s",
		target.Name,
		target.Generation,
		target.Spec.IsEnabled(),
		secret.ResourceVersion,
		hex.EncodeToString(sum[:8]),
	)
}

// SecretKey returns the kubeconfig key, defaulting to "kubeconfig".
func SecretKey(ref platformv1alpha1.SecretKeySelector) string {
	if ref.Key == "" {
		return defaultSecretKey
	}
	return ref.Key
}
