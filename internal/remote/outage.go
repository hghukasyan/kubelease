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
	"sync"
	"time"
)

// OutageTracker records per-target unavailability so many leases share one
// backoff window instead of independently rediscovering the same outage.
type OutageTracker struct {
	mu      sync.Mutex
	until   map[string]time.Time
	minBack time.Duration
	maxBack time.Duration
}

// NewOutageTracker returns a tracker with bounded exponential-style windows.
func NewOutageTracker(minBack, maxBack time.Duration) *OutageTracker {
	if minBack <= 0 {
		minBack = 15 * time.Second
	}
	if maxBack < minBack {
		maxBack = 2 * time.Minute
	}
	return &OutageTracker{
		until:   map[string]time.Time{},
		minBack: minBack,
		maxBack: maxBack,
	}
}

// MarkUnavailable records that targetName should not be hot-looped until later.
func (t *OutageTracker) MarkUnavailable(targetName string, now time.Time) time.Duration {
	if t == nil || targetName == "" || targetName == "local" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.until[targetName]
	back := t.minBack
	if ok && prev.After(now) {
		// Extend toward max.
		remaining := prev.Sub(now)
		back = remaining * 2
		if back < t.minBack {
			back = t.minBack
		}
		if back > t.maxBack {
			back = t.maxBack
		}
	}
	t.until[targetName] = now.Add(back)
	return back
}

// RequeueAfter returns remaining backoff for a target, or 0 if clear.
func (t *OutageTracker) RequeueAfter(targetName string, now time.Time) time.Duration {
	if t == nil || targetName == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	until, ok := t.until[targetName]
	if !ok || !until.After(now) {
		return 0
	}
	return until.Sub(now)
}

// Clear removes outage state after a successful operation.
func (t *OutageTracker) Clear(targetName string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.until, targetName)
}

// TargetGate limits concurrent remote operations per ClusterTarget.
type TargetGate struct {
	mu    sync.Mutex
	sem   map[string]chan struct{}
	limit int
}

// NewTargetGate creates a per-target semaphore (limit concurrent ops each).
func NewTargetGate(limit int) *TargetGate {
	if limit <= 0 {
		limit = 4
	}
	return &TargetGate{sem: map[string]chan struct{}{}, limit: limit}
}

// Acquire blocks until a slot is available for targetName (local is no-op).
func (g *TargetGate) Acquire(targetName string) func() {
	if g == nil || targetName == "" || targetName == "local" {
		return func() {}
	}
	g.mu.Lock()
	ch, ok := g.sem[targetName]
	if !ok {
		ch = make(chan struct{}, g.limit)
		g.sem[targetName] = ch
	}
	g.mu.Unlock()
	ch <- struct{}{}
	return func() { <-ch }
}
