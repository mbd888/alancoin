// Package ratelimit provides rate limiting middleware for the Alancoin API.
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Config configures rate limiting
type Config struct {
	// RequestsPerMinute is the max requests per IP per minute
	RequestsPerMinute int
	// BurstSize allows brief bursts above the limit
	BurstSize int
	// CleanupInterval is how often to clean old entries
	CleanupInterval time.Duration
}

// DefaultConfig returns sensible defaults
func DefaultConfig() Config {
	return Config{
		RequestsPerMinute: 60, // 1 req/sec average
		BurstSize:         10, // Allow bursts of 10
		CleanupInterval:   time.Minute,
	}
}

// Limiter tracks rate limits by key
type Limiter struct {
	cfg     Config
	mu      sync.RWMutex
	clients map[string]*clientState
	stop    chan struct{}
}

type clientState struct {
	tokens    float64
	lastCheck time.Time
}

// New creates a new rate limiter
func New(cfg Config) *Limiter {
	l := &Limiter{
		cfg:     cfg,
		clients: make(map[string]*clientState),
		stop:    make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// cleanup removes stale entries periodically
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(l.cfg.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-2 * time.Minute)
			for key, state := range l.clients {
				if state.lastCheck.Before(cutoff) {
					delete(l.clients, key)
				}
			}
			l.mu.Unlock()
		case <-l.stop:
			return
		}
	}
}

// Stop stops the cleanup goroutine
func (l *Limiter) Stop() {
	close(l.stop)
}

// Allow checks if a request should be allowed
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	state, exists := l.clients[key]

	if !exists {
		l.clients[key] = &clientState{
			tokens:    float64(l.cfg.BurstSize - 1),
			lastCheck: now,
		}
		return true
	}

	// Token bucket algorithm
	elapsed := now.Sub(state.lastCheck).Seconds()
	tokensPerSecond := float64(l.cfg.RequestsPerMinute) / 60.0
	state.tokens += elapsed * tokensPerSecond

	// Cap at burst size
	if state.tokens > float64(l.cfg.BurstSize) {
		state.tokens = float64(l.cfg.BurstSize)
	}

	state.lastCheck = now

	if state.tokens >= 1 {
		state.tokens--
		return true
	}

	return false
}

// Middleware returns a Gin middleware that rate limits by IP
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.ClientIP()

		// Allow authenticated requests higher limits
		if apiKey := c.GetHeader("Authorization"); apiKey != "" {
			key = "auth:" + apiKey[:min(20, len(apiKey))]
		}

		if !l.Allow(key) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate_limit_exceeded",
				"message":     "Too many requests. Please slow down.",
				"retry_after": 1,
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// MiddlewareWithConfig creates middleware with custom config
func MiddlewareWithConfig(cfg Config) gin.HandlerFunc {
	limiter := New(cfg)
	return limiter.Middleware()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
