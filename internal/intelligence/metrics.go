package intelligence

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	computeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "intelligence",
		Name:      "compute_duration_seconds",
		Help:      "Duration of intelligence profile computation.",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30},
	})

	profilesComputed = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "intelligence",
		Name:      "profiles_computed_total",
		Help:      "Total intelligence profiles computed.",
	})

	avgCreditScoreGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "intelligence",
		Name:      "avg_credit_score",
		Help:      "Network average credit score.",
	})

	avgRiskScoreGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "intelligence",
		Name:      "avg_risk_score",
		Help:      "Network average risk score.",
	})

	realtimeUpdatesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "intelligence",
		Name:      "realtime_updates_total",
		Help:      "Total real-time profile updates from settlement events.",
	})
)

func recordComputeMetrics(profiles int, duration time.Duration, avgCredit, avgRisk float64) {
	computeDuration.Observe(duration.Seconds())
	profilesComputed.Add(float64(profiles))
	avgCreditScoreGauge.Set(avgCredit)
	avgRiskScoreGauge.Set(avgRisk)
}
