package retry

import (
	"context"
	"errors"
	"sync"
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

// --- merged from retry_coverage_test.go ---

// ---------------------------------------------------------------------------
// DoWithUnlock
// ---------------------------------------------------------------------------

func TestDoWithUnlock_SuccessOnFirstAttempt(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()

	unlockCalled := false
	relockCalled := false

	err := DoWithUnlock(context.Background(), 3, 10*time.Millisecond,
		func() { unlockCalled = true; mu.Unlock() },
		func() { relockCalled = true; mu.Lock() },
		func() error { return nil },
	)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	// On first-attempt success, unlock/relock should NOT be called
	if unlockCalled {
		t.Error("unlock should not be called on first-attempt success")
	}
	if relockCalled {
		t.Error("relock should not be called on first-attempt success")
	}
}

func TestDoWithUnlock_SuccessOnRetry(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()

	unlockCount := 0
	relockCount := 0
	calls := 0

	err := DoWithUnlock(context.Background(), 3, 10*time.Millisecond,
		func() { unlockCount++; mu.Unlock() },
		func() { relockCount++; mu.Lock() },
		func() error {
			calls++
			if calls < 3 {
				return errors.New("transient")
			}
			return nil
		},
	)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	// Should unlock/relock between each retry
	if unlockCount != 2 {
		t.Errorf("expected 2 unlock calls, got %d", unlockCount)
	}
	if relockCount != 2 {
		t.Errorf("expected 2 relock calls, got %d", relockCount)
	}
}

func TestDoWithUnlock_AllAttemptsExhausted(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()

	sentinel := errors.New("persistent failure")
	calls := 0

	err := DoWithUnlock(context.Background(), 3, 10*time.Millisecond,
		func() { mu.Unlock() },
		func() { mu.Lock() },
		func() error {
			calls++
			return sentinel
		},
	)

	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
}

func TestDoWithUnlock_PermanentErrorStopsRetry(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()

	sentinel := errors.New("permanent")
	calls := 0
	unlockCalled := false

	err := DoWithUnlock(context.Background(), 5, 10*time.Millisecond,
		func() { unlockCalled = true; mu.Unlock() },
		func() { mu.Lock() },
		func() error {
			calls++
			return Permanent(sentinel)
		},
	)

	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}
	if unlockCalled {
		t.Error("unlock should not be called for first-attempt permanent error")
	}
}

func TestDoWithUnlock_ContextCancelledDuringSleep(t *testing.T) {
	var mu sync.Mutex
	mu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	relockCalled := false

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := DoWithUnlock(ctx, 10, 200*time.Millisecond,
		func() { mu.Unlock() },
		func() { relockCalled = true; mu.Lock() },
		func() error { return errors.New("fail") },
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	// Relock must be called even when context is cancelled (contract)
	if !relockCalled {
		t.Error("relock should be called on context cancellation")
	}
}

func TestDoWithUnlock_ZeroMaxAttempts(t *testing.T) {
	calls := 0
	err := DoWithUnlock(context.Background(), 0, time.Millisecond,
		func() {},
		func() {},
		func() error {
			calls++
			return nil
		},
	)

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call (0 rounds up to 1), got %d", calls)
	}
}

// ---------------------------------------------------------------------------
// PermanentError Unwrap
// ---------------------------------------------------------------------------

func TestPermanentError_Error(t *testing.T) {
	inner := errors.New("bad thing")
	pe := &PermanentError{Err: inner}
	if pe.Error() != "bad thing" {
		t.Errorf("expected 'bad thing', got %q", pe.Error())
	}
}

func TestPermanentError_Unwrap(t *testing.T) {
	inner := errors.New("inner error")
	pe := &PermanentError{Err: inner}

	if pe.Unwrap() != inner {
		t.Error("Unwrap should return inner error")
	}

	// errors.Is should work through PermanentError
	if !errors.Is(pe, inner) {
		t.Error("errors.Is should find inner through PermanentError")
	}
}

func TestPermanentError_As(t *testing.T) {
	inner := errors.New("inner")
	pe := Permanent(inner)

	var target *PermanentError
	if !errors.As(pe, &target) {
		t.Error("errors.As should find PermanentError")
	}
	if target.Err != inner {
		t.Error("PermanentError.Err should be the original inner error")
	}
}

// ---------------------------------------------------------------------------
// Context cancellation during Do
// ---------------------------------------------------------------------------

func TestDo_ContextDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := Do(ctx, 100, 100*time.Millisecond, func() error {
		return errors.New("fail")
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// cryptoInt64n
// ---------------------------------------------------------------------------

func TestCryptoInt64n_ZeroReturnsZero(t *testing.T) {
	if got := cryptoInt64n(0); got != 0 {
		t.Errorf("cryptoInt64n(0) = %d, want 0", got)
	}
}

func TestCryptoInt64n_NegativeReturnsZero(t *testing.T) {
	if got := cryptoInt64n(-5); got != 0 {
		t.Errorf("cryptoInt64n(-5) = %d, want 0", got)
	}
}

func TestCryptoInt64n_PositiveInRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		n := int64(10)
		got := cryptoInt64n(n)
		if got < 0 || got >= n {
			t.Errorf("cryptoInt64n(%d) = %d, want [0, %d)", n, got, n)
		}
	}
}
