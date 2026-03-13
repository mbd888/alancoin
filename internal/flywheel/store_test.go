package flywheel

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore_SaveAndRecent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Save 3 snapshots
	for i := 0; i < 3; i++ {
		err := store.Save(ctx, &State{
			HealthScore: float64(i * 10),
			HealthTier:  TierCold,
			ComputedAt:  time.Now().Add(time.Duration(i) * time.Minute),
		})
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
	}

	// Fetch all 3
	results, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent failed: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Newest first
	if results[0].HealthScore != 20 {
		t.Errorf("expected newest first (score 20), got %f", results[0].HealthScore)
	}
	if results[2].HealthScore != 0 {
		t.Errorf("expected oldest last (score 0), got %f", results[2].HealthScore)
	}
}

func TestMemoryStore_RecentLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		_ = store.Save(ctx, &State{HealthScore: float64(i), ComputedAt: time.Now()})
	}

	results, err := store.Recent(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3, got %d", len(results))
	}
}

func TestMemoryStore_Empty(t *testing.T) {
	store := NewMemoryStore()
	results, err := store.Recent(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty, got %d", len(results))
	}
}

func TestMemoryStore_Isolation(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	original := &State{HealthScore: 42, HealthTier: TierSpinning, ComputedAt: time.Now()}
	if err := store.Save(ctx, original); err != nil {
		t.Fatal(err)
	}

	// Mutate original after save
	original.HealthScore = 99

	// Retrieved value should still be 42
	results, _ := store.Recent(ctx, 1)
	if results[0].HealthScore != 42 {
		t.Errorf("store should copy on save, got %f", results[0].HealthScore)
	}

	// Mutate retrieved value
	results[0].HealthScore = 0
	results2, _ := store.Recent(ctx, 1)
	if results2[0].HealthScore != 42 {
		t.Errorf("store should copy on read, got %f", results2[0].HealthScore)
	}
}

func TestMemoryStore_BoundedGrowth(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Insert more than the 1000 limit
	for i := 0; i < 1050; i++ {
		_ = store.Save(ctx, &State{HealthScore: float64(i), ComputedAt: time.Now()})
	}

	store.mu.RLock()
	n := len(store.snapshots)
	store.mu.RUnlock()

	if n > 1000 {
		t.Errorf("expected bounded to 1000, got %d", n)
	}
}
