package flywheel

import (
	"context"
	"testing"
)

func TestAdjustFeeBPS(t *testing.T) {
	engine := NewIncentiveEngine()
	ctx := context.Background()

	tests := []struct {
		tier    string
		baseBPS int
		wantBPS int
	}{
		{"new", 250, 250},         // no discount
		{"emerging", 250, 225},    // 25 bps off
		{"established", 250, 200}, // 50 bps off
		{"trusted", 250, 163},     // 87 bps off
		{"elite", 250, 125},       // 125 bps off
		{"elite", 100, 0},         // discount exceeds base → clamped to 0
		{"unknown", 250, 250},     // unrecognized tier → no discount
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
}

func TestBoostScore(t *testing.T) {
	engine := NewIncentiveEngine()
	ctx := context.Background()

	tests := []struct {
		tier      string
		baseScore float64
		wantMin   float64
		wantMax   float64
	}{
		{"new", 50.0, 50.0, 50.0},         // 1.0x
		{"emerging", 50.0, 52.4, 52.6},    // 1.05x
		{"established", 50.0, 57.4, 57.6}, // 1.15x
		{"trusted", 50.0, 64.9, 65.1},     // 1.30x
		{"elite", 50.0, 74.9, 75.1},       // 1.50x
		{"elite", 80.0, 99.9, 100.0},      // capped at 100
		{"unknown", 50.0, 50.0, 50.0},     // unrecognized → no boost
	}

	for _, tt := range tests {
		got := engine.BoostScore(ctx, tt.tier, tt.baseScore)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("BoostScore(%q, %.1f) = %.2f, want [%.1f, %.1f]",
				tt.tier, tt.baseScore, got, tt.wantMin, tt.wantMax)
		}
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
	if engine.GetFeeDiscountBPS("unknown") != 0 {
		t.Error("expected 0 for unknown tier")
	}
}

func TestGetDiscoveryBoostMultiplier(t *testing.T) {
	engine := NewIncentiveEngine()
	if engine.GetDiscoveryBoostMultiplier("elite") != 1.50 {
		t.Error("expected 1.50x for elite")
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
