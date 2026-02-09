package reputation

import (
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
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

func TestCalculateMetrics_PendingExcluded(t *testing.T) {
	// Pending transactions should NOT count toward reputation metrics.
	provider := NewRegistryProvider(nil)

	agent := &registry.Agent{
		Address:   "0x1234",
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}

	txns := []*registry.Transaction{
		{From: "0x1234", To: "0xaaaa", Amount: "1.00", Status: "confirmed", CreatedAt: time.Now()},
		{From: "0x1234", To: "0xbbbb", Amount: "2.00", Status: "pending", CreatedAt: time.Now()},
		{From: "0x1234", To: "0xcccc", Amount: "3.00", Status: "confirmed", CreatedAt: time.Now()},
		{From: "0x1234", To: "0xdddd", Amount: "4.00", Status: "pending", CreatedAt: time.Now()},
	}

	metrics := provider.calculateMetrics(agent, txns)

	// Only 2 confirmed transactions should be counted
	if metrics.TotalTransactions != 2 {
		t.Errorf("Expected 2 total transactions (excluding pending), got %d", metrics.TotalTransactions)
	}
	if metrics.SuccessfulTxns != 2 {
		t.Errorf("Expected 2 successful transactions, got %d", metrics.SuccessfulTxns)
	}
	// Volume should only include confirmed: 1.00 + 3.00 = 4.00
	if metrics.TotalVolumeUSD < 3.99 || metrics.TotalVolumeUSD > 4.01 {
		t.Errorf("Expected volume ~4.00 (excluding pending), got %f", metrics.TotalVolumeUSD)
	}
}

func TestCalculateMetrics_WashTradingCap(t *testing.T) {
	// maxTxPerCounterparty=5 should cap reputation from repeated counterparties.
	provider := NewRegistryProvider(nil)

	agent := &registry.Agent{
		Address:   "0x1234",
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}

	// 10 transactions all with the same counterparty
	var txns []*registry.Transaction
	for i := 0; i < 10; i++ {
		txns = append(txns, &registry.Transaction{
			From:      "0x1234",
			To:        "0xsame",
			Amount:    "1.00",
			Status:    "confirmed",
			CreatedAt: time.Now(),
		})
	}

	metrics := provider.calculateMetrics(agent, txns)

	// Only first 5 should count (maxTxPerCounterparty = 5)
	if metrics.TotalTransactions != 5 {
		t.Errorf("Expected 5 transactions (capped), got %d", metrics.TotalTransactions)
	}
	if metrics.TotalVolumeUSD < 4.99 || metrics.TotalVolumeUSD > 5.01 {
		t.Errorf("Expected volume ~5.00 (capped), got %f", metrics.TotalVolumeUSD)
	}
	// But unique counterparties should still count the one
	if metrics.UniqueCounterparties != 1 {
		t.Errorf("Expected 1 unique counterparty, got %d", metrics.UniqueCounterparties)
	}
}

func TestCalculateMetrics_FailedTransactions(t *testing.T) {
	provider := NewRegistryProvider(nil)

	agent := &registry.Agent{
		Address:   "0x1234",
		CreatedAt: time.Now().Add(-48 * time.Hour),
	}

	txns := []*registry.Transaction{
		{From: "0x1234", To: "0xaaaa", Amount: "1.00", Status: "confirmed", CreatedAt: time.Now()},
		{From: "0x1234", To: "0xbbbb", Amount: "2.00", Status: "failed", CreatedAt: time.Now()},
		{From: "0x1234", To: "0xcccc", Amount: "3.00", Status: "reverted", CreatedAt: time.Now()},
	}

	metrics := provider.calculateMetrics(agent, txns)

	if metrics.TotalTransactions != 3 {
		t.Errorf("Expected 3 total (confirmed+failed+reverted), got %d", metrics.TotalTransactions)
	}
	if metrics.SuccessfulTxns != 1 {
		t.Errorf("Expected 1 successful, got %d", metrics.SuccessfulTxns)
	}
	if metrics.FailedTxns != 2 {
		t.Errorf("Expected 2 failed, got %d", metrics.FailedTxns)
	}
	// Volume should only include confirmed
	if metrics.TotalVolumeUSD < 0.99 || metrics.TotalVolumeUSD > 1.01 {
		t.Errorf("Expected volume ~1.00 (only confirmed), got %f", metrics.TotalVolumeUSD)
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
