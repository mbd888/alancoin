package metrics

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// StartDBStatsCollector
// ---------------------------------------------------------------------------

func TestStartDBStatsCollector_ExitsOnContextCancel(t *testing.T) {
	// sql.Open with the postgres driver registers the driver but doesn't connect.
	// Stats() works on an unopened DB as long as the *sql.DB is non-nil.
	db, err := openTestDB()
	if err != nil {
		t.Skipf("skipping: cannot open sql.DB stub: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		StartDBStatsCollector(ctx, db, 20*time.Millisecond)
		close(done)
	}()

	// Let it tick at least once
	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-done:
		// Collector exited
	case <-time.After(2 * time.Second):
		t.Error("StartDBStatsCollector did not exit after context cancel")
	}
}

// openTestDB creates a non-nil *sql.DB that supports Stats() without a real database.
func openTestDB() (*sql.DB, error) {
	return sql.Open("postgres", "postgres://localhost/nonexist?sslmode=disable")
}

// ---------------------------------------------------------------------------
// statusBucket extended
// ---------------------------------------------------------------------------

func TestStatusBucket_AllBuckets(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{99, "1xx"},
		{100, "1xx"},
		{199, "1xx"},
		{200, "2xx"},
		{299, "2xx"},
		{300, "3xx"},
		{399, "3xx"},
		{400, "4xx"},
		{499, "4xx"},
		{500, "5xx"},
		{599, "5xx"},
		{600, "5xx"},
	}

	for _, tt := range tests {
		if got := statusBucket(tt.code); got != tt.want {
			t.Errorf("statusBucket(%d) = %s, want %s", tt.code, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Middleware records both counter and histogram
// ---------------------------------------------------------------------------

func TestMiddleware_RecordsCounterAndHistogram(t *testing.T) {
	r := gin.New()
	r.Use(Middleware())
	r.GET("/api/test", func(c *gin.Context) {
		c.JSON(200, gin.H{"ok": true})
	})
	r.POST("/api/create", func(c *gin.Context) {
		c.JSON(201, gin.H{"created": true})
	})

	// Trigger some requests
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/test", nil)
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", w.Code)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/create", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Errorf("Expected 201, got %d", w.Code)
	}

	// Verify metrics endpoint includes our counters
	metricsRouter := gin.New()
	metricsRouter.GET("/metrics", Handler())
	mw := httptest.NewRecorder()
	mreq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRouter.ServeHTTP(mw, mreq)

	body := mw.Body.String()
	if !strings.Contains(body, "alancoin_http_requests_total") {
		t.Error("Expected alancoin_http_requests_total in metrics output")
	}
	if !strings.Contains(body, "alancoin_http_request_duration_seconds") {
		t.Error("Expected alancoin_http_request_duration_seconds in metrics output")
	}
}

// ---------------------------------------------------------------------------
// Handler returns valid prometheus output
// ---------------------------------------------------------------------------

func TestHandler_ReturnsPrometheusFormat(t *testing.T) {
	r := gin.New()
	r.GET("/metrics", Handler())

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	// Prometheus output should contain HELP or TYPE lines
	body := w.Body.String()
	if !strings.Contains(body, "# HELP") || !strings.Contains(body, "# TYPE") {
		t.Error("Expected Prometheus format with HELP and TYPE lines")
	}
}

// ---------------------------------------------------------------------------
// Metric registration — spot check counters/gauges exist
// ---------------------------------------------------------------------------

func TestMetricRegistration_CountersAndGauges(t *testing.T) {
	// These are package-level vars registered in init(). Verify they are non-nil.
	counters := []interface{}{
		HTTPRequestsTotal,
		TransactionsTotal,
		EscrowsTotal,
		WebhookDeliveriesTotal,
		EscrowCreatedTotal,
		EscrowConfirmedTotal,
		EscrowDisputedTotal,
		EscrowAutoReleasedTotal,
		WorkflowCreatedTotal,
		WorkflowCompletedTotal,
		WorkflowAbortedTotal,
		WorkflowBreakerTotal,
		CoalitionCreatedTotal,
		CoalitionSettledTotal,
		CoalitionExpiredTotal,
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
		EventBusRetries,
		EventBusDeadLettered,
	}

	for i, c := range counters {
		if c == nil {
			t.Errorf("Counter/CounterVec at index %d is nil", i)
		}
	}

	gauges := []interface{}{
		ActiveSessionKeys,
		ActiveWebSocketClients,
		DBOpenConnections,
		DBIdleConnections,
		DBInUseConnections,
		DBWaitCount,
		DBWaitDuration,
		GoroutineCount,
		EventBusPending,
	}

	for i, g := range gauges {
		if g == nil {
			t.Errorf("Gauge at index %d is nil", i)
		}
	}

	histograms := []interface{}{
		HTTPRequestDuration,
		EscrowDuration,
		CoalitionDuration,
		MatviewRefreshDuration,
	}

	for i, h := range histograms {
		if h == nil {
			t.Errorf("Histogram at index %d is nil", i)
		}
	}
}

// ---------------------------------------------------------------------------
// Exercise counter increments (ensures no registration panics)
// ---------------------------------------------------------------------------

func TestMetricIncrements_NoPanic(t *testing.T) {
	// Increment various counters/gauges to ensure they were registered properly
	EscrowCreatedTotal.Inc()
	EscrowConfirmedTotal.Inc()
	EscrowDisputedTotal.Inc()
	EscrowAutoReleasedTotal.Inc()
	WorkflowCreatedTotal.Inc()
	WorkflowCompletedTotal.Inc()
	WorkflowAbortedTotal.Inc()
	WorkflowBreakerTotal.Inc()
	CoalitionCreatedTotal.Inc()
	CoalitionSettledTotal.Inc()
	CoalitionExpiredTotal.Inc()
	SessionKeyTransactionsTotal.Inc()
	KYACertificatesIssuedTotal.Inc()
	KYACertificatesRevokedTotal.Inc()
	ChargebackBudgetExceededTotal.Inc()
	ArbitrationCasesFiledTotal.Inc()
	ForensicsEventsIngested.Inc()
	EventBusPublished.Inc()
	EventBusConsumed.Inc()
	EventBusDropped.Inc()
	EventBusErrors.Inc()
	EventBusBatchesProcessed.Inc()
	EventBusRetries.Inc()
	EventBusDeadLettered.Inc()

	// Gauge set
	ActiveSessionKeys.Set(5)
	ActiveWebSocketClients.Set(10)
	GoroutineCount.Set(50)
	EventBusPending.Set(3)

	// Histogram observe
	EscrowDuration.Observe(1.5)
	CoalitionDuration.Observe(60.0)

	// CounterVec with labels
	ChargebackSpendTotal.WithLabelValues("compute").Add(100)
	ArbitrationCasesResolvedTotal.WithLabelValues("buyer_wins").Inc()
	ForensicsAlertsTotal.WithLabelValues("high").Inc()
	MatviewRefreshDuration.WithLabelValues("agent_stats").Observe(0.5)
}
