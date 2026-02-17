package syncutil

import (
	"context"
	"hash/fnv"
	"sync"
)

// ContextShardedMutex provides a fixed-size pool of channel-based mutexes
// that support context cancellation. Unlike ShardedMutex, callers can bail
// out if their context is cancelled while waiting to acquire a lock.
type ContextShardedMutex struct {
	shards [256]chanMutex
	once   sync.Once
}

// chanMutex is a mutex implemented via a buffered channel, allowing select{}
// with a context cancellation channel.
type chanMutex struct {
	ch chan struct{}
}

// NewContextShardedMutex creates a new context-aware sharded mutex.
func NewContextShardedMutex() *ContextShardedMutex {
	m := &ContextShardedMutex{}
	m.init()
	return m
}

func (m *ContextShardedMutex) init() {
	m.once.Do(func() {
		for i := range m.shards {
			m.shards[i].ch = make(chan struct{}, 1)
			m.shards[i].ch <- struct{}{} // Start unlocked.
		}
	})
}

// LockContext acquires the mutex for the given key, respecting context cancellation.
// On success, returns an unlock function and nil error. The caller MUST call the
// unlock function when done.
// On context cancellation, returns nil and the context error.
func (m *ContextShardedMutex) LockContext(ctx context.Context, key string) (func(), error) {
	m.init()
	shard := &m.shards[m.shardIdx(key)]

	select {
	case <-shard.ch:
		// Acquired the lock.
		return func() { shard.ch <- struct{}{} }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *ContextShardedMutex) shardIdx(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32() % 256
}
