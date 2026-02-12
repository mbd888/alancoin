package syncutil

import (
	"hash/fnv"
	"sync"
)

// ShardedMutex provides a fixed-size pool of mutexes keyed by string.
// Unlike sync.Map-based per-key locks, this uses bounded memory regardless
// of how many keys are seen, at the cost of occasional false sharing between
// keys that hash to the same shard.
type ShardedMutex struct {
	shards [256]sync.Mutex
}

// Lock acquires the mutex for the given key and returns an unlock function.
func (s *ShardedMutex) Lock(key string) func() {
	mu := s.shard(key)
	mu.Lock()
	return mu.Unlock
}

func (s *ShardedMutex) shard(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &s.shards[h.Sum32()%256]
}
