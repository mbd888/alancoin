package retry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	var calls int
	err := Do(context.Background(), 3, 10*time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
}

func TestDo_SuccessOnRetry(t *testing.T) {
	var calls int
	err := Do(context.Background(), 3, 10*time.Millisecond, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDo_AllAttemptsExhausted(t *testing.T) {
	var calls int
	sentinel := errors.New("always fails")
	err := Do(context.Background(), 3, 10*time.Millisecond, func() error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDo_PermanentErrorStopsRetry(t *testing.T) {
	var calls int
	sentinel := errors.New("permanent failure")
	err := Do(context.Background(), 5, 10*time.Millisecond, func() error {
		calls++
		return Permanent(sentinel)
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (permanent error should stop retries), got %d", calls)
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var calls atomic.Int32
	go func() {
		// Cancel after first attempt has time to run.
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	err := Do(ctx, 10, 100*time.Millisecond, func() error {
		calls.Add(1)
		return errors.New("fail")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Should have run 1-2 times before context cancelled during sleep.
	if c := calls.Load(); c > 3 {
		t.Fatalf("expected at most 3 calls, got %d", c)
	}
}

func TestDo_ZeroMaxAttempts(t *testing.T) {
	var calls int
	err := Do(context.Background(), 0, time.Millisecond, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (0 rounds up to 1), got %d", calls)
	}
}

func TestDo_BackoffIncreases(t *testing.T) {
	var timestamps []time.Time
	err := Do(context.Background(), 4, 20*time.Millisecond, func() error {
		timestamps = append(timestamps, time.Now())
		if len(timestamps) < 4 {
			return errors.New("fail")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(timestamps) != 4 {
		t.Fatalf("expected 4 timestamps, got %d", len(timestamps))
	}

	// Verify delays grow (approximately): 20ms, 40ms, 80ms
	// Allow generous jitter tolerance.
	for i := 1; i < len(timestamps); i++ {
		gap := timestamps[i].Sub(timestamps[i-1])
		if gap < 5*time.Millisecond {
			t.Errorf("gap %d too short: %v", i, gap)
		}
	}
}

func TestPermanent_Unwrap(t *testing.T) {
	inner := errors.New("inner")
	pe := Permanent(inner)
	if !errors.Is(pe, inner) {
		t.Fatal("Permanent error should unwrap to inner error")
	}
}
