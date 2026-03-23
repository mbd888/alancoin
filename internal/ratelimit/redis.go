package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mbd888/alancoin/internal/metrics"
)

const (
	rlKeyPrefix  = "rl:"
	rlOpTimeout  = 50 * time.Millisecond
	rlWindowSecs = 60 // 1-minute window
)

// SetRedisBackend configures the limiter to use Redis for distributed rate limiting.
// On Redis errors, falls back to the in-memory token bucket.
func (l *Limiter) SetRedisBackend(rdb *redis.Client, logger *slog.Logger) {
	rl := &redisLimiter{
		rdb:      rdb,
		fallback: l,
		logger:   logger,
	}
	l.allowOverride = rl.allowWithLimit
}

type redisLimiter struct {
	rdb      *redis.Client
	fallback *Limiter
	logger   *slog.Logger
}

// allowWithLimit uses a fixed-window counter in Redis.
// Key: rl:{key}:{minute_bucket} with 2-minute TTL.
func (r *redisLimiter) allowWithLimit(key string, rpm, burst int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), rlOpTimeout)
	defer cancel()

	// Fixed-window keyed by minute
	bucket := time.Now().Unix() / rlWindowSecs
	redisKey := fmt.Sprintf("%s%s:%d", rlKeyPrefix, key, bucket)

	start := time.Now()
	pipe := r.rdb.Pipeline()
	incr := pipe.Incr(ctx, redisKey)
	pipe.Expire(ctx, redisKey, 2*time.Minute) // TTL slightly longer than window
	_, err := pipe.Exec(ctx)
	metrics.RedisOpDuration.WithLabelValues("ratelimit_incr").Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.RedisErrors.WithLabelValues("ratelimit_incr").Inc()
		metrics.RedisFallbacks.WithLabelValues("ratelimit").Inc()
		r.logger.Warn("redis rate limit failed, using in-memory fallback", "error", err)
		return r.fallbackAllow(key, rpm, burst)
	}

	count := incr.Val()
	// Allow rpm + burst in each window to match token bucket burst behavior
	return count <= int64(rpm+burst)
}

// fallbackAllow uses the in-memory token bucket directly (bypassing allowOverride).
func (r *redisLimiter) fallbackAllow(key string, rpm, burst int) bool {
	r.fallback.mu.Lock()
	defer r.fallback.mu.Unlock()

	now := time.Now()
	state, exists := r.fallback.clients[key]
	if !exists {
		r.fallback.clients[key] = &clientState{
			tokens:    float64(burst - 1),
			lastCheck: now,
		}
		return true
	}

	elapsed := now.Sub(state.lastCheck).Seconds()
	tokensPerSecond := float64(rpm) / 60.0
	state.tokens += elapsed * tokensPerSecond
	if state.tokens > float64(burst) {
		state.tokens = float64(burst)
	}
	state.lastCheck = now

	if state.tokens >= 1 {
		state.tokens--
		return true
	}
	return false
}
