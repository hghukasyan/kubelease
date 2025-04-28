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
	"io"
	"net/http"
	"time"

	appmetrics "github.com/hghukasyan/kubelease/internal/metrics"
)

func (s *Server) handleGitHub(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	actionLabel := "github"
	resultLabel := resultError
	defer func() {
		webhookRequestsTotal.WithLabelValues(actionLabel, resultLabel).Inc()
		webhookRequestDuration.WithLabelValues(actionLabel).Observe(time.Since(start).Seconds())
	}()

	if r.Method != http.MethodPost {
		resultLabel = resultInvalid
		writeJSON(w, http.StatusMethodNotAllowed, responseBody{Status: "error", Message: "method not allowed"})
		return
	}
	if !s.Config.GitHubEnabled {
		resultLabel = resultInvalid
		writeJSON(w, http.StatusNotFound, responseBody{Status: "error", Message: "github webhook is disabled"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.Config.RequestTimeout)
	defer cancel()

	maxBody := s.Config.MaxBodyBytes
	if s.Config.GitHubMaxBodyBytes > 0 {
		maxBody = s.Config.GitHubMaxBodyBytes
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		resultLabel = resultInvalid
		writeJSON(w, http.StatusBadRequest, responseBody{Status: "error", Message: "failed to read body"})
		return
	}
	if int64(len(body)) > maxBody {
		resultLabel = resultInvalid
		writeJSON(w, http.StatusRequestEntityTooLarge, responseBody{Status: "error", Message: "request body too large"})
		return
	}

	secret, err := s.githubWebhookSecret(ctx)
	if err != nil {
		resultLabel = resultError
		s.Log.Error(err, "github webhook secret unavailable")
		writeJSON(w, http.StatusServiceUnavailable, responseBody{Status: "error", Message: "webhook secret unavailable"})
		return
	}
	if err := VerifyGitHubSignature(secret, body, r.Header.Get(HeaderGitHubSignature256)); err != nil {
		resultLabel = resultUnauthorized
		s.Log.Info("github signature rejected", "error", err.Error())
		writeJSON(w, http.StatusUnauthorized, responseBody{Status: "error", Message: "invalid signature"})
		return
	}

	eventType := r.Header.Get(HeaderGitHubEvent)
	deliveryID := r.Header.Get(HeaderGitHubDelivery)
	actionLabel = "github:" + eventType

	switch eventType {
	case "ping":
		resultLabel = resultOK
		writeJSON(w, http.StatusOK, responseBody{Status: "ok", Message: "pong"})
		return
	case "pull_request":
		// continue
	case "":
		resultLabel = resultInvalid
		writeJSON(w, http.StatusBadRequest, responseBody{Status: "error", Message: "missing X-GitHub-Event"})
		return
	default:
		resultLabel = resultOK
		writeJSON(w, http.StatusOK, responseBody{Status: "ok", Message: "ignored event " + eventType})
		return
	}

	ev, ref, err := parsePullRequestEvent(body)
	if err != nil {
		resultLabel = resultInvalid
		s.Log.Info("github payload rejected", "error", err.Error())
		writeJSON(w, http.StatusBadRequest, responseBody{Status: "error", Message: err.Error()})
		return
	}
	actionLabel = "github:pull_request:" + ev.Action

	out, err := s.handleGitHubPREvent(ctx, ev, ref, deliveryID)
	if err != nil {
		resultLabel = resultError
		s.Log.Error(err, "github event failed", "action", ev.Action, "repo", ref.FullName(), "pr", ref.Number)
		writeJSON(w, http.StatusInternalServerError, responseBody{Status: "error", Message: "internal error"})
		return
	}

	switch {
	case out.StatusCode >= 200 && out.StatusCode < 300:
		if out.Duplicate {
			resultLabel = resultDuplicate
		} else {
			resultLabel = resultOK
		}
	case out.StatusCode == http.StatusUnauthorized:
		resultLabel = resultUnauthorized
	case out.StatusCode == 422:
		resultLabel = resultPolicyViolation
	case out.StatusCode >= 400 && out.StatusCode < 500:
		resultLabel = resultInvalid
	default:
		resultLabel = resultError
	}

	s.Log.Info("github webhook handled",
		"action", ev.Action,
		"repo", ref.FullName(),
		"pr", ref.Number,
		"lease", out.LeaseName,
		"delivery", deliveryID,
		"status", out.StatusCode,
		"duplicate", out.Duplicate,
		"merged", ev.PullRequest.Merged,
	)

	status := "ok"
	if out.StatusCode >= 400 {
		status = "error"
	}
	if mapped := mapSourceAction(ev.Action); mapped != "" {
		result := appmetrics.ResultSuccess
		if out.StatusCode >= 400 {
			result = appmetrics.ResultFailure
		}
		appmetrics.ObserveSource(appmetrics.ProviderGitHub, mapped, result)
	}
	writeJSON(w, out.StatusCode, responseBody{
		Status:    status,
		Message:   out.Message,
		LeaseName: out.LeaseName,
		Namespace: out.Namespace,
		Duplicate: out.Duplicate,
	})
}

func (s *Server) githubWebhookSecret(ctx context.Context) ([]byte, error) {
	if s.GitHubSecretProvider != nil {
		tok, err := s.GitHubSecretProvider.Token(ctx)
		if err != nil {
			return nil, err
		}
		return []byte(tok), nil
	}
	return nil, errGitHubSecretNotConfigured
}

var errGitHubSecretNotConfigured = errString("github webhook secret provider not configured")

type errString string

func (e errString) Error() string { return string(e) }
