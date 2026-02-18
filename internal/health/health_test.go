package health

import (
	"context"
	"sync"
	"testing"
)

func TestRegistryEmpty(t *testing.T) {
	r := NewRegistry()
	healthy, statuses := r.CheckAll(context.Background())
	if !healthy {
		t.Fatal("empty registry should be healthy")
	}
	if len(statuses) != 0 {
		t.Fatalf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestRegistryAllHealthy(t *testing.T) {
	r := NewRegistry()
	r.Register("db", func(_ context.Context) Status {
		return Status{Name: "db", Healthy: true}
	})
	r.Register("cache", func(_ context.Context) Status {
		return Status{Name: "cache", Healthy: true, Detail: "ok"}
	})

	healthy, statuses := r.CheckAll(context.Background())
	if !healthy {
		t.Fatal("all-healthy registry should report healthy")
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
}

func TestRegistryOneUnhealthy(t *testing.T) {
	r := NewRegistry()
	r.Register("db", func(_ context.Context) Status {
		return Status{Name: "db", Healthy: true}
	})
	r.Register("cache", func(_ context.Context) Status {
		return Status{Name: "cache", Healthy: false, Detail: "connection refused"}
	})

	healthy, statuses := r.CheckAll(context.Background())
	if healthy {
		t.Fatal("registry with unhealthy checker should report unhealthy")
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	if statuses[1].Detail != "connection refused" {
		t.Fatalf("expected detail 'connection refused', got %q", statuses[1].Detail)
	}
}

func TestRegistryConcurrentRegisterAndCheck(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup

	// Register concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Register("checker", func(_ context.Context) Status {
				return Status{Name: "checker", Healthy: true}
			})
		}(i)
	}

	// Check concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.CheckAll(context.Background())
		}()
	}

	wg.Wait()
}
