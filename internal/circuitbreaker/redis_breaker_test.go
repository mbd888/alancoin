package circuitbreaker

import (
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestBreaker(t *testing.T, mr *miniredis.Miniredis) *Breaker {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	b := New(3, 5*time.Second)
	b.SetRedisBackend(rdb, slog.Default())
	return b
}

func TestRedisAllow_ClosedCircuit(t *testing.T) {
	mr := miniredis.RunT(t)
	b := newTestBreaker(t, mr)

	if !b.Allow("provider-1") {
		t.Error("expected Allow=true for new key (closed circuit)")
	}
}

func TestRedisRecordFailure_TripsOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	b := newTestBreaker(t, mr) // threshold=3

	b.RecordFailure("provider-1")
	b.RecordFailure("provider-1")

	if !b.Allow("provider-1") {
		t.Error("should still allow before threshold")
	}

	b.RecordFailure("provider-1") // 3rd failure = trips open

	if b.Allow("provider-1") {
		t.Error("should reject after threshold reached (open circuit)")
	}
}

func TestRedisAllow_OpenTimesOut_HalfOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	// Use a very short open duration so real time passes
	b := New(3, 100*time.Millisecond)
	b.SetRedisBackend(rdb, slog.Default())

	// Trip open
	for i := 0; i < 3; i++ {
		b.RecordFailure("provider-1")
	}
	if b.Allow("provider-1") {
		t.Error("should be open")
	}

	// Wait past open duration
	time.Sleep(150 * time.Millisecond)

	if !b.Allow("provider-1") {
		t.Error("should allow probe request after open duration (half-open)")
	}

	// Half-open: reject additional requests
	if b.Allow("provider-1") {
		t.Error("should reject during half-open (already probing)")
	}
}

func TestRedisRecordSuccess_ClosesCircuit(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	b := New(3, 100*time.Millisecond)
	b.SetRedisBackend(rdb, slog.Default())

	// Trip open
	for i := 0; i < 3; i++ {
		b.RecordFailure("provider-1")
	}

	// Wait for half-open
	time.Sleep(150 * time.Millisecond)
	b.Allow("provider-1") // transitions to half-open

	// Probe succeeds
	b.RecordSuccess("provider-1")

	// Should be closed now
	if !b.Allow("provider-1") {
		t.Error("should allow after probe success (closed)")
	}
}

func TestRedisRecordFailure_HalfOpenGoesOpen(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	b := New(3, 100*time.Millisecond)
	b.SetRedisBackend(rdb, slog.Default())

	// Trip open
	for i := 0; i < 3; i++ {
		b.RecordFailure("provider-1")
	}

	// Wait for half-open
	time.Sleep(150 * time.Millisecond)
	b.Allow("provider-1")

	// Probe fails
	b.RecordFailure("provider-1")

	// Should be back to open
	if b.Allow("provider-1") {
		t.Error("should be open after probe failure")
	}
}

func TestRedisBreaker_Fallback_OnRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	b := newTestBreaker(t, mr)

	// Close Redis
	mr.Close()

	// Should fall back to local breaker (new key = closed = allow)
	if !b.Allow("provider-1") {
		t.Error("should allow via local fallback when Redis is down")
	}
}

func TestRedisBreaker_IndependentKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	b := newTestBreaker(t, mr)

	// Trip provider-1 open
	for i := 0; i < 3; i++ {
		b.RecordFailure("provider-1")
	}

	// provider-2 should still be open
	if !b.Allow("provider-2") {
		t.Error("provider-2 should be unaffected by provider-1")
	}
	if b.Allow("provider-1") {
		t.Error("provider-1 should be open")
	}
}
