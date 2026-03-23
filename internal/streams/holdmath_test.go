package streams

import (
	"math"
	"testing"
)

func TestRecommendHold_BasicCase(t *testing.T) {
	// 2 ticks/sec, $0.001/tick, 5 minutes, 95% confidence
	hold := RecommendHold(0.001, 2.0, 300, 0.95)

	// Expected ticks: 600. Poisson 95th percentile of 600 ≈ 640.
	// Hold ≈ 640 × $0.001 = $0.640
	expected := ExpectedCost(0.001, 2.0, 300)
	if hold <= expected {
		t.Errorf("hold %f should exceed expected cost %f (padding for confidence)", hold, expected)
	}
	if hold > expected*1.5 {
		t.Errorf("hold %f should not be more than 50%% above expected %f", hold, expected)
	}
}

func TestRecommendHold_HighConfidence(t *testing.T) {
	// Higher confidence → larger hold
	hold95 := RecommendHold(0.01, 1.0, 60, 0.95)
	hold99 := RecommendHold(0.01, 1.0, 60, 0.99)

	if hold99 <= hold95 {
		t.Errorf("99%% confidence hold %f should exceed 95%% hold %f", hold99, hold95)
	}
}

func TestRecommendHold_HighTickRate(t *testing.T) {
	// 100 ticks/sec (uses normal approximation path)
	hold := RecommendHold(0.001, 100.0, 60, 0.99)

	expected := ExpectedCost(0.001, 100.0, 60) // $6.00
	if hold <= expected {
		t.Errorf("hold %f should exceed expected %f", hold, expected)
	}
	if hold > expected*1.3 {
		t.Errorf("hold %f shouldn't be >30%% above expected %f for large λ", hold, expected)
	}
}

func TestRecommendHold_InvalidInputs(t *testing.T) {
	if RecommendHold(0, 1, 60, 0.95) != 0 {
		t.Error("zero price should return 0")
	}
	if RecommendHold(0.01, 0, 60, 0.95) != 0 {
		t.Error("zero tick rate should return 0")
	}
	if RecommendHold(0.01, 1, 0, 0.95) != 0 {
		t.Error("zero duration should return 0")
	}
	if RecommendHold(0.01, 1, 60, 0) != 0 {
		t.Error("zero confidence should return 0")
	}
	if RecommendHold(0.01, 1, 60, 1) != 0 {
		t.Error("confidence=1 should return 0")
	}
}

func TestExpectedCost(t *testing.T) {
	// 10 ticks/sec, $0.50/tick, 120 seconds = 10 × 120 × 0.50 = $600
	cost := ExpectedCost(0.50, 10, 120)
	if math.Abs(cost-600) > 0.01 {
		t.Errorf("expected cost = %f, want 600", cost)
	}
}

func TestHoldEfficiency(t *testing.T) {
	// If hold is exactly the expected cost, efficiency = 1.0 (maximum risk)
	eff := HoldEfficiency(600, 0.50, 10, 120)
	if math.Abs(eff-1.0) > 0.01 {
		t.Errorf("efficiency at expected cost = %f, want 1.0", eff)
	}

	// If hold is 2× expected, efficiency = 0.5
	eff = HoldEfficiency(1200, 0.50, 10, 120)
	if math.Abs(eff-0.5) > 0.01 {
		t.Errorf("efficiency at 2× expected = %f, want 0.5", eff)
	}
}

func TestPoissonQuantile_SmallLambda(t *testing.T) {
	// Poisson(5), 95th percentile should be ~8-9
	k := poissonQuantile(5, 0.95)
	if k < 8 || k > 10 {
		t.Errorf("Poisson(5) 95th percentile = %d, expected 8-10", k)
	}
}

func TestPoissonQuantile_LargeLambda(t *testing.T) {
	// Poisson(1000), 95th percentile ≈ 1000 + 1.645 × √1000 ≈ 1052
	k := poissonQuantile(1000, 0.95)
	expected := 1000 + 1.645*math.Sqrt(1000)
	if math.Abs(float64(k)-expected) > 5 {
		t.Errorf("Poisson(1000) 95th percentile = %d, expected ~%f", k, expected)
	}
}

func TestNormalQuantile(t *testing.T) {
	// z(0.95) ≈ 1.645
	z := normalQuantile(0.95)
	if math.Abs(z-1.645) > 0.01 {
		t.Errorf("z(0.95) = %f, want ~1.645", z)
	}

	// z(0.50) = 0
	z = normalQuantile(0.50)
	if z != 0 {
		t.Errorf("z(0.50) = %f, want 0", z)
	}

	// z(0.99) ≈ 2.326
	z = normalQuantile(0.99)
	if math.Abs(z-2.326) > 0.02 {
		t.Errorf("z(0.99) = %f, want ~2.326", z)
	}
}

func TestRecommendHold_MonotonicInConfidence(t *testing.T) {
	confidences := []float64{0.50, 0.80, 0.90, 0.95, 0.99}
	prev := 0.0
	for _, c := range confidences {
		hold := RecommendHold(0.01, 5.0, 120, c)
		if hold < prev {
			t.Errorf("hold at confidence %f (%f) should be >= hold at lower confidence (%f)",
				c, hold, prev)
		}
		prev = hold
	}
}
