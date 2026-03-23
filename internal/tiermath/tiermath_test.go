package tiermath

import (
	"math"
	"testing"
)

func TestScale_Endpoints(t *testing.T) {
	if Scale(50, 100000, "new") != 50 {
		t.Error("new should return base")
	}
	if Scale(50, 100000, "elite") != 100000 {
		t.Error("elite should return max")
	}
}

func TestScale_Monotonic(t *testing.T) {
	prev := 0.0
	for _, tier := range TierNames {
		v := Scale(10, 1000, tier)
		if v < prev {
			t.Errorf("tier %s: %f should be >= %f", tier, v, prev)
		}
		prev = v
	}
}

func TestScale_UnknownTierDefaultsToEstablished(t *testing.T) {
	got := Scale(10, 1000, "bogus")
	want := Scale(10, 1000, "established")
	if got != want {
		t.Errorf("unknown tier = %f, want established = %f", got, want)
	}
}

func TestScale_BaseEqualsMax(t *testing.T) {
	if Scale(42, 42, "trusted") != 42 {
		t.Error("base == max should return base for all tiers")
	}
}

func TestScale_BaseZero(t *testing.T) {
	if Scale(0, 100, "new") != 0 {
		t.Error("base=0, new tier should be 0")
	}
	if Scale(0, 100, "elite") != 100 {
		t.Error("base=0, elite tier should be max")
	}
	// Intermediate: linear interpolation
	mid := Scale(0, 100, "established")
	if mid < 40 || mid > 60 {
		t.Errorf("base=0, established should be ~50, got %f", mid)
	}
}

func TestScale_NegativeInputs(t *testing.T) {
	if Scale(-5, 100, "elite") != -5 {
		t.Error("negative base should return base")
	}
	if Scale(5, -100, "elite") != 5 {
		t.Error("negative max should return base")
	}
}

func TestIndex_KnownTiers(t *testing.T) {
	for i, name := range TierNames {
		if Index(name) != i {
			t.Errorf("Index(%q) = %d, want %d", name, Index(name), i)
		}
	}
}

func TestIndex_UnknownDefaultsToEstablished(t *testing.T) {
	if Index("nonexistent") != 2 {
		t.Errorf("unknown tier should default to 2 (established), got %d", Index("nonexistent"))
	}
}

func TestTierCurve_At(t *testing.T) {
	c := TierCurve{Base: 3, Max: 100}
	if c.At("new") != 3 {
		t.Error("At(new) should be base")
	}
	if c.At("elite") != 100 {
		t.Error("At(elite) should be max")
	}
}

func TestTierCurve_AtInt(t *testing.T) {
	c := TierCurve{Base: 3, Max: 100}
	if c.AtInt("new") != 3 {
		t.Error("AtInt(new) should be 3")
	}
	if c.AtInt("elite") != 100 {
		t.Error("AtInt(elite) should be 100")
	}
}

func TestTierCurve_All(t *testing.T) {
	c := TierCurve{Base: 10, Max: 1000}
	all := c.All()
	if len(all) != NumTiers {
		t.Fatalf("All() should return %d values", NumTiers)
	}
	if all[0] != 10 {
		t.Errorf("All()[0] = %f, want 10", all[0])
	}
	if all[NumTiers-1] != 1000 {
		t.Errorf("All()[4] = %f, want 1000", all[NumTiers-1])
	}
	// Monotonic
	for i := 1; i < NumTiers; i++ {
		if all[i] < all[i-1] {
			t.Errorf("All()[%d]=%f < All()[%d]=%f", i, all[i], i-1, all[i-1])
		}
	}
}

func TestScale_ConstantRatio(t *testing.T) {
	vals := make([]float64, NumTiers)
	for i, tier := range TierNames {
		vals[i] = Scale(10, 10000, tier)
	}
	expectedRatio := math.Pow(10000.0/10.0, 1.0/4.0)
	for i := 1; i < NumTiers; i++ {
		ratio := vals[i] / vals[i-1]
		if math.Abs(ratio-expectedRatio)/expectedRatio > 0.001 {
			t.Errorf("ratio %d/%d = %f, want %f", i, i-1, ratio, expectedRatio)
		}
	}
}
