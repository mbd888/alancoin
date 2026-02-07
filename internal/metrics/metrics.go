// Package metrics provides Prometheus instrumentation for the Alancoin platform.
package metrics

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTPRequestsTotal counts HTTP requests by method, path, and status.
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests by method, path pattern, and status code.",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration observes request latency by method and path.
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "alancoin",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// TransactionsTotal counts platform transactions by status.
	TransactionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "transactions_total",
			Help:      "Total transactions recorded by status.",
		},
		[]string{"status"},
	)

	// EscrowsTotal counts escrow operations by final status.
	EscrowsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "escrows_total",
			Help:      "Total escrow operations by status.",
		},
		[]string{"status"},
	)

	// WebhookDeliveriesTotal counts webhook delivery attempts by result.
	WebhookDeliveriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "webhook_deliveries_total",
			Help:      "Total webhook deliveries by result.",
		},
		[]string{"result"},
	)

	// ActiveSessionKeys tracks current active session keys.
	ActiveSessionKeys = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "alancoin",
			Name:      "active_session_keys",
			Help:      "Number of currently active session keys.",
		},
	)

	// ActiveWebSocketClients tracks connected WebSocket clients.
	ActiveWebSocketClients = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "alancoin",
			Name:      "active_websocket_clients",
			Help:      "Number of currently connected WebSocket clients.",
		},
	)
)

func init() {
	prometheus.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		TransactionsTotal,
		EscrowsTotal,
		WebhookDeliveriesTotal,
		ActiveSessionKeys,
		ActiveWebSocketClients,
	)
}

// Middleware returns a gin middleware that records request metrics.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		timer := prometheus.NewTimer(HTTPRequestDuration.WithLabelValues(
			c.Request.Method,
			c.FullPath(), // Uses route pattern, not actual path (avoids cardinality explosion)
		))

		c.Next()

		timer.ObserveDuration()
		HTTPRequestsTotal.WithLabelValues(
			c.Request.Method,
			c.FullPath(),
			statusBucket(c.Writer.Status()),
		).Inc()
	}
}

// Handler returns the Prometheus metrics HTTP handler for /metrics endpoint.
func Handler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// statusBucket groups HTTP status codes into buckets (2xx, 3xx, 4xx, 5xx).
func statusBucket(code int) string {
	switch {
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
