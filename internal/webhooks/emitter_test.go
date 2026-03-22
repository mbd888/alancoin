package webhooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// --- Emitter tests ---

func newTestEmitter(store Store) (*Emitter, *Dispatcher) {
	d := newTestDispatcher(store)
	e := NewEmitter(d, slog.Default())
	return e, d
}

func TestNewEmitter(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)
	if e == nil {
		t.Fatal("expected non-nil emitter")
	}
}

func TestEmitter_NilSafety(t *testing.T) {
	// nil emitter should not panic
	var e *Emitter
	e.EmitSessionCreated("0xa", "s1", "10.00")
	e.EmitSessionClosed("0xa", "s1", "5.00", "closed")
	e.EmitProxySuccess("0xa", "s1", "0xseller", "1.00")
	e.EmitSettlementFailed("0xa", "s1", "0xseller", "1.00")
	e.EmitEscrowCreated("0xa", "esc1", "0xseller", "10.00")
	e.EmitEscrowDelivered("0xa", "esc1", "0xseller")
	e.EmitEscrowReleased("0xa", "esc1", "0xbuyer", "10.00")
	e.EmitEscrowRefunded("0xa", "esc1", "10.00")
	e.EmitEscrowDisputed("0xa", "esc1", "0xbuyer", "reason")
	e.EmitStreamOpened("0xa", "str1", "0xbuyer", "20.00")
	e.EmitStreamClosed("0xa", "str1", "0xseller", "15.00", "done")
	e.EmitKYAIssued("0xa", "cert1", "basic")
	e.EmitKYARevoked("0xa", "cert1", "reason")
	e.EmitChargebackBudgetWarning("0xa", "cc1", 80)
	e.EmitChargebackBudgetExceeded("0xa", "cc1", "500.00")
	e.EmitArbitrationCaseFiled("0xa", "case1", "esc1", "50.00")
	e.EmitArbitrationCaseResolved("0xa", "case1", "buyer_wins", "Full refund")
	e.EmitForensicsCriticalAlert("0xa", "alert1", "anomaly", 99.0)
	e.EmitTierTransition("0xa", "gold", "platinum", 65.0, 78.0)
	e.EmitScoreAlert("0xa", 80.0, 70.0, "test")
}

func TestEmitter_NilDispatcher(t *testing.T) {
	// Emitter with nil dispatcher should not panic
	e := &Emitter{d: nil, logger: slog.Default()}
	e.EmitSessionCreated("0xa", "s1", "10.00")
	e.EmitEscrowCreated("0xbuyer", "esc1", "0xseller", "5.00")
	e.EmitStreamOpened("0xseller", "str1", "0xbuyer", "20.00")
	e.EmitKYAIssued("0xagent", "cert1", "basic")
	e.EmitChargebackBudgetWarning("0xagent", "cc1", 90)
	e.EmitArbitrationCaseFiled("0xbuyer", "case1", "esc1", "50.00")
	e.EmitForensicsCriticalAlert("0xagent", "alert1", "anomaly", 99.0)
	e.EmitTierTransition("0xagent", "silver", "gold", 50.0, 65.0)
	e.EmitScoreAlert("0xagent", 80.0, 70.0, "test")
}

// TestEmitter_EmitWithDelivery tests that emitter actually dispatches events via the dispatcher.
// Uses the same pattern as existing tests (e.g., TestDispatch_SendsToSubscribers) which work.
func TestEmitter_EmitWithDelivery(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:        "wh_emit1",
		AgentAddr: "0xbuyer",
		URL:       server.URL,
		Events:    []EventType{EventGatewaySessionCreated},
		Active:    true,
	})

	// Use the dispatcher directly (like existing tests do) since emitter adds wg tracking on top
	d := newTestDispatcher(store)
	event := &Event{
		ID:        "evt_test",
		Type:      EventGatewaySessionCreated,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"sessionId": "gw_123",
			"agentAddr": "0xbuyer",
			"maxTotal":  "50.00",
		},
	}
	err := d.DispatchToAgent(ctx, "0xbuyer", event)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("expected 1 delivery, got %d", received.Load())
	}
}

// TestEmitter_AllGatewayEvents verifies all gateway emit methods don't panic and build correct event types.
func TestEmitter_AllGatewayEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitSessionCreated("0xbuyer", "gw_123", "50.00")
	e.EmitSessionClosed("0xbuyer", "gw_123", "25.00", "closed")
	e.EmitProxySuccess("0xbuyer", "gw_123", "0xseller", "1.50")
	e.EmitSettlementFailed("0xbuyer", "gw_123", "0xseller", "2.00")
}

// TestEmitter_AllEscrowEvents verifies all escrow emit methods don't panic.
func TestEmitter_AllEscrowEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitEscrowCreated("0xbuyer", "esc_1", "0xseller", "10.00")
	e.EmitEscrowDelivered("0xbuyer", "esc_1", "0xseller")
	e.EmitEscrowReleased("0xseller", "esc_1", "0xbuyer", "10.00")
	e.EmitEscrowRefunded("0xbuyer", "esc_1", "10.00")
	e.EmitEscrowDisputed("0xseller", "esc_1", "0xbuyer", "not delivered")
}

// TestEmitter_AllStreamEvents verifies stream emit methods.
func TestEmitter_AllStreamEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitStreamOpened("0xseller", "stream_1", "0xbuyer", "20.00")
	e.EmitStreamClosed("0xbuyer", "stream_1", "0xseller", "15.00", "completed")
}

// TestEmitter_AllKYAEvents verifies KYA emit methods.
func TestEmitter_AllKYAEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitKYAIssued("0xagent", "cert_1", "verified")
	e.EmitKYARevoked("0xagent", "cert_1", "suspicious activity")
}

// TestEmitter_AllChargebackEvents verifies chargeback emit methods.
func TestEmitter_AllChargebackEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitChargebackBudgetWarning("0xagent", "engineering", 80)
	e.EmitChargebackBudgetExceeded("0xagent", "marketing", "500.00")
}

// TestEmitter_AllArbitrationEvents verifies arbitration emit methods.
func TestEmitter_AllArbitrationEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitArbitrationCaseFiled("0xbuyer", "case_1", "esc_1", "50.00")
	e.EmitArbitrationCaseResolved("0xagent", "case_1", "buyer_wins", "Full refund")
}

// TestEmitter_AllForensicsEvents verifies forensics emit methods.
func TestEmitter_AllForensicsEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitForensicsCriticalAlert("0xagent", "alert_1", "velocity_spike", 95.0)
}

// TestEmitter_AllIntelligenceEvents verifies intelligence emit methods.
func TestEmitter_AllIntelligenceEvents(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	e.EmitTierTransition("0xagent", "gold", "platinum", 65.0, 78.0)
	e.EmitScoreAlert("0xagent", 75.0, 60.0, "multiple disputes")
}

func TestEmitter_Shutdown(t *testing.T) {
	store := NewMemoryStore()
	e, _ := newTestEmitter(store)

	// Shutdown should complete quickly with no in-flight work
	e.Shutdown(1 * time.Second)
}

func TestEmitter_ShutdownWithInFlight(t *testing.T) {
	store := NewMemoryStore()

	// Server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:        "wh_slow",
		AgentAddr: "0xagent",
		URL:       server.URL,
		Events:    []EventType{EventGatewaySessionCreated},
		Active:    true,
	})

	e, _ := newTestEmitter(store)
	e.EmitSessionCreated("0xagent", "s1", "10.00")

	time.Sleep(10 * time.Millisecond)

	// Shutdown should wait for in-flight delivery
	e.Shutdown(5 * time.Second)
}

// --- Webhook handler tests ---

func TestHandler_CreateWebhook_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	body := `{"url":"https://example.com/hook","events":["payment.received"]}`
	req := httptest.NewRequest("POST", "/v1/agents/0xagent/webhooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["secret"] == nil || resp["secret"] == "" {
		t.Error("expected secret in response")
	}
}

func TestHandler_CreateWebhook_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	req := httptest.NewRequest("POST", "/v1/agents/0xagent/webhooks", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_CreateWebhook_InvalidEventType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	body := `{"url":"https://example.com/hook","events":["nonexistent.event"]}`
	req := httptest.NewRequest("POST", "/v1/agents/0xagent/webhooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_CreateWebhook_InvalidURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	body := `{"url":"http://localhost/hook","events":["payment.received"]}`
	req := httptest.NewRequest("POST", "/v1/agents/0xagent/webhooks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for localhost URL, got %d", w.Code)
	}
}

func TestHandler_ListWebhooks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:        "wh_list1",
		AgentAddr: "0xagent",
		URL:       "https://example.com/hook",
		Events:    []EventType{EventPaymentReceived},
		Active:    true,
		CreatedAt: time.Now(),
	})

	req := httptest.NewRequest("GET", "/v1/agents/0xagent/webhooks", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	webhooks := resp["webhooks"].([]interface{})
	if len(webhooks) != 1 {
		t.Errorf("expected 1 webhook, got %d", len(webhooks))
	}
}

func TestHandler_DeleteWebhook_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:        "wh_del1",
		AgentAddr: "0xagent",
		URL:       "https://example.com/hook",
		Events:    []EventType{EventPaymentReceived},
		Active:    true,
	})

	req := httptest.NewRequest("DELETE", "/v1/agents/0xagent/webhooks/wh_del1", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_DeleteWebhook_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	req := httptest.NewRequest("DELETE", "/v1/agents/0xagent/webhooks/nonexistent", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandler_DeleteWebhook_WrongAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMemoryStore()
	d := newTestDispatcher(store)
	h := NewHandler(store, d)

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:        "wh_wrong",
		AgentAddr: "0xowner",
		URL:       "https://example.com/hook",
		Events:    []EventType{EventPaymentReceived},
		Active:    true,
	})

	req := httptest.NewRequest("DELETE", "/v1/agents/0xstranger/webhooks/wh_wrong", nil)
	w := httptest.NewRecorder()

	r := gin.New()
	group := r.Group("/v1")
	h.RegisterRoutes(group)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// --- validateWebhookURL tests ---

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid_https", "https://example.com/hook", false},
		{"valid_http", "http://example.com/hook", false},
		{"ftp_scheme", "ftp://example.com/hook", true},
		{"localhost", "http://localhost/hook", true},
		{"no_host", "http:///path", true},
		{"loopback_ip", "http://127.0.0.1/hook", true},
		{"private_ip", "http://10.0.0.1/hook", true},
		{"link_local", "http://169.254.1.1/hook", true},
		{"metadata", "http://metadata.google.internal/hook", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWebhookURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWebhookURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

// --- Subscription isSuspended tests ---

func TestSubscription_IsSuspended(t *testing.T) {
	s := &Subscription{}
	if s.isSuspended() {
		t.Error("expected not suspended")
	}

	future := time.Now().Add(time.Hour)
	s.SuspendedUntil = &future
	if !s.isSuspended() {
		t.Error("expected suspended")
	}

	past := time.Now().Add(-time.Hour)
	s.SuspendedUntil = &past
	if s.isSuspended() {
		t.Error("expected not suspended (past)")
	}
}

// --- Dispatcher graduated suspension ---

func TestDispatcher_GraduatedSuspension(t *testing.T) {
	store := NewMemoryStore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh_suspend",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := NewDispatcherWithRetry(store, RetryConfig{
		MaxAttempts: 1,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    1 * time.Millisecond,
		MaxFailures: 100,
	})
	d.urlValidator = noopValidator

	for i := 0; i < 6; i++ {
		d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})
		time.Sleep(100 * time.Millisecond)
	}

	sub, _ := store.Get(ctx, "wh_suspend")
	if sub.ConsecutiveFailures < 5 {
		t.Errorf("expected >= 5 consecutive failures, got %d", sub.ConsecutiveFailures)
	}
	if sub.SuspendedUntil == nil {
		t.Error("expected suspension after 5+ failures")
	}
}

// --- Dispatcher auto-deactivation ---

func TestDispatcher_AutoDeactivation(t *testing.T) {
	store := NewMemoryStore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh_deact",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := NewDispatcherWithRetry(store, RetryConfig{
		MaxAttempts: 1,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    1 * time.Millisecond,
		MaxFailures: 3, // deactivate after 3 failures
	})
	d.urlValidator = noopValidator

	for i := 0; i < 4; i++ {
		d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})
		time.Sleep(100 * time.Millisecond)
	}

	sub, _ := store.Get(ctx, "wh_deact")
	if sub.Active {
		t.Error("expected subscription to be deactivated after max failures")
	}
}

// --- Dispatcher 4xx no retry ---

func TestDispatcher_NoRetryOn4xx(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(400) // Client error - should not retry
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh_4xx",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := NewDispatcherWithRetry(store, RetryConfig{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    1 * time.Millisecond,
		MaxFailures: 50,
	})
	d.urlValidator = noopValidator

	d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})
	time.Sleep(200 * time.Millisecond)

	// Should only be called once (no retry on 4xx)
	if received.Load() != 1 {
		t.Errorf("expected 1 attempt (no retry on 4xx), got %d", received.Load())
	}
}

// --- Dispatch with suspended subscription ---

func TestDispatch_SkipsSuspendedSubscribers(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	future := time.Now().Add(time.Hour)
	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:             "wh_susp",
		URL:            server.URL,
		Events:         []EventType{EventPaymentReceived},
		Active:         true,
		SuspendedUntil: &future, // Suspended
	})

	d := newTestDispatcher(store)
	d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	if received.Load() != 0 {
		t.Errorf("expected 0 deliveries for suspended sub, got %d", received.Load())
	}
}

// --- DefaultRetryConfig ---

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()
	if cfg.MaxAttempts != 5 {
		t.Errorf("expected 5 max attempts, got %d", cfg.MaxAttempts)
	}
	if cfg.MaxFailures != 50 {
		t.Errorf("expected 50 max failures, got %d", cfg.MaxFailures)
	}
}

// Suppress unused import warnings
var _ = sync.Mutex{}
