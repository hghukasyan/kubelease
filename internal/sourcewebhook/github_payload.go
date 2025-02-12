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
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	platformv1alpha1 "github.com/hghukasyan/kubelease/api/v1alpha1"
)

// githubPullRequestEvent is the subset of the GitHub pull_request webhook payload
// needed for lease lifecycle. No GitHub API calls are made.
type githubPullRequestEvent struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		Number int  `json:"number"`
		Merged bool `json:"merged"`
		Head   struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Owner    struct {
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

func parsePullRequestEvent(body []byte) (githubPullRequestEvent, GitHubPRRef, error) {
	var ev githubPullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return githubPullRequestEvent{}, GitHubPRRef{}, fmt.Errorf("malformed JSON: %w", err)
	}
	ref, err := ev.toRef()
	if err != nil {
		return githubPullRequestEvent{}, GitHubPRRef{}, err
	}
	return ev, ref, nil
}

func (ev githubPullRequestEvent) toRef() (GitHubPRRef, error) {
	owner := strings.TrimSpace(ev.Repository.Owner.Login)
	repo := strings.TrimSpace(ev.Repository.Name)
	if owner == "" || repo == "" {
		return GitHubPRRef{}, fmt.Errorf("repository owner/name are required")
	}
	number := ev.Number
	if number == 0 {
		number = ev.PullRequest.Number
	}
	if number <= 0 {
		return GitHubPRRef{}, fmt.Errorf("pull request number is required")
	}
	sender := strings.TrimSpace(ev.PullRequest.User.Login)
	if sender == "" {
		sender = strings.TrimSpace(ev.Sender.Login)
	}
	return GitHubPRRef{
		Owner:  owner,
		Repo:   repo,
		Number: number,
		SHA:    strings.TrimSpace(ev.PullRequest.Head.SHA),
		Branch: strings.TrimSpace(ev.PullRequest.Head.Ref),
		Sender: sender,
	}, nil
}

func githubMetadataAnnotations(ref GitHubPRRef, deliveryID string) map[string]string {
	out := map[string]string{
		AnnotationSource:         SourceGitHub,
		AnnotationGitHubOwner:    ref.Owner,
		AnnotationGitHubRepo:     ref.Repo,
		AnnotationGitHubPR:       strconv.Itoa(ref.Number),
		AnnotationGitHubFullName: ref.FullName(),
	}
	if ref.SHA != "" {
		out[AnnotationGitHubSHA] = ref.SHA
	}
	if ref.Branch != "" {
		out[AnnotationGitHubBranch] = ref.Branch
	}
	if deliveryID != "" {
		out[AnnotationGitHubDelivery] = deliveryID
		out[AnnotationRequestID] = "github:" + deliveryID
	}
	return out
}

func githubMetadataLabels(ref GitHubPRRef) map[string]string {
	return map[string]string{
		platformv1alpha1.LabelManagedBy: platformv1alpha1.ManagedByValue,
		LabelGitHubOwner:                dnsLabel(ref.Owner),
		LabelGitHubRepo:                 dnsLabel(ref.Repo),
		LabelGitHubPR:                   strconv.Itoa(ref.Number),
	}
}
