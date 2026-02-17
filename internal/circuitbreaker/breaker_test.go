package circuitbreaker

import (
	"sync"
	"testing"
	"time"
)

func TestBreaker_AllowWhenClosed(t *testing.T) {
	b := New(3, 100*time.Millisecond)
	if !b.Allow("svc1") {
		t.Fatal("expected closed circuit to allow")
	}
}

func TestBreaker_TripsAfterThreshold(t *testing.T) {
	b := New(3, 100*time.Millisecond)

	// 2 failures = still closed
	b.RecordFailure("svc1")
	b.RecordFailure("svc1")
	if !b.Allow("svc1") {
		t.Fatal("should still allow before threshold")
	}

	// 3rd failure = open
	b.RecordFailure("svc1")
	if b.Allow("svc1") {
		t.Fatal("should be open after 3 failures")
	}
	if b.State("svc1") != StateOpen {
		t.Fatalf("expected StateOpen, got %v", b.State("svc1"))
	}
}

func TestBreaker_OpenToHalfOpenAfterDuration(t *testing.T) {
	b := New(2, 50*time.Millisecond)

	b.RecordFailure("svc1")
	b.RecordFailure("svc1")
	if b.Allow("svc1") {
		t.Fatal("should be open")
	}

	// Wait for open duration.
	time.Sleep(60 * time.Millisecond)

	// Should transition to half-open and allow one probe.
	if !b.Allow("svc1") {
		t.Fatal("should allow probe in half-open")
	}
	if b.State("svc1") != StateHalfOpen {
		t.Fatalf("expected StateHalfOpen, got %v", b.State("svc1"))
	}

	// Second request while half-open should be rejected.
	if b.Allow("svc1") {
		t.Fatal("should reject second request in half-open")
	}
}

func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	b := New(2, 50*time.Millisecond)

	b.RecordFailure("svc1")
	b.RecordFailure("svc1")
	time.Sleep(60 * time.Millisecond)
	b.Allow("svc1") // Transitions to half-open

	b.RecordSuccess("svc1")
	if b.State("svc1") != StateClosed {
		t.Fatalf("expected StateClosed after success, got %v", b.State("svc1"))
	}
	if !b.Allow("svc1") {
		t.Fatal("should allow after recovery")
	}
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	b := New(2, 50*time.Millisecond)

	b.RecordFailure("svc1")
	b.RecordFailure("svc1")
	time.Sleep(60 * time.Millisecond)
	b.Allow("svc1") // Transitions to half-open

	b.RecordFailure("svc1")
	if b.State("svc1") != StateOpen {
		t.Fatalf("expected StateOpen after half-open failure, got %v", b.State("svc1"))
	}
}

func TestBreaker_SuccessResets(t *testing.T) {
	b := New(3, 100*time.Millisecond)

	b.RecordFailure("svc1")
	b.RecordFailure("svc1")
	b.RecordSuccess("svc1")

	// Should not trip with only 1 more failure (counter was reset).
	b.RecordFailure("svc1")
	if !b.Allow("svc1") {
		t.Fatal("should still be closed after reset")
	}
}

func TestBreaker_IndependentKeys(t *testing.T) {
	b := New(2, 100*time.Millisecond)

	b.RecordFailure("svc1")
	b.RecordFailure("svc1")

	// svc1 is open, svc2 should be unaffected.
	if b.Allow("svc1") {
		t.Fatal("svc1 should be open")
	}
	if !b.Allow("svc2") {
		t.Fatal("svc2 should be closed")
	}
}

func TestBreaker_UnknownKeyIsClosed(t *testing.T) {
	b := New(2, 100*time.Millisecond)
	if b.State("unknown") != StateClosed {
		t.Fatalf("expected StateClosed for unknown key, got %v", b.State("unknown"))
	}
}

func TestBreaker_OnTransitionCallback(t *testing.T) {
	b := New(2, 50*time.Millisecond)

	var mu sync.Mutex
	var transitions []struct{ from, to State }
	b.OnTransition(func(key string, from, to State) {
		mu.Lock()
		transitions = append(transitions, struct{ from, to State }{from, to})
		mu.Unlock()
	})

	b.RecordFailure("svc1")
	b.RecordFailure("svc1") // Should trigger closed→open.

	// Give goroutine time to run.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].from != StateClosed || transitions[0].to != StateOpen {
		t.Fatalf("expected closed→open, got %v→%v", transitions[0].from, transitions[0].to)
	}
	mu.Unlock()
}

func TestState_String(t *testing.T) {
	tests := []struct {
		s    State
		want string
	}{
		{StateClosed, "closed"},
		{StateOpen, "open"},
		{StateHalfOpen, "half_open"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}
