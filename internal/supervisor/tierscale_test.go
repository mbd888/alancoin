package supervisor

import (
	"math"
	"math/big"
	"testing"
)

func TestGeometricScale_Endpoints(t *testing.T) {
	// The formula must return exactly base at tier 0 and max at tier 4.
	tests := []struct {
		name      string
		base, max float64
		tier      string
		want      float64
	}{
		{"velocity_new", VelocityCurve.Base, VelocityCurve.Max, "new", VelocityCurve.Base},
		{"velocity_elite", VelocityCurve.Base, VelocityCurve.Max, "elite", VelocityCurve.Max},
		{"concurrency_new", ConcurrencyCurve.Base, ConcurrencyCurve.Max, "new", ConcurrencyCurve.Base},
		{"concurrency_elite", ConcurrencyCurve.Base, ConcurrencyCurve.Max, "elite", ConcurrencyCurve.Max},
		{"pertx_new", PerTxCurve.Base, PerTxCurve.Max, "new", PerTxCurve.Base},
		{"pertx_elite", PerTxCurve.Base, PerTxCurve.Max, "elite", PerTxCurve.Max},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GeometricScale(tt.base, tt.max, tt.tier)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("GeometricScale(%g, %g, %q) = %g, want %g", tt.base, tt.max, tt.tier, got, tt.want)
			}
		})
	}
}

func TestGeometricScale_MonotonicallyIncreasing(t *testing.T) {
	tiers := []string{"new", "emerging", "established", "trusted", "elite"}
	dimensions := []struct {
		name      string
		base, max float64
	}{
		{"velocity", VelocityCurve.Base, VelocityCurve.Max},
		{"concurrency", ConcurrencyCurve.Base, ConcurrencyCurve.Max},
		{"pertx", PerTxCurve.Base, PerTxCurve.Max},
	}

	for _, dim := range dimensions {
		prev := 0.0
		for _, tier := range tiers {
			v := GeometricScale(dim.base, dim.max, tier)
			if v <= prev {
				t.Errorf("%s: tier %s value %g should be > previous %g", dim.name, tier, v, prev)
			}
			prev = v
		}
	}
}

func TestGeometricScale_ConstantMultiplier(t *testing.T) {
	// The geometric property: each step multiplies by the same ratio.
	// ratio = (max/base)^(1/4)
	tiers := []string{"new", "emerging", "established", "trusted", "elite"}

	v := make([]float64, 5)
	for i, tier := range tiers {
		v[i] = GeometricScale(VelocityCurve.Base, VelocityCurve.Max, tier)
	}

	// All consecutive ratios should be equal
	expectedRatio := math.Pow(VelocityCurve.Max/VelocityCurve.Base, 0.25)
	for i := 1; i < 5; i++ {
		ratio := v[i] / v[i-1]
		if math.Abs(ratio-expectedRatio)/expectedRatio > 0.001 {
			t.Errorf("ratio %s/%s = %g, expected %g",
				tiers[i], tiers[i-1], ratio, expectedRatio)
		}
	}
}

func TestGeometricScale_UnknownTierDefaultsToEstablished(t *testing.T) {
	got := GeometricScale(VelocityCurve.Base, VelocityCurve.Max, "nonexistent")
	want := GeometricScale(VelocityCurve.Base, VelocityCurve.Max, "established")
	if got != want {
		t.Errorf("unknown tier = %g, want established = %g", got, want)
	}
}

func TestVelocityLimitForTier_ReturnsValidBigInt(t *testing.T) {
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		limit := VelocityLimitForTier(tier)
		if limit.Sign() <= 0 {
			t.Errorf("tier %s: velocity limit should be positive, got %s", tier, limit.String())
		}
	}

	// New tier should be exactly $50 = 50_000_000 micro-USDC
	newLimit := VelocityLimitForTier("new")
	if newLimit.Cmp(big.NewInt(50_000_000)) != 0 {
		t.Errorf("new tier velocity = %s, want 50000000", newLimit.String())
	}

	// Elite tier should be exactly $100,000 = 100_000_000_000 micro-USDC
	eliteLimit := VelocityLimitForTier("elite")
	if eliteLimit.Cmp(big.NewInt(100_000_000_000)) != 0 {
		t.Errorf("elite tier velocity = %s, want 100000000000", eliteLimit.String())
	}
}

func TestConcurrencyLimitForTier_Values(t *testing.T) {
	// Endpoints must be exact
	if ConcurrencyLimitForTier("new") != 3 {
		t.Errorf("new = %d, want 3", ConcurrencyLimitForTier("new"))
	}
	if ConcurrencyLimitForTier("elite") != 100 {
		t.Errorf("elite = %d, want 100", ConcurrencyLimitForTier("elite"))
	}

	// All values should be positive integers
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		v := ConcurrencyLimitForTier(tier)
		if v < 1 {
			t.Errorf("tier %s: concurrency limit should be >= 1, got %d", tier, v)
		}
	}
}

func TestPerTxLimitForTier_NewIsFiveDollars(t *testing.T) {
	limit := PerTxLimitForTier("new")
	// $5 = 5_000_000
	if limit.Cmp(big.NewInt(5_000_000)) != 0 {
		t.Errorf("new tier per-tx = %s, want 5000000", limit.String())
	}
}
