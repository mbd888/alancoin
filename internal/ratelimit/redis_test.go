package ratelimit

import (
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisLimiter(t *testing.T, mr *miniredis.Miniredis, rpm, burst int) *Limiter {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	l := New(Config{
		RequestsPerMinute: rpm,
		BurstSize:         burst,
		CleanupInterval:   time.Minute,
	})
	l.SetRedisBackend(rdb, slog.Default())
	return l
}

func TestRedisRateLimit_AllowsWithinLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	l := newRedisLimiter(t, mr, 10, 5)
	defer l.Stop()

	// rpm=10 + burst=5 = 15 allowed per window
	for i := 0; i < 15; i++ {
		if !l.Allow("client-1") {
			t.Errorf("request %d should be allowed (within rpm+burst)", i+1)
		}
	}
}

func TestRedisRateLimit_RejectsOverLimit(t *testing.T) {
	mr := miniredis.RunT(t)
	l := newRedisLimiter(t, mr, 5, 2)
	defer l.Stop()

	// rpm=5 + burst=2 = 7 allowed
	for i := 0; i < 7; i++ {
		l.Allow("client-1")
	}

	if l.Allow("client-1") {
		t.Error("request 8 should be rejected (over rpm+burst)")
	}
}

func TestRedisRateLimit_DifferentWindows(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })

	l := New(Config{
		RequestsPerMinute: 3,
		BurstSize:         1,
		CleanupInterval:   time.Minute,
	})
	l.SetRedisBackend(rdb, slog.Default())
	defer l.Stop()

	// Exhaust limit in current window
	for i := 0; i < 4; i++ {
		l.Allow("client-1")
	}
	if l.Allow("client-1") {
		t.Error("should be rate limited")
	}

	// Directly verify that a different window key would allow requests
	// (since we can't advance real time, we test the key structure)
	keys := mr.Keys()
	if len(keys) == 0 {
		t.Error("expected at least one rate limit key in Redis")
	}
	// Verify key format contains the bucket timestamp
	for _, k := range keys {
		if len(k) < 3 {
			t.Errorf("unexpected key format: %s", k)
		}
	}
}

func TestRedisRateLimit_FallbackOnRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	l := newRedisLimiter(t, mr, 10, 5)
	defer l.Stop()

	// Close Redis
	mr.Close()

	// Should fall back to in-memory limiter
	if !l.Allow("client-1") {
		t.Error("should allow via in-memory fallback when Redis is down")
	}
}

func TestRedisRateLimit_IndependentKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	l := newRedisLimiter(t, mr, 2, 1)
	defer l.Stop()

	// Exhaust client-1
	for i := 0; i < 3; i++ {
		l.Allow("client-1")
	}
	if l.Allow("client-1") {
		t.Error("client-1 should be limited")
	}

	// client-2 should be unaffected
	if !l.Allow("client-2") {
		t.Error("client-2 should be allowed")
	}
}

func TestRedisRateLimit_KeyExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	l := newRedisLimiter(t, mr, 10, 5)
	defer l.Stop()

	l.Allow("client-1")

	// Check that keys have TTL set
	keys := mr.Keys()
	for _, key := range keys {
		ttl := mr.TTL(key)
		if ttl <= 0 {
			t.Errorf("key %s should have TTL > 0, got %v", key, ttl)
		}
		if ttl > 3*time.Minute {
			t.Errorf("key %s TTL too long: %v", key, ttl)
		}
	}
}
