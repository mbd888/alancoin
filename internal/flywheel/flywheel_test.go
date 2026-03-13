package flywheel

import (
	"context"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/registry"
)

// stubStore implements registry.Store for testing. Only the methods used
// by the flywheel Engine are implemented; everything else panics.
type stubStore struct {
	registry.Store
	agents []*registry.Agent
	txns   []*registry.Transaction
}

func (s *stubStore) ListAgents(_ context.Context, _ registry.AgentQuery) ([]*registry.Agent, error) {
	return s.agents, nil
}

func (s *stubStore) GetRecentTransactions(_ context.Context, _ int) ([]*registry.Transaction, error) {
	return s.txns, nil
}

func makeAgent(addr string, txCount int64, successRate float64, createdDaysAgo int) *registry.Agent {
	return &registry.Agent{
		Address:   addr,
		CreatedAt: time.Now().Add(-time.Duration(createdDaysAgo) * 24 * time.Hour),
		Stats: registry.AgentStats{
			TransactionCount: txCount,
			SuccessRate:      successRate,
			LastActive:       time.Now().Add(-1 * time.Hour),
		},
	}
}

func makeTx(from, to, amount, status string, hoursAgo float64) *registry.Transaction {
	return &registry.Transaction{
		From:      from,
		To:        to,
		Amount:    amount,
		Status:    status,
		CreatedAt: time.Now().Add(-time.Duration(hoursAgo*3600) * time.Second),
	}
}

func TestEngine_Compute_Empty(t *testing.T) {
	store := &stubStore{}
	engine := NewEngine(store)

	state, err := engine.Compute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.TotalAgents != 0 {
		t.Errorf("expected 0 agents, got %d", state.TotalAgents)
	}
	if state.HealthScore != 0 {
		t.Errorf("expected 0 health, got %f", state.HealthScore)
	}
	if state.HealthTier != TierCold {
		t.Errorf("expected cold tier, got %s", state.HealthTier)
	}
}

func TestEngine_Compute_WithActivity(t *testing.T) {
	agents := []*registry.Agent{
		makeAgent("0xA", 100, 0.95, 30),
		makeAgent("0xB", 50, 0.90, 20),
		makeAgent("0xC", 10, 0.80, 10),
		makeAgent("0xD", 2, 0.50, 5),
		makeAgent("0xE", 0, 0.0, 1), // new agent
	}

	txns := []*registry.Transaction{
		// Recent txns (last hour)
		makeTx("0xa", "0xb", "1.000000", "confirmed", 0.5),
		makeTx("0xb", "0xc", "2.000000", "confirmed", 0.3),
		makeTx("0xa", "0xc", "0.500000", "confirmed", 0.8),
		// Older txns (3 days ago)
		makeTx("0xa", "0xb", "1.000000", "confirmed", 72),
		makeTx("0xc", "0xd", "0.100000", "confirmed", 96),
		// Prior 7d window (10 days ago)
		makeTx("0xa", "0xb", "1.000000", "confirmed", 240),
	}

	store := &stubStore{agents: agents, txns: txns}
	engine := NewEngine(store)

	state, err := engine.Compute(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if state.TotalAgents != 5 {
		t.Errorf("expected 5 agents, got %d", state.TotalAgents)
	}
	if state.TransactionsPerHour < 3 {
		t.Errorf("expected at least 3 txns/hr, got %f", state.TransactionsPerHour)
	}
	if state.VelocityScore <= 0 {
		t.Error("expected positive velocity score")
	}
	if state.TotalEdges <= 0 {
		t.Error("expected positive edge count")
	}
	if state.HealthScore < 0 || state.HealthScore > 100 {
		t.Errorf("health score out of range: %f", state.HealthScore)
	}

	// Tier distribution should have entries
	if len(state.TierDistribution) == 0 {
		t.Error("expected non-empty tier distribution")
	}
}

func TestEngine_History(t *testing.T) {
	store := &stubStore{
		agents: []*registry.Agent{makeAgent("0xA", 10, 0.90, 5)},
		txns:   []*registry.Transaction{makeTx("0xa", "0xb", "1.0", "confirmed", 0.5)},
	}
	engine := NewEngine(store)

	// Compute twice
	if _, err := engine.Compute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Compute(context.Background()); err != nil {
		t.Fatal(err)
	}

	history := engine.History()
	if len(history) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(history))
	}
}

func TestEngine_Latest_NilBeforeCompute(t *testing.T) {
	engine := NewEngine(&stubStore{})
	if engine.Latest() != nil {
		t.Error("expected nil before first compute")
	}
}

func TestHealthTier(t *testing.T) {
	tests := []struct {
		score    float64
		expected string
	}{
		{0, TierCold},
		{19.9, TierCold},
		{20, TierWarming},
		{39.9, TierWarming},
		{40, TierSpinning},
		{59.9, TierSpinning},
		{60, TierAccelerating},
		{79.9, TierAccelerating},
		{80, TierFlywheel},
		{100, TierFlywheel},
	}

	for _, tt := range tests {
		got := healthTier(tt.score)
		if got != tt.expected {
			t.Errorf("healthTier(%v) = %q, want %q", tt.score, got, tt.expected)
		}
	}
}

func TestApproximateTier(t *testing.T) {
	tests := []struct {
		txCount     int64
		successRate float64
		expected    string
	}{
		{0, 0, "new"},
		{5, 0.50, "emerging"},
		{20, 0.80, "established"},
		{50, 0.90, "trusted"},
		{100, 0.95, "elite"},
		{200, 0.99, "elite"},
		{100, 0.80, "established"}, // high volume but lower success rate
	}

	for _, tt := range tests {
		a := &registry.Agent{Stats: registry.AgentStats{
			TransactionCount: tt.txCount,
			SuccessRate:      tt.successRate,
		}}
		got := approximateTier(a)
		if got != tt.expected {
			t.Errorf("approximateTier(tx=%d, rate=%.2f) = %q, want %q",
				tt.txCount, tt.successRate, got, tt.expected)
		}
	}
}

func TestParseFloat(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"", 0},
		{"0", 0},
		{"1.5", 1.5},
		{"100.25", 100.25},
	}

	for _, tt := range tests {
		got := parseFloat(tt.input)
		if got != tt.expected {
			t.Errorf("parseFloat(%q) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

func TestComputeHealth_Weights(t *testing.T) {
	state := &State{
		VelocityScore:      100,
		GrowthScore:        100,
		DensityScore:       100,
		EffectivenessScore: 100,
		RetentionScore:     100,
	}

	engine := NewEngine(&stubStore{})
	engine.computeHealth(state)

	if state.HealthScore != 100 {
		t.Errorf("all sub-scores at 100 should give health 100, got %f", state.HealthScore)
	}
	if state.HealthTier != TierFlywheel {
		t.Errorf("expected flywheel tier, got %s", state.HealthTier)
	}
}

func TestGraphDensity(t *testing.T) {
	// 4 agents, 3 unique directed edges → some density
	agents := []*registry.Agent{
		makeAgent("0xA", 10, 0.90, 5),
		makeAgent("0xB", 10, 0.90, 5),
		makeAgent("0xC", 10, 0.90, 5),
		makeAgent("0xD", 10, 0.90, 5),
	}
	txns := []*registry.Transaction{
		makeTx("0xa", "0xb", "1.0", "confirmed", 1),
		makeTx("0xb", "0xa", "1.0", "confirmed", 1), // reciprocal edge
		makeTx("0xb", "0xc", "1.0", "confirmed", 1),
		makeTx("0xc", "0xa", "1.0", "confirmed", 1),
	}

	store := &stubStore{agents: agents, txns: txns}
	engine := NewEngine(store)

	state, err := engine.Compute(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if state.TotalEdges != 4 {
		t.Errorf("expected 4 edges, got %d", state.TotalEdges)
	}
	if state.GraphDensity <= 0 {
		t.Error("expected positive graph density")
	}
	if state.ActiveAgents7d != 3 {
		t.Errorf("expected 3 active agents, got %d", state.ActiveAgents7d)
	}
	if state.Reciprocity <= 0 {
		t.Error("expected positive reciprocity (a→b and c→a form partial reciprocity)")
	}
}
