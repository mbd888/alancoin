package streams

import "github.com/prometheus/client_golang/prometheus"

var (
	streamsOpened = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "streams",
		Name:      "opened_total",
		Help:      "Total streams opened.",
	})

	streamsClosed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "streams",
		Name:      "closed_total",
		Help:      "Total streams closed by final status.",
	}, []string{"status"}) // "closed", "stale_closed", "disputed", "settlement_failed"

	streamTicksTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "streams",
		Name:      "ticks_total",
		Help:      "Total ticks recorded across all streams.",
	})

	streamDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "streams",
		Name:      "duration_seconds",
		Help:      "Time from stream open to close in seconds.",
		Buckets:   []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600},
	})

	streamSettlementAmount = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "streams",
		Name:      "settlement_amount_usdc",
		Help:      "Distribution of stream settlement amounts in USDC.",
		Buckets:   []float64{0.001, 0.01, 0.1, 1, 10, 100, 1000},
	})
)

func init() {
	prometheus.MustRegister(
		streamsOpened,
		streamsClosed,
		streamTicksTotal,
		streamDuration,
		streamSettlementAmount,
	)
}
