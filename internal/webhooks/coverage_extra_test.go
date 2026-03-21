package webhooks

import (
	"context"
	"sync"
	"testing"
	"time"
)

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
