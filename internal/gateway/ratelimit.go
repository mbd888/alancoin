package gateway

import (
	"hash/fnv"
	"sync"
	"time"
)

const (
	defaultMaxRequestsPerMinute = 100
	rlShardCount                = 32
)

type rateLimitEntry struct {
	count       int
	windowStart time.Time
	limit       int
}

type rlShard struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
}

type rateLimiter struct {
	shards [rlShardCount]rlShard
	window time.Duration
}

func newRateLimiter() *rateLimiter {
	rl := &rateLimiter{
		window: time.Minute,
	}
	for i := range rl.shards {
		rl.shards[i].entries = make(map[string]*rateLimitEntry)
	}
	return rl
}

func (rl *rateLimiter) shardFor(sessionID string) *rlShard {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	return &rl.shards[h.Sum32()%rlShardCount]
}

// allow checks whether a request for the given session is within rate limits.
func (rl *rateLimiter) allow(sessionID string) bool {
	s := rl.shardFor(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[sessionID]
	if !ok {
		s.entries[sessionID] = &rateLimitEntry{
			count:       1,
			windowStart: time.Now(),
			limit:       defaultMaxRequestsPerMinute,
		}
		return true
	}

	now := time.Now()
	if now.Sub(entry.windowStart) >= rl.window {
		entry.count = 1
		entry.windowStart = now
		return true
	}

	if entry.count >= entry.limit {
		return false
	}

	entry.count++
	return true
}

// setLimit configures the rate limit for a session.
// Must be called before the first allow() for the limit to take effect
// from the start; otherwise the default is used until setLimit is called.
func (rl *rateLimiter) setLimit(sessionID string, limit int) {
	s := rl.shardFor(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[sessionID]
	if ok {
		entry.limit = limit
	} else {
		s.entries[sessionID] = &rateLimitEntry{
			limit:       limit,
			windowStart: time.Now(),
		}
	}
}

// remove deletes rate limit state for a session.
func (rl *rateLimiter) remove(sessionID string) {
	s := rl.shardFor(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, sessionID)
}

// sweep removes entries that haven't seen activity in 2 windows.
// Called by the Timer goroutine.
func (rl *rateLimiter) sweep() int {
	now := time.Now()
	removed := 0
	for i := range rl.shards {
		s := &rl.shards[i]
		s.mu.Lock()
		for k, entry := range s.entries {
			if now.Sub(entry.windowStart) > 2*rl.window {
				delete(s.entries, k)
				removed++
			}
		}
		s.mu.Unlock()
	}
	return removed
}

// size returns the current number of tracked sessions.
func (rl *rateLimiter) size() int {
	total := 0
	for i := range rl.shards {
		s := &rl.shards[i]
		s.mu.Lock()
		total += len(s.entries)
		s.mu.Unlock()
	}
	return total
}
