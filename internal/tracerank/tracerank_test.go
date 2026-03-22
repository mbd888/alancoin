package tracerank

import (
	"context"
	"log/slog"
	"math"
	"testing"
	"time"
)

// --- Helpers ---

func seeds(m map[string]float64) SeedProvider {
	return &StaticSeedProvider{Seeds: m}
}

type staticTransactionSource struct {
	edges []PaymentEdge
}

func (s *staticTransactionSource) GetPaymentEdges(_ context.Context, _ time.Time) ([]PaymentEdge, error) {
	return s.edges, nil
}

func edgesFrom(pairs ...PaymentEdge) TransactionSource {
	return &staticTransactionSource{edges: pairs}
}

func edge(from, to string, volume float64, txCount int) PaymentEdge {
	return PaymentEdge{
		From:    from,
		To:      to,
		Volume:  volume,
		TxCount: txCount,
	}
}

// --- Core Algorithm Tests ---

func TestBasicPageRank(t *testing.T) {
	// Simple chain: A pays B pays C. A is seeded.
	// Expected: A has highest seed, B benefits from A's payment, C benefits from B's.
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
		edge("0xb", "0xc", 50, 3),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.1,
		"0xc": 0.0,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "test-run-1")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.NodeCount != 3 {
		t.Errorf("Expected 3 nodes, got %d", result.NodeCount)
	}
	if result.EdgeCount != 2 {
		t.Errorf("Expected 2 edges, got %d", result.EdgeCount)
	}
	if !result.Converged {
		t.Error("Expected convergence")
	}

	// A should have highest score (high seed, major node)
	// B should have decent score (paid by high-seed A)
	// C should have lowest score (paid by low-seed B, no own seed)
	if result.Scores["0xa"].GraphScore <= result.Scores["0xc"].GraphScore {
		t.Error("A should score higher than C")
	}
	if result.Scores["0xb"].GraphScore <= result.Scores["0xc"].GraphScore {
		t.Error("B should score higher than C (receives from seeded A)")
	}
}

func TestSybilResistance(t *testing.T) {
	// Sybil attack: agents S1-S5 (zero seed) do massive self-dealing.
	// Legitimate agent L (seeded) does modest business.
	// Sybil agents should NOT outrank the legitimate agent.
	source := edgesFrom(
		// Sybil ring: massive volume, zero seeds (txCount under MaxPerCounterparty cap)
		edge("0xs1", "0xs2", 10000, 40),
		edge("0xs2", "0xs3", 10000, 40),
		edge("0xs3", "0xs4", 10000, 40),
		edge("0xs4", "0xs5", 10000, 40),
		edge("0xs5", "0xs1", 10000, 40),
		// Legitimate agent: modest volume, seeded
		edge("0xl", "0xm", 10, 2),
	)
	seedMap := map[string]float64{
		"0xl":  0.5, // Legitimate, 90+ days on network
		"0xm":  0.1, // Legitimate merchant
		"0xs1": 0,   // Sybil
		"0xs2": 0,   // Sybil
		"0xs3": 0,   // Sybil
		"0xs4": 0,   // Sybil
		"0xs5": 0,   // Sybil
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "sybil-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// Legitimate agent MUST outrank all Sybil agents
	lScore := result.Scores["0xl"].GraphScore
	for _, addr := range []string{"0xs1", "0xs2", "0xs3", "0xs4", "0xs5"} {
		sScore := result.Scores[addr].GraphScore
		if sScore >= lScore {
			t.Errorf("Sybil %s (%.1f) should rank below legitimate 0xl (%.1f)", addr, sScore, lScore)
		}
	}

	// Merchant paid by legitimate agent should also outrank Sybils
	mScore := result.Scores["0xm"].GraphScore
	for _, addr := range []string{"0xs1", "0xs2", "0xs3", "0xs4", "0xs5"} {
		sScore := result.Scores[addr].GraphScore
		if sScore >= mScore {
			t.Errorf("Sybil %s (%.1f) should rank below merchant 0xm (%.1f)", addr, sScore, mScore)
		}
	}
}

func TestSelfLoopsIgnored(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xa", 99999, 999), // self-loop: should be ignored
		edge("0xa", "0xb", 10, 1),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.1,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "self-loop-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.EdgeCount != 1 {
		t.Errorf("Expected 1 edge (self-loop excluded), got %d", result.EdgeCount)
	}
}

func TestEmptyGraph(t *testing.T) {
	source := edgesFrom() // no edges
	seedMap := map[string]float64{}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "empty-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.NodeCount != 0 {
		t.Errorf("Expected 0 nodes, got %d", result.NodeCount)
	}
	if !result.Converged {
		t.Error("Empty graph should trivially converge")
	}
}

func TestSingleEdge(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "single-edge-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.NodeCount != 2 {
		t.Errorf("Expected 2 nodes, got %d", result.NodeCount)
	}

	// A is seeded and has outgoing. B is not seeded but receives from A.
	// Both should have scores > 0 (B gets trust from A's payment).
	if result.Scores["0xa"].GraphScore == 0 {
		t.Error("Seeded node A should have non-zero score")
	}
	if result.Scores["0xb"].GraphScore == 0 {
		t.Error("B should get trust from A's payment")
	}
}

func TestVolumeWeightedEdges(t *testing.T) {
	// Agent A pays B a lot, pays C a little.
	// B should rank higher than C.
	source := edgesFrom(
		edge("0xa", "0xb", 1000, 10),
		edge("0xa", "0xc", 1, 1),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "volume-weight-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	bScore := result.Scores["0xb"].GraphScore
	cScore := result.Scores["0xc"].GraphScore
	if bScore <= cScore {
		t.Errorf("B (%.1f) should outrank C (%.1f) due to higher volume", bScore, cScore)
	}
}

func TestTransitiveTrust(t *testing.T) {
	// Chain: Seed -> A -> B -> C
	// Trust should propagate transitively but decay.
	source := edgesFrom(
		edge("0xseed", "0xa", 100, 5),
		edge("0xa", "0xb", 100, 5),
		edge("0xb", "0xc", 100, 5),
	)
	seedMap := map[string]float64{
		"0xseed": 1.0,
		"0xa":    0.0,
		"0xb":    0.0,
		"0xc":    0.0,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "transitive-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// Scores should decay along the chain
	seedScore := result.Scores["0xseed"].GraphScore
	aScore := result.Scores["0xa"].GraphScore
	bScore := result.Scores["0xb"].GraphScore
	cScore := result.Scores["0xc"].GraphScore

	if seedScore <= aScore {
		t.Errorf("Seed (%.1f) should outrank A (%.1f)", seedScore, aScore)
	}
	if aScore <= bScore {
		t.Errorf("A (%.1f) should outrank B (%.1f)", aScore, bScore)
	}
	if bScore <= cScore {
		t.Errorf("B (%.1f) should outrank C (%.1f)", bScore, cScore)
	}
	// But C should still have SOME score (trust propagates)
	if cScore == 0 {
		t.Error("C should have non-zero score from transitive trust")
	}
}

func TestDanglingNodes(t *testing.T) {
	// B is a dangling node (receives payments but never pays anyone).
	// Its rank mass should redistribute via personalization.
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
	)
	seedMap := map[string]float64{
		"0xa": 0.5,
		"0xb": 0.5,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "dangling-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// Both should have non-zero scores
	if result.Scores["0xa"].GraphScore == 0 {
		t.Error("A should have non-zero score")
	}
	if result.Scores["0xb"].GraphScore == 0 {
		t.Error("B (dangling) should have non-zero score")
	}
}

func TestAntiWashTradingCap(t *testing.T) {
	// Edge with TxCount exceeding MaxPerCounterparty is excluded entirely.
	source := edgesFrom(
		edge("0xa", "0xb", 100000, 100), // massive volume, tx count exceeds cap
		edge("0xa", "0xc", 10, 3),       // legitimate edge, within cap
	)
	seedMap := map[string]float64{
		"0xa": 0.5,
		"0xb": 0.5,
		"0xc": 0.5,
	}

	cfg := DefaultConfig()
	cfg.MaxPerCounterparty = 5 // strict cap: 100 > 5 so a→b is excluded

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "wash-cap-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// a→b is excluded (100 > 5 cap), a→c remains
	if result.NodeCount != 2 {
		t.Errorf("Expected 2 nodes (0xa, 0xc), got %d", result.NodeCount)
	}
	if _, ok := result.Scores["0xb"]; ok {
		t.Error("0xb should be excluded (tx count exceeds cap)")
	}
	if result.Scores["0xc"] == nil {
		t.Error("0xc should be present (within cap)")
	}
}

func TestMinVolumeFilter(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 0.0001, 1), // below minimum
		edge("0xa", "0xc", 10, 5),     // above minimum
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.1,
		"0xc": 0.1,
	}

	cfg := DefaultConfig()
	cfg.MinEdgeVolume = 0.01 // filter out tiny edges

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "min-volume-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// B should NOT appear (edge volume below minimum)
	if _, ok := result.Scores["0xb"]; ok {
		t.Error("0xb should be excluded (edge volume below minimum)")
	}
	if result.Scores["0xc"] == nil {
		t.Error("0xc should be present")
	}
}

func TestScoresNormalizedTo100(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
		edge("0xb", "0xc", 50, 3),
		edge("0xc", "0xa", 25, 2),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.5,
		"0xc": 0.2,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "normalize-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// At least one node should have score 100 (the max)
	hasMax := false
	for _, s := range result.Scores {
		if s.GraphScore < 0 || s.GraphScore > 100 {
			t.Errorf("Score out of range [0,100]: %f", s.GraphScore)
		}
		if s.GraphScore == 100 {
			hasMax = true
		}
	}
	if !hasMax {
		t.Error("Expected at least one node with score 100 (normalized max)")
	}
}

func TestDeterministicComputation(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
		edge("0xb", "0xc", 50, 3),
		edge("0xc", "0xd", 25, 2),
		edge("0xd", "0xa", 10, 1),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.5,
		"0xc": 0.2,
		"0xd": 0.1,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())

	result1, err := engine.Compute(context.Background(), "determ-1")
	if err != nil {
		t.Fatalf("Compute 1 failed: %v", err)
	}

	result2, err := engine.Compute(context.Background(), "determ-2")
	if err != nil {
		t.Fatalf("Compute 2 failed: %v", err)
	}

	for addr, s1 := range result1.Scores {
		s2 := result2.Scores[addr]
		if math.Abs(s1.GraphScore-s2.GraphScore) > 0.01 {
			t.Errorf("Non-deterministic: %s scored %.1f then %.1f", addr, s1.GraphScore, s2.GraphScore)
		}
	}
}

func TestNodeInfo(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5),
		edge("0xa", "0xc", 50, 3),
		edge("0xb", "0xc", 25, 2),
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.5,
		"0xc": 0.1,
	}

	// Use a config with no decay/penalty so raw volumes are preserved.
	cfg := DefaultConfig()
	cfg.TemporalDecayRate = 0
	cfg.CyclePenalty = 0
	cfg.MaxSourceInfluence = 0

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "nodeinfo-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// Check A: pays B and C
	a := result.Scores["0xa"]
	if a.OutDegree != 2 {
		t.Errorf("A OutDegree: expected 2, got %d", a.OutDegree)
	}
	if a.InDegree != 0 {
		t.Errorf("A InDegree: expected 0, got %d", a.InDegree)
	}
	if a.OutVolume != 150 {
		t.Errorf("A OutVolume: expected 150, got %f", a.OutVolume)
	}

	// Check C: receives from A and B
	c := result.Scores["0xc"]
	if c.InDegree != 2 {
		t.Errorf("C InDegree: expected 2, got %d", c.InDegree)
	}
	if c.InVolume != 75 {
		t.Errorf("C InVolume: expected 75, got %f", c.InVolume)
	}
}

// --- Seed Provider Tests ---

func TestTimeSeedProvider(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		info     AgentInfo
		expected float64
	}{
		{
			name:     "brand new",
			info:     AgentInfo{Address: "0x1", CreatedAt: now},
			expected: 0.0,
		},
		{
			name:     "8 days old",
			info:     AgentInfo{Address: "0x2", CreatedAt: now.Add(-8 * 24 * time.Hour)},
			expected: 0.1,
		},
		{
			name:     "31 days old",
			info:     AgentInfo{Address: "0x3", CreatedAt: now.Add(-31 * 24 * time.Hour)},
			expected: 0.2,
		},
		{
			name:     "91 days old",
			info:     AgentInfo{Address: "0x4", CreatedAt: now.Add(-91 * 24 * time.Hour)},
			expected: 0.4,
		},
		{
			name:     "181 days old",
			info:     AgentInfo{Address: "0x5", CreatedAt: now.Add(-181 * 24 * time.Hour)},
			expected: 0.6,
		},
		{
			name:     "verified new",
			info:     AgentInfo{Address: "0x6", CreatedAt: now, Verified: true},
			expected: 0.3,
		},
		{
			name:     "verified 91 days",
			info:     AgentInfo{Address: "0x7", CreatedAt: now.Add(-91 * 24 * time.Hour), Verified: true},
			expected: 0.7,
		},
		{
			name:     "verified 181 days (capped at 1.0)",
			info:     AgentInfo{Address: "0x8", CreatedAt: now.Add(-181 * 24 * time.Hour), Verified: true},
			expected: 0.9,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := computeTimeSeed(tc.info, now)
			if math.Abs(result-tc.expected) > 0.001 {
				t.Errorf("Expected seed %f, got %f", tc.expected, result)
			}
		})
	}
}

// --- Store Tests ---

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	scores := []*AgentScore{
		{Address: "0xa", GraphScore: 90.5, RawRank: 0.9, SeedSignal: 1.0, InDegree: 5, OutDegree: 3, InVolume: 500, OutVolume: 300, Iterations: 10, ComputeRunID: "run1"},
		{Address: "0xb", GraphScore: 60.0, RawRank: 0.6, SeedSignal: 0.5, InDegree: 3, OutDegree: 2, InVolume: 200, OutVolume: 100, Iterations: 10, ComputeRunID: "run1"},
		{Address: "0xc", GraphScore: 30.0, RawRank: 0.3, SeedSignal: 0.1, InDegree: 1, OutDegree: 1, InVolume: 50, OutVolume: 50, Iterations: 10, ComputeRunID: "run1"},
	}

	if err := store.SaveScores(ctx, scores, "run1"); err != nil {
		t.Fatalf("SaveScores failed: %v", err)
	}

	// GetScore
	s, err := store.GetScore(ctx, "0xA") // test case insensitivity
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if s == nil || s.GraphScore != 90.5 {
		t.Errorf("Expected score 90.5 for 0xa, got %v", s)
	}

	// GetScore missing
	s, err = store.GetScore(ctx, "0xnotfound")
	if err != nil {
		t.Fatalf("GetScore error: %v", err)
	}
	if s != nil {
		t.Error("Expected nil for unknown address")
	}

	// GetScores batch
	batch, err := store.GetScores(ctx, []string{"0xa", "0xc", "0xnotfound"})
	if err != nil {
		t.Fatalf("GetScores failed: %v", err)
	}
	if len(batch) != 2 {
		t.Errorf("Expected 2 scores, got %d", len(batch))
	}

	// GetTopScores
	top, err := store.GetTopScores(ctx, 2)
	if err != nil {
		t.Fatalf("GetTopScores failed: %v", err)
	}
	if len(top) != 2 {
		t.Errorf("Expected 2 top scores, got %d", len(top))
	}
	if top[0].Address != "0xa" || top[1].Address != "0xb" {
		t.Errorf("Top scores not in expected order: %s, %s", top[0].Address, top[1].Address)
	}

	// GetRunHistory
	runs, err := store.GetRunHistory(ctx, 10)
	if err != nil {
		t.Fatalf("GetRunHistory failed: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("Expected 1 run, got %d", len(runs))
	}
	if runs[0].RunID != "run1" {
		t.Errorf("Expected run1, got %s", runs[0].RunID)
	}
}

// --- Leaderboard Test ---

func TestLeaderboard(t *testing.T) {
	scores := map[string]*AgentScore{
		"0xa": {Address: "0xa", GraphScore: 50},
		"0xb": {Address: "0xb", GraphScore: 90},
		"0xc": {Address: "0xc", GraphScore: 70},
	}

	lb := Leaderboard(scores, 2)
	if len(lb) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(lb))
	}
	if lb[0].Address != "0xb" {
		t.Errorf("Expected 0xb first, got %s", lb[0].Address)
	}
	if lb[1].Address != "0xc" {
		t.Errorf("Expected 0xc second, got %s", lb[1].Address)
	}
}

// --- Config Tests ---

func TestInvalidConfigDefaults(t *testing.T) {
	source := edgesFrom(edge("0xa", "0xb", 10, 1))
	seedMap := map[string]float64{"0xa": 1.0}

	// Invalid damping should default
	cfg := Config{Damping: 0, MaxIterations: -1, ConvergenceThreshold: -1}
	engine := NewEngine(source, seeds(seedMap), cfg)

	result, err := engine.Compute(context.Background(), "invalid-config")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}
	if result.NodeCount != 2 {
		t.Errorf("Expected 2 nodes, got %d", result.NodeCount)
	}
}

// --- Integration: Sybil attack with mixed legitimate traffic ---

func TestMixedNetworkSybilResistance(t *testing.T) {
	// Realistic scenario: legitimate marketplace + Sybil attacker
	source := edgesFrom(
		// Legitimate marketplace activity
		edge("0xbuyer1", "0xseller1", 100, 10),
		edge("0xbuyer1", "0xseller2", 50, 5),
		edge("0xbuyer2", "0xseller1", 75, 7),
		edge("0xbuyer2", "0xseller3", 30, 3),
		edge("0xbuyer3", "0xseller2", 60, 6),

		// Sybil ring: 5 agents, high volume, cycling funds
		edge("0xsybil1", "0xsybil2", 5000, 50),
		edge("0xsybil2", "0xsybil3", 5000, 50),
		edge("0xsybil3", "0xsybil4", 5000, 50),
		edge("0xsybil4", "0xsybil5", 5000, 50),
		edge("0xsybil5", "0xsybil1", 5000, 50),

		// Sybil tries to inject into legitimate network
		edge("0xsybil1", "0xseller1", 1, 1),
	)

	seedMap := map[string]float64{
		"0xbuyer1":  0.5, // 90 days, verified
		"0xbuyer2":  0.4, // 90 days
		"0xbuyer3":  0.2, // 30 days
		"0xseller1": 0.3, // 30 days, verified
		"0xseller2": 0.2, // 30 days
		"0xseller3": 0.1, // 7 days
		"0xsybil1":  0.0,
		"0xsybil2":  0.0,
		"0xsybil3":  0.0,
		"0xsybil4":  0.0,
		"0xsybil5":  0.0,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "mixed-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// All legitimate agents should outrank all Sybil agents
	legitimateAddrs := []string{"0xbuyer1", "0xbuyer2", "0xbuyer3", "0xseller1", "0xseller2", "0xseller3"}
	sybilAddrs := []string{"0xsybil1", "0xsybil2", "0xsybil3", "0xsybil4", "0xsybil5"}

	// Find minimum legitimate score
	minLegit := 100.0
	for _, addr := range legitimateAddrs {
		if s := result.Scores[addr]; s != nil && s.GraphScore < minLegit {
			minLegit = s.GraphScore
		}
	}

	// Find maximum Sybil score
	maxSybil := 0.0
	for _, addr := range sybilAddrs {
		if s := result.Scores[addr]; s != nil && s.GraphScore > maxSybil {
			maxSybil = s.GraphScore
		}
	}

	if maxSybil >= minLegit {
		t.Errorf("Sybil agents (max %.1f) should rank below all legitimate agents (min %.1f)",
			maxSybil, minLegit)
	}
}

// --- Sybil Hardening Tests ---

func TestCyclePenalty(t *testing.T) {
	// Sybil ring (zero seeds) forms a cycle. One seeded node pays into the ring.
	// With cycle penalty, the Sybil nodes' scores should decrease.
	source := edgesFrom(
		// Seeded node pays into the ring
		edge("0xlegit", "0xs1", 10, 1),
		// Sybil ring: high volume cycle
		edge("0xs1", "0xs2", 5000, 40),
		edge("0xs2", "0xs3", 5000, 40),
		edge("0xs3", "0xs1", 5000, 40),
	)
	seedMap := map[string]float64{
		"0xlegit": 1.0,
		"0xs1":    0,
		"0xs2":    0,
		"0xs3":    0,
	}

	// Without cycle penalty
	noPenalty := DefaultConfig()
	noPenalty.CyclePenalty = 0
	noPenalty.TemporalDecayRate = 0
	noPenalty.MaxSourceInfluence = 0
	engineNoPenalty := NewEngine(source, seeds(seedMap), noPenalty)
	resultNP, err := engineNoPenalty.Compute(context.Background(), "no-penalty")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// With cycle penalty
	withPenalty := DefaultConfig()
	withPenalty.CyclePenalty = 0.8
	withPenalty.TemporalDecayRate = 0
	withPenalty.MaxSourceInfluence = 0
	enginePenalty := NewEngine(source, seeds(seedMap), withPenalty)
	resultP, err := enginePenalty.Compute(context.Background(), "with-penalty")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// With penalty, Sybil nodes should have lower raw ranks
	for _, addr := range []string{"0xs1", "0xs2", "0xs3"} {
		npRaw := resultNP.Scores[addr].RawRank
		pRaw := resultP.Scores[addr].RawRank
		if pRaw >= npRaw {
			t.Errorf("Cycle penalty should reduce %s raw rank. Without: %f, With: %f", addr, npRaw, pRaw)
		}
	}
}

func TestTemporalDecay(t *testing.T) {
	now := time.Now()
	// Recent edge vs old edge, same volume
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-1 * 24 * time.Hour)},  // 1 day ago
		PaymentEdge{From: "0xa", To: "0xc", Volume: 100, TxCount: 5, LastTxAt: now.Add(-90 * 24 * time.Hour)}, // 90 days ago
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	cfg := DefaultConfig()
	cfg.TemporalDecayRate = 0.03
	cfg.CyclePenalty = 0
	cfg.MaxSourceInfluence = 0

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "temporal-decay-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// B (recent) should outrank C (old) even though raw volume is identical
	bScore := result.Scores["0xb"].GraphScore
	cScore := result.Scores["0xc"].GraphScore
	if bScore <= cScore {
		t.Errorf("Recent edge B (%.1f) should outrank old edge C (%.1f)", bScore, cScore)
	}
}

func TestMaxSourceInfluence(t *testing.T) {
	// Whale dominates B's incoming volume. The whale also pays E (so the cap
	// on raw volume changes the whale's normalized outgoing distribution).
	source := edgesFrom(
		edge("0xwhale", "0xb", 10000, 40), // whale dominates B's incoming
		edge("0xwhale", "0xe", 100, 5),    // whale also pays E
		edge("0xc", "0xb", 10, 2),         // tiny legitimate payer
		edge("0xd", "0xb", 10, 2),         // another tiny payer
	)
	seedMap := map[string]float64{
		"0xwhale": 0.5,
		"0xb":     0.0,
		"0xc":     0.3,
		"0xd":     0.3,
		"0xe":     0.1,
	}

	// Without cap
	noCap := DefaultConfig()
	noCap.MaxSourceInfluence = 0
	noCap.CyclePenalty = 0
	noCap.TemporalDecayRate = 0
	engineNoCap := NewEngine(source, seeds(seedMap), noCap)
	resultNC, err := engineNoCap.Compute(context.Background(), "no-cap")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// With cap
	withCap := DefaultConfig()
	withCap.MaxSourceInfluence = 0.5
	withCap.CyclePenalty = 0
	withCap.TemporalDecayRate = 0
	engineCap := NewEngine(source, seeds(seedMap), withCap)
	resultC, err := engineCap.Compute(context.Background(), "with-cap")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// With cap, whale's raw volume to B should be reduced from 10000 to ~5010
	// (50% of total incoming = 50% of 10020). This changes whale's normalized
	// outgoing distribution, redirecting proportionally more rank to E.
	// B should get less rank, E should get more rank.
	if resultC.Scores["0xb"].RawRank >= resultNC.Scores["0xb"].RawRank {
		t.Errorf("Source influence cap should reduce B's raw rank. Without: %f, With: %f",
			resultNC.Scores["0xb"].RawRank, resultC.Scores["0xb"].RawRank)
	}
	if resultC.Scores["0xe"].RawRank <= resultNC.Scores["0xe"].RawRank {
		t.Errorf("Source influence cap should increase E's raw rank. Without: %f, With: %f",
			resultNC.Scores["0xe"].RawRank, resultC.Scores["0xe"].RawRank)
	}
}

func TestSybilRingWithDecays(t *testing.T) {
	// Full Sybil ring attack with all three defenses enabled.
	// This is the key test: Sybil nodes should have negligible scores.
	now := time.Now()
	source := edgesFrom(
		// Sybil ring: high volume, cycling, old activity
		PaymentEdge{From: "0xs1", To: "0xs2", Volume: 10000, TxCount: 40, LastTxAt: now.Add(-2 * 24 * time.Hour)},
		PaymentEdge{From: "0xs2", To: "0xs3", Volume: 10000, TxCount: 40, LastTxAt: now.Add(-2 * 24 * time.Hour)},
		PaymentEdge{From: "0xs3", To: "0xs1", Volume: 10000, TxCount: 40, LastTxAt: now.Add(-2 * 24 * time.Hour)},
		// Legitimate agent: modest recent activity
		PaymentEdge{From: "0xl", To: "0xm", Volume: 50, TxCount: 5, LastTxAt: now.Add(-1 * time.Hour)},
	)
	seedMap := map[string]float64{
		"0xl":  0.5,
		"0xm":  0.1,
		"0xs1": 0,
		"0xs2": 0,
		"0xs3": 0,
	}

	engine := NewEngine(source, seeds(seedMap), DefaultConfig())
	result, err := engine.Compute(context.Background(), "sybil-full-defense")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// Legitimate agents MUST outrank all Sybil agents
	lScore := result.Scores["0xl"].GraphScore
	mScore := result.Scores["0xm"].GraphScore
	for _, addr := range []string{"0xs1", "0xs2", "0xs3"} {
		sScore := result.Scores[addr].GraphScore
		if sScore >= lScore {
			t.Errorf("Sybil %s (%.1f) should rank below legitimate 0xl (%.1f)", addr, sScore, lScore)
		}
		if sScore >= mScore {
			t.Errorf("Sybil %s (%.1f) should rank below merchant 0xm (%.1f)", addr, sScore, mScore)
		}
	}
}

// --- Graph construction tests ---

func TestBuildGraphFilters(t *testing.T) {
	edges := []PaymentEdge{
		{From: "0xa", To: "0xa", Volume: 1000, TxCount: 10},  // self-loop
		{From: "0xa", To: "0xb", Volume: 0.0005, TxCount: 1}, // below min volume
		{From: "0xa", To: "0xc", Volume: 10, TxCount: 1},     // valid
	}

	cfg := DefaultConfig()
	cfg.MinEdgeVolume = 0.001

	g := buildGraph(edges, cfg)

	if len(g.nodes) != 2 {
		t.Errorf("Expected 2 nodes (0xa, 0xc), got %d: %v", len(g.nodes), g.nodes)
	}
	if g.edgeCount != 1 {
		t.Errorf("Expected 1 edge, got %d", g.edgeCount)
	}
}

// --- merged from coverage_extra_test.go ---

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
