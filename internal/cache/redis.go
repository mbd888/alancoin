// Package cache provides a shared Redis client for distributed state coordination.
// When Redis is not configured, components fall back to in-memory implementations.
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisClient wraps a Redis connection with short timeouts for hot-path operations.
type RedisClient struct {
	rdb *redis.Client
}

// NewRedisClient connects to Redis and verifies connectivity.
func NewRedisClient(redisURL string) (*RedisClient, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 100 * time.Millisecond
	opts.WriteTimeout = 100 * time.Millisecond
	opts.PoolSize = 50
	opts.MinIdleConns = 5

	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}

	return &RedisClient{rdb: rdb}, nil
}

// Client returns the underlying redis.Client for use by components.
func (c *RedisClient) Client() *redis.Client { return c.rdb }

// Close shuts down the Redis connection pool.
func (c *RedisClient) Close() error { return c.rdb.Close() }

// Healthy returns nil if Redis is reachable.
func (c *RedisClient) Healthy(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}
