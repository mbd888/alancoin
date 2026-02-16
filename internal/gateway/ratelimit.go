package gateway

import (
	"sync"
	"time"
)

const defaultMaxRequestsPerMinute = 100

type rateLimitEntry struct {
	count       int
	windowStart time.Time
	limit       int
}

type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	window  time.Duration
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		entries: make(map[string]*rateLimitEntry),
		window:  time.Minute,
	}
}

// allow checks whether a request for the given session is within rate limits.
func (rl *rateLimiter) allow(sessionID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.entries[sessionID]
	if !ok {
		rl.entries[sessionID] = &rateLimitEntry{
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
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.entries[sessionID]
	if ok {
		entry.limit = limit
	} else {
		rl.entries[sessionID] = &rateLimitEntry{
			limit:       limit,
			windowStart: time.Now(),
		}
	}
}

// remove deletes rate limit state for a session.
func (rl *rateLimiter) remove(sessionID string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.entries, sessionID)
}

// sweep removes entries that haven't seen activity in 2 windows.
// Called by the Timer goroutine.
func (rl *rateLimiter) sweep() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	removed := 0
	for k, entry := range rl.entries {
		if now.Sub(entry.windowStart) > 2*rl.window {
			delete(rl.entries, k)
			removed++
		}
	}
	return removed
}

// size returns the current number of tracked sessions.
func (rl *rateLimiter) size() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return len(rl.entries)
}
