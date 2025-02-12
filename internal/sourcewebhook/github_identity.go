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
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var nonDNSLabel = regexp.MustCompile(`[^a-z0-9-]+`)

// GitHubPRRef identifies a pull request uniquely for lease mapping.
type GitHubPRRef struct {
	Owner  string
	Repo   string
	Number int
	SHA    string
	Branch string
	Sender string
}

// FullName returns owner/repo.
func (r GitHubPRRef) FullName() string {
	return r.Owner + "/" + r.Repo
}

// LeaseName returns a deterministic DNS-1123 lease name.
// Example: payments-api PR 1842 → payments-api-pr-1842
// Long names are truncated with a short hash so the result stays ≤ 63 chars.
func LeaseName(ref GitHubPRRef) string {
	repo := dnsLabel(ref.Repo)
	if repo == "" {
		repo = "repo"
	}
	suffix := "-pr-" + strconv.Itoa(ref.Number)
	base := repo + suffix
	if len(base) <= 63 && isDNS1123Label(base) {
		return base
	}

	// Keep suffix intact; shorten repo with a stable hash of owner/repo/pr.
	hash := shortHash(ref.FullName() + "#" + strconv.Itoa(ref.Number))
	maxRepo := 63 - len(suffix) - 1 - len(hash) // repo-hash-pr-N
	if maxRepo < 1 {
		// Extreme: hash-pr-N
		name := hash + suffix
		return trimTo63(name)
	}
	shortRepo := repo
	if len(shortRepo) > maxRepo {
		shortRepo = strings.Trim(shortRepo[:maxRepo], "-")
	}
	if shortRepo == "" {
		shortRepo = "r"
	}
	return trimTo63(shortRepo + "-" + hash + suffix)
}

func dnsLabel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = nonDNSLabel.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return s
}

func isDNS1123Label(s string) bool {
	if len(s) == 0 || len(s) > 63 {
		return false
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' {
			continue
		}
		return false
	}
	return true
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}

func trimTo63(s string) string {
	if len(s) <= 63 {
		return s
	}
	s = s[:63]
	return strings.Trim(s, "-")
}

// PolicyForRepo resolves the EnvironmentLeasePolicy for a repository.
func (s *Server) PolicyForRepo(fullName string) string {
	fullName = strings.ToLower(strings.TrimSpace(fullName))
	if s.Config.GitHubRepoPolicies != nil {
		if p, ok := s.Config.GitHubRepoPolicies[fullName]; ok && p != "" {
			return p
		}
	}
	return s.Config.DefaultPolicy
}

// AssertNoGitHubSecretInSpec is a documentation/test helper: CR specs must never
// carry GitHub tokens. Secrets live only in Kubernetes Secrets.
func AssertNoGitHubSecretInSpec(annotations map[string]string) error {
	for k, v := range annotations {
		lk := strings.ToLower(k)
		lv := strings.ToLower(v)
		if strings.Contains(lk, "token") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "password") || strings.Contains(lv, "ghp_") {
			return fmt.Errorf("refusing to store secret-like annotation %q on lease", k)
		}
	}
	return nil
}
