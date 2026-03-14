package gateway

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	gwProviderHealth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "provider_health",
		Help:      "Provider health state (1=active in this state, 0=not). States: healthy, degraded, unhealthy, unknown.",
	}, []string{"provider", "status"})

	gwRerouteTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "reroute_total",
		Help:      "Total requests rerouted from one provider to another due to failure.",
	}, []string{"from_provider", "to_provider"})

	gwRacerExpansionTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "racer_expansion_total",
		Help:      "Total requests where RACER expanded the candidate set due to low confidence.",
	})

	gwRequestOutcome = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "request_outcome_total",
		Help:      "Total request outcomes by type: succeeded (first provider), rerouted (failover), escalated (all failed).",
	}, []string{"outcome"})
)

func init() {
	prometheus.MustRegister(
		gwProviderHealth,
		gwRerouteTotal,
		gwRacerExpansionTotal,
		gwRequestOutcome,
	)
}

// updateProviderHealthMetric sets the gauge for a provider's health state.
// Only one state is set to 1 at a time; all others are reset to 0.
func updateProviderHealthMetric(providerID string, state HealthState) {
	states := []string{"healthy", "degraded", "unhealthy", "unknown"}
	for _, s := range states {
		val := float64(0)
		if s == state.String() {
			val = 1
		}
		gwProviderHealth.WithLabelValues(providerID, s).Set(val)
	}
}

// recordRerouteMetric increments the reroute counter.
func recordRerouteMetric(fromProvider, toProvider string) {
	gwRerouteTotal.WithLabelValues(fromProvider, toProvider).Inc()
}

// recordRacerExpansionMetric increments the RACER expansion counter.
func recordRacerExpansionMetric() {
	gwRacerExpansionTotal.Inc()
}

// recordRequestOutcomeMetric increments the request outcome counter.
func recordRequestOutcomeMetric(outcome RequestOutcome) {
	gwRequestOutcome.WithLabelValues(outcome.String()).Inc()
}
