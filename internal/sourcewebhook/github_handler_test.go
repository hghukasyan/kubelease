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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/sourcewebhook"
)

const (
	githubTestSecret = "github-secret"
	testPolicyName   = "preview-default"
	testOwner        = "my-company"
)

func signGitHub(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newGitHubServer(t *testing.T, objs ...client.Object) *sourcewebhook.Server {
	t.Helper()
	scheme := testScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&platformv1alpha1.EnvironmentLease{})
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}
	return &sourcewebhook.Server{
		Client: builder.Build(),
		Config: sourcewebhook.Config{
			DefaultPolicy:         testPolicyName,
			NamespaceGenerateName: "preview-",
			MaxBodyBytes:          64 << 10,
			GitHubMaxBodyBytes:    1 << 20,
			RequestTimeout:        5 * time.Second,
			GitHubEnabled:         true,
			GitHubRepoPolicies: map[string]string{
				"my-company/payments-api": testPolicyName,
			},
		},
		Log:                  logr.Discard(),
		Clock:                lease.FixedClock{T: time.Date(2025, 2, 12, 12, 0, 0, 0, time.UTC)},
		TokenProvider:        sourcewebhook.StaticTokenProvider{Value: "test-token"},
		GitHubSecretProvider: sourcewebhook.StaticTokenProvider{Value: githubTestSecret},
	}
}

func prPayload(action, repo string, number int, merged bool, sha, branch string) []byte {
	body := map[string]any{
		"action": action,
		"number": number,
		"pull_request": map[string]any{
			"number": number,
			"merged": merged,
			"head": map[string]any{
				"sha": sha,
				"ref": branch,
			},
			"user": map[string]any{"login": "octocat"},
		},
		"repository": map[string]any{
			"name":      repo,
			"full_name": testOwner + "/" + repo,
			"owner":     map[string]any{"login": testOwner},
		},
		"sender": map[string]any{"login": "octocat"},
	}
	raw, _ := json.Marshal(body)
	return raw
}

func doGitHub(t *testing.T, h http.Handler, secret string, body []byte, delivery string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/github/hooks", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sourcewebhook.HeaderGitHubEvent, "pull_request")
	req.Header.Set(sourcewebhook.HeaderGitHubDelivery, delivery)
	if secret != "" {
		req.Header.Set(sourcewebhook.HeaderGitHubSignature256, signGitHub(body, secret))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestGitHubValidSignatureOpened(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	body := prPayload("opened", "payments-api", 1842, false, "abc123", "feature/x")
	resp := doGitHub(t, srv.Handler(), githubTestSecret, body, "delivery-1")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	got := &platformv1alpha1.EnvironmentLease{}
	if err := srv.Client.Get(context.Background(), types.NamespacedName{Name: "payments-api-pr-1842"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[sourcewebhook.AnnotationGitHubFullName] != "my-company/payments-api" {
		t.Fatalf("full_name=%s", got.Annotations[sourcewebhook.AnnotationGitHubFullName])
	}
	if got.Annotations[sourcewebhook.AnnotationGitHubSHA] != "abc123" {
		t.Fatalf("sha=%s", got.Annotations[sourcewebhook.AnnotationGitHubSHA])
	}
	if got.Annotations[sourcewebhook.AnnotationGitHubBranch] != "feature/x" {
		t.Fatalf("branch=%s", got.Annotations[sourcewebhook.AnnotationGitHubBranch])
	}
	if got.Spec.PolicyRef == nil || got.Spec.PolicyRef.Name != testPolicyName {
		t.Fatalf("policy=%v", got.Spec.PolicyRef)
	}
	// Secrets must never appear on the CR.
	for k, v := range got.Annotations {
		if strings.Contains(strings.ToLower(k), "secret") || strings.Contains(v, githubTestSecret) {
			t.Fatalf("secret leaked onto lease annotation %s", k)
		}
	}
}

func TestGitHubInvalidSignature(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	body := prPayload("opened", "payments-api", 1842, false, "abc", "main")
	resp := doGitHub(t, srv.Handler(), "wrong-secret", body, "delivery-bad")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestGitHubDuplicateDelivery(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	h := srv.Handler()
	body := prPayload("opened", "payments-api", 1842, false, "abc", "main")
	if resp := doGitHub(t, h, githubTestSecret, body, "delivery-dup"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("first=%d", resp.StatusCode)
	}
	resp := doGitHub(t, h, githubTestSecret, body, "delivery-dup")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dup=%d", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if out["duplicate"] != true {
		t.Fatalf("expected duplicate: %#v", out)
	}
}

func TestGitHubClosedAndMergedExpire(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	h := srv.Handler()
	open := prPayload("opened", "payments-api", 99, false, "sha1", "branch")
	if resp := doGitHub(t, h, githubTestSecret, open, "d-open"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("open=%d", resp.StatusCode)
	}

	closed := prPayload("closed", "payments-api", 99, false, "sha1", "branch")
	if resp := doGitHub(t, h, githubTestSecret, closed, "d-close"); resp.StatusCode != http.StatusAccepted {
		t.Fatalf("close=%d", resp.StatusCode)
	}

	// Recreate for merged path.
	srv2 := newGitHubServer(t, previewPolicy())
	h2 := srv2.Handler()
	if resp := doGitHub(t, h2, githubTestSecret, open, "d-open2"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("open2=%d", resp.StatusCode)
	}
	merged := prPayload("closed", "payments-api", 99, true, "sha1", "branch")
	resp := doGitHub(t, h2, githubTestSecret, merged, "d-merge")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("merge=%d", resp.StatusCode)
	}
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !strings.Contains(fmt.Sprint(out["message"]), "merged") {
		t.Fatalf("message=%v", out["message"])
	}
}

func TestGitHubReopenedCreatesWhenAbsent(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	body := prPayload("reopened", "payments-api", 7, false, "sha2", "reopen-branch")
	resp := doGitHub(t, srv.Handler(), githubTestSecret, body, "d-reopen")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	got := &platformv1alpha1.EnvironmentLease{}
	if err := srv.Client.Get(context.Background(), types.NamespacedName{Name: "payments-api-pr-7"}, got); err != nil {
		t.Fatal(err)
	}
}

func TestGitHubReopenedEnsuresExisting(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	h := srv.Handler()
	open := prPayload("opened", "payments-api", 8, false, "oldsha", "old")
	if resp := doGitHub(t, h, githubTestSecret, open, "d1"); resp.StatusCode != http.StatusCreated {
		t.Fatalf("open=%d", resp.StatusCode)
	}
	reopen := prPayload("reopened", "payments-api", 8, false, "newsha", "new")
	resp := doGitHub(t, h, githubTestSecret, reopen, "d2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reopen=%d", resp.StatusCode)
	}
	got := &platformv1alpha1.EnvironmentLease{}
	if err := srv.Client.Get(context.Background(), types.NamespacedName{Name: "payments-api-pr-8"}, got); err != nil {
		t.Fatal(err)
	}
	if got.Annotations[sourcewebhook.AnnotationGitHubSHA] != "newsha" {
		t.Fatalf("sha=%s", got.Annotations[sourcewebhook.AnnotationGitHubSHA])
	}
}

func TestGitHubLongRepoName(t *testing.T) {
	t.Parallel()
	longRepo := strings.Repeat("payments-api-very-long-name", 4) // >> 63
	ref := sourcewebhook.GitHubPRRef{Owner: "my-company", Repo: longRepo, Number: 1842}
	name := sourcewebhook.LeaseName(ref)
	if len(name) > 63 {
		t.Fatalf("lease name too long: %q (%d)", name, len(name))
	}
	if !strings.Contains(name, "-pr-1842") {
		t.Fatalf("missing pr suffix: %s", name)
	}

	srv := newGitHubServer(t, previewPolicy())
	srv.Config.GitHubRepoPolicies[strings.ToLower(testOwner+"/"+longRepo)] = testPolicyName
	body := prPayload("opened", longRepo, 1842, false, "sha", "branch")
	resp := doGitHub(t, srv.Handler(), githubTestSecret, body, "d-long")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestGitHubNameCollision(t *testing.T) {
	t.Parallel()
	existing := &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{
			Name: "payments-api-pr-1842",
			Annotations: map[string]string{
				sourcewebhook.AnnotationGitHubFullName: "other-org/payments-api",
				sourcewebhook.AnnotationGitHubOwner:    "other-org",
				sourcewebhook.AnnotationGitHubRepo:     "payments-api",
			},
		},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			Namespace: platformv1alpha1.NamespaceSpec{GenerateName: "preview-"},
		},
	}
	srv := newGitHubServer(t, previewPolicy(), existing)
	body := prPayload("opened", "payments-api", 1842, false, "sha", "branch")
	resp := doGitHub(t, srv.Handler(), githubTestSecret, body, "d-collision")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d want 409", resp.StatusCode)
	}
}

func TestGitHubMalformedPayload(t *testing.T) {
	t.Parallel()
	srv := newGitHubServer(t, previewPolicy())
	body := []byte(`{"action":"opened"`)
	resp := doGitHub(t, srv.Handler(), githubTestSecret, body, "d-bad")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestGitHubLeaseNameExample(t *testing.T) {
	t.Parallel()
	name := sourcewebhook.LeaseName(sourcewebhook.GitHubPRRef{
		Owner: "my-company", Repo: "payments-api", Number: 1842,
	})
	if name != "payments-api-pr-1842" {
		t.Fatalf("got %s", name)
	}
}

func TestVerifyGitHubSignatureHelper(t *testing.T) {
	t.Parallel()
	body := []byte(`{"ok":true}`)
	sig := signGitHub(body, "s3cr3t")
	if err := sourcewebhook.VerifyGitHubSignature([]byte("s3cr3t"), body, sig); err != nil {
		t.Fatal(err)
	}
	if err := sourcewebhook.VerifyGitHubSignature([]byte("s3cr3t"), body, "sha256=deadbeef"); err == nil {
		t.Fatal("expected error")
	}
}
