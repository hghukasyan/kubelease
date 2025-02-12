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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// handleGitHubPREvent maps GitHub pull_request actions to lease lifecycle.
//
//	opened   -> ensure lease exists
//	reopened -> ensure a new/active lease exists
//	closed   -> expire lease (controller cleans Namespace)
//	merged   -> expire lease (closed + merged=true)
//
// Webhook code never deletes Namespaces directly.
func (s *Server) handleGitHubPREvent(ctx context.Context, ev githubPullRequestEvent, ref GitHubPRRef, deliveryID string) (ActionResult, error) {
	name := LeaseName(ref)

	switch ev.Action {
	case "opened", "reopened":
		return s.ensureGitHubLease(ctx, ref, name, deliveryID)
	case "closed":
		return s.expireGitHubLease(ctx, ref, name, deliveryID, ev.PullRequest.Merged)
	default:
		return ActionResult{
			StatusCode: 200,
			Message:    fmt.Sprintf("ignored pull_request action %q", ev.Action),
			LeaseName:  name,
		}, nil
	}
}

func (s *Server) ensureGitHubLease(ctx context.Context, ref GitHubPRRef, name, deliveryID string) (ActionResult, error) {
	policyName := s.PolicyForRepo(ref.FullName())
	if policyName == "" {
		return ActionResult{StatusCode: 500, Message: "no EnvironmentLeasePolicy configured for repository"}, nil
	}

	existing := &platformv1alpha1.EnvironmentLease{}
	err := s.Client.Get(ctx, types.NamespacedName{Name: name}, existing)
	if err == nil {
		if deliveryID != "" && existing.Annotations[AnnotationGitHubDelivery] == deliveryID {
			return ActionResult{
				StatusCode: 200,
				Duplicate:  true,
				Message:    "duplicate github delivery",
				LeaseName:  existing.Name,
				Namespace:  existing.Status.Namespace,
			}, nil
		}
		if conflict := githubIdentityConflict(existing, ref); conflict != "" {
			return ActionResult{StatusCode: 409, Message: conflict, LeaseName: name}, nil
		}
		if !existing.DeletionTimestamp.IsZero() {
			return ActionResult{
				StatusCode: 409,
				Message:    "lease is expiring; retry after cleanup completes",
				LeaseName:  name,
			}, nil
		}
		if err := s.patchGitHubMetadata(ctx, existing, ref, deliveryID); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{
			StatusCode: 200,
			Message:    "lease ensured",
			LeaseName:  existing.Name,
			Namespace:  existing.Status.Namespace,
		}, nil
	}
	if !apierrors.IsNotFound(err) {
		return ActionResult{}, fmt.Errorf("get lease: %w", err)
	}

	leaseObj := s.buildGitHubLease(ref, name, policyName, deliveryID)
	if err := AssertNoGitHubSecretInSpec(leaseObj.Annotations); err != nil {
		return ActionResult{StatusCode: 500, Message: err.Error()}, nil
	}

	if err := s.validateAgainstPolicy(ctx, leaseObj, policyName); err != nil {
		return ActionResult{StatusCode: 422, Message: err.Error()}, nil
	}

	if err := s.Client.Create(ctx, leaseObj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ActionResult{StatusCode: 200, Duplicate: true, Message: "lease already exists", LeaseName: name}, nil
		}
		return ActionResult{}, fmt.Errorf("create lease: %w", err)
	}
	return ActionResult{StatusCode: 201, Message: "lease created", LeaseName: name}, nil
}

func (s *Server) buildGitHubLease(ref GitHubPRRef, name, policyName, deliveryID string) *platformv1alpha1.EnvironmentLease {
	return &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: githubMetadataAnnotations(ref, deliveryID),
			Labels:      githubMetadataLabels(ref),
		},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			PolicyRef: &platformv1alpha1.LocalObjectReference{Name: policyName},
			Owner: platformv1alpha1.OwnerSpec{
				Name: ref.Sender,
				Team: ref.Owner,
			},
			Namespace: platformv1alpha1.NamespaceSpec{
				GenerateName: s.Config.NamespaceGenerateName,
				Labels: map[string]string{
					"environment":    "preview",
					"source":         SourceGitHub,
					LabelGitHubOwner: dnsLabel(ref.Owner),
					LabelGitHubRepo:  dnsLabel(ref.Repo),
					LabelGitHubPR:    fmt.Sprintf("%d", ref.Number),
				},
			},
		},
	}
}

func (s *Server) patchGitHubMetadata(ctx context.Context, leaseObj *platformv1alpha1.EnvironmentLease, ref GitHubPRRef, deliveryID string) error {
	patched := leaseObj.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	for k, v := range githubMetadataAnnotations(ref, deliveryID) {
		patched.Annotations[k] = v
	}
	if patched.Labels == nil {
		patched.Labels = map[string]string{}
	}
	for k, v := range githubMetadataLabels(ref) {
		patched.Labels[k] = v
	}
	if err := AssertNoGitHubSecretInSpec(patched.Annotations); err != nil {
		return err
	}
	return s.Client.Patch(ctx, patched, client.MergeFrom(leaseObj))
}

func (s *Server) expireGitHubLease(ctx context.Context, ref GitHubPRRef, name, deliveryID string, merged bool) (ActionResult, error) {
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ActionResult{
				StatusCode: 200,
				Duplicate:  true,
				Message:    "lease already absent",
				LeaseName:  name,
			}, nil
		}
		return ActionResult{}, fmt.Errorf("get lease: %w", err)
	}

	if deliveryID != "" && leaseObj.Annotations[AnnotationGitHubDelivery] == deliveryID &&
		!leaseObj.DeletionTimestamp.IsZero() {
		return ActionResult{
			StatusCode: 200,
			Duplicate:  true,
			Message:    "duplicate github delivery",
			LeaseName:  name,
		}, nil
	}
	if conflict := githubIdentityConflict(leaseObj, ref); conflict != "" {
		return ActionResult{StatusCode: 409, Message: conflict, LeaseName: name}, nil
	}
	if !leaseObj.DeletionTimestamp.IsZero() {
		return ActionResult{
			StatusCode: 200,
			Duplicate:  true,
			Message:    "expire already requested",
			LeaseName:  name,
		}, nil
	}

	// Mark SourceClosed before delete so the controller preserves the reason.
	base := leaseObj.DeepCopy()
	if base.Annotations == nil {
		base.Annotations = map[string]string{}
	}
	if deliveryID != "" {
		base.Annotations[AnnotationGitHubDelivery] = deliveryID
		base.Annotations[AnnotationRequestID] = "github:" + deliveryID
	}
	if merged {
		base.Annotations["kubelease.io/github-merged"] = "true"
	}
	base.Status.ExpirationReason = platformv1alpha1.ExpirationReasonSourceClosed
	if err := s.Client.Status().Update(ctx, base); err != nil {
		// Status may be uninitialized; continue with delete using original object.
		s.Log.Info("could not persist SourceClosed before expire", "lease", name, "error", err.Error())
	} else {
		leaseObj = base
	}

	if err := s.Client.Delete(ctx, leaseObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ActionResult{StatusCode: 200, Duplicate: true, Message: "lease already absent", LeaseName: name}, nil
		}
		return ActionResult{}, fmt.Errorf("delete lease: %w", err)
	}

	msg := "expire requested"
	if merged {
		msg = "expire requested (merged)"
	}
	return ActionResult{StatusCode: 202, Message: msg, LeaseName: name}, nil
}

func githubIdentityConflict(existing *platformv1alpha1.EnvironmentLease, ref GitHubPRRef) string {
	if existing.Annotations == nil {
		return ""
	}
	full := existing.Annotations[AnnotationGitHubFullName]
	if full == "" {
		owner := existing.Annotations[AnnotationGitHubOwner]
		repo := existing.Annotations[AnnotationGitHubRepo]
		if owner != "" && repo != "" {
			full = owner + "/" + repo
		}
	}
	if full != "" && full != ref.FullName() {
		return fmt.Sprintf("lease name collision: existing source %s vs %s", full, ref.FullName())
	}
	return ""
}
