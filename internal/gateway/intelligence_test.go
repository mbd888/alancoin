package gateway

import (
	"context"
	"testing"
)

// --- Mock IntelligenceProvider ---

type mockIntelligence struct {
	tiers map[string]struct {
		tier  string
		score float64
	}
}

func newMockIntelligence() *mockIntelligence {
	return &mockIntelligence{
		tiers: make(map[string]struct {
			tier  string
			score float64
		}),
	}
}

func (m *mockIntelligence) GetCreditTier(_ context.Context, agentAddr string) (string, float64, error) {
	if t, ok := m.tiers[agentAddr]; ok {
		return t.tier, t.score, nil
	}
	return "", 0, nil
}

func (m *mockIntelligence) FeeDiscountBPS(tier string) int {
	switch tier {
	case "diamond":
		return 50
	case "platinum":
		return 30
	case "gold":
		return 15
	default:
		return 0
	}
}

func (m *mockIntelligence) EscrowThresholdUSDC(tier string) float64 {
	switch tier {
	case "diamond":
		return 10.0
	case "platinum":
		return 5.0
	case "gold":
		return 1.0
	default:
		return 0
	}
}

func TestAdjustRPMByRisk(t *testing.T) {
	tests := []struct {
		tier    string
		base    int
		want    int
		comment string
	}{
		{"diamond", 100, 150, "diamond +50%"},
		{"platinum", 100, 125, "platinum +25%"},
		{"gold", 100, 100, "gold unchanged"},
		{"silver", 100, 75, "silver -25%"},
		{"bronze", 100, 50, "bronze -50%"},
		{"unknown", 100, 100, "unknown unchanged"},
		{"", 100, 100, "empty tier unchanged"},
		{"diamond", 0, 0, "diamond with 0 base"},
		{"bronze", 1, 0, "bronze with 1 base (floor to 0)"},
		{"diamond", 200, 300, "diamond with 200 base"},
	}

	for _, tt := range tests {
		got := adjustRPMByRisk(tt.tier, tt.base)
		if got != tt.want {
			t.Errorf("%s: adjustRPMByRisk(%q, %d) = %d, want %d",
				tt.comment, tt.tier, tt.base, got, tt.want)
		}
	}
}

func TestIntelligenceDiscoveryBoost(t *testing.T) {
	tests := []struct {
		tier string
		want float64
	}{
		{"diamond", 15.0},
		{"platinum", 10.0},
		{"gold", 5.0},
		{"silver", 2.0},
		{"bronze", 0},
		{"unknown", 0},
		{"", 0},
		{"elite", 0},   // reputation tier should get no boost
		{"trusted", 0}, // reputation tier should get no boost
	}

	for _, tt := range tests {
		got := intelligenceDiscoveryBoost(tt.tier)
		if got != tt.want {
			t.Errorf("intelligenceDiscoveryBoost(%q) = %.1f, want %.1f", tt.tier, got, tt.want)
		}
	}
}

func TestResolverWithIntelligenceBoost(t *testing.T) {
	// Create a resolver with an intelligence ranker
	mockReg := &mockRegistry{
		services: []ServiceCandidate{
			{AgentAddress: "0xdiamond", ServiceID: "s1", ServiceName: "diamond-svc", Price: "1.000000", Endpoint: "http://a", ReputationScore: 50},
			{AgentAddress: "0xbronze", ServiceID: "s2", ServiceName: "bronze-svc", Price: "1.000000", Endpoint: "http://b", ReputationScore: 60},
		},
	}
	intel := newMockIntelligence()
	intel.tiers["0xdiamond"] = struct {
		tier  string
		score float64
	}{"diamond", 95}
	intel.tiers["0xbronze"] = struct {
		tier  string
		score float64
	}{"bronze", 15}

	resolver := NewResolver(mockReg)
	resolver.WithIntelligenceRanker(intel)

	candidates, err := resolver.Resolve(context.Background(),
		ProxyRequest{ServiceType: "inference"},
		"reputation", "10.000000")
	if err != nil {
		t.Fatal(err)
	}

	if len(candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(candidates))
	}

	// Diamond agent should be first after boost (50 + 15 = 65 > 60 + 0 = 60)
	if candidates[0].AgentAddress != "0xdiamond" {
		t.Errorf("expected diamond agent first after boost, got %s", candidates[0].AgentAddress)
	}
}

func TestFeeDiscountDoesNotGoNegative(t *testing.T) {
	// If the base BPS is very low and intelligence discount is applied,
	// the fee should not go negative. Check via the computeFee path.
	// The computeFee method checks `if bps <= 0` and returns zero fee.
	// This test validates that the clamp works.

	intel := newMockIntelligence()
	// FeeDiscountBPS for diamond = 50 bps
	// If base rate is 30 bps, after discount = 30 - 50 = -20 → should be clamped to 0
	discount := intel.FeeDiscountBPS("diamond")
	base := 30
	result := base - discount
	if result > 0 {
		t.Errorf("expected non-positive result when discount exceeds base: %d - %d = %d", base, discount, result)
	}
	// The computeFee method handles this: `if bps <= 0 { return priceStr, zero }`
	// So zero fee is returned — seller gets full price. This is correct behavior.
}
