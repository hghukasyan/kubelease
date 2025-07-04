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
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := platformv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRESTConfigFromKubeconfig_Invalid(t *testing.T) {
	_, err := RESTConfigFromKubeconfig([]byte("not-a-kubeconfig"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProvider_CacheInvalidationOnSecretChange(t *testing.T) {
	scheme := testScheme(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kube", Namespace: "kubelease", ResourceVersion: "1"},
		Data:       map[string][]byte{"kubeconfig": validMinimalKubeconfig()},
	}
	target := &platformv1alpha1.ClusterTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "dev-east", Generation: 1},
		Spec: platformv1alpha1.ClusterTargetSpec{
			Credentials: platformv1alpha1.ClusterCredentials{
				SecretRef: platformv1alpha1.SecretKeySelector{
					Name: "kube", Namespace: "kubelease", Key: "kubeconfig",
				},
			},
		},
	}
	hub := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	builds := 0
	p := NewProvider(Options{
		Hub:    hub,
		Scheme: scheme,
		NewClient: func(config *rest.Config, options client.Options) (client.Client, error) {
			builds++
			return fake.NewClientBuilder().WithScheme(scheme).Build(), nil
		},
	})

	ctx := t.Context()
	if _, err := p.ClientFor(ctx, target); err != nil {
		t.Fatalf("first ClientFor: %v", err)
	}
	if _, err := p.ClientFor(ctx, target); err != nil {
		t.Fatalf("second ClientFor: %v", err)
	}
	if builds != 1 {
		t.Fatalf("expected 1 build from cache, got %d", builds)
	}

	// Rotate secret content → fingerprint changes → rebuild.
	current := &corev1.Secret{}
	if err := hub.Get(ctx, client.ObjectKey{Namespace: "kubelease", Name: "kube"}, current); err != nil {
		t.Fatal(err)
	}
	current.Data["kubeconfig"] = append(append([]byte{}, validMinimalKubeconfig()...), []byte("\n# rotated")...)
	if err := hub.Update(ctx, current); err != nil {
		t.Fatal(err)
	}

	if _, err := p.ClientFor(ctx, target); err != nil {
		t.Fatalf("ClientFor after rotation: %v", err)
	}
	if builds != 2 {
		t.Fatalf("expected rebuild after secret change, builds=%d", builds)
	}

	p.Invalidate(target.Name)
	if _, err := p.ClientFor(ctx, target); err != nil {
		t.Fatal(err)
	}
	if builds != 3 {
		t.Fatalf("expected rebuild after Invalidate, builds=%d", builds)
	}
}

func TestProvider_DisabledTarget(t *testing.T) {
	scheme := testScheme(t)
	enabled := false
	target := &platformv1alpha1.ClusterTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "off"},
		Spec: platformv1alpha1.ClusterTargetSpec{
			Enabled: &enabled,
			Credentials: platformv1alpha1.ClusterCredentials{
				SecretRef: platformv1alpha1.SecretKeySelector{Name: "x", Namespace: "ns"},
			},
		},
	}
	p := NewProvider(Options{Hub: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme})
	_, err := p.ClientFor(t.Context(), target)
	if !errors.Is(err, ErrTargetDisabled) {
		t.Fatalf("expected ErrTargetDisabled, got %v", err)
	}
}

func TestProvider_BoundedCacheEviction(t *testing.T) {
	scheme := testScheme(t)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "kube", Namespace: "ns", ResourceVersion: "1"},
		Data:       map[string][]byte{"kubeconfig": validMinimalKubeconfig()},
	}
	hub := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	builds := 0
	p := NewProvider(Options{
		Hub:             hub,
		Scheme:          scheme,
		MaxCacheEntries: 2,
		NewClient: func(config *rest.Config, options client.Options) (client.Client, error) {
			builds++
			return fake.NewClientBuilder().WithScheme(scheme).Build(), nil
		},
	})

	for _, name := range []string{"a", "b", "c"} {
		target := &platformv1alpha1.ClusterTarget{
			ObjectMeta: metav1.ObjectMeta{Name: name, Generation: 1},
			Spec: platformv1alpha1.ClusterTargetSpec{
				Credentials: platformv1alpha1.ClusterCredentials{
					SecretRef: platformv1alpha1.SecretKeySelector{Name: "kube", Namespace: "ns"},
				},
			},
		}
		if _, err := p.ClientFor(t.Context(), target); err != nil {
			t.Fatal(err)
		}
	}
	if builds != 3 {
		t.Fatalf("builds=%d", builds)
	}
	// Re-fetch "a" should rebuild if evicted.
	targetA := &platformv1alpha1.ClusterTarget{
		ObjectMeta: metav1.ObjectMeta{Name: "a", Generation: 1},
		Spec: platformv1alpha1.ClusterTargetSpec{
			Credentials: platformv1alpha1.ClusterCredentials{
				SecretRef: platformv1alpha1.SecretKeySelector{Name: "kube", Namespace: "ns"},
			},
		},
	}
	if _, err := p.ClientFor(t.Context(), targetA); err != nil {
		t.Fatal(err)
	}
	if builds < 4 {
		t.Fatalf("expected eviction rebuild, builds=%d", builds)
	}
}

func TestResolveTarget_LocalFallback(t *testing.T) {
	scheme := testScheme(t)
	hub := fake.NewClientBuilder().WithScheme(scheme).Build()
	local := fake.NewClientBuilder().WithScheme(scheme).Build()
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
	}
	sess, err := ResolveTarget(t.Context(), hub, local, nil, leaseObj)
	if err != nil {
		t.Fatal(err)
	}
	if !sess.Local || sess.Name != platformv1alpha1.LocalClusterName || !sess.SetLeaseOwner {
		t.Fatalf("unexpected session: %+v", sess)
	}
}

func TestResolveTarget_MissingTarget(t *testing.T) {
	scheme := testScheme(t)
	hub := fake.NewClientBuilder().WithScheme(scheme).Build()
	local := fake.NewClientBuilder().WithScheme(scheme).Build()
	p := NewProvider(Options{Hub: hub, Scheme: scheme})
	leaseObj := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			ClusterRef: &platformv1alpha1.LocalObjectReference{Name: "missing"},
		},
	}
	_, err := ResolveTarget(t.Context(), hub, local, p, leaseObj)
	te, ok := AsTargetError(err)
	if !ok || te.Reason != platformv1alpha1.ReasonTargetNotFound {
		t.Fatalf("expected TargetNotFound, got %v", err)
	}
}

// validMinimalKubeconfig is enough for clientcmd parsing (server URL only).
func validMinimalKubeconfig() []byte {
	return []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
    insecure-skip-tls-verify: true
  name: t
contexts:
- context:
    cluster: t
    user: u
  name: t
current-context: t
users:
- name: u
  user:
    token: unused
`)
}
