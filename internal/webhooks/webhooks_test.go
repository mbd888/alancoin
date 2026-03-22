package webhooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// noopValidator allows any URL (including loopback) for test servers.
func noopValidator(_ string) error { return nil }

// newTestDispatcher creates a dispatcher that skips SSRF checks for localhost test servers.
func newTestDispatcher(store Store) *Dispatcher {
	d := NewDispatcher(store)
	d.urlValidator = noopValidator
	return d
}

// ---------------------------------------------------------------------------
// MemoryStore tests
// ---------------------------------------------------------------------------

func TestMemoryStore_CRUD(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sub := &Subscription{
		ID:        "wh_test1",
		AgentAddr: "0xagent1",
		URL:       "https://example.com/hook",
		Secret:    "secret123",
		Events:    []EventType{EventPaymentReceived},
		Active:    true,
		CreatedAt: time.Now(),
	}

	// Create
	if err := store.Create(ctx, sub); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Get
	got, err := store.Get(ctx, "wh_test1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.URL != "https://example.com/hook" {
		t.Errorf("Expected URL, got %s", got.URL)
	}

	// Update
	sub.Active = false
	store.Update(ctx, sub)
	got, _ = store.Get(ctx, "wh_test1")
	if got.Active {
		t.Error("Expected inactive after update")
	}

	// Delete
	store.Delete(ctx, "wh_test1")
	_, err = store.Get(ctx, "wh_test1")
	if err == nil {
		t.Error("Expected error after delete")
	}
}

func TestMemoryStore_GetByAgent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &Subscription{ID: "wh1", AgentAddr: "0xa", Events: []EventType{EventPaymentReceived}})
	store.Create(ctx, &Subscription{ID: "wh2", AgentAddr: "0xb", Events: []EventType{EventPaymentReceived}})
	store.Create(ctx, &Subscription{ID: "wh3", AgentAddr: "0xa", Events: []EventType{EventPaymentSent}})

	subs, _ := store.GetByAgent(ctx, "0xa")
	if len(subs) != 2 {
		t.Errorf("Expected 2 subs for 0xa, got %d", len(subs))
	}
}

func TestMemoryStore_GetByEvent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &Subscription{ID: "wh1", Events: []EventType{EventPaymentReceived, EventBalanceDeposit}})
	store.Create(ctx, &Subscription{ID: "wh2", Events: []EventType{EventPaymentSent}})
	store.Create(ctx, &Subscription{ID: "wh3", Events: []EventType{EventPaymentReceived}})

	subs, _ := store.GetByEvent(ctx, EventPaymentReceived)
	if len(subs) != 2 {
		t.Errorf("Expected 2 subs for payment.received, got %d", len(subs))
	}
}

// ---------------------------------------------------------------------------
// Signature tests
// ---------------------------------------------------------------------------

func TestSign(t *testing.T) {
	d := newTestDispatcher(NewMemoryStore())

	payload := []byte(`{"type":"payment.received","data":{}}`)
	secret := "test_secret_key"

	sig := d.sign(payload, secret)

	// Verify manually
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(payload)
	expected := hex.EncodeToString(h.Sum(nil))

	if sig != expected {
		t.Errorf("Signature mismatch: got %s, want %s", sig, expected)
	}
}

func TestSign_DifferentSecrets(t *testing.T) {
	d := newTestDispatcher(NewMemoryStore())

	payload := []byte(`{"test": true}`)
	sig1 := d.sign(payload, "secret1")
	sig2 := d.sign(payload, "secret2")

	if sig1 == sig2 {
		t.Error("Different secrets should produce different signatures")
	}
}

// ---------------------------------------------------------------------------
// Dispatch tests
// ---------------------------------------------------------------------------

func TestDispatch_SendsToSubscribers(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := newTestDispatcher(store)
	event := &Event{
		Type:      EventPaymentReceived,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"amount": "5.00"},
	}

	err := d.Dispatch(ctx, event)
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	// Wait for async delivery
	time.Sleep(200 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("Expected 1 webhook delivery, got %d", received.Load())
	}
}

func TestDispatch_SkipsInactiveSubscribers(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: false, // Inactive
	})

	d := newTestDispatcher(store)
	d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	if received.Load() != 0 {
		t.Errorf("Expected 0 deliveries for inactive sub, got %d", received.Load())
	}
}

func TestDispatch_IncludesSignature(t *testing.T) {
	store := NewMemoryStore()

	var mu sync.Mutex
	var gotSig string
	var gotBody []byte
	secret := "test_webhook_secret" //nolint:gosec // test credential

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotSig = r.Header.Get("X-Alancoin-Signature")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
		Secret: secret,
	})

	d := newTestDispatcher(store)
	d.Dispatch(ctx, &Event{
		Type:      EventPaymentReceived,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"amount": "5.00"},
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if gotSig == "" {
		t.Fatal("Expected signature header")
	}

	// Verify signature
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(gotBody)
	expected := hex.EncodeToString(h.Sum(nil))

	if gotSig != expected {
		t.Errorf("Signature mismatch: %s != %s", gotSig, expected)
	}
}

func TestDispatch_IncludesEventHeaders(t *testing.T) {
	store := NewMemoryStore()

	var mu sync.Mutex
	var gotEventType string
	var gotTimestamp string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotEventType = r.Header.Get("X-Alancoin-Event")
		gotTimestamp = r.Header.Get("X-Alancoin-Timestamp")
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventBalanceDeposit},
		Active: true,
	})

	d := newTestDispatcher(store)
	d.Dispatch(ctx, &Event{Type: EventBalanceDeposit, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if gotEventType != "balance.deposit" {
		t.Errorf("Expected event type balance.deposit, got %s", gotEventType)
	}
	if gotTimestamp == "" {
		t.Error("Expected timestamp header")
	}
}

func TestDispatch_PayloadFormat(t *testing.T) {
	store := NewMemoryStore()

	var mu sync.Mutex
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := newTestDispatcher(store)
	d.Dispatch(ctx, &Event{
		Type:      EventPaymentReceived,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"from": "0xsender", "amount": "10.00"},
	})

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	var parsed Event
	if err := json.Unmarshal(gotBody, &parsed); err != nil {
		t.Fatalf("Failed to parse webhook payload: %v", err)
	}
	if parsed.Type != EventPaymentReceived {
		t.Errorf("Expected type payment.received, got %s", parsed.Type)
	}
}

func TestDispatch_ErrorUpdatesSubscription(t *testing.T) {
	store := NewMemoryStore()

	// Server that returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := NewDispatcherWithRetry(store, RetryConfig{
		MaxAttempts: 1,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
		MaxFailures: 50,
	})
	d.urlValidator = noopValidator
	d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	sub, _ := store.Get(ctx, "wh1")
	if sub.LastError == "" {
		t.Error("Expected lastError to be set after 500 response")
	}
}

func TestDispatch_SuccessUpdatesSubscription(t *testing.T) {
	store := NewMemoryStore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{
		ID:     "wh1",
		URL:    server.URL,
		Events: []EventType{EventPaymentReceived},
		Active: true,
	})

	d := newTestDispatcher(store)
	d.Dispatch(ctx, &Event{Type: EventPaymentReceived, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	sub, _ := store.Get(ctx, "wh1")
	if sub.LastSuccess == nil {
		t.Error("Expected lastSuccess to be set after successful delivery")
	}
	if sub.LastError != "" {
		t.Errorf("Expected no error after success, got %s", sub.LastError)
	}
}

// ---------------------------------------------------------------------------
// DispatchToAgent tests
// ---------------------------------------------------------------------------

func TestDispatchToAgent_FiltersCorrectly(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	// Agent A has 2 hooks
	store.Create(ctx, &Subscription{ID: "wh1", AgentAddr: "0xa", URL: server.URL, Events: []EventType{EventPaymentReceived}, Active: true})
	store.Create(ctx, &Subscription{ID: "wh2", AgentAddr: "0xa", URL: server.URL, Events: []EventType{EventPaymentSent}, Active: true})
	// Agent B has 1 hook
	store.Create(ctx, &Subscription{ID: "wh3", AgentAddr: "0xb", URL: server.URL, Events: []EventType{EventPaymentReceived}, Active: true})

	d := newTestDispatcher(store)
	d.DispatchToAgent(ctx, "0xa", &Event{Type: EventPaymentReceived, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	if received.Load() != 1 {
		t.Errorf("Expected 1 delivery (agent A, payment.received only), got %d", received.Load())
	}
}

func TestDispatchToAgent_NoMatchingEvents(t *testing.T) {
	store := NewMemoryStore()

	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()

	ctx := context.Background()
	store.Create(ctx, &Subscription{ID: "wh1", AgentAddr: "0xa", URL: server.URL, Events: []EventType{EventPaymentSent}, Active: true})

	d := newTestDispatcher(store)
	d.DispatchToAgent(ctx, "0xa", &Event{Type: EventPaymentReceived, Timestamp: time.Now()})

	time.Sleep(200 * time.Millisecond)

	if received.Load() != 0 {
		t.Errorf("Expected 0 deliveries for non-matching event, got %d", received.Load())
	}
}

// --- merged from coverage_extra_test.go ---

// ---------------------------------------------------------------------------
// Subscription.isSuspended additional coverage
// ---------------------------------------------------------------------------

func TestSubscription_IsSuspended_NilTime(t *testing.T) {
	s := &Subscription{SuspendedUntil: nil}
	if s.isSuspended() {
		t.Error("nil SuspendedUntil should not be suspended")
	}
}

// ---------------------------------------------------------------------------
// NewDispatcherWithRetry coverage
// ---------------------------------------------------------------------------

func TestNewDispatcherWithRetry_Custom(t *testing.T) {
	store := NewMemoryStore()
	cfg := RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    5 * time.Second,
		MaxFailures: 10,
	}
	d := NewDispatcherWithRetry(store, cfg)
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	if d.retry.MaxAttempts != 3 {
		t.Errorf("maxAttempts = %d, want 3", d.retry.MaxAttempts)
	}
}

// ---------------------------------------------------------------------------
// NewDispatcher coverage
// ---------------------------------------------------------------------------

func TestNewDispatcher_Defaults(t *testing.T) {
	store := NewMemoryStore()
	d := NewDispatcher(store)
	if d == nil {
		t.Fatal("expected non-nil dispatcher")
	}
	if d.retry.MaxAttempts != 5 {
		t.Errorf("default maxAttempts = %d, want 5", d.retry.MaxAttempts)
	}
}

// ---------------------------------------------------------------------------
// NewHandler coverage
// ---------------------------------------------------------------------------

func TestNewHandler_Creates(t *testing.T) {
	store := NewMemoryStore()
	d := NewDispatcher(store)
	h := NewHandler(store, d)
	if h == nil {
		t.Fatal("expected non-nil handler")
	}
}

// ---------------------------------------------------------------------------
// generateSecret coverage
// ---------------------------------------------------------------------------

func TestGenerateSecret_Unique(t *testing.T) {
	s1 := generateSecret()
	s2 := generateSecret()
	if s1 == s2 {
		t.Error("two generated secrets should not be identical")
	}
	if len(s1) != 64 { // 32 bytes = 64 hex chars
		t.Errorf("secret length = %d, want 64", len(s1))
	}
}

// ---------------------------------------------------------------------------
// EventType constants coverage
// ---------------------------------------------------------------------------

func TestEventTypeConstants(t *testing.T) {
	// Verify key event type strings are correct
	types := map[EventType]string{
		EventPaymentReceived:            "payment.received",
		EventPaymentSent:                "payment.sent",
		EventSessionKeyUsed:             "session_key.used",
		EventGatewaySessionCreated:      "gateway.session.created",
		EventEscrowCreated:              "escrow.created",
		EventStreamOpened:               "stream.opened",
		EventKYAIssued:                  "kya.certificate.issued",
		EventChargebackBudgetWarning:    "chargeback.budget.warning",
		EventArbitrationCaseFiled:       "arbitration.case.filed",
		EventForensicsAlertCritical:     "forensics.alert.critical",
		EventIntelligenceTierTransition: "intelligence.tier.transition",
		EventIntelligenceScoreAlert:     "intelligence.score.alert",
	}
	for et, expected := range types {
		if string(et) != expected {
			t.Errorf("%v = %q, want %q", et, string(et), expected)
		}
	}
}

// ---------------------------------------------------------------------------
// MemoryStore concurrent access
// ---------------------------------------------------------------------------

func TestMemoryStore_ConcurrentReadWrite(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		idx := i
		go func() {
			defer wg.Done()
			store.Create(ctx, &Subscription{
				ID:        "wh_" + intStr(idx),
				AgentAddr: "0xa",
				Events:    []EventType{EventPaymentReceived},
				Active:    true,
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = store.GetByAgent(ctx, "0xa")
			_, _ = store.GetByEvent(ctx, EventPaymentReceived)
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// MemoryStore Delete non-existent (no error)
// ---------------------------------------------------------------------------

func TestMemoryStore_DeleteNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Delete on empty store should not error
	err := store.Delete(ctx, "does-not-exist")
	if err != nil {
		t.Errorf("expected no error deleting non-existent, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore Update
// ---------------------------------------------------------------------------

func TestMemoryStore_UpdateFields(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	sub := &Subscription{
		ID:     "wh_upd",
		URL:    "https://example.com/hook",
		Active: true,
	}
	store.Create(ctx, sub)

	sub.Active = false
	sub.LastError = "timeout"
	sub.ConsecutiveFailures = 5
	store.Update(ctx, sub)

	got, _ := store.Get(ctx, "wh_upd")
	if got.Active {
		t.Error("expected inactive after update")
	}
	if got.ConsecutiveFailures != 5 {
		t.Errorf("failures = %d, want 5", got.ConsecutiveFailures)
	}
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
