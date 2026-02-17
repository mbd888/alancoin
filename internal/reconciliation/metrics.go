package reconciliation

import "github.com/prometheus/client_golang/prometheus"

var (
	reconcileLedgerMismatches = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "reconciliation",
		Name:      "ledger_mismatches",
		Help:      "Number of ledger balance mismatches found in last reconciliation run.",
	})

	reconcileStuckEscrows = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "reconciliation",
		Name:      "stuck_escrows",
		Help:      "Number of expired/stuck escrows found in last reconciliation run.",
	})

	reconcileStaleStreams = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "reconciliation",
		Name:      "stale_streams",
		Help:      "Number of stale open streams found in last reconciliation run.",
	})

	reconcileOrphanedHolds = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "reconciliation",
		Name:      "orphaned_holds",
		Help:      "Number of orphaned ledger holds found in last reconciliation run.",
	})

	reconcileDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "reconciliation",
		Name:      "run_duration_seconds",
		Help:      "Duration of reconciliation runs in seconds.",
		Buckets:   []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60},
	})

	reconcileErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "reconciliation",
		Name:      "errors_total",
		Help:      "Total reconciliation check errors.",
	})
)

func init() {
	prometheus.MustRegister(
		reconcileLedgerMismatches,
		reconcileStuckEscrows,
		reconcileStaleStreams,
		reconcileOrphanedHolds,
		reconcileDuration,
		reconcileErrors,
	)
}
