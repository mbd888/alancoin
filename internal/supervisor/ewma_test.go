package supervisor

import (
	"testing"
	"time"
)

func TestEWMA_BasicUpdate(t *testing.T) {
	e := NewEWMA(1 * time.Minute)
	now := time.Now()

	// First event
	e.Update(100, now)
	if e.Value != 100 {
		t.Fatalf("expected 100, got %f", e.Value)
	}

	// Immediate second event — adds on top
	e.Update(200, now)
	if e.Value != 300 {
		t.Fatalf("expected 300 for same-time events, got %f", e.Value)
	}
}

func TestEWMA_Decay(t *testing.T) {
	e := NewEWMA(1 * time.Minute)
	now := time.Now()

	e.Update(1000, now)

	// After 1 time constant, value should decay by e^(-1) ≈ 0.368
	later := now.Add(1 * time.Minute)
	est := e.Estimate(later)
	expected := 1000 * 0.367879 // e^(-1)
	if est < expected*0.99 || est > expected*1.01 {
		t.Fatalf("expected ~%f after 1τ, got %f", expected, est)
	}
}

func TestEWMA_EstimateDoesNotMutate(t *testing.T) {
	e := NewEWMA(1 * time.Minute)
	now := time.Now()
	e.Update(1000, now)

	// Estimate at future time should not change internal state
	_ = e.Estimate(now.Add(30 * time.Second))
	if e.Value != 1000 {
		t.Fatalf("Estimate should not mutate Value, got %f", e.Value)
	}
}

func TestEWMACascade_AllScalesUpdate(t *testing.T) {
	c := NewEWMACascade()
	now := time.Now()

	c.Update(1000, now)
	c.Update(2000, now)

	estimates := c.Estimates(now)
	for i, est := range estimates {
		if est.Int64() != 3000 {
			t.Errorf("scale %d: expected 3000 at same time, got %d", i, est.Int64())
		}
	}
}
