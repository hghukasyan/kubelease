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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Bounded label values for source metrics (never lease_name/namespace/repo/user/PR).
const (
	ProviderWebhook = "webhook"
	ProviderGitHub  = "github"

	ActionCreate = "create"
	ActionExpire = "expire"
	ActionTouch  = "touch"

	ResultSuccess = "success"
	ResultFailure = "failure"
)

var (
	// Leases is the number of EnvironmentLeases by phase.
	// Label "phase" is bounded to the LeasePhase enum.
	Leases = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubelease_leases",
		Help: "Number of EnvironmentLeases by phase",
	}, []string{"phase"})

	LeasesCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_leases_created_total",
		Help: "Total EnvironmentLeases that reached Active for the first time",
	})

	LeasesExpiredTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_leases_expired_total",
		Help: "Total EnvironmentLeases that reached effective expiration",
	})

	RenewalsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_renewals_total",
		Help: "Total successful lease renewals (expiresAt moved later)",
	})

	CleanupFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_cleanup_failures_total",
		Help: "Total failed environment cleanup attempts",
	})

	ProvisionFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_provision_failures_total",
		Help: "Total failed environment provisioning attempts",
	})

	WarningEventsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_warning_events_total",
		Help: "Total LeaseExpiring warning events emitted",
	})

	// SourceEventsTotal counts integration-source outcomes.
	// Labels: provider=github|webhook, action=create|expire|touch, result=success|failure
	SourceEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubelease_source_events_total",
		Help: "Total integration source events by provider, action, and result",
	}, []string{"provider", "action", "result"})

	// SourceErrorsTotal counts integration-source failures (subset of source events).
	SourceErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubelease_source_errors_total",
		Help: "Total integration source errors by provider and action",
	}, []string{"provider", "action"})

	IdleExpirationsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_idle_expirations_total",
		Help: "Total leases expired due to idle timeout",
	})

	ManualExpirationsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_manual_expirations_total",
		Help: "Total leases expired via explicit delete/expire (manual)",
	})

	PolicyRejectionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_policy_rejections_total",
		Help: "Total leases rejected for policy violations",
	})

	// ClusterTargets is the number of ClusterTarget objects by readiness.
	// Label "ready" is bounded to true|false.
	ClusterTargets = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubelease_cluster_targets",
		Help: "Number of ClusterTargets by ready status",
	}, []string{"ready"})

	// RemoteOperationsTotal counts remote cluster API operations.
	// Labels: operation=create|update|delete|get, result=success|failure.
	// Deliberately omits target name to keep cardinality bounded.
	RemoteOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubelease_remote_operations_total",
		Help: "Total remote cluster operations by operation and result",
	}, []string{"operation", "result"})

	ClusterConnectionFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_cluster_connection_failures_total",
		Help: "Total failures building or authenticating remote cluster clients",
	})

	// ClusterHealth is 1 when Ready=True else 0.
	// Label "cluster" is the ClusterTarget name — cardinality equals configured targets
	// (typically small). Do not add namespace/lease/PR labels.
	ClusterHealth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "kubelease_cluster_health",
		Help: "ClusterTarget readiness (1=ready, 0=not). Label cluster=ClusterTarget name.",
	}, []string{"cluster"})

	ClusterOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "kubelease_cluster_operations_total",
		Help: "Remote cluster operations by cluster, operation, result",
	}, []string{"cluster", "operation", "result"})

	ClusterOperationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "kubelease_cluster_operation_duration_seconds",
		Help:    "Remote cluster operation latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"cluster", "operation"})

	ClusterAuthFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_cluster_auth_failures_total",
		Help: "Total remote kubeconfig/auth failures (no secret values logged)",
	})

	PlacementAttemptsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_placement_attempts_total",
		Help: "Total placement decisions attempted",
	})

	PlacementFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_placement_failures_total",
		Help: "Total placement decisions with no matching target",
	})
)

func init() {
	metrics.Registry.MustRegister(
		Leases,
		LeasesCreatedTotal,
		LeasesExpiredTotal,
		RenewalsTotal,
		CleanupFailuresTotal,
		ProvisionFailuresTotal,
		WarningEventsTotal,
		SourceEventsTotal,
		SourceErrorsTotal,
		IdleExpirationsTotal,
		ManualExpirationsTotal,
		PolicyRejectionsTotal,
		ClusterTargets,
		RemoteOperationsTotal,
		ClusterConnectionFailuresTotal,
		ClusterHealth,
		ClusterOperationsTotal,
		ClusterOperationDuration,
		ClusterAuthFailuresTotal,
		PlacementAttemptsTotal,
		PlacementFailuresTotal,
	)
}

// ObserveRemote records a remote cluster API operation outcome.
func ObserveRemote(operation, result string) {
	RemoteOperationsTotal.WithLabelValues(operation, result).Inc()
}

// ObserveClusterOp records a per-cluster operation (bounded by ClusterTarget count).
func ObserveClusterOp(cluster, operation, result string, seconds float64) {
	if cluster == "" {
		cluster = "local"
	}
	ClusterOperationsTotal.WithLabelValues(cluster, operation, result).Inc()
	if seconds >= 0 {
		ClusterOperationDuration.WithLabelValues(cluster, operation).Observe(seconds)
	}
	ObserveRemote(operation, result)
}

// ObserveSource records a bounded-cardinality source event.
func ObserveSource(provider, action, result string) {
	SourceEventsTotal.WithLabelValues(provider, action, result).Inc()
	if result == ResultFailure {
		SourceErrorsTotal.WithLabelValues(provider, action).Inc()
	}
}
