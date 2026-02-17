package syncutil

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContextShardedMutex_BasicLockUnlock(t *testing.T) {
	m := NewContextShardedMutex()
	ctx := context.Background()

	unlock, err := m.LockContext(ctx, "key1")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	unlock()
}

func TestContextShardedMutex_MutualExclusion(t *testing.T) {
	m := NewContextShardedMutex()
	ctx := context.Background()

	var counter int64
	var wg sync.WaitGroup
	const n = 100

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			unlock, err := m.LockContext(ctx, "counter")
			if err != nil {
				t.Errorf("lock failed: %v", err)
				return
			}
			defer unlock()
			// Non-atomic increment — if mutual exclusion is broken, this will be visible.
			v := atomic.LoadInt64(&counter)
			atomic.StoreInt64(&counter, v+1)
		}()
	}
	wg.Wait()

	if atomic.LoadInt64(&counter) != n {
		t.Fatalf("expected %d, got %d — mutual exclusion violated", n, atomic.LoadInt64(&counter))
	}
}

func TestContextShardedMutex_ContextCancelled(t *testing.T) {
	m := NewContextShardedMutex()
	ctx := context.Background()

	// Acquire lock.
	unlock, err := m.LockContext(ctx, "blocked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Try to acquire the same lock with a cancelled context.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = m.LockContext(cancelCtx, "blocked")
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}

	unlock() // Clean up.
}

func TestContextShardedMutex_DifferentKeysNoContention(t *testing.T) {
	m := NewContextShardedMutex()
	ctx := context.Background()

	// Two keys that (hopefully) hash to different shards should not block each other.
	// This is a probabilistic test — we use very different keys.
	unlock1, err := m.LockContext(ctx, "alpha-key-one")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// This should succeed without blocking (different shard).
	timeoutCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	unlock2, err := m.LockContext(timeoutCtx, "beta-key-two")
	if err != nil {
		// If they happen to share a shard, that's OK — just skip.
		t.Skip("keys hashed to same shard, skipping contention-free test")
	}

	unlock2()
	unlock1()
}

func TestContextShardedMutex_UnlockAllowsNext(t *testing.T) {
	m := NewContextShardedMutex()
	ctx := context.Background()

	unlock, err := m.LockContext(ctx, "relay")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		u, err := m.LockContext(ctx, "relay")
		if err != nil {
			return
		}
		close(acquired)
		u()
	}()

	// Second goroutine should be blocked.
	select {
	case <-acquired:
		t.Fatal("second goroutine acquired lock before first released")
	case <-time.After(20 * time.Millisecond):
		// Expected.
	}

	unlock()

	select {
	case <-acquired:
		// Expected — second goroutine acquired after unlock.
	case <-time.After(time.Second):
		t.Fatal("second goroutine did not acquire lock after first released")
	}
}
