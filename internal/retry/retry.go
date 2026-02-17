// Package retry provides a shared retry utility with exponential backoff and jitter.
package retry

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"
)

// cryptoInt64n returns a random int64 in [0, n) using crypto/rand.
func cryptoInt64n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	v := binary.LittleEndian.Uint64(b[:]) >> 1 // ensure fits in int64
	return int64(v % uint64(n))                //nolint:gosec // n>0, v%n < n, safe
}

// PermanentError wraps an error that should not be retried.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so that Do will not retry it.
func Permanent(err error) error {
	return &PermanentError{Err: err}
}

// Do calls fn up to maxAttempts times with exponential backoff and jitter.
// It stops early if:
//   - fn returns nil (success)
//   - fn returns a *PermanentError (not retryable)
//   - ctx is cancelled
//
// baseDelay is doubled on each retry with +-25% jitter.
func Do(ctx context.Context, maxAttempts int, baseDelay time.Duration, fn func() error) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var err error
	delay := baseDelay

	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		// Don't retry permanent errors.
		var pe *PermanentError
		if errors.As(err, &pe) {
			return pe.Err
		}

		// Don't sleep after the last attempt.
		if attempt == maxAttempts-1 {
			break
		}

		// Exponential backoff with +-25% jitter.
		jitter := delay / 4
		sleep := delay - jitter + time.Duration(cryptoInt64n(int64(2*jitter+1)))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(sleep):
		}

		delay *= 2
	}

	return err
}

// DoWithUnlock is like Do but calls unlock before sleeping and relock after.
// This is used when a mutex must be released during backoff to avoid blocking
// other goroutines on the same shard. The caller must hold the lock on entry.
// On return, the lock is held only if relock was called (i.e., at least one retry).
// If no retry occurs (success on first attempt or first-attempt permanent error),
// the lock state is unchanged.
//
// relockFn must re-acquire the lock and return it. fn is always called with the lock held.
func DoWithUnlock(ctx context.Context, maxAttempts int, baseDelay time.Duration,
	unlock func(), relock func(), fn func() error) error {

	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var err error
	delay := baseDelay

	for attempt := 0; attempt < maxAttempts; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}

		var pe *PermanentError
		if errors.As(err, &pe) {
			return pe.Err
		}

		if attempt == maxAttempts-1 {
			break
		}

		// Release lock during backoff sleep.
		unlock()

		jitter := delay / 4
		sleep := delay - jitter + time.Duration(cryptoInt64n(int64(2*jitter+1)))

		select {
		case <-ctx.Done():
			relock() // Caller expects lock held on return.
			return ctx.Err()
		case <-time.After(sleep):
		}

		relock()
		delay *= 2
	}

	return err
}
