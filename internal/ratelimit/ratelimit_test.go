package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestLimiterAllow(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         5,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	key := "test-ip"

	// Should allow burst size requests immediately
	for i := 0; i < 5; i++ {
		if !limiter.Allow(key) {
			t.Errorf("Request %d should be allowed (within burst)", i)
		}
	}

	// Next request should be denied
	if limiter.Allow(key) {
		t.Error("Request after burst should be denied")
	}

	// Wait for token replenishment (1 second = 1 token at 60/min)
	time.Sleep(time.Second)

	// Should allow again
	if !limiter.Allow(key) {
		t.Error("Request after waiting should be allowed")
	}
}

func TestLimiterMultipleClients(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         3,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	// Client A uses up their tokens
	for i := 0; i < 3; i++ {
		limiter.Allow("client-a")
	}

	// Client A is now rate limited
	if limiter.Allow("client-a") {
		t.Error("Client A should be rate limited")
	}

	// Client B should still have tokens
	if !limiter.Allow("client-b") {
		t.Error("Client B should not be rate limited")
	}
}

func TestLimiterTokenReplenishment(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 600, // 10 per second
		BurstSize:         1,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	key := "test"

	// Use the one token
	if !limiter.Allow(key) {
		t.Error("First request should be allowed")
	}

	// Should be denied
	if limiter.Allow(key) {
		t.Error("Second immediate request should be denied")
	}

	// Wait 100ms (should get 1 token at 10/sec)
	time.Sleep(110 * time.Millisecond)

	// Should be allowed again
	if !limiter.Allow(key) {
		t.Error("Request after 100ms should be allowed")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.RequestsPerMinute != 60 {
		t.Errorf("Expected 60 requests/min, got %d", cfg.RequestsPerMinute)
	}
	if cfg.BurstSize != 10 {
		t.Errorf("Expected burst size 10, got %d", cfg.BurstSize)
	}
	if cfg.CleanupInterval != time.Minute {
		t.Errorf("Expected 1 minute cleanup interval, got %v", cfg.CleanupInterval)
	}
}

// --- merged from ratelimit_coverage_test.go ---

func init() {
	gin.SetMode(gin.TestMode)
}

// ---------------------------------------------------------------------------
// AllowWithLimit
// ---------------------------------------------------------------------------

func TestAllowWithLimit_CustomRPMAndBurst(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         5,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	key := "custom-limit-key"

	// Use a custom limit: RPM=120, burst=3
	for i := 0; i < 3; i++ {
		if !limiter.AllowWithLimit(key, 120, 3) {
			t.Errorf("Request %d should be allowed within custom burst", i)
		}
	}

	// 4th request should be denied (burst of 3 exhausted)
	if limiter.AllowWithLimit(key, 120, 3) {
		t.Error("Request beyond custom burst should be denied")
	}
}

func TestAllowWithLimit_TokenCappedAtBurst(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 6000, // 100/sec — very fast replenishment
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	key := "cap-test"

	// First request creates state with tokens = burst-1 = 1
	if !limiter.AllowWithLimit(key, 6000, 2) {
		t.Error("First request should be allowed")
	}

	// Wait long enough that tokens would exceed burst if not capped
	time.Sleep(200 * time.Millisecond)

	// Consume burst
	for i := 0; i < 2; i++ {
		if !limiter.AllowWithLimit(key, 6000, 2) {
			t.Errorf("Request %d after wait should be allowed (tokens capped at burst)", i)
		}
	}
	// Next should be denied immediately (burst cap means at most 2 tokens)
	if limiter.AllowWithLimit(key, 6000, 2) {
		t.Error("Should be denied after consuming capped burst")
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

func TestMiddleware_HealthEndpointsSkipRateLimit(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 1,
		BurstSize:         1,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/health", func(c *gin.Context) { c.String(200, "ok") })
	router.GET("/health/live", func(c *gin.Context) { c.String(200, "ok") })
	router.GET("/health/ready", func(c *gin.Context) { c.String(200, "ok") })
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Health paths should always pass, even after exhausting the rate limit
	for _, path := range []string{"/health", "/health/live", "/health/ready"} {
		for i := 0; i < 5; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "10.0.0.1:12345"
			router.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s request %d: expected 200, got %d", path, i, w.Code)
			}
		}
	}
}

func TestMiddleware_RateLimitsNonHealthPaths(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Exhaust burst
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/test", nil)
		req.RemoteAddr = "10.0.0.2:12345"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should be allowed, got %d", i, w.Code)
		}
	}

	// Next request should be rate limited
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected 429, got %d", w.Code)
	}
}

func TestMiddleware_FlyClientIPHeader(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Exhaust burst for Fly-Client-IP=1.2.3.4
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/test", nil)
		req.RemoteAddr = "10.0.0.3:12345"
		req.Header.Set("Fly-Client-IP", "1.2.3.4")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should pass, got %d", i, w.Code)
		}
	}

	// Request from different Fly-Client-IP should still pass
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.RemoteAddr = "10.0.0.3:12345"
	req.Header.Set("Fly-Client-IP", "5.6.7.8")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Request from different Fly-Client-IP should pass, got %d", w.Code)
	}
}

func TestMiddleware_AuthorizationKeyUsesHash(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Exhaust burst for one auth token
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/test", nil)
		req.RemoteAddr = "10.0.0.4:12345"
		req.Header.Set("Authorization", "token-alpha")
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should pass, got %d", i, w.Code)
		}
	}

	// Same IP but different auth token should have separate bucket
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.RemoteAddr = "10.0.0.4:12345"
	req.Header.Set("Authorization", "token-beta")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Different auth token should have separate bucket, got %d", w.Code)
	}
}

func TestMiddleware_RemoteAddrWithoutPort(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// RemoteAddr without port (edge case)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.RemoteAddr = "10.0.0.5"
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// TenantMiddleware
// ---------------------------------------------------------------------------

func TestTenantMiddleware_NoTenantPassesThrough(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	provider := func(tenantID string) int { return 10 }
	router := gin.New()
	router.Use(limiter.TenantMiddleware("tenant_id", provider))
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// No tenant in context — should pass through
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for no-tenant request, got %d", w.Code)
	}
}

func TestTenantMiddleware_WithTenantAppliesLimit(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         100,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	provider := func(tenantID string) int { return 60 }
	router := gin.New()
	// Set tenant in context, then apply tenant middleware
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", "tenant-abc")
		c.Next()
	})
	router.Use(limiter.TenantMiddleware("tenant_id", provider))
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// RPM=60, burst = max(60/6, 5) = 10
	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/test", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should pass within tenant burst, got %d", i, w.Code)
		}
	}

	// Exceeding burst should return 429
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected 429 after tenant burst exhausted, got %d", w.Code)
	}
}

func TestTenantMiddleware_ZeroRPMPassesThrough(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	provider := func(tenantID string) int { return 0 } // No custom limit
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", "tenant-xyz")
		c.Next()
	})
	router.Use(limiter.TenantMiddleware("tenant_id", provider))
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Zero RPM from provider means no tenant-level limit — passes through
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for zero-RPM tenant, got %d", w.Code)
	}
}

func TestTenantMiddleware_HealthSkipped(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 1,
		BurstSize:         1,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	provider := func(tenantID string) int { return 1 }
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tenant_id", "tenant-health")
		c.Next()
	})
	router.Use(limiter.TenantMiddleware("tenant_id", provider))
	router.GET("/health", func(c *gin.Context) { c.String(200, "ok") })

	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/health", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Health request %d should pass, got %d", i, w.Code)
		}
	}
}

func TestTenantMiddleware_LowRPMMinBurst(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         100,
		CleanupInterval:   time.Minute,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	// RPM=6, burst = max(6/6, 5) = max(1, 5) = 5
	provider := func(tenantID string) int { return 6 }
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set("tid", "low-rpm-tenant")
		c.Next()
	})
	router.Use(limiter.TenantMiddleware("tid", provider))
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Should allow at least 5 requests (minimum burst)
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/test", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should be allowed (min burst=5), got %d", i, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// MiddlewareWithConfig
// ---------------------------------------------------------------------------

func TestMiddlewareWithConfig(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         2,
		CleanupInterval:   time.Minute,
	}

	router := gin.New()
	router.Use(MiddlewareWithConfig(cfg))
	router.GET("/v1/test", func(c *gin.Context) { c.String(200, "ok") })

	// Should work like regular middleware
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/test", nil)
		req.RemoteAddr = "10.0.0.10:12345"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should pass, got %d", i, w.Code)
		}
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/test", nil)
	req.RemoteAddr = "10.0.0.10:12345"
	router.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Expected 429 after exhausting MiddlewareWithConfig burst, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Stop and cleanup
// ---------------------------------------------------------------------------

func TestStop_CanBeCalledOnce(t *testing.T) {
	limiter := New(Config{
		RequestsPerMinute: 60,
		BurstSize:         5,
		CleanupInterval:   10 * time.Millisecond,
	})
	// Just verify stop does not panic
	limiter.Stop()
}

func TestCleanup_RemovesStaleEntries(t *testing.T) {
	cfg := Config{
		RequestsPerMinute: 60,
		BurstSize:         5,
		CleanupInterval:   50 * time.Millisecond,
	}
	limiter := New(cfg)
	defer limiter.Stop()

	// Add an entry
	limiter.Allow("stale-key")

	// Manually set lastCheck to the past so cleanup removes it
	limiter.mu.Lock()
	if state, ok := limiter.clients["stale-key"]; ok {
		state.lastCheck = time.Now().Add(-5 * time.Minute)
	}
	limiter.mu.Unlock()

	// Wait for cleanup to run
	time.Sleep(100 * time.Millisecond)

	limiter.mu.RLock()
	_, exists := limiter.clients["stale-key"]
	limiter.mu.RUnlock()

	if exists {
		t.Error("Expected stale entry to be cleaned up")
	}
}
