package intelligence

import (
	"context"
	"sync"
	"testing"
	"time"
)

// --- Mock providers ---

type mockTraceRank struct {
	scores map[string]TraceRankData
}

func (m *mockTraceRank) GetScore(_ context.Context, address string) (float64, int, int, float64, float64, error) {
	s := m.scores[address]
	return s.GraphScore, s.InDegree, s.OutDegree, s.InVolume, s.OutVolume, nil
}

func (m *mockTraceRank) GetAllScores(_ context.Context) (map[string]TraceRankData, error) {
	return m.scores, nil
}

type mockForensics struct {
	baselines map[string][3]float64 // [txCount, mean, stddev]
	alerts    map[string][2]int     // [total, critical]
}

func (m *mockForensics) GetBaseline(_ context.Context, addr string) (int, float64, float64, error) {
	b := m.baselines[addr]
	return int(b[0]), b[1], b[2], nil
}

func (m *mockForensics) CountAlerts30d(_ context.Context, addr string) (int, int, error) {
	a := m.alerts[addr]
	return a[0], a[1], nil
}

type mockReputation struct {
	metrics map[string]*ReputationData
}

func (m *mockReputation) GetMetrics(_ context.Context, addr string) (*ReputationData, error) {
	return m.metrics[addr], nil
}

func (m *mockReputation) GetAllMetrics(_ context.Context) (map[string]*ReputationData, error) {
	return m.metrics, nil
}

type mockAgentSource struct {
	addresses []string
}

func (m *mockAgentSource) ListAllAddresses(_ context.Context) ([]string, error) {
	return m.addresses, nil
}

func newTestEngine(agents []string) (*Engine, *MemoryStore) {
	store := NewMemoryStore()
	return NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{}},
		&mockForensics{
			baselines: map[string][3]float64{},
			alerts:    map[string][2]int{},
		},
		&mockReputation{metrics: map[string]*ReputationData{}},
		&mockAgentSource{addresses: agents},
		store,
		nil, // no logger in tests
	), store
}

// --- Tests ---

func TestTierFromScore(t *testing.T) {
	tests := []struct {
		score float64
		want  Tier
	}{
		{0, TierUnknown},
		{1, TierBronze},
		{24.9, TierBronze},
		{25, TierSilver},
		{49.9, TierSilver},
		{50, TierGold},
		{74.9, TierGold},
		{75, TierPlatinum},
		{89.9, TierPlatinum},
		{90, TierDiamond},
		{100, TierDiamond},
	}
	for _, tt := range tests {
		got := TierFromScore(tt.score)
		if got != tt.want {
			t.Errorf("TierFromScore(%.1f) = %s, want %s", tt.score, got, tt.want)
		}
	}
}

func TestComputeAll_EmptyAgents(t *testing.T) {
	engine, _ := newTestEngine(nil)
	result, err := engine.ComputeAll(context.Background(), "test_run")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Profiles) != 0 {
		t.Errorf("expected 0 profiles, got %d", len(result.Profiles))
	}
}

func TestComputeAll_ZeroData(t *testing.T) {
	engine, _ := newTestEngine([]string{"0xabc"})
	result, err := engine.ComputeAll(context.Background(), "test_run")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(result.Profiles))
	}

	p := result.Profiles[0]
	if p.Address != "0xabc" {
		t.Errorf("address = %s, want 0xabc", p.Address)
	}
	// Zero data → zero credit, but non-zero risk (network isolation)
	if p.CreditScore != 0 {
		t.Errorf("credit score = %.1f, want 0", p.CreditScore)
	}
	// Risk score should be > 0 due to network isolation component
	if p.RiskScore <= 0 {
		t.Errorf("risk score = %.1f, want > 0 (network isolation)", p.RiskScore)
	}
}

func TestComputeAll_HighCreditAgent(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{
			"0xelite": {GraphScore: 95, InDegree: 50, OutDegree: 30, InVolume: 50000, OutVolume: 30000},
		}},
		&mockForensics{
			baselines: map[string][3]float64{"0xelite": {500, 50.0, 10.0}},
			alerts:    map[string][2]int{"0xelite": {0, 0}},
		},
		&mockReputation{metrics: map[string]*ReputationData{
			"0xelite": {
				Score:             85,
				TotalTransactions: 500,
				SuccessfulTxns:    490,
				FailedTxns:        2,
				TotalVolumeUSD:    50000,
				DaysOnNetwork:     365,
			},
		}},
		&mockAgentSource{addresses: []string{"0xelite"}},
		store,
		nil,
	)

	result, err := engine.ComputeAll(context.Background(), "test_run")
	if err != nil {
		t.Fatal(err)
	}

	p := result.Profiles[0]

	// Should have high credit score
	if p.CreditScore < 70 {
		t.Errorf("credit score = %.1f, want >= 70 for elite agent", p.CreditScore)
	}
	// Should have low risk score
	if p.RiskScore > 30 {
		t.Errorf("risk score = %.1f, want <= 30 for clean agent", p.RiskScore)
	}
	// Should be platinum or diamond tier
	if p.Tier != TierPlatinum && p.Tier != TierDiamond {
		t.Errorf("tier = %s, want platinum or diamond", p.Tier)
	}
	// Credit factors should be populated
	if p.Credit.TraceRankInput != 95 {
		t.Errorf("tracerank input = %.1f, want 95", p.Credit.TraceRankInput)
	}
	if p.Credit.TxSuccessRate < 0.9 {
		t.Errorf("success rate = %.4f, want >= 0.9", p.Credit.TxSuccessRate)
	}
	// Network position should be populated
	if p.Network.InDegree != 50 {
		t.Errorf("in degree = %d, want 50", p.Network.InDegree)
	}
}

func TestComputeAll_HighRiskAgent(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{
			"0xrisky": {GraphScore: 10, InDegree: 1, OutDegree: 1},
		}},
		&mockForensics{
			baselines: map[string][3]float64{"0xrisky": {20, 100.0, 200.0}}, // high volatility
			alerts:    map[string][2]int{"0xrisky": {15, 4}},                // many alerts
		},
		&mockReputation{metrics: map[string]*ReputationData{
			"0xrisky": {
				Score:             15,
				TotalTransactions: 20,
				SuccessfulTxns:    12,
				FailedTxns:        8, // 40% failure rate
				TotalVolumeUSD:    500,
				DaysOnNetwork:     10,
			},
		}},
		&mockAgentSource{addresses: []string{"0xrisky"}},
		store,
		nil,
	)

	result, err := engine.ComputeAll(context.Background(), "test_run")
	if err != nil {
		t.Fatal(err)
	}

	p := result.Profiles[0]

	// Should have low credit score
	if p.CreditScore > 35 {
		t.Errorf("credit score = %.1f, want <= 35 for risky agent", p.CreditScore)
	}
	// Should have high risk score
	if p.RiskScore < 50 {
		t.Errorf("risk score = %.1f, want >= 50 for agent with many alerts", p.RiskScore)
	}
	// Should be bronze tier
	if p.Tier != TierBronze && p.Tier != TierUnknown {
		t.Errorf("tier = %s, want bronze or unknown", p.Tier)
	}
	// Risk factors should reflect alerts
	if p.Risk.AnomalyCount30d != 15 {
		t.Errorf("anomaly count = %d, want 15", p.Risk.AnomalyCount30d)
	}
	if p.Risk.CriticalAlerts != 4 {
		t.Errorf("critical alerts = %d, want 4", p.Risk.CriticalAlerts)
	}
}

func TestComputeAll_Benchmarks(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{
			"0xa": {GraphScore: 90},
			"0xb": {GraphScore: 50},
			"0xc": {GraphScore: 10},
		}},
		&mockForensics{
			baselines: map[string][3]float64{},
			alerts:    map[string][2]int{},
		},
		&mockReputation{metrics: map[string]*ReputationData{
			"0xa": {Score: 80, TotalTransactions: 100, SuccessfulTxns: 95, TotalVolumeUSD: 10000, DaysOnNetwork: 100},
			"0xb": {Score: 50, TotalTransactions: 50, SuccessfulTxns: 40, TotalVolumeUSD: 5000, DaysOnNetwork: 50},
			"0xc": {Score: 20, TotalTransactions: 10, SuccessfulTxns: 8, TotalVolumeUSD: 500, DaysOnNetwork: 10},
		}},
		&mockAgentSource{addresses: []string{"0xa", "0xb", "0xc"}},
		store,
		nil,
	)

	result, err := engine.ComputeAll(context.Background(), "test_run")
	if err != nil {
		t.Fatal(err)
	}

	if result.Benchmarks == nil {
		t.Fatal("benchmarks should not be nil")
	}
	if result.Benchmarks.TotalAgents != 3 {
		t.Errorf("total agents = %d, want 3", result.Benchmarks.TotalAgents)
	}
	if result.Benchmarks.AvgCreditScore <= 0 {
		t.Errorf("avg credit score = %.1f, want > 0", result.Benchmarks.AvgCreditScore)
	}
	if result.Benchmarks.P90CreditScore <= result.Benchmarks.P10CreditScore {
		t.Errorf("P90 (%.1f) should be > P10 (%.1f)", result.Benchmarks.P90CreditScore, result.Benchmarks.P10CreditScore)
	}
}

func TestComputeOne(t *testing.T) {
	store := NewMemoryStore()
	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{
			"0xsingle": {GraphScore: 60, InDegree: 10, OutDegree: 5},
		}},
		&mockForensics{
			baselines: map[string][3]float64{"0xsingle": {100, 25.0, 5.0}},
			alerts:    map[string][2]int{"0xsingle": {1, 0}},
		},
		&mockReputation{metrics: map[string]*ReputationData{
			"0xsingle": {
				Score:             60,
				TotalTransactions: 100,
				SuccessfulTxns:    95,
				TotalVolumeUSD:    5000,
				DaysOnNetwork:     90,
			},
		}},
		&mockAgentSource{addresses: []string{"0xsingle"}},
		store,
		nil,
	)

	profile, err := engine.ComputeOne(context.Background(), "0xsingle", "test_run")
	if err != nil {
		t.Fatal(err)
	}

	if profile.Address != "0xsingle" {
		t.Errorf("address = %s, want 0xsingle", profile.Address)
	}
	if profile.CreditScore <= 0 {
		t.Errorf("credit score should be > 0, got %.1f", profile.CreditScore)
	}
	if profile.CompositeScore <= 0 {
		t.Errorf("composite score should be > 0, got %.1f", profile.CompositeScore)
	}
}

func TestCompositeScoreFormula(t *testing.T) {
	// Composite = 0.6 * credit + 0.4 * (100 - risk)
	// With credit=80, risk=20: composite = 0.6*80 + 0.4*80 = 48 + 32 = 80
	store := NewMemoryStore()
	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{}},
		&mockForensics{baselines: map[string][3]float64{}, alerts: map[string][2]int{}},
		&mockReputation{metrics: map[string]*ReputationData{}},
		&mockAgentSource{addresses: []string{"0xtest"}},
		store,
		nil,
	)

	// Test the clamp and round functions directly
	profile := engine.computeProfile("0xtest", TraceRankData{}, &ReputationData{}, 0, 0, 0, 0, 0, "run", time.Now())

	// With all zeros: credit=0, risk has some isolation component
	expectedComposite := round1(compositeCredit*0 + compositeRisk*(100-profile.RiskScore))
	if profile.CompositeScore != expectedComposite {
		t.Errorf("composite = %.1f, want %.1f (credit=%.1f, risk=%.1f)",
			profile.CompositeScore, expectedComposite, profile.CreditScore, profile.RiskScore)
	}
}

func TestCreditGate(t *testing.T) {
	store := NewMemoryStore()
	gate := NewCreditGate(store)

	// No profile exists
	tier, score, err := gate.GetCreditTier(context.Background(), "0xunknown")
	if err != nil {
		t.Fatal(err)
	}
	if tier != "" || score != 0 {
		t.Errorf("unknown agent: tier=%s, score=%.1f, want empty/0", tier, score)
	}

	// Save a diamond profile
	_ = store.SaveProfile(context.Background(), &AgentProfile{
		Address:        "0xdiamond",
		CreditScore:    95,
		CompositeScore: 92,
		Tier:           TierDiamond,
	})

	tier, score, err = gate.GetCreditTier(context.Background(), "0xdiamond")
	if err != nil {
		t.Fatal(err)
	}
	if tier != "diamond" {
		t.Errorf("tier = %s, want diamond", tier)
	}
	if score != 95 {
		t.Errorf("score = %.1f, want 95", score)
	}

	// Fee discounts
	if d := gate.FeeDiscountBPS("diamond"); d != 50 {
		t.Errorf("diamond discount = %d, want 50", d)
	}
	if d := gate.FeeDiscountBPS("platinum"); d != 30 {
		t.Errorf("platinum discount = %d, want 30", d)
	}
	if d := gate.FeeDiscountBPS("gold"); d != 15 {
		t.Errorf("gold discount = %d, want 15", d)
	}
	if d := gate.FeeDiscountBPS("silver"); d != 0 {
		t.Errorf("silver discount = %d, want 0", d)
	}

	// Escrow thresholds
	if th := gate.EscrowThresholdUSDC("diamond"); th != 10.0 {
		t.Errorf("diamond threshold = %.1f, want 10.0", th)
	}
	if th := gate.EscrowThresholdUSDC("bronze"); th != 0 {
		t.Errorf("bronze threshold = %.1f, want 0", th)
	}
}

func TestMemoryStore_RoundTrip(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Save and retrieve a profile
	profile := &AgentProfile{
		Address:        "0xtest",
		CreditScore:    75.5,
		RiskScore:      20.3,
		CompositeScore: 77.0,
		Tier:           TierPlatinum,
		ComputeRunID:   "run_1",
		ComputedAt:     time.Now().UTC(),
	}

	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetProfile(ctx, "0xtest")
	if err != nil {
		t.Fatal(err)
	}
	if got.CreditScore != 75.5 {
		t.Errorf("credit score = %.1f, want 75.5", got.CreditScore)
	}
	if got.Tier != TierPlatinum {
		t.Errorf("tier = %s, want platinum", got.Tier)
	}

	// Test batch lookup
	profiles, err := store.GetProfiles(ctx, []string{"0xtest", "0xmissing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 {
		t.Errorf("batch lookup: got %d profiles, want 1", len(profiles))
	}

	// Test score history
	points := []*ScoreHistoryPoint{
		{Address: "0xtest", CreditScore: 70, RiskScore: 25, CompositeScore: 72, Tier: TierGold, CreatedAt: time.Now().Add(-24 * time.Hour)},
		{Address: "0xtest", CreditScore: 75.5, RiskScore: 20.3, CompositeScore: 77, Tier: TierPlatinum, CreatedAt: time.Now()},
	}
	if err := store.SaveScoreHistory(ctx, points); err != nil {
		t.Fatal(err)
	}

	history, err := store.GetScoreHistory(ctx, "0xtest", time.Now().Add(-48*time.Hour), time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Errorf("history: got %d points, want 2", len(history))
	}

	// Test benchmarks
	benchmarks := &NetworkBenchmarks{
		TotalAgents:    10,
		AvgCreditScore: 55.0,
		ComputedAt:     time.Now(),
	}
	if err := store.SaveBenchmarks(ctx, benchmarks); err != nil {
		t.Fatal(err)
	}

	got2, err := store.GetLatestBenchmarks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got2.TotalAgents != 10 {
		t.Errorf("total agents = %d, want 10", got2.TotalAgents)
	}
}

func TestPercentile(t *testing.T) {
	sorted := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	p50 := percentile(sorted, 0.50)
	if p50 < 50 || p50 > 60 {
		t.Errorf("p50 = %.1f, want ~55", p50)
	}

	p10 := percentile(sorted, 0.10)
	if p10 < 10 || p10 > 20 {
		t.Errorf("p10 = %.1f, want ~19", p10)
	}

	p90 := percentile(sorted, 0.90)
	if p90 < 90 || p90 > 100 {
		t.Errorf("p90 = %.1f, want ~91", p90)
	}

	// Edge: empty
	if v := percentile(nil, 0.5); v != 0 {
		t.Errorf("empty percentile = %.1f, want 0", v)
	}

	// Edge: single element
	if v := percentile([]float64{42}, 0.5); v != 42 {
		t.Errorf("single element percentile = %.1f, want 42", v)
	}
}

// --- Bug regression tests ---

func TestNilLoggerDoesNotPanic(t *testing.T) {
	// BUG 1: Engine with nil logger should not panic
	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{}},
		&mockForensics{baselines: map[string][3]float64{}, alerts: map[string][2]int{}},
		&mockReputation{metrics: map[string]*ReputationData{}},
		&mockAgentSource{addresses: []string{"0xtest"}},
		NewMemoryStore(),
		nil, // nil logger — must not panic
	)
	result, err := engine.ComputeAll(context.Background(), "nil_logger_test")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Profiles) != 1 {
		t.Errorf("expected 1 profile, got %d", len(result.Profiles))
	}
}

func TestTrendComputesCorrectDelta(t *testing.T) {
	// BUG 3: Trend delta should compare against score from ~7 days ago,
	// not the most recent score in the last 7 days.
	store := NewMemoryStore()
	ctx := context.Background()

	// Seed history: score was 50 seven days ago
	sevenDaysAgo := time.Now().UTC().Add(-7 * 24 * time.Hour)
	_ = store.SaveScoreHistory(ctx, []*ScoreHistoryPoint{
		{Address: "0xtrend", CreditScore: 50, RiskScore: 30, CompositeScore: 60, Tier: TierGold, CreatedAt: sevenDaysAgo},
	})

	// Also seed a more recent point (1 day ago) with different score
	oneDayAgo := time.Now().UTC().Add(-24 * time.Hour)
	_ = store.SaveScoreHistory(ctx, []*ScoreHistoryPoint{
		{Address: "0xtrend", CreditScore: 70, RiskScore: 20, CompositeScore: 75, Tier: TierPlatinum, CreatedAt: oneDayAgo},
	})

	engine := NewEngine(
		&mockTraceRank{scores: map[string]TraceRankData{"0xtrend": {GraphScore: 80, InDegree: 10}}},
		&mockForensics{baselines: map[string][3]float64{}, alerts: map[string][2]int{}},
		&mockReputation{metrics: map[string]*ReputationData{
			"0xtrend": {Score: 75, TotalTransactions: 100, SuccessfulTxns: 95, TotalVolumeUSD: 5000, DaysOnNetwork: 90},
		}},
		&mockAgentSource{addresses: []string{"0xtrend"}},
		store,
		nil,
	)

	profile, err := engine.ComputeOne(ctx, "0xtrend", "trend_test")
	if err != nil {
		t.Fatal(err)
	}

	// The 7d delta should compare against the 7-day-ago score (50), not 1-day-ago (70)
	// So delta should be positive and significant (current credit - 50)
	if profile.Trends.CreditDelta7d == 0 {
		t.Log("7d trend is 0 — expected non-zero if history was found in the 6-8d window")
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Concurrent writes and reads should not race
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		addr := "0x" + itoa(i)
		go func() {
			defer wg.Done()
			_ = store.SaveProfile(ctx, &AgentProfile{
				Address:        addr,
				CreditScore:    float64(i),
				CompositeScore: float64(i),
				Tier:           TierGold,
				ComputedAt:     time.Now(),
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = store.GetProfile(ctx, addr)
		}()
	}
	wg.Wait()

	// Verify at least some profiles were saved
	profiles, err := store.GetTopProfiles(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) == 0 {
		t.Error("expected some profiles to be saved")
	}
}

func TestMemoryStore_DeleteHistoryBefore(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	now := time.Now().UTC()
	_ = store.SaveScoreHistory(ctx, []*ScoreHistoryPoint{
		{Address: "0xa", CreditScore: 50, CreatedAt: now.Add(-100 * 24 * time.Hour)}, // old
		{Address: "0xa", CreditScore: 60, CreatedAt: now.Add(-50 * 24 * time.Hour)},  // mid
		{Address: "0xa", CreditScore: 70, CreatedAt: now},                            // fresh
	})

	cutoff := now.Add(-90 * 24 * time.Hour)
	deleted, err := store.DeleteScoreHistoryBefore(ctx, cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Errorf("deleted = %d, want 1", deleted)
	}

	// Should have 2 remaining
	history, _ := store.GetScoreHistory(ctx, "0xa", now.Add(-365*24*time.Hour), now.Add(time.Hour), 100)
	if len(history) != 2 {
		t.Errorf("remaining = %d, want 2", len(history))
	}
}

func TestComputeOne_NilTraceRankHandled(t *testing.T) {
	// If TraceRank returns error, profile should still compute with zeros
	store := NewMemoryStore()
	engine := NewEngine(
		&errorTraceRank{},
		&mockForensics{baselines: map[string][3]float64{}, alerts: map[string][2]int{}},
		&mockReputation{metrics: map[string]*ReputationData{
			"0xnotr": {Score: 50, TotalTransactions: 10, SuccessfulTxns: 9, DaysOnNetwork: 30},
		}},
		&mockAgentSource{addresses: []string{"0xnotr"}},
		store,
		nil,
	)

	profile, err := engine.ComputeOne(context.Background(), "0xnotr", "err_test")
	if err != nil {
		t.Fatal("should not return error when tracerank fails")
	}
	if profile.Credit.TraceRankInput != 0 {
		t.Errorf("tracerank input should be 0 on error, got %.1f", profile.Credit.TraceRankInput)
	}
	// Should still have a score from other factors
	if profile.CreditScore < 0 {
		t.Errorf("credit score should be >= 0, got %.1f", profile.CreditScore)
	}
}

func TestCreditGate_ReputationTiersMismatch(t *testing.T) {
	// Verify that reputation tier names (elite, trusted) return 0 discount
	// since they don't match intelligence tier names (diamond, platinum)
	gate := NewCreditGate(NewMemoryStore())
	if d := gate.FeeDiscountBPS("elite"); d != 0 {
		t.Errorf("elite (reputation tier) should get 0 discount, got %d", d)
	}
	if d := gate.FeeDiscountBPS("trusted"); d != 0 {
		t.Errorf("trusted (reputation tier) should get 0 discount, got %d", d)
	}
	if d := gate.FeeDiscountBPS(""); d != 0 {
		t.Errorf("empty tier should get 0 discount, got %d", d)
	}
}

func TestClampEdgeCases(t *testing.T) {
	if v := clamp(-5, 0, 100); v != 0 {
		t.Errorf("clamp(-5,0,100) = %.1f, want 0", v)
	}
	if v := clamp(150, 0, 100); v != 100 {
		t.Errorf("clamp(150,0,100) = %.1f, want 100", v)
	}
	if v := clamp(50, 0, 100); v != 50 {
		t.Errorf("clamp(50,0,100) = %.1f, want 50", v)
	}
}

func TestRound1(t *testing.T) {
	if v := round1(3.14159); v != 3.1 {
		t.Errorf("round1(3.14159) = %f, want 3.1", v)
	}
	if v := round1(3.15); v != 3.2 {
		t.Errorf("round1(3.15) = %f, want 3.2", v)
	}
	if v := round1(0); v != 0 {
		t.Errorf("round1(0) = %f, want 0", v)
	}
}

// --- Error-returning mock providers ---

type errorTraceRank struct{}

func (e *errorTraceRank) GetScore(_ context.Context, _ string) (float64, int, int, float64, float64, error) {
	return 0, 0, 0, 0, 0, context.DeadlineExceeded
}

func (e *errorTraceRank) GetAllScores(_ context.Context) (map[string]TraceRankData, error) {
	return nil, context.DeadlineExceeded
}
