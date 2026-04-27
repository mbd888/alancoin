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

	// --- Escrow metrics (extended) ---

	EscrowCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "escrow_created_total",
		Help:      "Total escrows created.",
	})

	EscrowConfirmedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "escrow_confirmed_total",
		Help:      "Total escrows confirmed (funds released to seller).",
	})

	EscrowDisputedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "escrow_disputed_total",
		Help:      "Total escrows disputed (funds refunded to buyer).",
	})

	EscrowAutoReleasedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "escrow_auto_released_total",
		Help:      "Total escrows auto-released after timeout.",
	})

	EscrowDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Name:      "escrow_duration_seconds",
		Help:      "Time from escrow creation to resolution in seconds.",
		Buckets:   []float64{10, 30, 60, 120, 300, 600, 1800, 3600, 86400},
	})

	// --- Workflow metrics ---

	WorkflowCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "workflow_created_total",
		Help:      "Total workflows created.",
	})

	WorkflowCompletedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "workflow_completed_total",
		Help:      "Total workflows completed.",
	})

	WorkflowAbortedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "workflow_aborted_total",
		Help:      "Total workflows aborted.",
	})

	WorkflowBreakerTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "workflow_circuit_broken_total",
		Help:      "Total workflows halted by circuit breaker.",
	})

	// --- Coalition escrow metrics ---

	CoalitionCreatedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "coalition_created_total",
		Help:      "Total coalition escrows created.",
	})

	CoalitionSettledTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "coalition_settled_total",
		Help:      "Total coalition escrows settled by oracle.",
	})

	CoalitionExpiredTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "coalition_expired_total",
		Help:      "Total coalition escrows auto-expired.",
	})

	CoalitionDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: "alancoin",
		Name:      "coalition_duration_seconds",
		Help:      "Time from coalition creation to settlement in seconds.",
		Buckets:   []float64{10, 30, 60, 120, 300, 600, 1800, 3600, 86400},
	})

	// --- Session key metrics (extended) ---

	SessionKeyTransactionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "sessionkey_transactions_total",
		Help:      "Total session key transactions processed.",
	})

	// --- Enterprise plugin metrics ---

	KYACertificatesIssuedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "kya_certificates_issued_total",
		Help:      "Total KYA certificates issued.",
	})

	KYACertificatesRevokedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "kya_certificates_revoked_total",
		Help:      "Total KYA certificates revoked.",
	})

	ChargebackSpendTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "chargeback_spend_total",
			Help:      "Total spend recorded through chargeback engine by service type.",
		},
		[]string{"service_type"},
	)

	ChargebackBudgetExceededTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "chargeback_budget_exceeded_total",
		Help:      "Total cost center budget exceeded events.",
	})

	ArbitrationCasesFiledTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "arbitration_cases_filed_total",
		Help:      "Total arbitration cases filed.",
	})

	ArbitrationCasesResolvedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "arbitration_cases_resolved_total",
			Help:      "Total arbitration cases resolved by outcome.",
		},
		[]string{"outcome"},
	)

	ForensicsAlertsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "forensics_alerts_total",
			Help:      "Total forensic alerts by severity.",
		},
		[]string{"severity"},
	)

	ForensicsEventsIngested = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "forensics_events_ingested_total",
		Help:      "Total spend events ingested by forensics engine.",
	})

	// --- Event bus metrics ---

	EventBusPublished = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_published_total",
		Help:      "Total events published to the event bus.",
	})

	EventBusConsumed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_consumed_total",
		Help:      "Total events consumed from the event bus.",
	})

	EventBusDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_dropped_total",
		Help:      "Total events dropped due to full buffer.",
	})

	EventBusErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_consumer_errors_total",
		Help:      "Total consumer handler errors.",
	})

	EventBusBatchesProcessed = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_batches_processed_total",
		Help:      "Total batches processed by consumers.",
	})

	EventBusPending = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Name:      "eventbus_pending",
		Help:      "Current number of events pending in the bus.",
	})

	EventBusRetries = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_retries_total",
		Help:      "Total event bus consumer retry attempts.",
	})

	EventBusDeadLettered = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "eventbus_dead_lettered_total",
		Help:      "Total events moved to dead letter queue after retry exhaustion.",
	})

	MatviewRefreshDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "alancoin",
			Name:      "matview_refresh_duration_seconds",
			Help:      "Duration of materialized view refreshes.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		},
		[]string{"view"},
	)

	// Redis distributed state metrics
	RedisOpDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "alancoin",
			Name:      "redis_operation_duration_seconds",
			Help:      "Redis operation latency by operation type.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.5},
		},
		[]string{"operation"},
	)

	RedisErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "redis_errors_total",
			Help:      "Total Redis operation errors by operation type.",
		},
		[]string{"operation"},
	)

	RedisFallbacks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "redis_fallbacks_total",
			Help:      "Total times Redis failed and in-memory fallback was used.",
		},
		[]string{"component"},
	)

	// Outbox and CDC lag metrics
	OutboxPollLag = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Name:      "outbox_poll_lag_seconds",
		Help:      "Age of the oldest unpublished event in the outbox.",
	})

	CDCPollLag = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "alancoin",
		Name:      "cdc_poll_lag_seconds",
		Help:      "Age of the oldest unprocessed ledger entry for CDC.",
	})

	CleanupDeletedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "alancoin",
			Name:      "eventbus_cleanup_deleted_total",
			Help:      "Total rows deleted by cleanup worker.",
		},
		[]string{"component"},
	)

	// WatcherReorgsDetected counts chain reorgs detected by the deposit watcher.
	// Incremented when a stored block hash diverges from the chain's canonical hash.
	WatcherReorgsDetected = prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: "alancoin",
		Name:      "watcher_reorgs_detected_total",
		Help:      "Total chain reorgs detected by the deposit watcher (rewinds triggered).",
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
		EscrowCreatedTotal,
		EscrowConfirmedTotal,
		EscrowDisputedTotal,
		EscrowAutoReleasedTotal,
		EscrowDuration,
		WorkflowCreatedTotal,
		WorkflowCompletedTotal,
		WorkflowAbortedTotal,
		WorkflowBreakerTotal,
		CoalitionCreatedTotal,
		CoalitionSettledTotal,
		CoalitionExpiredTotal,
		CoalitionDuration,
		SessionKeyTransactionsTotal,
		KYACertificatesIssuedTotal,
		KYACertificatesRevokedTotal,
		ChargebackSpendTotal,
		ChargebackBudgetExceededTotal,
		ArbitrationCasesFiledTotal,
		ArbitrationCasesResolvedTotal,
		ForensicsAlertsTotal,
		ForensicsEventsIngested,
		EventBusPublished,
		EventBusConsumed,
		EventBusDropped,
		EventBusErrors,
		EventBusBatchesProcessed,
		EventBusPending,
		EventBusRetries,
		EventBusDeadLettered,
		MatviewRefreshDuration,
		RedisOpDuration,
		RedisErrors,
		RedisFallbacks,
		OutboxPollLag,
		CDCPollLag,
		CleanupDeletedTotal,
		WatcherReorgsDetected,
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
