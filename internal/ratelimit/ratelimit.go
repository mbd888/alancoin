// Package ratelimit provides rate limiting middleware for the Alancoin API.
package ratelimit

import (
	"net"
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

// Allow checks if a request should be allowed using the default RPM.
func (l *Limiter) Allow(key string) bool {
	return l.AllowWithLimit(key, l.cfg.RequestsPerMinute, l.cfg.BurstSize)
}

// AllowWithLimit checks if a request should be allowed using a custom RPM and burst.
func (l *Limiter) AllowWithLimit(key string, rpm, burst int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	state, exists := l.clients[key]

	if !exists {
		l.clients[key] = &clientState{
			tokens:    float64(burst - 1),
			lastCheck: now,
		}
		return true
	}

	// Token bucket algorithm
	elapsed := now.Sub(state.lastCheck).Seconds()
	tokensPerSecond := float64(rpm) / 60.0
	state.tokens += elapsed * tokensPerSecond

	// Cap at burst size
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

// Middleware returns a Gin middleware that rate limits by IP
func (l *Limiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip rate limiting for health checks only
		path := c.Request.URL.Path
		if path == "/health" || path == "/health/live" || path == "/health/ready" {
			c.Next()
			return
		}

		// Use the direct connection IP, not c.ClientIP() which trusts
		// X-Forwarded-For and can be spoofed to bypass rate limiting.
		key, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		if key == "" {
			key = c.Request.RemoteAddr
		}

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

// TenantRPMProvider returns the rate limit RPM for a tenant.
// Returns 0 if the tenant has no custom limit (falls through to global).
type TenantRPMProvider func(tenantID string) int

// TenantMiddleware returns a Gin middleware that applies per-tenant rate limits.
// It runs after auth middleware has set the tenant context key.
// If the request has no tenant, the global limiter is used as fallback.
func (l *Limiter) TenantMiddleware(tenantKey string, provider TenantRPMProvider) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip rate limiting for health checks
		path := c.Request.URL.Path
		if path == "/health" || path == "/health/live" || path == "/health/ready" {
			c.Next()
			return
		}

		tenantID, _ := c.Get(tenantKey)
		tid, _ := tenantID.(string)
		if tid == "" {
			c.Next() // No tenant — global limiter already applied upstream
			return
		}

		rpm := provider(tid)
		if rpm <= 0 {
			c.Next()
			return
		}

		burst := rpm / 6 // ~10s burst window
		if burst < 5 {
			burst = 5
		}

		key := "tenant:" + tid
		if !l.AllowWithLimit(key, rpm, burst) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "tenant_rate_limit_exceeded",
				"message":     "Tenant rate limit exceeded. Upgrade your plan for higher limits.",
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
