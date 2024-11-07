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
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
	"github.com/hghukasyan/kubelease/internal/lease"
	"github.com/hghukasyan/kubelease/internal/policy"
)

// ActionResult is the outcome of handling a webhook request.
type ActionResult struct {
	StatusCode int
	Duplicate  bool
	Message    string
	LeaseName  string
	Namespace  string
}

func (s *Server) handleAction(ctx context.Context, req Request) (ActionResult, error) {
	switch req.Action {
	case ActionCreate:
		return s.createLease(ctx, req)
	case ActionExpire:
		return s.expireLease(ctx, req)
	case ActionTouch:
		return s.touchLease(ctx, req)
	default:
		return ActionResult{StatusCode: 400, Message: "unsupported action"}, nil
	}
}

func (s *Server) createLease(ctx context.Context, req Request) (ActionResult, error) {
	if s.Config.DefaultPolicy == "" {
		return ActionResult{StatusCode: 500, Message: "webhook default policy is not configured"}, nil
	}

	name := strings.TrimSpace(req.Name)
	existing := &platformv1alpha1.EnvironmentLease{}
	err := s.Client.Get(ctx, types.NamespacedName{Name: name}, existing)
	if err == nil {
		if req.RequestID != "" && existing.Annotations[AnnotationRequestID] == req.RequestID {
			return ActionResult{
				StatusCode: 200,
				Duplicate:  true,
				Message:    "lease already exists for requestId",
				LeaseName:  existing.Name,
				Namespace:  existing.Status.Namespace,
			}, nil
		}
		if req.RequestID == "" {
			// Name-based idempotency when no requestId is provided.
			return ActionResult{
				StatusCode: 200,
				Duplicate:  true,
				Message:    "lease already exists",
				LeaseName:  existing.Name,
				Namespace:  existing.Status.Namespace,
			}, nil
		}
		return ActionResult{
			StatusCode: 409,
			Message:    "lease already exists with a different requestId",
			LeaseName:  existing.Name,
		}, nil
	}
	if !apierrors.IsNotFound(err) {
		return ActionResult{}, fmt.Errorf("get lease: %w", err)
	}

	leaseObj := s.buildLease(req)

	if err := s.validateAgainstPolicy(ctx, leaseObj); err != nil {
		return ActionResult{StatusCode: 422, Message: err.Error()}, nil
	}

	if err := s.Client.Create(ctx, leaseObj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return ActionResult{StatusCode: 200, Duplicate: true, Message: "lease already exists", LeaseName: name}, nil
		}
		return ActionResult{}, fmt.Errorf("create lease: %w", err)
	}

	return ActionResult{
		StatusCode: 201,
		Message:    "lease created",
		LeaseName:  leaseObj.Name,
	}, nil
}

func (s *Server) buildLease(req Request) *platformv1alpha1.EnvironmentLease {
	name := strings.TrimSpace(req.Name)
	owner := strings.TrimSpace(req.Owner)
	team := strings.TrimSpace(req.Team)
	if team == "" && owner != "" {
		team = owner
	}

	annotations := map[string]string{
		AnnotationSource: SourceValue,
	}
	if req.RequestID != "" {
		annotations[AnnotationRequestID] = req.RequestID
	}

	// Intentionally omit TTL, maxTTL, quota, networkPolicy, and exact namespace
	// names — those come only from the configured EnvironmentLeasePolicy.
	return &platformv1alpha1.EnvironmentLease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: annotations,
			Labels: map[string]string{
				platformv1alpha1.LabelManagedBy: platformv1alpha1.ManagedByValue,
			},
		},
		Spec: platformv1alpha1.EnvironmentLeaseSpec{
			PolicyRef: &platformv1alpha1.LocalObjectReference{Name: s.Config.DefaultPolicy},
			Owner: platformv1alpha1.OwnerSpec{
				Name: owner,
				Team: team,
			},
			Namespace: platformv1alpha1.NamespaceSpec{
				GenerateName: s.Config.NamespaceGenerateName,
				Labels: map[string]string{
					"environment": "preview",
					"source":      SourceValue,
				},
			},
		},
	}
}

func (s *Server) validateAgainstPolicy(ctx context.Context, leaseObj *platformv1alpha1.EnvironmentLease) error {
	pol := &platformv1alpha1.EnvironmentLeasePolicy{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: s.Config.DefaultPolicy}, pol); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("policy %q not found", s.Config.DefaultPolicy)
		}
		return fmt.Errorf("get policy: %w", err)
	}
	if _, err := policy.Resolve(leaseObj.Spec, pol); err != nil {
		return fmt.Errorf("policy violation: %w", err)
	}
	return nil
}

func (s *Server) expireLease(ctx context.Context, req Request) (ActionResult, error) {
	name := strings.TrimSpace(req.Name)
	leaseObj := &platformv1alpha1.EnvironmentLease{}
	if err := s.Client.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
		if apierrors.IsNotFound(err) {
			// Idempotent expire: already gone.
			return ActionResult{StatusCode: 200, Duplicate: true, Message: "lease already absent", LeaseName: name}, nil
		}
		return ActionResult{}, fmt.Errorf("get lease: %w", err)
	}

	if req.RequestID != "" {
		if leaseObj.Annotations == nil {
			leaseObj.Annotations = map[string]string{}
		}
		// Record request id on delete path via annotation patch when present.
		if leaseObj.Annotations[AnnotationRequestID] == req.RequestID && !leaseObj.DeletionTimestamp.IsZero() {
			return ActionResult{StatusCode: 200, Duplicate: true, Message: "expire already requested", LeaseName: name}, nil
		}
	}

	if !leaseObj.DeletionTimestamp.IsZero() {
		return ActionResult{StatusCode: 200, Duplicate: true, Message: "expire already requested", LeaseName: name}, nil
	}

	if err := s.Client.Delete(ctx, leaseObj); err != nil {
		if apierrors.IsNotFound(err) {
			return ActionResult{StatusCode: 200, Duplicate: true, Message: "lease already absent", LeaseName: name}, nil
		}
		return ActionResult{}, fmt.Errorf("delete lease: %w", err)
	}
	return ActionResult{StatusCode: 202, Message: "expire requested", LeaseName: name}, nil
}

func (s *Server) touchLease(ctx context.Context, req Request) (ActionResult, error) {
	name := strings.TrimSpace(req.Name)
	var result ActionResult
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		leaseObj := &platformv1alpha1.EnvironmentLease{}
		if err := s.Client.Get(ctx, types.NamespacedName{Name: name}, leaseObj); err != nil {
			if apierrors.IsNotFound(err) {
				result = ActionResult{StatusCode: 404, Message: "lease not found", LeaseName: name}
				return nil
			}
			return err
		}

		idleTTL := idleTTLFrom(leaseObj)
		if err := lease.RecordActivity(leaseObj, s.clock().Now(), idleTTL); err != nil {
			result = ActionResult{StatusCode: 409, Message: err.Error(), LeaseName: name}
			return nil
		}
		if err := s.Client.Status().Update(ctx, leaseObj); err != nil {
			return err
		}
		result = ActionResult{
			StatusCode: 200,
			Message:    "activity recorded",
			LeaseName:  name,
			Namespace:  leaseObj.Status.Namespace,
		}
		return nil
	})
	if err != nil {
		return ActionResult{}, err
	}
	return result, nil
}

func idleTTLFrom(leaseObj *platformv1alpha1.EnvironmentLease) time.Duration {
	if leaseObj.Status.Effective != nil && leaseObj.Status.Effective.IdleTTL != nil {
		return leaseObj.Status.Effective.IdleTTL.Duration
	}
	if leaseObj.Spec.IdleTTL != nil {
		return leaseObj.Spec.IdleTTL.Duration
	}
	return 0
}
