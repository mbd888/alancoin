package ledger

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	// LedgerOpsTotal counts ledger operations by type.
	LedgerOpsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "ledger_operations_total",
			Help:      "Total ledger operations by type.",
		},
		[]string{"type"},
	)

	// LedgerOpDuration observes operation latency by type.
	LedgerOpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "alancoin",
			Name:      "ledger_operation_duration_seconds",
			Help:      "Ledger operation duration in seconds.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"type"},
	)

	// LedgerBalanceAvailable tracks the sum of all available balances.
	LedgerBalanceAvailable = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "alancoin",
			Name:      "ledger_balance_available_total",
			Help:      "Sum of all agent available balances.",
		},
	)

	// LedgerBalancePending tracks the sum of all pending balances.
	LedgerBalancePending = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "alancoin",
			Name:      "ledger_balance_pending_total",
			Help:      "Sum of all agent pending balances.",
		},
	)

	// LedgerBalanceEscrowed tracks the sum of all escrowed balances.
	LedgerBalanceEscrowed = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "alancoin",
			Name:      "ledger_balance_escrowed_total",
			Help:      "Sum of all agent escrowed balances.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		LedgerOpsTotal,
		LedgerOpDuration,
		LedgerBalanceAvailable,
		LedgerBalancePending,
		LedgerBalanceEscrowed,
	)
}

// observeOp increments the operation counter and returns a function to observe duration.
func observeOp(opType string) func() {
	LedgerOpsTotal.WithLabelValues(opType).Inc()
	start := time.Now()
	return func() {
		LedgerOpDuration.WithLabelValues(opType).Observe(time.Since(start).Seconds())
	}
}
