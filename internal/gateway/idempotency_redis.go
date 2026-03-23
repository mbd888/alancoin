package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mbd888/alancoin/internal/metrics"
)

const (
	idemKeyPrefix    = "idem:"
	idemPendingValue = "__pending__"
	idemPollInterval = 50 * time.Millisecond
	idemOpTimeout    = 50 * time.Millisecond
)

// redisIdempotencyStore implements IdempotencyStore using Redis for cross-node deduplication.
type redisIdempotencyStore struct {
	rdb    *redis.Client
	ttl    time.Duration
	logger *slog.Logger
}

// NewRedisIdempotencyStore creates a Redis-backed idempotency store.
func NewRedisIdempotencyStore(rdb *redis.Client, ttl time.Duration, logger *slog.Logger) IdempotencyStore {
	return &redisIdempotencyStore{rdb: rdb, ttl: ttl, logger: logger}
}

func (s *redisIdempotencyStore) redisKey(sessionID, idempotencyKey string) string {
	return idemKeyPrefix + sessionID + ":" + idempotencyKey
}

// GetOrReserve attempts to reserve the key using SET NX. If the key already exists,
// checks whether it's pending (another node processing) or complete (cached result).
// For pending keys, polls until the result appears or ctx is cancelled.
func (s *redisIdempotencyStore) GetOrReserve(ctx context.Context, sessionID, idempotencyKey string) (*ProxyResult, error, bool) {
	key := s.redisKey(sessionID, idempotencyKey)

	opCtx, cancel := context.WithTimeout(ctx, idemOpTimeout)
	defer cancel()

	// Try to reserve with SET NX
	start := time.Now()
	ok, err := s.rdb.SetNX(opCtx, key, idemPendingValue, s.ttl).Result()
	metrics.RedisOpDuration.WithLabelValues("idem_setnx").Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.RedisErrors.WithLabelValues("idem_setnx").Inc()
		metrics.RedisFallbacks.WithLabelValues("idempotency").Inc()
		s.logger.Warn("redis idempotency SetNX failed, proceeding without dedup", "error", err)
		return nil, nil, false
	}
	if ok {
		// We reserved it — caller must call Complete or Cancel.
		return nil, nil, false
	}

	// Key exists — check value
	return s.waitForResult(ctx, key)
}

// waitForResult polls Redis until the key's value transitions from pending to a result,
// or the context is cancelled.
func (s *redisIdempotencyStore) waitForResult(ctx context.Context, key string) (*ProxyResult, error, bool) {
	for {
		opCtx, cancel := context.WithTimeout(ctx, idemOpTimeout)
		val, err := s.rdb.Get(opCtx, key).Result()
		cancel()

		if err == redis.Nil {
			// Key was deleted (cancelled by other node) — caller should process.
			return nil, nil, false
		}
		if err != nil {
			s.logger.Warn("redis idempotency GET failed, proceeding without dedup", "error", err)
			return nil, nil, false
		}

		if val != idemPendingValue {
			// Result is ready — deserialize
			var result ProxyResult
			if err := json.Unmarshal([]byte(val), &result); err != nil {
				s.logger.Warn("redis idempotency unmarshal failed", "error", err)
				return nil, nil, false
			}
			return &result, nil, true
		}

		// Still pending — wait and retry
		select {
		case <-ctx.Done():
			return nil, ctx.Err(), true
		case <-time.After(idemPollInterval):
		}
	}
}

// Complete stores the result, replacing the pending sentinel.
func (s *redisIdempotencyStore) Complete(sessionID, idempotencyKey string, result *ProxyResult) {
	key := s.redisKey(sessionID, idempotencyKey)
	data, err := json.Marshal(result)
	if err != nil {
		s.logger.Warn("redis idempotency marshal failed", "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), idemOpTimeout)
	defer cancel()

	if err := s.rdb.Set(ctx, key, string(data), s.ttl).Err(); err != nil {
		s.logger.Warn("redis idempotency SET failed", "error", err)
	}
}

// Cancel removes the reservation so other requests can process.
func (s *redisIdempotencyStore) Cancel(sessionID, idempotencyKey string) {
	key := s.redisKey(sessionID, idempotencyKey)

	ctx, cancel := context.WithTimeout(context.Background(), idemOpTimeout)
	defer cancel()

	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		s.logger.Warn("redis idempotency DEL failed", "error", err)
	}
}

// Sweep is a no-op — Redis TTL handles expiry.
func (s *redisIdempotencyStore) Sweep() int { return 0 }

// Size returns an approximate count (no-op for Redis).
func (s *redisIdempotencyStore) Size() int { return 0 }
