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
		Help: "Total EnvironmentLeases that reached TTL expiration",
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
	)
}
