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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// HeaderGitHubEvent is the GitHub event type header.
	HeaderGitHubEvent = "X-GitHub-Event"
	// HeaderGitHubDelivery is the unique delivery GUID.
	HeaderGitHubDelivery = "X-GitHub-Delivery"
	// HeaderGitHubSignature256 is the HMAC SHA-256 signature header.
	HeaderGitHubSignature256 = "X-Hub-Signature-256"

	// SourceGitHub is the AnnotationSource value for GitHub-backed leases.
	SourceGitHub = "github"

	AnnotationGitHubOwner    = "kubelease.io/github-owner"
	AnnotationGitHubRepo     = "kubelease.io/github-repo"
	AnnotationGitHubPR       = "kubelease.io/github-pr"
	AnnotationGitHubSHA      = "kubelease.io/github-sha"
	AnnotationGitHubBranch   = "kubelease.io/github-branch"
	AnnotationGitHubDelivery = "kubelease.io/github-delivery"
	AnnotationGitHubFullName = "kubelease.io/github-full-name"

	LabelGitHubOwner = "kubelease.io/github-owner"
	LabelGitHubRepo  = "kubelease.io/github-repo"
	LabelGitHubPR    = "kubelease.io/github-pr"
)

// VerifyGitHubSignature validates X-Hub-Signature-256 (sha256=<hex>).
// The webhook secret must come from a Kubernetes Secret, never from a CR spec.
func VerifyGitHubSignature(secret, body []byte, signatureHeader string) error {
	if len(secret) == 0 {
		return fmt.Errorf("github webhook secret is empty")
	}
	signatureHeader = strings.TrimSpace(signatureHeader)
	const prefix = "sha256="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return fmt.Errorf("missing or invalid %s header", HeaderGitHubSignature256)
	}
	got, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, prefix))
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	expected := mac.Sum(nil)
	if !hmac.Equal(got, expected) {
		return fmt.Errorf("invalid github webhook signature")
	}
	return nil
}
