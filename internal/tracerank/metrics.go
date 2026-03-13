package tracerank

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	computeDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "compute_duration_seconds",
		Help:      "Duration of TraceRank computation in seconds.",
		Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})

	computeNodes = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "compute_nodes",
		Help:      "Number of nodes in the last TraceRank computation.",
	})

	computeEdges = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "compute_edges",
		Help:      "Number of edges in the last TraceRank computation.",
	})

	computeIterations = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "compute_iterations",
		Help:      "Number of iterations in the last TraceRank computation.",
	})

	computeConverged = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "compute_converged",
		Help:      "Whether the last TraceRank computation converged (1=yes, 0=no).",
	})

	scoreMax = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "score_max",
		Help:      "Maximum TraceRank score in the last computation.",
	})

	scoreMean = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "tracerank",
		Name:      "score_mean",
		Help:      "Mean TraceRank score in the last computation.",
	})
)

// RecordMetrics updates Prometheus gauges from a computation result.
func RecordMetrics(r *ComputeResult) {
	computeDuration.Observe(r.Duration.Seconds())
	computeNodes.Set(float64(r.NodeCount))
	computeEdges.Set(float64(r.EdgeCount))
	computeIterations.Set(float64(r.Iterations))
	if r.Converged {
		computeConverged.Set(1)
	} else {
		computeConverged.Set(0)
	}

	if r.NodeCount > 0 {
		max := 0.0
		total := 0.0
		for _, s := range r.Scores {
			if s.GraphScore > max {
				max = s.GraphScore
			}
			total += s.GraphScore
		}
		scoreMax.Set(max)
		scoreMean.Set(total / float64(r.NodeCount))
	}
}
