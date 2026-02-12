package stakes

import (
	"context"
	"math"
	"testing"
	"time"
)

func TestPortfolioAnalytics_Basic(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	// Create two stakes from different agents
	stake1, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})
	stake2, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent2",
		RevenueShare:  0.20,
		TotalShares:   50,
		PricePerShare: "2.000000",
		VestingPeriod: "1d",
	})

	// Invest
	investor := "0xinvestor"
	svc.Invest(context.Background(), stake1.ID, InvestRequest{InvestorAddr: investor, Shares: 10})
	svc.Invest(context.Background(), stake2.ID, InvestRequest{InvestorAddr: investor, Shares: 5})

	// Simulate earnings by updating holdings directly
	holdings, _ := store.ListHoldingsByInvestor(context.Background(), investor)
	for _, h := range holdings {
		h.TotalEarned = "2.000000"
		store.UpdateHolding(context.Background(), h)
	}

	pa, err := analytics.GetPortfolioAnalytics(context.Background(), investor)
	if err != nil {
		t.Fatalf("portfolio analytics: %v", err)
	}

	if pa.HoldingCount != 2 {
		t.Errorf("expected 2 holdings, got %d", pa.HoldingCount)
	}
	if pa.ActiveHoldings != 2 {
		t.Errorf("expected 2 active, got %d", pa.ActiveHoldings)
	}
	// Total invested: 10*1 + 5*2 = 20
	if pa.TotalInvested != "20.000000" {
		t.Errorf("expected totalInvested 20.000000, got %s", pa.TotalInvested)
	}
	// Total earned: 2 + 2 = 4
	if pa.TotalEarned != "4.000000" {
		t.Errorf("expected totalEarned 4.000000, got %s", pa.TotalEarned)
	}
	// ROI: 4/20 * 100 = 20%
	if pa.ROI != 20.0 {
		t.Errorf("expected ROI 20.0, got %.2f", pa.ROI)
	}
	// Diversification: 1 - ((10/20)^2 + (10/20)^2) = 1 - 0.5 = 0.5
	if math.Abs(pa.DiversificationIndex-0.5) > 0.01 {
		t.Errorf("expected diversification ~0.5, got %.4f", pa.DiversificationIndex)
	}

	if len(pa.Holdings) != 2 {
		t.Fatalf("expected 2 holding analytics, got %d", len(pa.Holdings))
	}
}

func TestPortfolioAnalytics_Empty(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewStakeAnalyticsService(store)

	pa, err := analytics.GetPortfolioAnalytics(context.Background(), "0xnobody")
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	if pa.HoldingCount != 0 {
		t.Errorf("expected 0 holdings, got %d", pa.HoldingCount)
	}
	if pa.TotalInvested != "0.000000" {
		t.Errorf("expected totalInvested 0.000000, got %s", pa.TotalInvested)
	}
	if pa.ROI != 0 {
		t.Errorf("expected 0 ROI, got %.2f", pa.ROI)
	}
	if pa.DiversificationIndex != 0 {
		t.Errorf("expected 0 diversification, got %.4f", pa.DiversificationIndex)
	}
}

func TestPortfolioAnalytics_SingleHolding(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "5.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{InvestorAddr: "0xinvestor", Shares: 20})

	pa, err := analytics.GetPortfolioAnalytics(context.Background(), "0xinvestor")
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// Single holding → diversification should be 0
	if pa.DiversificationIndex != 0 {
		t.Errorf("expected 0 diversification for single holding, got %.4f", pa.DiversificationIndex)
	}
	// Total invested: 20 * 5 = 100
	if pa.TotalInvested != "100.000000" {
		t.Errorf("expected totalInvested 100.000000, got %s", pa.TotalInvested)
	}
}

func TestStakeNAV_Basic(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{InvestorAddr: "0xinvestor", Shares: 50})

	// Add undistributed revenue manually
	s, _ := store.GetStake(context.Background(), stake.ID)
	s.Undistributed = "10.000000"
	store.UpdateStake(context.Background(), s)

	nav, err := analytics.GetStakeNAV(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("nav: %v", err)
	}

	if nav.IssuedShares != 50 {
		t.Errorf("expected 50 issued shares, got %d", nav.IssuedShares)
	}
	// Pool value: 50 raised + 10 undistributed = 60
	if nav.TotalPoolValue != "60.000000" {
		t.Errorf("expected pool value 60.000000, got %s", nav.TotalPoolValue)
	}
	// NAV per share: 60 / 50 = 1.2
	if nav.NAVPerShare != "1.200000" {
		t.Errorf("expected navPerShare 1.200000, got %s", nav.NAVPerShare)
	}
	if nav.PricePerShare != "1.000000" {
		t.Errorf("expected pricePerShare 1.000000, got %s", nav.PricePerShare)
	}
}

func TestStakeNAV_NoShares(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})

	nav, err := analytics.GetStakeNAV(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("nav: %v", err)
	}

	if nav.IssuedShares != 0 {
		t.Errorf("expected 0 issued shares, got %d", nav.IssuedShares)
	}
	if nav.NAVPerShare != "0.000000" {
		t.Errorf("expected navPerShare 0.000000, got %s", nav.NAVPerShare)
	}
}

func TestStakeNAV_NotFound(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewStakeAnalyticsService(store)

	_, err := analytics.GetStakeNAV(context.Background(), "stk_nonexistent")
	if err != ErrStakeNotFound {
		t.Errorf("expected ErrStakeNotFound, got %v", err)
	}
}

func TestStakePerformance_Basic(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})
	svc.Invest(context.Background(), stake.ID, InvestRequest{InvestorAddr: "0xinvestor1", Shares: 30})
	svc.Invest(context.Background(), stake.ID, InvestRequest{InvestorAddr: "0xinvestor2", Shares: 20})

	// Simulate distributions
	now := time.Now()
	store.CreateDistribution(context.Background(), &Distribution{
		ID:             "dist_1",
		StakeID:        stake.ID,
		AgentAddr:      "0xagent1",
		RevenueAmount:  "5.000000",
		ShareAmount:    "5.000000",
		PerShareAmount: "0.100000",
		ShareCount:     50,
		HoldingCount:   2,
		Status:         "completed",
		CreatedAt:      now,
	})
	store.CreateDistribution(context.Background(), &Distribution{
		ID:             "dist_2",
		StakeID:        stake.ID,
		AgentAddr:      "0xagent1",
		RevenueAmount:  "3.000000",
		ShareAmount:    "3.000000",
		PerShareAmount: "0.060000",
		ShareCount:     50,
		HoldingCount:   2,
		Status:         "completed",
		CreatedAt:      now,
	})

	// Update stake totals
	s, _ := store.GetStake(context.Background(), stake.ID)
	s.TotalDistributed = "8.000000"
	s.Undistributed = "2.000000"
	store.UpdateStake(context.Background(), s)

	perf, err := analytics.GetStakePerformance(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("performance: %v", err)
	}

	if perf.IssuedShares != 50 {
		t.Errorf("expected 50 issued, got %d", perf.IssuedShares)
	}
	if perf.HolderCount != 2 {
		t.Errorf("expected 2 holders, got %d", perf.HolderCount)
	}
	if perf.DistributionCount != 2 {
		t.Errorf("expected 2 distributions, got %d", perf.DistributionCount)
	}
	// Cumulative return: 8 / 50 * 100 = 16%
	if perf.CumulativeReturn != 16.0 {
		t.Errorf("expected 16.0%% cumulative return, got %.2f%%", perf.CumulativeReturn)
	}
	// Avg per-share: (0.100000 + 0.060000) / 2 = 0.080000
	if perf.AvgPerSharePayout != "0.080000" {
		t.Errorf("expected avgPerSharePayout 0.080000, got %s", perf.AvgPerSharePayout)
	}
	if perf.TotalDistributed != "8.000000" {
		t.Errorf("expected totalDistributed 8.000000, got %s", perf.TotalDistributed)
	}
	if len(perf.RecentDistributions) != 2 {
		t.Errorf("expected 2 recent distributions, got %d", len(perf.RecentDistributions))
	}
}

func TestStakePerformance_NoDistributions(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockLedger())
	analytics := NewStakeAnalyticsService(store)

	stake, _ := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     "0xagent1",
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.000000",
		VestingPeriod: "1d",
	})

	perf, err := analytics.GetStakePerformance(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("performance: %v", err)
	}

	if perf.CumulativeReturn != 0 {
		t.Errorf("expected 0%% cumulative return, got %.2f%%", perf.CumulativeReturn)
	}
	if perf.DistributionCount != 0 {
		t.Errorf("expected 0 distributions, got %d", perf.DistributionCount)
	}
	if perf.AvgPerSharePayout != "0.000000" {
		t.Errorf("expected avgPerSharePayout 0.000000, got %s", perf.AvgPerSharePayout)
	}
}

func TestStakePerformance_NotFound(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewStakeAnalyticsService(store)

	_, err := analytics.GetStakePerformance(context.Background(), "stk_nonexistent")
	if err != ErrStakeNotFound {
		t.Errorf("expected ErrStakeNotFound, got %v", err)
	}
}

func TestPortfolioAnalytics_AnnualizedReturn(t *testing.T) {
	store := NewMemoryStore()
	analytics := NewStakeAnalyticsService(store)

	// Manually create a holding with a known creation time (180 days ago)
	now := time.Now()
	holding := &Holding{
		ID:           "hld_test",
		StakeID:      "stk_test",
		InvestorAddr: "0xinvestor",
		Shares:       10,
		CostBasis:    "100.000000",
		TotalEarned:  "10.000000", // 10% ROI
		VestedAt:     now,
		Status:       string(HoldingStatusActive),
		CreatedAt:    now.Add(-180 * 24 * time.Hour),
		UpdatedAt:    now,
	}
	store.CreateHolding(context.Background(), holding)

	// Create a stake so the holding lookup doesn't fail
	stake := &Stake{
		ID:               "stk_test",
		AgentAddr:        "0xagent1",
		TotalShares:      100,
		AvailableShares:  90,
		PricePerShare:    "10.000000",
		Status:           string(StakeStatusOpen),
		TotalRaised:      "100.000000",
		TotalDistributed: "10.000000",
		Undistributed:    "0.000000",
		CreatedAt:        now.Add(-180 * 24 * time.Hour),
		UpdatedAt:        now,
	}
	store.CreateStake(context.Background(), stake)

	pa, err := analytics.GetPortfolioAnalytics(context.Background(), "0xinvestor")
	if err != nil {
		t.Fatalf("analytics: %v", err)
	}

	// ROI: 10/100 * 100 = 10%
	if math.Abs(pa.ROI-10.0) > 0.01 {
		t.Errorf("expected ROI ~10.0, got %.2f", pa.ROI)
	}
	// Annualized: 10% * (365/180) ≈ 20.28%
	expectedAnnualized := 10.0 * (365.0 / 180.0)
	if math.Abs(pa.AnnualizedReturn-expectedAnnualized) > 0.5 {
		t.Errorf("expected annualized ~%.2f, got %.2f", expectedAnnualized, pa.AnnualizedReturn)
	}
}
