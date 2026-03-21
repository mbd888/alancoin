package tracerank

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// DefaultConfig coverage
// ---------------------------------------------------------------------------

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Damping != DefaultDamping {
		t.Errorf("damping = %f, want %f", cfg.Damping, DefaultDamping)
	}
	if cfg.MaxIterations != DefaultMaxIterations {
		t.Errorf("maxIterations = %d, want %d", cfg.MaxIterations, DefaultMaxIterations)
	}
	if cfg.ConvergenceThreshold != DefaultConvergenceThreshold {
		t.Errorf("threshold = %e", cfg.ConvergenceThreshold)
	}
	if cfg.MinEdgeVolume != 0.001 {
		t.Errorf("minEdgeVolume = %f", cfg.MinEdgeVolume)
	}
	if cfg.MaxPerCounterparty != 50 {
		t.Errorf("maxPerCounterparty = %d", cfg.MaxPerCounterparty)
	}
	if cfg.CyclePenalty != 0.8 {
		t.Errorf("cyclePenalty = %f", cfg.CyclePenalty)
	}
	if cfg.MaxSourceInfluence != 0.5 {
		t.Errorf("maxSourceInfluence = %f", cfg.MaxSourceInfluence)
	}
}

// ---------------------------------------------------------------------------
// NewEngine config validation
// ---------------------------------------------------------------------------

func TestNewEngine_InvalidDamping(t *testing.T) {
	// Invalid damping values should be reset to default
	source := edgesFrom()
	seedMap := map[string]float64{}

	e := NewEngine(source, seeds(seedMap), Config{Damping: 0})
	if e.cfg.Damping != DefaultDamping {
		t.Errorf("zero damping should be reset to default, got %f", e.cfg.Damping)
	}

	e = NewEngine(source, seeds(seedMap), Config{Damping: 1.0})
	if e.cfg.Damping != DefaultDamping {
		t.Errorf("damping=1.0 should be reset to default, got %f", e.cfg.Damping)
	}

	e = NewEngine(source, seeds(seedMap), Config{Damping: -1})
	if e.cfg.Damping != DefaultDamping {
		t.Errorf("negative damping should be reset to default, got %f", e.cfg.Damping)
	}
}

func TestNewEngine_InvalidMaxIterations(t *testing.T) {
	source := edgesFrom()
	e := NewEngine(source, seeds(map[string]float64{}), Config{MaxIterations: 0})
	if e.cfg.MaxIterations != DefaultMaxIterations {
		t.Errorf("zero maxIterations should be reset to default, got %d", e.cfg.MaxIterations)
	}
}

func TestNewEngine_InvalidConvergenceThreshold(t *testing.T) {
	source := edgesFrom()
	e := NewEngine(source, seeds(map[string]float64{}), Config{ConvergenceThreshold: -1})
	if e.cfg.ConvergenceThreshold != DefaultConvergenceThreshold {
		t.Errorf("negative threshold should be reset to default, got %e", e.cfg.ConvergenceThreshold)
	}
}

// ---------------------------------------------------------------------------
// Compute with empty graph
// ---------------------------------------------------------------------------

func TestCompute_EmptyGraph(t *testing.T) {
	source := edgesFrom()
	engine := NewEngine(source, seeds(map[string]float64{}), DefaultConfig())

	result, err := engine.Compute(context.Background(), "empty-test")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.NodeCount != 0 {
		t.Errorf("nodeCount = %d, want 0", result.NodeCount)
	}
	if !result.Converged {
		t.Error("empty graph should be 'converged'")
	}
	if result.RunID != "empty-test" {
		t.Errorf("runID = %q", result.RunID)
	}
}

// ---------------------------------------------------------------------------
// Compute with no seeds (uniform fallback)
// ---------------------------------------------------------------------------

func TestCompute_NoSeeds(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
	)
	engine := NewEngine(source, seeds(map[string]float64{}), Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
	})

	result, err := engine.Compute(context.Background(), "no-seeds")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.NodeCount != 2 {
		t.Errorf("nodeCount = %d, want 2", result.NodeCount)
	}
	// Both should have scores since uniform fallback is used
	for addr, score := range result.Scores {
		if score.GraphScore <= 0 {
			t.Errorf("agent %s: graphScore = %f, want > 0 (uniform seeds)", addr, score.GraphScore)
		}
	}
}

// ---------------------------------------------------------------------------
// Graph: self-loops filtered
// ---------------------------------------------------------------------------

func TestGraph_SelfLoopsFiltered(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xa", 100, 5), // self-loop
		edge("0xa", "0xb", 50, 3),
	)
	engine := NewEngine(source, seeds(map[string]float64{"0xa": 1.0, "0xb": 0.0}), Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
	})

	result, err := engine.Compute(context.Background(), "selfloop-test")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.EdgeCount != 1 {
		t.Errorf("edgeCount = %d, want 1 (self-loop filtered)", result.EdgeCount)
	}
}

// ---------------------------------------------------------------------------
// Graph: min volume filter
// ---------------------------------------------------------------------------

func TestGraph_MinVolumeFilter(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 0.0001, 1), // below min
		edge("0xa", "0xc", 1.0, 1),    // above min
	)
	engine := NewEngine(source, seeds(map[string]float64{"0xa": 1.0}), Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
	})

	result, err := engine.Compute(context.Background(), "vol-filter")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.Scores["0xb"] != nil {
		t.Error("0xb should be excluded (edge volume below minimum)")
	}
}

// ---------------------------------------------------------------------------
// Graph: min tx count filter
// ---------------------------------------------------------------------------

func TestGraph_MinTxCountFilter(t *testing.T) {
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 0}, // below min
		edge("0xa", "0xc", 100, 1),                                   // above min
	)
	engine := NewEngine(source, seeds(map[string]float64{"0xa": 1.0}), Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
	})

	result, err := engine.Compute(context.Background(), "txcount-filter")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.Scores["0xb"] != nil {
		t.Error("0xb should be excluded (tx count below minimum)")
	}
}

// ---------------------------------------------------------------------------
// Leaderboard
// ---------------------------------------------------------------------------

func TestLeaderboard_Empty(t *testing.T) {
	list := Leaderboard(nil, 10)
	if len(list) != 0 {
		t.Errorf("expected empty leaderboard, got %d", len(list))
	}
}

func TestLeaderboard_Sorted(t *testing.T) {
	scores := map[string]*AgentScore{
		"0xa": {Address: "0xa", GraphScore: 50},
		"0xb": {Address: "0xb", GraphScore: 90},
		"0xc": {Address: "0xc", GraphScore: 70},
	}
	list := Leaderboard(scores, 0)
	if len(list) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list))
	}
	if list[0].Address != "0xb" {
		t.Errorf("first = %s, want 0xb (highest)", list[0].Address)
	}
	if list[1].Address != "0xc" {
		t.Errorf("second = %s, want 0xc", list[1].Address)
	}
}

func TestLeaderboard_LimitApplied(t *testing.T) {
	scores := map[string]*AgentScore{
		"0xa": {Address: "0xa", GraphScore: 50},
		"0xb": {Address: "0xb", GraphScore: 90},
		"0xc": {Address: "0xc", GraphScore: 70},
	}
	list := Leaderboard(scores, 2)
	if len(list) != 2 {
		t.Errorf("expected 2 entries with limit, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// MemoryStore coverage
// ---------------------------------------------------------------------------

func TestMemoryStore_GetScores(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	scores := []*AgentScore{
		{Address: "0xAlice", GraphScore: 90},
		{Address: "0xBob", GraphScore: 70},
	}
	_ = store.SaveScores(ctx, scores, "run_1")

	result, err := store.GetScores(ctx, []string{"0xAlice", "0xBob", "0xMissing"})
	if err != nil {
		t.Fatalf("GetScores: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}
	if result["0xalice"] == nil {
		t.Error("expected 0xalice in results")
	}
	if result["0xmissing"] != nil {
		t.Error("0xmissing should not be in results")
	}
}

func TestMemoryStore_GetScore_NotFound(t *testing.T) {
	store := NewMemoryStore()
	score, err := store.GetScore(context.Background(), "0xnonexistent")
	if err != nil {
		t.Fatalf("GetScore: %v", err)
	}
	if score != nil {
		t.Error("expected nil for nonexistent score")
	}
}

func TestMemoryStore_GetTopScores_Limit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	scores := []*AgentScore{
		{Address: "0xa", GraphScore: 90},
		{Address: "0xb", GraphScore: 70},
		{Address: "0xc", GraphScore: 50},
	}
	_ = store.SaveScores(ctx, scores, "run_1")

	top, err := store.GetTopScores(ctx, 2)
	if err != nil {
		t.Fatalf("GetTopScores: %v", err)
	}
	if len(top) != 2 {
		t.Errorf("expected 2, got %d", len(top))
	}
}

func TestMemoryStore_GetRunHistory_DefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	runs, err := store.GetRunHistory(ctx, 0)
	if err != nil {
		t.Fatalf("GetRunHistory: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0, got %d", len(runs))
	}
}

func TestMemoryStore_SaveScores_CalculatesMetadata(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	scores := []*AgentScore{
		{Address: "0xa", GraphScore: 100, Iterations: 42},
		{Address: "0xb", GraphScore: 50, Iterations: 42},
	}
	_ = store.SaveScores(ctx, scores, "run_meta")

	runs, _ := store.GetRunHistory(ctx, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].MaxScore != 100 {
		t.Errorf("maxScore = %f, want 100", runs[0].MaxScore)
	}
	if runs[0].MeanScore != 75 {
		t.Errorf("meanScore = %f, want 75", runs[0].MeanScore)
	}
	if runs[0].Iterations != 42 {
		t.Errorf("iterations = %d, want 42", runs[0].Iterations)
	}
}

// ---------------------------------------------------------------------------
// RecordMetrics coverage
// ---------------------------------------------------------------------------

func TestRecordMetrics(t *testing.T) {
	result := &ComputeResult{
		Scores: map[string]*AgentScore{
			"0xa": {GraphScore: 90},
			"0xb": {GraphScore: 50},
		},
		NodeCount:  2,
		EdgeCount:  1,
		Iterations: 10,
		Converged:  true,
		Duration:   100 * time.Millisecond,
	}
	// Should not panic
	RecordMetrics(result)
}

func TestRecordMetrics_NotConverged(t *testing.T) {
	result := &ComputeResult{
		Scores:    map[string]*AgentScore{},
		NodeCount: 0,
		Converged: false,
		Duration:  50 * time.Millisecond,
	}
	RecordMetrics(result)
}

// ---------------------------------------------------------------------------
// Worker coverage
// ---------------------------------------------------------------------------

func TestWorker_StartAndStop(t *testing.T) {
	source := edgesFrom(edge("0xa", "0xb", 100, 5))
	engine := NewEngine(source, seeds(map[string]float64{"0xa": 1.0}), DefaultConfig())
	store := NewMemoryStore()
	worker := NewWorker(engine, store, 50*time.Millisecond, slog.Default())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	// Let the worker run at least once
	time.Sleep(150 * time.Millisecond)
	cancel()
	<-done

	// Scores should have been computed
	score, _ := store.GetScore(context.Background(), "0xa")
	if score == nil {
		t.Error("expected scores to be computed after worker run")
	}
}

func TestWorker_StopChannel(t *testing.T) {
	source := edgesFrom()
	engine := NewEngine(source, seeds(map[string]float64{}), DefaultConfig())
	store := NewMemoryStore()
	worker := NewWorker(engine, store, time.Hour, slog.Default())

	done := make(chan struct{})
	go func() {
		worker.Start(context.Background())
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	worker.Stop()
	<-done
}

// ---------------------------------------------------------------------------
// Seed providers coverage
// ---------------------------------------------------------------------------

func TestStaticSeedProvider_GetSeed(t *testing.T) {
	p := &StaticSeedProvider{Seeds: map[string]float64{"0xa": 0.5}}
	s, err := p.GetSeed(context.Background(), "0xA")
	if err != nil {
		t.Fatalf("GetSeed: %v", err)
	}
	if s != 0.5 {
		t.Errorf("seed = %f, want 0.5", s)
	}

	s, _ = p.GetSeed(context.Background(), "0xunknown")
	if s != 0 {
		t.Errorf("unknown seed = %f, want 0", s)
	}
}

func TestStaticSeedProvider_GetAllSeeds(t *testing.T) {
	p := &StaticSeedProvider{Seeds: map[string]float64{"0xA": 0.5, "0xB": 0.8}}
	all, err := p.GetAllSeeds(context.Background())
	if err != nil {
		t.Fatalf("GetAllSeeds: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("expected 2 seeds, got %d", len(all))
	}
	if all["0xa"] != 0.5 {
		t.Errorf("0xa = %f, want 0.5", all["0xa"])
	}
}

// ---------------------------------------------------------------------------
// TimeSeedProvider coverage
// ---------------------------------------------------------------------------

type mockAgentInfoProvider struct {
	agents []AgentInfo
}

func (m *mockAgentInfoProvider) GetAllAgentInfo(_ context.Context) ([]AgentInfo, error) {
	return m.agents, nil
}

func TestTimeSeedProvider_GetSeed(t *testing.T) {
	now := time.Now()
	provider := NewTimeSeedProvider(&mockAgentInfoProvider{
		agents: []AgentInfo{
			{Address: "0xNew", CreatedAt: now},
			{Address: "0xOld", CreatedAt: now.Add(-200 * 24 * time.Hour), Verified: true},
		},
	})

	// New agent: 0 seed (< 7 days)
	s, err := provider.GetSeed(context.Background(), "0xNew")
	if err != nil {
		t.Fatalf("GetSeed: %v", err)
	}
	if s != 0 {
		t.Errorf("new agent seed = %f, want 0", s)
	}

	// Old verified agent: age + verified bonus
	s, _ = provider.GetSeed(context.Background(), "0xOld")
	if s <= 0.6 {
		t.Errorf("old verified agent seed = %f, want > 0.6", s)
	}

	// Unknown agent
	s, _ = provider.GetSeed(context.Background(), "0xUnknown")
	if s != 0 {
		t.Errorf("unknown agent seed = %f, want 0", s)
	}
}

func TestTimeSeedProvider_GetAllSeeds(t *testing.T) {
	now := time.Now()
	provider := NewTimeSeedProvider(&mockAgentInfoProvider{
		agents: []AgentInfo{
			{Address: "0xA", CreatedAt: now.Add(-40 * 24 * time.Hour)}, // 30+ days: 0.2
		},
	})

	all, err := provider.GetAllSeeds(context.Background())
	if err != nil {
		t.Fatalf("GetAllSeeds: %v", err)
	}
	if all["0xa"] < 0.19 || all["0xa"] > 0.21 {
		t.Errorf("30d agent seed = %f, want ~0.2", all["0xa"])
	}
}

func TestTimeSeedProvider_CacheTTL(t *testing.T) {
	provider := NewTimeSeedProvider(&mockAgentInfoProvider{
		agents: []AgentInfo{{Address: "0xA", CreatedAt: time.Now().Add(-100 * 24 * time.Hour)}},
	})

	// First call populates cache
	s1, _ := provider.GetAllSeeds(context.Background())
	// Second call should use cache
	s2, _ := provider.GetAllSeeds(context.Background())
	if s1["0xa"] != s2["0xa"] {
		t.Error("cached value should match")
	}
}

func TestComputeTimeSeed_AllTiers(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name    string
		agent   AgentInfo
		minSeed float64
		maxSeed float64
	}{
		{"zero_time", AgentInfo{}, 0, 0.001},
		{"7d", AgentInfo{CreatedAt: now.Add(-8 * 24 * time.Hour)}, 0.09, 0.11},
		{"30d", AgentInfo{CreatedAt: now.Add(-35 * 24 * time.Hour)}, 0.19, 0.21},
		{"90d", AgentInfo{CreatedAt: now.Add(-100 * 24 * time.Hour)}, 0.39, 0.41},
		{"180d", AgentInfo{CreatedAt: now.Add(-200 * 24 * time.Hour)}, 0.59, 0.61},
		{"verified_new", AgentInfo{Verified: true}, 0.29, 0.31},
		{"verified_old", AgentInfo{CreatedAt: now.Add(-200 * 24 * time.Hour), Verified: true}, 0.89, 0.91},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seed := computeTimeSeed(tt.agent, now)
			if seed < tt.minSeed || seed > tt.maxSeed {
				t.Errorf("seed = %f, want [%f, %f]", seed, tt.minSeed, tt.maxSeed)
			}
		})
	}
}

func TestComputeTimeSeed_Cap(t *testing.T) {
	// Very old + verified shouldn't exceed 1.0
	now := time.Now()
	agent := AgentInfo{
		CreatedAt: now.Add(-500 * 24 * time.Hour),
		Verified:  true,
	}
	seed := computeTimeSeed(agent, now)
	if seed > 1.0 {
		t.Errorf("seed = %f, should be capped at 1.0", seed)
	}
}

// ---------------------------------------------------------------------------
// StoreScoreProvider coverage
// ---------------------------------------------------------------------------

func TestStoreScoreProvider(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_ = store.SaveScores(ctx, []*AgentScore{
		{Address: "0xa", GraphScore: 80},
		{Address: "0xb", GraphScore: 60},
	}, "run_1")

	provider := NewStoreScoreProvider(store)

	score, err := provider.GetScore(ctx, "0xA")
	if err != nil {
		t.Fatalf("GetScore: %v", err)
	}
	if score.GraphScore != 80 {
		t.Errorf("graphScore = %f, want 80", score.GraphScore)
	}

	scores, err := provider.GetScores(ctx, []string{"0xa", "0xb"})
	if err != nil {
		t.Fatalf("GetScores: %v", err)
	}
	if len(scores) != 2 {
		t.Errorf("expected 2 scores, got %d", len(scores))
	}

	top, err := provider.GetTopScores(ctx, 1)
	if err != nil {
		t.Fatalf("GetTopScores: %v", err)
	}
	if len(top) != 1 {
		t.Errorf("expected 1, got %d", len(top))
	}
	if top[0].GraphScore != 80 {
		t.Errorf("top score = %f, want 80", top[0].GraphScore)
	}
}

// ---------------------------------------------------------------------------
// Graph internals: sortStrings
// ---------------------------------------------------------------------------

func TestSortStrings(t *testing.T) {
	s := []string{"c", "a", "b"}
	sortStrings(s)
	if s[0] != "a" || s[1] != "b" || s[2] != "c" {
		t.Errorf("sort failed: %v", s)
	}
}

func TestSortStrings_Empty(t *testing.T) {
	sortStrings(nil)
	sortStrings([]string{})
	sortStrings([]string{"a"})
}

// ---------------------------------------------------------------------------
// parseAmount coverage
// ---------------------------------------------------------------------------

func TestParseAmount(t *testing.T) {
	if v := parseAmount(""); v != 0 {
		t.Errorf("empty = %f", v)
	}
	if v := parseAmount("invalid"); v != 0 {
		t.Errorf("invalid = %f", v)
	}
	if v := parseAmount("12.50"); v != 12.5 {
		t.Errorf("12.50 = %f", v)
	}
}

// ---------------------------------------------------------------------------
// Edge window (time-based filtering)
// ---------------------------------------------------------------------------

func TestCompute_WithEdgeWindow(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-1 * time.Hour)},
	)
	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		EdgeWindow:           24 * time.Hour,
	}
	engine := NewEngine(source, seeds(map[string]float64{"0xa": 1.0}), cfg)
	result, err := engine.Compute(context.Background(), "window-test")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.NodeCount != 2 {
		t.Errorf("nodeCount = %d, want 2 (within window)", result.NodeCount)
	}
}

// ---------------------------------------------------------------------------
// Anti-wash trading: MaxPerCounterparty
// ---------------------------------------------------------------------------

func TestGraph_MaxPerCounterparty(t *testing.T) {
	// Create edges that exceed the per-counterparty cap
	source := edgesFrom(
		edge("0xa", "0xb", 10, 30), // 30 tx
		edge("0xa", "0xb", 10, 30), // another 30 tx to same pair = 60 total
	)
	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		MaxPerCounterparty:   50, // cap at 50
	}
	engine := NewEngine(source, seeds(map[string]float64{"0xa": 1.0}), cfg)
	result, err := engine.Compute(context.Background(), "wash-test")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	// First edge (30 tx) should be included. Second (30 more = 60 total > 50) should be excluded.
	if result.NodeCount < 2 {
		t.Errorf("nodeCount = %d, want >= 2", result.NodeCount)
	}
}

// ---------------------------------------------------------------------------
// Negative seeds clamped
// ---------------------------------------------------------------------------

func TestCompute_NegativeSeedsClamped(t *testing.T) {
	source := edgesFrom(edge("0xa", "0xb", 100, 5))
	engine := NewEngine(source, seeds(map[string]float64{"0xa": -5.0, "0xb": 2.0}), Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
	})
	result, err := engine.Compute(context.Background(), "clamp-seeds")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if result.NodeCount != 2 {
		t.Errorf("nodeCount = %d", result.NodeCount)
	}
}
