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
	"time"
)

// NextReconcileAfter returns how long to wait until the next meaningful event:
// the next undelivered warning threshold, or expiration. Returns 0 if action
// is due now (already expired or a warning is due).
func NextReconcileAfter(
	now time.Time,
	expiresAt time.Time,
	warnings []time.Duration,
	delivered []string,
) time.Duration {
	if !now.Before(expiresAt) {
		return 0
	}

	deliveredSet := map[string]struct{}{}
	for _, d := range delivered {
		deliveredSet[d] = struct{}{}
	}

	next := expiresAt
	for _, w := range warnings {
		key := CanonicalWarningKey(w)
		if _, ok := deliveredSet[key]; ok {
			continue
		}
		threshold := expiresAt.Add(-w)
		if threshold.Before(now) {
			// Due now.
			return 0
		}
		if threshold.Before(next) {
			next = threshold
		}
	}

	until := next.Sub(now)
	if until < 0 {
		return 0
	}
	return until
}
