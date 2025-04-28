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
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	appmetrics "github.com/hghukasyan/kubelease/internal/metrics"
)

const (
	resultOK              = "ok"
	resultError           = "error"
	resultInvalid         = "invalid"
	resultUnauthorized    = "unauthorized"
	resultDuplicate       = "duplicate"
	resultPolicyViolation = "policy_violation"
)

var (
	webhookRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubelease_webhook_requests_total",
		Help: "Total generic webhook source requests by action and result",
	}, []string{"action", "result"})

	webhookRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubelease_webhook_request_duration_seconds",
		Help:    "Generic webhook source request latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"action"})
)

func init() {
	crmetrics.Registry.MustRegister(webhookRequestsTotal, webhookRequestDuration)
}

type responseBody struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	LeaseName string `json:"leaseName,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Duplicate bool   `json:"duplicate,omitempty"`
}

// Handler returns the HTTP mux for the webhook service.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/v1/leases", s.handleLeases)
	mux.HandleFunc("/v1/github/hooks", s.handleGitHub)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s.TokenProvider == nil {
		http.Error(w, "token provider not configured", http.StatusServiceUnavailable)
		return
	}
	if _, err := s.TokenProvider.Token(ctx); err != nil {
		http.Error(w, "token not ready", http.StatusServiceUnavailable)
		return
	}
	if s.Config.GitHubEnabled {
		if s.GitHubSecretProvider == nil {
			http.Error(w, "github secret provider not configured", http.StatusServiceUnavailable)
			return
		}
		if _, err := s.GitHubSecretProvider.Token(ctx); err != nil {
			http.Error(w, "github secret not ready", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleLeases(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	actionLabel := "unknown"
	resultLabel := resultError
	defer func() {
		webhookRequestsTotal.WithLabelValues(actionLabel, resultLabel).Inc()
		webhookRequestDuration.WithLabelValues(actionLabel).Observe(time.Since(start).Seconds())
	}()

	ctx, cancel := context.WithTimeout(r.Context(), s.Config.RequestTimeout)
	defer cancel()

	if err := authorize(ctx, s.TokenProvider, r.Header.Get("Authorization"), r.Header.Get("X-KubeLease-Token")); err != nil {
		resultLabel = resultUnauthorized
		s.Log.Info("webhook unauthorized", "error", err.Error())
		writeJSON(w, http.StatusUnauthorized, responseBody{Status: "error", Message: "unauthorized"})
		return
	}

	req, err := decodeRequest(r, s.Config.MaxBodyBytes)
	if err != nil {
		resultLabel = resultInvalid
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, responseBody{Status: "error", Message: err.Error()})
			return
		}
		s.Log.Info("webhook invalid request", "error", err.Error())
		writeJSON(w, http.StatusBadRequest, responseBody{Status: "error", Message: err.Error()})
		return
	}
	actionLabel = string(req.Action)

	if err := req.validate(); err != nil {
		resultLabel = resultInvalid
		s.Log.Info("webhook validation failed", "error", err.Error(), "action", req.Action)
		writeJSON(w, http.StatusBadRequest, responseBody{Status: "error", Message: err.Error()})
		return
	}

	// Prefer Idempotency-Key header when body requestId is empty.
	if req.RequestID == "" {
		req.RequestID = r.Header.Get("Idempotency-Key")
	}

	out, err := s.handleAction(ctx, req)
	if err != nil {
		resultLabel = resultError
		s.Log.Error(err, "webhook action failed", "action", req.Action, "name", req.Name)
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

	s.Log.Info("webhook handled",
		"action", req.Action,
		"name", req.Name,
		"status", out.StatusCode,
		"duplicate", out.Duplicate,
		"requestId", req.RequestID,
	)

	status := "ok"
	if out.StatusCode >= 400 {
		status = "error"
	}
	observeWebhookSource(string(req.Action), out.StatusCode, out.Duplicate)
	writeJSON(w, out.StatusCode, responseBody{
		Status:    status,
		Message:   out.Message,
		LeaseName: out.LeaseName,
		Namespace: out.Namespace,
		Duplicate: out.Duplicate,
	})
}

func observeWebhookSource(action string, statusCode int, duplicate bool) {
	mapped := mapSourceAction(action)
	if mapped == "" {
		return
	}
	result := appmetrics.ResultSuccess
	if statusCode >= 400 {
		result = appmetrics.ResultFailure
	}
	// Duplicates are successful idempotent outcomes.
	_ = duplicate
	appmetrics.ObserveSource(appmetrics.ProviderWebhook, mapped, result)
}

func mapSourceAction(action string) string {
	switch Action(strings.ToLower(action)) {
	case ActionCreate:
		return appmetrics.ActionCreate
	case ActionExpire:
		return appmetrics.ActionExpire
	case ActionTouch:
		return appmetrics.ActionTouch
	default:
		// GitHub pull_request actions
		switch strings.ToLower(action) {
		case "opened", "reopened":
			return appmetrics.ActionCreate
		case "closed":
			return appmetrics.ActionExpire
		default:
			return ""
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, body responseBody) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
