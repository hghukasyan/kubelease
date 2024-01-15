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

package lease

import (
	"fmt"
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WarningDurations extracts and sorts warning durations descending (largest first).
func WarningDurations(warnings []metav1.Duration) []time.Duration {
	out := make([]time.Duration, 0, len(warnings))
	for _, w := range warnings {
		if w.Duration > 0 {
			out = append(out, w.Duration)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// CanonicalWarningKey returns a stable string key for a warning duration.
func CanonicalWarningKey(d time.Duration) string {
	return d.String()
}

// PendingWarnings returns warning durations that should fire at now:
// expiresAt - warning <= now < expiresAt, and not already delivered.
func PendingWarnings(
	now time.Time,
	expiresAt time.Time,
	warnings []time.Duration,
	delivered []string,
) []time.Duration {
	if !now.Before(expiresAt) {
		return nil
	}
	deliveredSet := map[string]struct{}{}
	for _, d := range delivered {
		deliveredSet[d] = struct{}{}
	}
	var pending []time.Duration
	for _, w := range warnings {
		key := CanonicalWarningKey(w)
		if _, ok := deliveredSet[key]; ok {
			continue
		}
		threshold := expiresAt.Add(-w)
		if !now.Before(threshold) {
			pending = append(pending, w)
		}
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i] > pending[j] })
	return pending
}

// MarkWarningDelivered appends a canonical warning key if not already present.
func MarkWarningDelivered(delivered []string, d time.Duration) []string {
	key := CanonicalWarningKey(d)
	for _, existing := range delivered {
		if existing == key {
			return delivered
		}
	}
	return append(append([]string{}, delivered...), key)
}

// WarningMessage formats a human-readable expiring message.
func WarningMessage(leaseName string, remaining time.Duration) string {
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("Environment %s will expire in approximately %s", leaseName, remaining.Round(time.Second))
}
