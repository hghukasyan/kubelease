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
	// ActiveLeases is the number of EnvironmentLeases currently in Active phase.
	// Updated by the controller when status transitions occur.
	ActiveLeases = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "kubelease_active_leases",
		Help: "Number of EnvironmentLeases currently in Active phase",
	})

	// ExpiredLeasesTotal counts leases that have reached TTL expiration.
	ExpiredLeasesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_expired_leases_total",
		Help: "Total number of EnvironmentLeases that reached TTL expiration",
	})

	// CleanupFailuresTotal counts failed cleanup attempts.
	CleanupFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_cleanup_failures_total",
		Help: "Total number of failed environment cleanup attempts",
	})

	// ProvisionFailuresTotal counts failed provisioning attempts.
	ProvisionFailuresTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "kubelease_provision_failures_total",
		Help: "Total number of failed environment provisioning attempts",
	})
)

func init() {
	metrics.Registry.MustRegister(
		ActiveLeases,
		ExpiredLeasesTotal,
		CleanupFailuresTotal,
		ProvisionFailuresTotal,
	)
}
