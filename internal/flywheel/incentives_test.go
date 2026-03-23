package flywheel

import (
	"context"
	"math"
	"testing"
)

func TestAdjustFeeBPS(t *testing.T) {
	engine := NewIncentiveEngine()
	ctx := context.Background()

	// Geometric scaling: endpoints preserved, intermediate values follow curve
	tests := []struct {
		tier    string
		baseBPS int
		wantBPS int
	}{
		{"new", 250, 250},     // 0 bps off
		{"elite", 250, 125},   // 125 bps off
		{"elite", 100, 0},     // discount exceeds base → clamped to 0
		{"unknown", 250, 250}, // unrecognized tier → no discount
	}

	for _, tt := range tests {
		got, err := engine.AdjustFeeBPS(ctx, tt.tier, tt.baseBPS)
		if err != nil {
			t.Fatalf("AdjustFeeBPS(%q, %d) error: %v", tt.tier, tt.baseBPS, err)
		}
		if got != tt.wantBPS {
			t.Errorf("AdjustFeeBPS(%q, %d) = %d, want %d", tt.tier, tt.baseBPS, got, tt.wantBPS)
		}
	}

	// Verify monotonic: higher tier → higher discount → lower effective BPS
	prev := 999
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		got, _ := engine.AdjustFeeBPS(ctx, tier, 250)
		if got > prev {
			t.Errorf("tier %s: adjusted %d should be <= previous %d", tier, got, prev)
		}
		prev = got
	}
}

func TestBoostScore(t *testing.T) {
	engine := NewIncentiveEngine()
	ctx := context.Background()

	// Endpoints exact
	got := engine.BoostScore(ctx, "new", 50.0)
	if got != 50.0 {
		t.Errorf("new tier: expected 50.0, got %f", got)
	}

	got = engine.BoostScore(ctx, "elite", 50.0)
	if math.Abs(got-75.0) > 0.1 {
		t.Errorf("elite tier: expected ~75.0, got %f", got)
	}

	// Monotonic: higher tier → higher boost
	prev := 0.0
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		got = engine.BoostScore(ctx, tier, 50.0)
		if got < prev-0.001 {
			t.Errorf("tier %s: boost %f should be >= previous %f", tier, got, prev)
		}
		prev = got
	}

	// Unknown tier → no boost
	got = engine.BoostScore(ctx, "unknown", 50.0)
	if got != 50.0 {
		t.Errorf("unknown: expected 50.0, got %f", got)
	}
}

func TestBoostScore_CapAt100(t *testing.T) {
	engine := NewIncentiveEngine()
	got := engine.BoostScore(context.Background(), "elite", 90.0)
	if got != 100 {
		t.Errorf("BoostScore(elite, 90) = %f, want 100 (capped)", got)
	}
}

func TestGetFeeDiscountBPS(t *testing.T) {
	engine := NewIncentiveEngine()
	if engine.GetFeeDiscountBPS("elite") != 125 {
		t.Error("expected 125 bps for elite")
	}
	if engine.GetFeeDiscountBPS("new") != 0 {
		t.Error("expected 0 for new tier")
	}
}

func TestGetDiscoveryBoostMultiplier(t *testing.T) {
	engine := NewIncentiveEngine()
	if engine.GetDiscoveryBoostMultiplier("elite") != 1.50 {
		t.Error("expected 1.50x for elite")
	}
	if engine.GetDiscoveryBoostMultiplier("new") != 1.0 {
		t.Error("expected 1.0x for new tier")
	}
	if engine.GetDiscoveryBoostMultiplier("unknown") != 1.0 {
		t.Error("expected 1.0x for unknown tier")
	}
}

func TestIncentiveSummary(t *testing.T) {
	engine := NewIncentiveEngine()
	summary := engine.IncentiveSummary()
	schedule, ok := summary["schedule"].([]map[string]interface{})
	if !ok {
		t.Fatal("expected schedule key in summary")
	}
	if len(schedule) != 5 {
		t.Errorf("expected 5 tiers, got %d", len(schedule))
	}

	// Verify elite tier values
	elite := schedule[4]
	if elite["tier"] != "elite" {
		t.Errorf("expected last tier to be elite, got %v", elite["tier"])
	}
	if elite["feeDiscountBPS"] != 125 {
		t.Errorf("expected 125 bps for elite, got %v", elite["feeDiscountBPS"])
	}
}

func TestGeometricIncentiveProperties(t *testing.T) {
	engine := NewIncentiveEngine()

	// Fee discounts should be monotonically increasing
	prevDiscount := -1
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		d := engine.GetFeeDiscountBPS(tier)
		if d < prevDiscount {
			t.Errorf("tier %s: discount %d should be >= %d", tier, d, prevDiscount)
		}
		prevDiscount = d
	}

	// Discovery boost multipliers should be monotonically increasing
	prevMult := 0.0
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		m := engine.GetDiscoveryBoostMultiplier(tier)
		if m < prevMult-0.001 {
			t.Errorf("tier %s: multiplier %f should be >= %f", tier, m, prevMult)
		}
		prevMult = m
	}

	// Endpoints exact
	if engine.GetFeeDiscountBPS("new") != 0 {
		t.Error("new tier fee discount should be 0")
	}
	if engine.GetFeeDiscountBPS("elite") != 125 {
		t.Error("elite tier fee discount should be 125")
	}
	if engine.GetDiscoveryBoostMultiplier("new") != 1.0 {
		t.Error("new tier boost should be 1.0x")
	}
	if engine.GetDiscoveryBoostMultiplier("elite") != 1.50 {
		t.Error("elite tier boost should be 1.50x")
	}
}
