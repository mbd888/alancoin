package gateway

import "context"

// IdempotencyStore deduplicates proxy requests by idempotency key.
// Implementations: memoryIdempotencyStore (in-process), redisIdempotencyStore (distributed).
type IdempotencyStore interface {
	// GetOrReserve checks for a cached result or reserves the key for processing.
	// Returns (result, err, true) if cached; (nil, nil, false) if reserved.
	// If another request is processing the same key, may block until it completes.
	GetOrReserve(ctx context.Context, sessionID, idempotencyKey string) (*ProxyResult, error, bool)

	// Complete stores the result and wakes any waiters.
	Complete(sessionID, idempotencyKey string, result *ProxyResult)

	// Cancel removes the reservation without storing a result.
	Cancel(sessionID, idempotencyKey string)

	// Sweep removes expired entries. Returns count removed.
	Sweep() int

	// Size returns current entry count.
	Size() int
}
