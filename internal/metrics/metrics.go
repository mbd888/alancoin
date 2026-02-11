// Package metrics provides Prometheus instrumentation for the Alancoin platform.
package metrics

import (
	"context"
	"database/sql"
	"runtime"
	"time"

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

	// DBOpenConnections tracks open database connections.
	DBOpenConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin", Name: "db_open_connections",
		Help: "Number of open database connections.",
	})
	// DBIdleConnections tracks idle database connections.
	DBIdleConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin", Name: "db_idle_connections",
		Help: "Number of idle database connections.",
	})
	// DBInUseConnections tracks in-use database connections.
	DBInUseConnections = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin", Name: "db_in_use_connections",
		Help: "Number of in-use database connections.",
	})
	// DBWaitCount tracks the total number of connections waited for.
	DBWaitCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin", Name: "db_wait_count_total",
		Help: "Total number of connections waited for.",
	})
	// DBWaitDuration tracks total time waited for connections.
	DBWaitDuration = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin", Name: "db_wait_duration_seconds_total",
		Help: "Total time waited for connections in seconds.",
	})
	// GoroutineCount tracks the current number of goroutines.
	GoroutineCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin", Name: "goroutines",
		Help: "Current number of goroutines.",
	})

	// --- Negotiation metrics ---

	// RFPsPublishedTotal counts RFPs published.
	RFPsPublishedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "rfps_published_total",
		Help:      "Total RFPs published.",
	})

	// BidsPlacedTotal counts bids placed.
	BidsPlacedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "bids_placed_total",
		Help:      "Total bids placed on RFPs.",
	})

	// RFPsAwardedTotal counts RFPs awarded.
	RFPsAwardedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "rfps_awarded_total",
		Help:      "Total RFPs awarded to a winning bid.",
	})

	// RFPsExpiredTotal counts RFPs that expired.
	RFPsExpiredTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "rfps_expired_total",
		Help:      "Total RFPs that expired without a winner.",
	})

	// BidScoreHistogram observes bid scores.
	BidScoreHistogram = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Name:      "bid_score",
		Help:      "Distribution of bid scores (0.0–1.0).",
		Buckets:   []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0},
	})

	// TimeToAwardSeconds observes time from RFP creation to award.
	TimeToAwardSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Name:      "time_to_award_seconds",
		Help:      "Time from RFP publication to winner selection in seconds.",
		Buckets:   []float64{60, 300, 900, 3600, 14400, 43200, 86400, 259200, 604800},
	})

	// BidsWithdrawnTotal counts bids withdrawn.
	BidsWithdrawnTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "bids_withdrawn_total",
		Help:      "Total bids withdrawn by sellers.",
	})

	// BondsForfeitedTotal counts bonds forfeited.
	BondsForfeitedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "bonds_forfeited_total",
		Help:      "Total bid bonds forfeited due to withdrawal penalties.",
	})
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
		DBOpenConnections,
		DBIdleConnections,
		DBInUseConnections,
		DBWaitCount,
		DBWaitDuration,
		GoroutineCount,
		RFPsPublishedTotal,
		BidsPlacedTotal,
		RFPsAwardedTotal,
		RFPsExpiredTotal,
		BidScoreHistogram,
		TimeToAwardSeconds,
		BidsWithdrawnTotal,
		BondsForfeitedTotal,
	)
}

// StartDBStatsCollector periodically samples sql.DBStats and runtime goroutine
// count into Prometheus gauges. Call in a goroutine; exits when ctx is done.
func StartDBStatsCollector(ctx context.Context, db *sql.DB, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats := db.Stats()
			DBOpenConnections.Set(float64(stats.OpenConnections))
			DBIdleConnections.Set(float64(stats.Idle))
			DBInUseConnections.Set(float64(stats.InUse))
			DBWaitCount.Set(float64(stats.WaitCount))
			DBWaitDuration.Set(stats.WaitDuration.Seconds())
			GoroutineCount.Set(float64(runtime.NumGoroutine()))
		}
	}
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
