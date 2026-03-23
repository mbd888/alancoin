package circuitbreaker

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mbd888/alancoin/internal/metrics"
)

const (
	cbKeyPrefix = "cb:"
	cbTTL       = 5 * time.Minute // auto-cleanup of recovered endpoints
	cbOpTimeout = 50 * time.Millisecond
)

// Lua script for RecordFailure: atomically increment failures and transition state.
// KEYS[1] = circuit breaker key
// ARGV[1] = failure threshold
// ARGV[2] = current time in milliseconds
var luaRecordFailure = redis.NewScript(`
local key = KEYS[1]
local threshold = tonumber(ARGV[1])
local now_ms = ARGV[2]

local failures = redis.call('HINCRBY', key, 'failures', 1)
redis.call('HSET', key, 'last_failure_ms', now_ms)
redis.call('EXPIRE', key, 300)

local state = tonumber(redis.call('HGET', key, 'state') or '0')
if state == 2 then
    redis.call('HSET', key, 'state', '1')
    return 1
elseif state == 0 and failures >= threshold then
    redis.call('HSET', key, 'state', '1')
    return 1
end
return state
`)

// Lua script for RecordSuccess: reset failures and close circuit if half-open.
// KEYS[1] = circuit breaker key
// ARGV[1] = current time in milliseconds
var luaRecordSuccess = redis.NewScript(`
local key = KEYS[1]
local state = tonumber(redis.call('HGET', key, 'state') or '0')

redis.call('HSET', key, 'failures', '0')
redis.call('EXPIRE', key, 300)

if state == 2 then
    redis.call('HSET', key, 'state', '0')
end
return 0
`)

// Lua script for Allow: check state and transition open → half-open if expired.
// KEYS[1] = circuit breaker key
// ARGV[1] = open duration in milliseconds
// ARGV[2] = current time in milliseconds
// Returns: 1 = allowed, 0 = rejected
var luaAllow = redis.NewScript(`
local key = KEYS[1]
local open_duration_ms = tonumber(ARGV[1])
local now_ms = tonumber(ARGV[2])

local state = tonumber(redis.call('HGET', key, 'state') or '0')
if state == 0 then
    return 1
end
if state == 1 then
    local last_failure = tonumber(redis.call('HGET', key, 'last_failure_ms') or '0')
    if (now_ms - last_failure) >= open_duration_ms then
        redis.call('HSET', key, 'state', '2')
        return 1
    end
    return 0
end
if state == 2 then
    return 0
end
return 1
`)

type redisCircuitBreaker struct {
	rdb          *redis.Client
	threshold    int
	openDuration time.Duration
	logger       *slog.Logger
}

// SetRedisBackend configures the breaker to use Redis for distributed state.
// On Redis errors, the breaker falls back to local in-memory state.
func (b *Breaker) SetRedisBackend(rdb *redis.Client, logger *slog.Logger) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.redisCB = &redisCircuitBreaker{
		rdb:          rdb,
		threshold:    b.threshold,
		openDuration: b.openDuration,
		logger:       logger,
	}
}

func (r *redisCircuitBreaker) redisKey(key string) string {
	return cbKeyPrefix + key
}

func (r *redisCircuitBreaker) Allow(key string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cbOpTimeout)
	defer cancel()

	nowMs := strconv.FormatInt(time.Now().UnixMilli(), 10)
	openMs := strconv.FormatInt(r.openDuration.Milliseconds(), 10)

	start := time.Now()
	result, err := luaAllow.Run(ctx, r.rdb, []string{r.redisKey(key)}, openMs, nowMs).Int()
	metrics.RedisOpDuration.WithLabelValues("cb_allow").Observe(time.Since(start).Seconds())
	if err != nil {
		metrics.RedisErrors.WithLabelValues("cb_allow").Inc()
		metrics.RedisFallbacks.WithLabelValues("circuitbreaker").Inc()
		r.logger.Warn("redis circuit breaker Allow failed", "key", key, "error", err)
		return false, err
	}
	return result == 1, nil
}

func (r *redisCircuitBreaker) RecordSuccess(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cbOpTimeout)
	defer cancel()

	nowMs := strconv.FormatInt(time.Now().UnixMilli(), 10)
	_, err := luaRecordSuccess.Run(ctx, r.rdb, []string{r.redisKey(key)}, nowMs).Result()
	if err != nil {
		r.logger.Warn("redis circuit breaker RecordSuccess failed", "key", key, "error", err)
	}
	return err
}

func (r *redisCircuitBreaker) RecordFailure(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cbOpTimeout)
	defer cancel()

	nowMs := strconv.FormatInt(time.Now().UnixMilli(), 10)
	threshold := strconv.Itoa(r.threshold)
	_, err := luaRecordFailure.Run(ctx, r.rdb, []string{r.redisKey(key)}, threshold, nowMs).Result()
	if err != nil {
		r.logger.Warn("redis circuit breaker RecordFailure failed", "key", key, "error", err)
	}
	return err
}
