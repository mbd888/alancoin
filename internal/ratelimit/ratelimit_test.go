package ratelimit

import (
	"testing"
	"time"
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
