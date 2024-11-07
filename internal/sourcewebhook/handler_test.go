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

package sourcewebhook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/sourcewebhook"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := platformv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func previewPolicy() *platformv1alpha1.EnvironmentLeasePolicy {
	return &platformv1alpha1.EnvironmentLeasePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-default"},
		Spec: platformv1alpha1.EnvironmentLeasePolicySpec{
			TTL: &platformv1alpha1.DurationPolicy{
				Default: &metav1.Duration{Duration: 8 * time.Hour},
				Maximum: &metav1.Duration{Duration: 72 * time.Hour},
			},
			IdleTTL: &platformv1alpha1.DurationPolicy{
				Default: &metav1.Duration{Duration: 30 * time.Minute},
			},
		},
	}
}

func newServer(t *testing.T, objs ...client.Object) *sourcewebhook.Server {
	t.Helper()
	scheme := testScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&platformv1alpha1.EnvironmentLease{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &sourcewebhook.Server{
		Client: builder.Build(),
		Config: sourcewebhook.Config{
			DefaultPolicy:         "preview-default",
			NamespaceGenerateName: "preview-",
			MaxBodyBytes:          64 << 10,
			RequestTimeout:        5 * time.Second,
		},
		Log:           logr.Discard(),
		Clock:         lease.FixedClock{T: time.Date(2024, 11, 7, 12, 0, 0, 0, time.UTC)},
		TokenProvider: sourcewebhook.StaticTokenProvider{Value: "test-token"},
	}
}

func doJSON(t *testing.T, h http.Handler, token string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if raw, ok := body.(string); ok {
			buf.WriteString(raw)
		} else {
			if err := json.NewEncoder(&buf).Encode(body); err != nil {
				t.Fatal(err)
			}
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/leases", &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestValidCreateRequest(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	h := srv.Handler()

	resp := doJSON(t, h, "test-token", map[string]any{
		"action": "create",
		"name":   "feature-482",
		"owner":  "payments",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	got := &platformv1alpha1.EnvironmentLease{}
	if err := srv.Client.Get(context.Background(), types.NamespacedName{Name: "feature-482"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.PolicyRef == nil || got.Spec.PolicyRef.Name != "preview-default" {
		t.Fatalf("policyRef=%v", got.Spec.PolicyRef)
	}
	if got.Spec.TTL != nil || got.Spec.MaxTTL != nil || got.Spec.Quota != nil {
		t.Fatal("create must not set TTL/maxTTL/quota from caller")
	}
	if got.Spec.Namespace.Name != "" {
		t.Fatal("create must not set exact namespace name")
	}
	if got.Spec.Owner.Name != "payments" {
		t.Fatalf("owner=%s", got.Spec.Owner.Name)
	}
}

func TestInvalidSecret(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	resp := doJSON(t, srv.Handler(), "wrong-token", map[string]any{
		"action": "create",
		"name":   "feature-482",
		"owner":  "payments",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestDuplicateRequest(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	h := srv.Handler()
	body := map[string]any{
		"action":    "create",
		"name":      "feature-482",
		"owner":     "payments",
		"requestId": "req-1",
	}
	if resp := doJSON(t, h, "test-token", body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first status=%d", resp.StatusCode)
	}
	resp := doJSON(t, h, "test-token", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("duplicate status=%d", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["duplicate"] != true {
		t.Fatalf("expected duplicate flag, got %#v", out)
	}
}

func TestMalformedJSON(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	resp := doJSON(t, srv.Handler(), "test-token", "{not-json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestUnsupportedAction(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	resp := doJSON(t, srv.Handler(), "test-token", map[string]any{
		"action": "pause",
		"name":   "feature-482",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestRejectsCallerSuppliedDangerousFields(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	resp := doJSON(t, srv.Handler(), "test-token", `{
		"action":"create",
		"name":"feature-482",
		"owner":"payments",
		"maxTTL":"720h",
		"namespace":"kube-system"
	}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 for unknown fields", resp.StatusCode)
	}
}

func TestPolicyViolation(t *testing.T) {
	t.Parallel()
	// Policy without ttl.default → create cannot resolve required TTL.
	pol := &platformv1alpha1.EnvironmentLeasePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "preview-default"},
		Spec:       platformv1alpha1.EnvironmentLeasePolicySpec{},
	}
	srv := newServer(t, pol)
	resp := doJSON(t, srv.Handler(), "test-token", map[string]any{
		"action": "create",
		"name":   "feature-482",
		"owner":  "payments",
	})
	if resp.StatusCode != 422 {
		t.Fatalf("status=%d want 422", resp.StatusCode)
	}
}

func TestExpireAndTouch(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 11, 7, 12, 0, 0, 0, time.UTC)
	created := metav1.NewTime(now)
	expires := metav1.NewTime(now.Add(8 * time.Hour))
	idle := metav1.Duration{Duration: 30 * time.Minute}
	existing := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{Name: "feature-482"},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			IdleTTL: &idle,
			Namespace: platformv1alpha1.NamespaceSpec{
				GenerateName: "preview-",
			},
		},
		Status: platformv1alpha1.EnvironmentLeaseStatus{
			CreatedAt: &created,
			ExpiresAt: &expires,
			Effective: &platformv1alpha1.EffectiveLeaseSpec{
				IdleTTL: &idle,
			},
		},
	}
	srv := newServer(t, previewPolicy(), existing)
	h := srv.Handler()

	touch := doJSON(t, h, "test-token", map[string]any{
		"action": "touch",
		"name":   "feature-482",
	})
	if touch.StatusCode != http.StatusOK {
		t.Fatalf("touch status=%d", touch.StatusCode)
	}
	got := &platformv1alpha1.EnvironmentLease{}
	if err := srv.Client.Get(context.Background(), types.NamespacedName{Name: "feature-482"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.LastActivityAt == nil {
		t.Fatal("expected lastActivityAt after touch")
	}

	expire := doJSON(t, h, "test-token", map[string]any{
		"action": "expire",
		"name":   "feature-482",
	})
	if expire.StatusCode != http.StatusAccepted {
		t.Fatalf("expire status=%d", expire.StatusCode)
	}

	// Idempotent expire when already gone.
	expire2 := doJSON(t, h, "test-token", map[string]any{
		"action": "expire",
		"name":   "feature-482",
	})
	if expire2.StatusCode != http.StatusOK {
		t.Fatalf("expire duplicate status=%d", expire2.StatusCode)
	}
}

func TestHealthzReadyz(t *testing.T) {
	t.Parallel()
	srv := newServer(t, previewPolicy())
	h := srv.Handler()

	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("healthz=%d", health.Code)
	}

	ready := httptest.NewRecorder()
	h.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readyz=%d", ready.Code)
	}
}
