package tracerank

import (
	"context"
	"math"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Decay function unit tests
// ---------------------------------------------------------------------------

func TestExponentialDecay(t *testing.T) {
	tests := []struct {
		name      string
		daysSince float64
		rate      float64
		expected  float64
		tolerance float64
	}{
		{"zero days", 0, 0.03, 1.0, 0.001},
		{"negative days", -5, 0.03, 1.0, 0.001},
		{"one day", 1, 0.03, math.Exp(-0.03), 0.001},
		{"23 days (half-life at rate 0.03)", 23, 0.03, 0.5, 0.02},
		{"90 days", 90, 0.03, math.Exp(-2.7), 0.001},
		{"zero rate", 30, 0, 1.0, 0.001},
		{"high rate", 10, 0.5, math.Exp(-5), 0.001},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ExponentialDecay(tc.daysSince, tc.rate)
			if math.Abs(result-tc.expected) > tc.tolerance {
				t.Errorf("ExponentialDecay(%v, %v) = %f, want %f (±%f)",
					tc.daysSince, tc.rate, result, tc.expected, tc.tolerance)
			}
		})
	}
}

func TestSCurveDecay(t *testing.T) {
	tests := []struct {
		name      string
		daysSince float64
		halfLife  float64
		steepness float64
		expected  float64
		tolerance float64
	}{
		{"at half-life", 30, 30, 0.2, 0.5, 0.001},
		{"well before half-life", 0, 30, 0.2, 1.0 / (1.0 + math.Exp(-6)), 0.01},
		{"well after half-life", 90, 30, 0.2, 1.0 / (1.0 + math.Exp(12)), 0.001},
		{"zero half-life", 10, 0, 0.2, 1.0, 0.001},
		{"zero steepness uses default 0.2", 30, 30, 0, 0.5, 0.001},
		{"steep transition", 30, 30, 1.0, 0.5, 0.001},
		{"steep well before", 10, 30, 1.0, 1.0 / (1.0 + math.Exp(-20)), 0.001},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := SCurveDecay(tc.daysSince, tc.halfLife, tc.steepness)
			if math.Abs(result-tc.expected) > tc.tolerance {
				t.Errorf("SCurveDecay(%v, %v, %v) = %f, want %f (±%f)",
					tc.daysSince, tc.halfLife, tc.steepness, result, tc.expected, tc.tolerance)
			}
		})
	}

	// Verify monotonic decrease: value at day 1 > day 30 > day 90
	d1 := SCurveDecay(1, 30, 0.2)
	d30 := SCurveDecay(30, 30, 0.2)
	d90 := SCurveDecay(90, 30, 0.2)
	if d1 <= d30 || d30 <= d90 {
		t.Errorf("S-curve should be monotonically decreasing: d1=%f, d30=%f, d90=%f", d1, d30, d90)
	}
}

func TestThresholdDecay(t *testing.T) {
	tests := []struct {
		name      string
		daysSince float64
		threshold float64
		residual  float64
		expected  float64
	}{
		{"before threshold", 15, 30, 0.1, 1.0},
		{"at threshold boundary", 29.99, 30, 0.1, 1.0},
		{"at threshold", 30, 30, 0.1, 0.1},
		{"after threshold", 60, 30, 0.1, 0.1},
		{"zero threshold", 10, 0, 0.1, 1.0},
		{"zero residual", 60, 30, 0.0, 0.0},
		{"negative residual clamped", 60, 30, -0.5, 0.0},
		{"residual > 1 clamped", 60, 30, 1.5, 1.0},
		{"full residual", 60, 30, 1.0, 1.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := ThresholdDecay(tc.daysSince, tc.threshold, tc.residual)
			if math.Abs(result-tc.expected) > 0.001 {
				t.Errorf("ThresholdDecay(%v, %v, %v) = %f, want %f",
					tc.daysSince, tc.threshold, tc.residual, result, tc.expected)
			}
		})
	}
}

func TestApplyDecay_FunctionDispatch(t *testing.T) {
	daysSince := 30.0

	// Exponential
	cfgExp := Config{
		DecayFunction:     DecayExponential,
		TemporalDecayRate: 0.03,
	}
	resultExp := applyDecay(daysSince, cfgExp)
	expectedExp := ExponentialDecay(daysSince, 0.03)
	if math.Abs(resultExp-expectedExp) > 0.001 {
		t.Errorf("DecayExponential: got %f, want %f", resultExp, expectedExp)
	}

	// S-Curve
	cfgSCurve := Config{
		DecayFunction:   DecaySCurve,
		SCurveHalfLife:  30,
		SCurveSteepness: 0.2,
	}
	resultSCurve := applyDecay(daysSince, cfgSCurve)
	expectedSCurve := SCurveDecay(daysSince, 30, 0.2)
	if math.Abs(resultSCurve-expectedSCurve) > 0.001 {
		t.Errorf("DecaySCurve: got %f, want %f", resultSCurve, expectedSCurve)
	}

	// Threshold
	cfgThreshold := Config{
		DecayFunction:     DecayThreshold,
		ThresholdDays:     30,
		ThresholdResidual: 0.1,
	}
	resultThreshold := applyDecay(daysSince, cfgThreshold)
	expectedThreshold := ThresholdDecay(daysSince, 30, 0.1)
	if math.Abs(resultThreshold-expectedThreshold) > 0.001 {
		t.Errorf("DecayThreshold: got %f, want %f", resultThreshold, expectedThreshold)
	}

	// Backward compat: DecayNone + TemporalDecayRate > 0 uses exponential
	cfgBackcompat := Config{
		DecayFunction:     DecayNone,
		TemporalDecayRate: 0.03,
	}
	resultBackcompat := applyDecay(daysSince, cfgBackcompat)
	if math.Abs(resultBackcompat-expectedExp) > 0.001 {
		t.Errorf("backward compat: got %f, want %f", resultBackcompat, expectedExp)
	}

	// DecayNone + no rate: weight = 1
	cfgNone := Config{DecayFunction: DecayNone}
	resultNone := applyDecay(daysSince, cfgNone)
	if resultNone != 1.0 {
		t.Errorf("DecayNone: got %f, want 1.0", resultNone)
	}
}

// ---------------------------------------------------------------------------
// Decay function integration with TraceRank engine
// ---------------------------------------------------------------------------

func TestSCurveDecay_InEngine(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-1 * 24 * time.Hour)},  // 1 day ago
		PaymentEdge{From: "0xa", To: "0xc", Volume: 100, TxCount: 5, LastTxAt: now.Add(-60 * 24 * time.Hour)}, // 60 days ago (past half-life of 30)
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		DecayFunction:        DecaySCurve,
		SCurveHalfLife:       30,
		SCurveSteepness:      0.2,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "scurve-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.Scores["0xb"] == nil {
		t.Fatal("0xb should be in results")
	}
	bScore := result.Scores["0xb"].GraphScore

	// 0xc may have very low score or be excluded due to heavy decay
	cScore := 0.0
	if result.Scores["0xc"] != nil {
		cScore = result.Scores["0xc"].GraphScore
	}
	if bScore <= cScore {
		t.Errorf("Recent edge B (%.1f) should outrank old edge C (%.1f) with S-curve decay", bScore, cScore)
	}
}

func TestThresholdDecay_InEngine(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-10 * 24 * time.Hour)}, // 10 days ago (within threshold)
		PaymentEdge{From: "0xa", To: "0xc", Volume: 100, TxCount: 5, LastTxAt: now.Add(-60 * 24 * time.Hour)}, // 60 days ago (past threshold)
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		DecayFunction:        DecayThreshold,
		ThresholdDays:        30,
		ThresholdResidual:    0.1,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "threshold-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	bScore := result.Scores["0xb"].GraphScore
	cScore := result.Scores["0xc"].GraphScore
	if bScore <= cScore {
		t.Errorf("Within-threshold B (%.1f) should outrank past-threshold C (%.1f)", bScore, cScore)
	}
	// C should still have a score (10% residual)
	if cScore == 0 {
		t.Error("Past-threshold C should still have a non-zero score (10% residual)")
	}
}

// ---------------------------------------------------------------------------
// Negative edge (dispute) tests
// ---------------------------------------------------------------------------

func TestNegativeEdges_ReduceReputation(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 10),      // A pays B
		edge("0xa", "0xc", 100, 10),      // A pays C (same volume)
		DisputeEdge("0xa", "0xb", 50, 1), // A disputes B
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		DisputePenaltyWeight: 1.0,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "dispute-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// B (disputed) should score lower than C (not disputed), even though
	// both received 100 USDC from A initially
	bScore := result.Scores["0xb"].GraphScore
	cScore := result.Scores["0xc"].GraphScore
	if bScore >= cScore {
		t.Errorf("Disputed B (%.1f) should rank below undisputed C (%.1f)", bScore, cScore)
	}
}

func TestNegativeEdges_MultipleDisputes(t *testing.T) {
	source := edgesFrom(
		edge("0xa", "0xb", 100, 10),
		edge("0xc", "0xb", 50, 5),
		DisputeEdge("0xd", "0xb", 30, 1), // dispute from D
		DisputeEdge("0xe", "0xb", 20, 1), // dispute from E
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.5,
		"0xd": 0.3,
		"0xe": 0.3,
	}

	// Without disputes
	cfgNoDis := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		DisputePenaltyWeight: 0, // disabled
	}
	engineNoDis := NewEngine(source, seeds(seedMap), cfgNoDis)
	resultNoDis, _ := engineNoDis.Compute(context.Background(), "no-dispute")

	// With disputes
	cfgWithDis := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		DisputePenaltyWeight: 0.5,
	}
	engineWithDis := NewEngine(source, seeds(seedMap), cfgWithDis)
	resultWithDis, _ := engineWithDis.Compute(context.Background(), "with-dispute")

	// B's score should be lower with disputes enabled
	bNoDispute := resultNoDis.Scores["0xb"].RawRank
	bWithDispute := resultWithDis.Scores["0xb"].RawRank
	if bWithDispute >= bNoDispute {
		t.Errorf("B with disputes (%.6f) should have lower raw rank than without (%.6f)",
			bWithDispute, bNoDispute)
	}
}

func TestNegativeEdges_CantGoBelowZero(t *testing.T) {
	// Dispute volume exceeds positive volume — edge should be removed, not go negative
	source := edgesFrom(
		edge("0xa", "0xb", 10, 1),
		DisputeEdge("0xc", "0xb", 100, 1), // dispute > positive
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.5,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		DisputePenaltyWeight: 1.0,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "clamp-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// B should still exist in the graph but with very low/zero score
	if result.Scores["0xb"] == nil {
		// B might not exist if the edge was fully removed
		return
	}
	bScore := result.Scores["0xb"].GraphScore
	if bScore > 50 {
		t.Errorf("Heavily disputed B should have low score, got %.1f", bScore)
	}
}

func TestDisputeEdgeConstructor(t *testing.T) {
	e := DisputeEdge("0xa", "0xb", 50, 3)
	if !e.Dispute {
		t.Error("DisputeEdge should have Dispute=true")
	}
	if e.From != "0xa" {
		t.Errorf("From = %q, want 0xa", e.From)
	}
	if e.To != "0xb" {
		t.Errorf("To = %q, want 0xb", e.To)
	}
	if e.Volume != 50 {
		t.Errorf("Volume = %f, want 50", e.Volume)
	}
	if e.TxCount != 3 {
		t.Errorf("TxCount = %d, want 3", e.TxCount)
	}
}

// ---------------------------------------------------------------------------
// Edge aging and pruning tests
// ---------------------------------------------------------------------------

func TestEdgePruning_OldEdgesExcluded(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-10 * 24 * time.Hour)},  // 10 days ago
		PaymentEdge{From: "0xa", To: "0xc", Volume: 100, TxCount: 5, LastTxAt: now.Add(-200 * 24 * time.Hour)}, // 200 days ago
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		MaxEdgeAge:           180, // prune edges > 180 days old
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "prune-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// Only 0xa and 0xb should be in the graph (0xc's edge is pruned at 200 days)
	if result.NodeCount != 2 {
		t.Errorf("Expected 2 nodes, got %d", result.NodeCount)
	}
	if result.Scores["0xb"] == nil {
		t.Error("0xb should be in results (recent edge)")
	}
	if result.Scores["0xc"] != nil {
		t.Error("0xc should be pruned (edge > 180 days old)")
	}
}

func TestEdgePruning_ZeroMaxAge_NoPruning(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-500 * 24 * time.Hour)}, // 500 days ago
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		MaxEdgeAge:           0, // disabled
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "no-prune-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.NodeCount != 2 {
		t.Errorf("Expected 2 nodes (no pruning), got %d", result.NodeCount)
	}
}

func TestEdgePruning_BoundaryEdge(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		PaymentEdge{From: "0xa", To: "0xb", Volume: 100, TxCount: 5, LastTxAt: now.Add(-179 * 24 * time.Hour)}, // 179 days ago (within)
		PaymentEdge{From: "0xa", To: "0xc", Volume: 100, TxCount: 5, LastTxAt: now.Add(-181 * 24 * time.Hour)}, // 181 days ago (pruned)
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
		"0xc": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		MaxEdgeAge:           180,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "boundary-prune-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.NodeCount != 2 {
		t.Errorf("Expected 2 nodes (179 days within, 181 pruned), got %d", result.NodeCount)
	}
	if result.Scores["0xb"] == nil {
		t.Error("0xb should be present (179 days, within limit)")
	}
	if result.Scores["0xc"] != nil {
		t.Error("0xc should be pruned (181 days, exceeds limit)")
	}
}

func TestEdgePruning_NoLastTxAt_NotPruned(t *testing.T) {
	// Edges without timestamps should not be pruned
	source := edgesFrom(
		edge("0xa", "0xb", 100, 5), // no LastTxAt
	)
	seedMap := map[string]float64{
		"0xa": 1.0,
		"0xb": 0.0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		MaxEdgeAge:           180,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "no-timestamp-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	if result.NodeCount != 2 {
		t.Errorf("Edges without timestamps should not be pruned, got %d nodes", result.NodeCount)
	}
}

// ---------------------------------------------------------------------------
// Combined: decay + pruning + disputes
// ---------------------------------------------------------------------------

func TestCombinedMeritRank(t *testing.T) {
	now := time.Now()
	source := edgesFrom(
		// Legitimate recent activity
		PaymentEdge{From: "0xlegit", To: "0xmerchant", Volume: 50, TxCount: 5, LastTxAt: now.Add(-1 * 24 * time.Hour)},
		// Old Sybil activity (should be heavily decayed)
		PaymentEdge{From: "0xs1", To: "0xs2", Volume: 5000, TxCount: 40, LastTxAt: now.Add(-90 * 24 * time.Hour)},
		PaymentEdge{From: "0xs2", To: "0xs1", Volume: 5000, TxCount: 40, LastTxAt: now.Add(-90 * 24 * time.Hour)},
		// Ancient edge (pruned)
		PaymentEdge{From: "0xold", To: "0xs1", Volume: 100, TxCount: 1, LastTxAt: now.Add(-200 * 24 * time.Hour)},
		// Dispute against Sybil node
		PaymentEdge{From: "0xlegit", To: "0xs1", Volume: 10, TxCount: 1, Dispute: true},
	)
	seedMap := map[string]float64{
		"0xlegit":    0.5,
		"0xmerchant": 0.1,
		"0xs1":       0,
		"0xs2":       0,
		"0xold":      0,
	}

	cfg := Config{
		Damping:              DefaultDamping,
		MaxIterations:        DefaultMaxIterations,
		ConvergenceThreshold: DefaultConvergenceThreshold,
		MinEdgeVolume:        0.001,
		MinEdgeTxCount:       1,
		TemporalDecayRate:    0.03,
		CyclePenalty:         0.8,
		MaxSourceInfluence:   0.5,
		MaxEdgeAge:           180,
		DisputePenaltyWeight: 0.5,
	}

	engine := NewEngine(source, seeds(seedMap), cfg)
	result, err := engine.Compute(context.Background(), "combined-merit-test")
	if err != nil {
		t.Fatalf("Compute failed: %v", err)
	}

	// 0xold should be pruned
	if result.Scores["0xold"] != nil {
		t.Error("0xold should be pruned (200 day edge)")
	}

	// Legitimate agent should outrank Sybil agents
	legitScore := result.Scores["0xlegit"].GraphScore
	merchantScore := result.Scores["0xmerchant"].GraphScore
	for _, addr := range []string{"0xs1", "0xs2"} {
		if s := result.Scores[addr]; s != nil && s.GraphScore >= legitScore {
			t.Errorf("Sybil %s (%.1f) should rank below legit (%.1f)", addr, s.GraphScore, legitScore)
		}
		if s := result.Scores[addr]; s != nil && s.GraphScore >= merchantScore {
			t.Errorf("Sybil %s (%.1f) should rank below merchant (%.1f)", addr, s.GraphScore, merchantScore)
		}
	}
}
