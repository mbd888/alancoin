package gateway

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	gwSessionsCreated = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "sessions_created_total",
		Help:      "Total gateway sessions created.",
	})

	gwSessionsClosed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "sessions_closed_total",
		Help:      "Total gateway sessions closed by reason.",
	}, []string{"reason"}) // "client", "expired", "settlement_failed"

	gwProxyRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "proxy_requests_total",
		Help:      "Total proxy requests by outcome.",
	}, []string{"status"}) // "success", "forward_failed", "policy_denied", "budget_exceeded", "no_service", "rate_limited", "tenant_suspended"

	gwProxyLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "proxy_latency_seconds",
		Help:      "End-to-end proxy request latency in seconds.",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})

	gwSettlementAmount = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "settlement_amount_usdc",
		Help:      "Distribution of settlement amounts in USDC.",
		Buckets:   []float64{0.001, 0.01, 0.1, 1, 10, 100, 1000},
	})

	gwActiveSessions = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "active_sessions",
		Help:      "Number of currently active gateway sessions.",
	})

	gwPolicyDenials = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "policy_denials_total",
		Help:      "Total policy denials by rule type.",
	}, []string{"rule_type"})

	gwExpiredSessionsClosed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "expired_sessions_closed_total",
		Help:      "Total sessions auto-closed by expiry timer.",
	})

	gwPolicyShadowDenials = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "policy_shadow_denials_total",
		Help:      "Total shadow-mode policy denials by rule type (logged only, not enforced).",
	}, []string{"rule_type"})

	gwSettlementRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Subsystem: "gateway",
		Name:      "settlement_retries_total",
		Help:      "Total settlement retry attempts (each retry after initial failure).",
	})
)

func init() {
	prometheus.MustRegister(
		gwSessionsCreated,
		gwSessionsClosed,
		gwProxyRequests,
		gwProxyLatency,
		gwSettlementAmount,
		gwActiveSessions,
		gwPolicyDenials,
		gwExpiredSessionsClosed,
		gwPolicyShadowDenials,
		gwSettlementRetries,
	)
}

// parseDecimal converts a USDC decimal string to float64 for Prometheus metrics.
// This is intentionally float64 — rounding is acceptable for observability histograms.
func parseDecimal(s string) float64 {
	if s == "" {
		return 0
	}
	var f float64
	_, _ = fmt.Sscanf(s, "%f", &f)
	return f
}
