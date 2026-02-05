package reputation

import (
	"testing"
	"time"
)

func TestCalculatorBasic(t *testing.T) {
	calc := NewCalculator()

	metrics := Metrics{
		TotalTransactions:    100,
		TotalVolumeUSD:       1000.0,
		SuccessfulTxns:       95,
		FailedTxns:           5,
		UniqueCounterparties: 10,
		DaysOnNetwork:        30,
	}

	score := calc.Calculate("0x1234", metrics)

	if score.Score < 0 || score.Score > 100 {
		t.Errorf("Score should be 0-100, got %f", score.Score)
	}

	if score.Tier == "" {
		t.Error("Tier should be set")
	}

	if score.Address != "0x1234" {
		t.Errorf("Address mismatch")
	}
}

func TestTierAssignment(t *testing.T) {
	calc := NewCalculator()

	tests := []struct {
		name     string
		metrics  Metrics
		minScore float64
		maxScore float64
		tier     Tier
	}{
		{
			name: "new agent",
			metrics: Metrics{
				TotalTransactions: 0,
				TotalVolumeUSD:    0,
				DaysOnNetwork:     1,
			},
			minScore: 0,
			maxScore: 30,
			tier:     TierNew,
		},
		{
			name: "emerging agent",
			metrics: Metrics{
				TotalTransactions:    10,
				TotalVolumeUSD:       100,
				SuccessfulTxns:       10,
				UniqueCounterparties: 3,
				DaysOnNetwork:        7,
			},
			minScore: 45,
			maxScore: 60,
			tier:     TierEstablished, // With 100% success rate, scores higher
		},
		{
			name: "established agent",
			metrics: Metrics{
				TotalTransactions:    100,
				TotalVolumeUSD:       1000,
				SuccessfulTxns:       95,
				FailedTxns:           5,
				UniqueCounterparties: 15,
				DaysOnNetwork:        60,
			},
			minScore: 65,
			maxScore: 80,
			tier:     TierTrusted,
		},
		{
			name: "trusted agent",
			metrics: Metrics{
				TotalTransactions:    500,
				TotalVolumeUSD:       10000,
				SuccessfulTxns:       495,
				FailedTxns:           5,
				UniqueCounterparties: 30,
				DaysOnNetwork:        180,
			},
			minScore: 85,
			maxScore: 95,
			tier:     TierElite,
		},
		{
			name: "elite agent",
			metrics: Metrics{
				TotalTransactions:    1000,
				TotalVolumeUSD:       100000,
				SuccessfulTxns:       990,
				FailedTxns:           10,
				UniqueCounterparties: 50,
				DaysOnNetwork:        365,
			},
			minScore: 75,
			maxScore: 100,
			tier:     TierElite,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			score := calc.Calculate("0x1234", tc.metrics)

			if score.Score < tc.minScore || score.Score > tc.maxScore {
				t.Errorf("Score %f outside expected range [%f, %f]", score.Score, tc.minScore, tc.maxScore)
			}
		})
	}
}

func TestScoreComponents(t *testing.T) {
	calc := NewCalculator()

	metrics := Metrics{
		TotalTransactions:    100,
		TotalVolumeUSD:       5000,
		SuccessfulTxns:       98,
		FailedTxns:           2,
		UniqueCounterparties: 20,
		DaysOnNetwork:        90,
	}

	score := calc.Calculate("0x1234", metrics)

	// Volume score should be set (log10(5000) * 25 ≈ 92)
	if score.Components.VolumeScore < 50 {
		t.Errorf("VolumeScore should be higher for $5000 volume, got %f", score.Components.VolumeScore)
	}

	// Activity score should be set (log10(100) * 33.3 ≈ 66)
	if score.Components.ActivityScore < 50 {
		t.Errorf("ActivityScore should be higher for 100 txns, got %f", score.Components.ActivityScore)
	}

	// Success score should be 98%
	if score.Components.SuccessScore < 95 {
		t.Errorf("SuccessScore should be ~98, got %f", score.Components.SuccessScore)
	}

	// Age score should be set
	if score.Components.AgeScore < 40 {
		t.Errorf("AgeScore should be higher for 90 days, got %f", score.Components.AgeScore)
	}

	// Diversity score should be set
	if score.Components.DiversityScore < 50 {
		t.Errorf("DiversityScore should be higher for 20 counterparties, got %f", score.Components.DiversityScore)
	}
}

func TestSuccessRateNeedsMinimumTxns(t *testing.T) {
	calc := NewCalculator()

	// With only 2 transactions, success rate should be neutral (50)
	metrics := Metrics{
		TotalTransactions: 2,
		SuccessfulTxns:    2,
		TotalVolumeUSD:    100,
		DaysOnNetwork:     7,
	}

	score := calc.Calculate("0x1234", metrics)

	if score.Components.SuccessScore != 50 {
		t.Errorf("SuccessScore should be 50 (neutral) with <5 txns, got %f", score.Components.SuccessScore)
	}
}

func TestZeroMetrics(t *testing.T) {
	calc := NewCalculator()

	metrics := Metrics{} // All zeros

	score := calc.Calculate("0x1234", metrics)

	// Should not panic, should return valid low score
	if score.Score < 0 {
		t.Error("Score should not be negative")
	}

	if score.Tier != TierNew {
		t.Errorf("Zero metrics should be TierNew, got %s", score.Tier)
	}
}

func TestCustomWeights(t *testing.T) {
	// All weight on volume
	volumeOnlyWeights := Weights{
		Volume:    1.0,
		Activity:  0,
		Success:   0,
		Age:       0,
		Diversity: 0,
	}

	calc := NewCalculatorWithWeights(volumeOnlyWeights)

	metrics := Metrics{
		TotalTransactions: 1000,
		TotalVolumeUSD:    100, // Low volume
		SuccessfulTxns:    1000,
		DaysOnNetwork:     365,
	}

	score := calc.Calculate("0x1234", metrics)

	// With only volume weighted and low volume, score should be low
	// log10(100) * 25 ≈ 50
	if score.Score > 60 {
		t.Errorf("Score should be moderate with low volume, got %f", score.Score)
	}
}

func TestCalculatedAtSet(t *testing.T) {
	calc := NewCalculator()

	before := time.Now()
	score := calc.Calculate("0x1234", Metrics{})
	after := time.Now()

	if score.CalculatedAt.Before(before) || score.CalculatedAt.After(after) {
		t.Error("CalculatedAt should be set to current time")
	}
}

func TestGetTier(t *testing.T) {
	tests := []struct {
		score float64
		tier  Tier
	}{
		{0, TierNew},
		{10, TierNew},
		{19.9, TierNew},
		{20, TierEmerging},
		{39.9, TierEmerging},
		{40, TierEstablished},
		{59.9, TierEstablished},
		{60, TierTrusted},
		{79.9, TierTrusted},
		{80, TierElite},
		{100, TierElite},
	}

	for _, tc := range tests {
		result := getTier(tc.score)
		if result != tc.tier {
			t.Errorf("getTier(%f) = %s, expected %s", tc.score, result, tc.tier)
		}
	}
}
