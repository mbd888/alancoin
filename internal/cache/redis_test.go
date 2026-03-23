package cache

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func TestNewRedisClient_Success(t *testing.T) {
	mr := miniredis.RunT(t)

	rc, err := NewRedisClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}
	defer rc.Close()

	if rc.Client() == nil {
		t.Error("Client() returned nil")
	}

	if err := rc.Healthy(context.Background()); err != nil {
		t.Errorf("Healthy: %v", err)
	}
}

func TestNewRedisClient_InvalidURL(t *testing.T) {
	_, err := NewRedisClient("not-a-url")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestNewRedisClient_Unreachable(t *testing.T) {
	_, err := NewRedisClient("redis://localhost:1") // port 1 unlikely to have Redis
	if err == nil {
		t.Error("expected error for unreachable host")
	}
}

func TestRedisClient_CloseAndHealthy(t *testing.T) {
	mr := miniredis.RunT(t)

	rc, err := NewRedisClient("redis://" + mr.Addr())
	if err != nil {
		t.Fatalf("NewRedisClient: %v", err)
	}

	if err := rc.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	if err := rc.Healthy(context.Background()); err == nil {
		t.Error("expected error after Close")
	}
}
