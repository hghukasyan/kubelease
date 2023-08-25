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

package resources

import (
	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// ManagedLabels returns the standard labels applied to every resource managed
// by an EnvironmentLease.
func ManagedLabels(leaseName string) map[string]string {
	return map[string]string{
		platformv1alpha1.LabelManagedBy: platformv1alpha1.ManagedByValue,
		platformv1alpha1.LabelLease:     leaseName,
	}
}

// MergeLabels returns a new map with base labels overlaid by extra. Extra wins
// on key conflicts except for management keys, which always come from base.
func MergeLabels(base, extra map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(extra))
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range base {
		out[k] = v
	}
	return out
}

// IsManagedByKubeLease reports whether the object carries KubeLease management labels
// for the given lease name.
func IsManagedByKubeLease(labels map[string]string, leaseName string) bool {
	if labels == nil {
		return false
	}
	return labels[platformv1alpha1.LabelManagedBy] == platformv1alpha1.ManagedByValue &&
		labels[platformv1alpha1.LabelLease] == leaseName
}
