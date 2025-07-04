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
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// TargetSession is the resolved client used to apply environment resources.
type TargetSession struct {
	// Name is reported in status.cluster.name ("local" or ClusterTarget name).
	Name string
	// Client talks to the cluster that hosts the Namespace.
	Client client.Client
	// Local is true when clusterRef was omitted.
	Local bool
	// SetLeaseOwner controls OwnerReferences to the EnvironmentLease CR.
	// Only safe on the local control cluster (remote clusters lack the CR).
	SetLeaseOwner bool
}

// ResolveTarget picks the local manager client or a remote ClusterTarget client.
//
// Semantics:
//
//	clusterRef omitted → local control-plane cluster
//	clusterRef set     → ClusterTarget credentials → remote client
func ResolveTarget(
	ctx context.Context,
	hub client.Client,
	local client.Client,
	provider Provider,
	leaseObj *platformv1alpha1.EnvironmentLease,
) (*TargetSession, error) {
	if leaseObj.Spec.ClusterRef == nil || leaseObj.Spec.ClusterRef.Name == "" {
		return &TargetSession{
			Name:          platformv1alpha1.LocalClusterName,
			Client:        local,
			Local:         true,
			SetLeaseOwner: true,
		}, nil
	}

	name := leaseObj.Spec.ClusterRef.Name
	target := &platformv1alpha1.ClusterTarget{}
	if err := hub.Get(ctx, types.NamespacedName{Name: name}, target); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, &TargetError{
				Reason:  platformv1alpha1.ReasonTargetNotFound,
				Message: fmt.Sprintf("ClusterTarget %q not found", name),
				Err:     err,
			}
		}
		return nil, &TargetError{
			Reason:  platformv1alpha1.ReasonTargetClusterUnavailable,
			Message: fmt.Sprintf("get ClusterTarget %q: %v", name, err),
			Err:     err,
		}
	}
	if !target.DeletionTimestamp.IsZero() {
		return nil, &TargetError{
			Reason:  platformv1alpha1.ReasonTargetClusterUnavailable,
			Message: fmt.Sprintf("ClusterTarget %q is deleting", name),
		}
	}
	if !target.Spec.IsEnabled() {
		return nil, &TargetError{
			Reason:  platformv1alpha1.ReasonTargetDisabled,
			Message: fmt.Sprintf("ClusterTarget %q is disabled", name),
			Err:     ErrTargetDisabled,
		}
	}

	cl, err := provider.ClientFor(ctx, target)
	if err != nil {
		reason := platformv1alpha1.ReasonTargetClusterUnavailable
		if errors.Is(err, ErrTargetDisabled) {
			reason = platformv1alpha1.ReasonTargetDisabled
		}
		return nil, &TargetError{
			Reason:  reason,
			Message: err.Error(),
			Err:     err,
		}
	}
	return &TargetSession{
		Name:          name,
		Client:        cl,
		Local:         false,
		SetLeaseOwner: false,
	}, nil
}

// TargetError classifies cluster-target resolution failures for Conditions.
type TargetError struct {
	Reason  string
	Message string
	Err     error
}

func (e *TargetError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "target error"
}

func (e *TargetError) Unwrap() error { return e.Err }

// AsTargetError extracts a TargetError if present.
func AsTargetError(err error) (*TargetError, bool) {
	var te *TargetError
	if errors.As(err, &te) {
		return te, true
	}
	return nil, false
}
