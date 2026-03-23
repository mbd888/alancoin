package gateway

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisIdemStore(t *testing.T, mr *miniredis.Miniredis) IdempotencyStore {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	return NewRedisIdempotencyStore(rdb, 10*time.Minute, slog.Default())
}

func TestRedisIdem_GetOrReserve_NewKey(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)
	ctx := context.Background()

	result, err, found := store.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Error("expected found=false for new key (reserved)")
	}
	if result != nil || err != nil {
		t.Errorf("expected nil result and err, got result=%v err=%v", result, err)
	}
}

func TestRedisIdem_Complete_ReturnsCachedResult(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)
	ctx := context.Background()

	// Reserve
	_, _, _ = store.GetOrReserve(ctx, "s1", "k1")

	// Complete
	expected := &ProxyResult{AmountPaid: "1.50", ServiceUsed: "0xSeller"}
	store.Complete("s1", "k1", expected)

	// Get again
	result, err, found := store.GetOrReserve(ctx, "s1", "k1")
	if !found {
		t.Error("expected found=true for completed key")
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.AmountPaid != "1.50" {
		t.Errorf("AmountPaid=%q, want 1.50", result.AmountPaid)
	}
	if result.ServiceUsed != "0xSeller" {
		t.Errorf("ServiceUsed=%q, want 0xSeller", result.ServiceUsed)
	}
}

func TestRedisIdem_Cancel_AllowsReprocessing(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)
	ctx := context.Background()

	// Reserve
	_, _, _ = store.GetOrReserve(ctx, "s1", "k1")

	// Cancel
	store.Cancel("s1", "k1")

	// Should be re-reservable
	_, _, found := store.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Error("expected found=false after cancel (should re-reserve)")
	}
}

func TestRedisIdem_PendingKey_PollsUntilResult(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Reserve from goroutine 1
	_, _, _ = store.GetOrReserve(ctx, "s1", "k1")

	var wg sync.WaitGroup
	var gotResult *ProxyResult
	var gotFound bool

	// Goroutine 2 polls
	wg.Add(1)
	go func() {
		defer wg.Done()
		gotResult, _, gotFound = store.GetOrReserve(ctx, "s1", "k1")
	}()

	// Complete after short delay
	time.Sleep(100 * time.Millisecond)
	store.Complete("s1", "k1", &ProxyResult{AmountPaid: "2.00"})

	wg.Wait()

	if !gotFound {
		t.Error("polling goroutine should have found result")
	}
	if gotResult == nil || gotResult.AmountPaid != "2.00" {
		t.Errorf("got result=%v, want AmountPaid=2.00", gotResult)
	}
}

func TestRedisIdem_Sweep_NoOp(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)

	removed := store.Sweep()
	if removed != 0 {
		t.Errorf("Sweep should be no-op for Redis, got %d", removed)
	}
}

func TestRedisIdem_Size_Zero(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)

	if store.Size() != 0 {
		t.Errorf("Size should return 0 for Redis impl")
	}
}

func TestRedisIdem_RedisDown_Graceful(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newRedisIdemStore(t, mr)
	ctx := context.Background()

	// Close Redis
	mr.Close()

	// Should return (nil, nil, false) — proceed without dedup
	result, err, found := store.GetOrReserve(ctx, "s1", "k1")
	if found {
		t.Error("expected found=false when Redis is down")
	}
	if result != nil || err != nil {
		t.Errorf("expected nil result and err, got result=%v err=%v", result, err)
	}
}
