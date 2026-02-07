package predictions

import (
	"context"
	"errors"
	"testing"
	"time"
)

// mockMetricProvider returns configurable metric values
type mockMetricProvider struct {
	agentMetrics   map[string]float64
	serviceMetrics map[string]float64
	marketMetrics  map[string]float64
	err            error
}

func newMockMetrics() *mockMetricProvider {
	return &mockMetricProvider{
		agentMetrics:   make(map[string]float64),
		serviceMetrics: make(map[string]float64),
		marketMetrics:  make(map[string]float64),
	}
}

func (m *mockMetricProvider) GetAgentMetric(ctx context.Context, agentAddr, metric string) (float64, error) {
	if m.err != nil {
		return 0, m.err
	}
	v, ok := m.agentMetrics[agentAddr+":"+metric]
	if !ok {
		return 0, errors.New("metric not found")
	}
	return v, nil
}

func (m *mockMetricProvider) GetServiceTypeMetric(ctx context.Context, serviceType, metric string) (float64, error) {
	if m.err != nil {
		return 0, m.err
	}
	v, ok := m.serviceMetrics[serviceType+":"+metric]
	if !ok {
		return 0, errors.New("metric not found")
	}
	return v, nil
}

func (m *mockMetricProvider) GetMarketMetric(ctx context.Context, metric string) (float64, error) {
	if m.err != nil {
		return 0, m.err
	}
	v, ok := m.marketMetrics[metric]
	if !ok {
		return 0, errors.New("metric not found")
	}
	return v, nil
}

// createTestPred creates a prediction directly via store, bypassing MakePrediction validation.
// This is needed for resolve tests that require past ResolvesAt times.
func createTestPred(store *MemoryStore, p *Prediction) *Prediction {
	if p.Status == "" {
		p.Status = StatusPending
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	store.Create(context.Background(), p)
	return p
}

// ---------------------------------------------------------------------------
// MakePrediction tests
// ---------------------------------------------------------------------------

func TestMakePrediction_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	pred, err := svc.MakePrediction(ctx, &Prediction{
		AuthorAddr:      "0xPredictor",
		AuthorName:      "Oracle",
		Type:            TypeAgentMetric,
		Statement:       "Agent will hit 1000 txns",
		TargetType:      "agent",
		TargetID:        "0xtarget",
		Metric:          "tx_count",
		Operator:        ">",
		TargetValue:     1000,
		ResolvesAt:      time.Now().Add(24 * time.Hour),
		ConfidenceLevel: 3,
	})
	if err != nil {
		t.Fatalf("MakePrediction failed: %v", err)
	}

	if pred.ID == "" || !hasPrefix(pred.ID, "pred_") {
		t.Errorf("Expected pred_ prefixed ID, got %q", pred.ID)
	}
	if pred.AuthorAddr != "0xpredictor" {
		t.Errorf("Expected lowercased author, got %s", pred.AuthorAddr)
	}
	if pred.Status != StatusPending {
		t.Errorf("Expected pending status, got %s", pred.Status)
	}
	if pred.ConfidenceLevel != 3 {
		t.Errorf("Expected confidence 3, got %d", pred.ConfidenceLevel)
	}
}

func TestMakePrediction_Validation(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	tests := []struct {
		name string
		pred Prediction
	}{
		{"empty statement", Prediction{TargetType: "agent", Metric: "tx_count", ResolvesAt: time.Now().Add(time.Hour)}},
		{"empty target type", Prediction{Statement: "test", Metric: "tx_count", ResolvesAt: time.Now().Add(time.Hour)}},
		{"empty metric", Prediction{Statement: "test", TargetType: "agent", ResolvesAt: time.Now().Add(time.Hour)}},
		{"past resolution", Prediction{Statement: "test", TargetType: "agent", Metric: "tx_count", ResolvesAt: time.Now().Add(-time.Hour)}},
	}

	for _, tt := range tests {
		_, err := svc.MakePrediction(ctx, &tt.pred)
		if err == nil {
			t.Errorf("Expected error for %q, got nil", tt.name)
		}
	}
}

func TestMakePrediction_ConfidenceClamp(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	base := Prediction{
		Statement:  "Test",
		TargetType: "agent",
		Metric:     "tx_count",
		ResolvesAt: time.Now().Add(time.Hour),
	}

	// Below min
	p := base
	p.ConfidenceLevel = 0
	pred, _ := svc.MakePrediction(ctx, &p)
	if pred.ConfidenceLevel != 1 {
		t.Errorf("Expected confidence clamped to 1, got %d", pred.ConfidenceLevel)
	}

	// Above max
	p2 := base
	p2.ConfidenceLevel = 10
	pred2, _ := svc.MakePrediction(ctx, &p2)
	if pred2.ConfidenceLevel != 5 {
		t.Errorf("Expected confidence clamped to 5, got %d", pred2.ConfidenceLevel)
	}
}

// ---------------------------------------------------------------------------
// evaluate tests
// ---------------------------------------------------------------------------

func TestEvaluate_Operators(t *testing.T) {
	svc := &Service{}

	tests := []struct {
		op       string
		actual   float64
		target   float64
		expected bool
	}{
		{">", 100, 50, true},
		{">", 50, 100, false},
		{">", 50, 50, false},
		{">=", 50, 50, true},
		{">=", 49, 50, false},
		{"<", 30, 50, true},
		{"<", 50, 50, false},
		{"<=", 50, 50, true},
		{"<=", 51, 50, false},
		{"=", 100, 100, true},
		{"=", 104, 100, true},  // Within 5% tolerance
		{"=", 96, 100, true},   // Within 5% tolerance
		{"=", 106, 100, false}, // Outside 5% tolerance
		{"==", 100, 100, true},
		{"change_pct", 10, 5, false}, // Always false (simplified)
		{"invalid", 10, 5, false},
	}

	for _, tt := range tests {
		result := svc.evaluate(tt.op, tt.actual, tt.target)
		if result != tt.expected {
			t.Errorf("evaluate(%q, %f, %f) = %v, want %v", tt.op, tt.actual, tt.target, result, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// ResolvePredictions tests
// ---------------------------------------------------------------------------

func TestResolvePredictions_Correct(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.agentMetrics["0xtarget:tx_count"] = 1500
	svc := NewService(store, metrics)
	ctx := context.Background()

	// Create directly via store to bypass MakePrediction's future-date validation
	createTestPred(store, &Prediction{
		AuthorAddr:  "0xoracle",
		Statement:   "Agent hits 1000",
		TargetType:  "agent",
		TargetID:    "0xtarget",
		Metric:      "tx_count",
		Operator:    ">",
		TargetValue: 1000,
		ResolvesAt:  time.Now().Add(-time.Minute),
	})

	resolved, err := svc.ResolvePredictions(ctx)
	if err != nil {
		t.Fatalf("ResolvePredictions failed: %v", err)
	}

	if resolved != 1 {
		t.Errorf("Expected 1 resolved, got %d", resolved)
	}

	// Check stats
	stats, _ := store.GetPredictorStats(ctx, "0xoracle")
	if stats.Correct != 1 {
		t.Errorf("Expected 1 correct, got %d", stats.Correct)
	}
	if stats.Accuracy != 1.0 {
		t.Errorf("Expected accuracy 1.0, got %f", stats.Accuracy)
	}
	if stats.Streak != 1 {
		t.Errorf("Expected streak 1, got %d", stats.Streak)
	}
}

func TestResolvePredictions_Wrong(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.agentMetrics["0xtarget:tx_count"] = 500
	svc := NewService(store, metrics)
	ctx := context.Background()

	createTestPred(store, &Prediction{
		AuthorAddr:  "0xoracle",
		Statement:   "Agent hits 1000",
		TargetType:  "agent",
		TargetID:    "0xtarget",
		Metric:      "tx_count",
		Operator:    ">",
		TargetValue: 1000,
		ResolvesAt:  time.Now().Add(-time.Minute),
	})

	svc.ResolvePredictions(ctx)

	stats, _ := store.GetPredictorStats(ctx, "0xoracle")
	if stats.Wrong != 1 {
		t.Errorf("Expected 1 wrong, got %d", stats.Wrong)
	}
	if stats.Streak != -1 {
		t.Errorf("Expected streak -1, got %d", stats.Streak)
	}
}

func TestResolvePredictions_SkipsFuture(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	svc.MakePrediction(ctx, &Prediction{
		AuthorAddr: "0xoracle",
		Statement:  "Future prediction",
		TargetType: "agent",
		Metric:     "tx_count",
		ResolvesAt: time.Now().Add(24 * time.Hour), // Not yet resolvable
	})

	resolved, _ := svc.ResolvePredictions(ctx)
	if resolved != 0 {
		t.Errorf("Expected 0 resolved for future prediction, got %d", resolved)
	}
}

func TestResolvePredictions_VoidOnMetricError(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	// Don't set any metrics - will cause "not found" error
	svc := NewService(store, metrics)
	ctx := context.Background()

	pred := createTestPred(store, &Prediction{
		AuthorAddr:  "0xoracle",
		Statement:   "Test",
		TargetType:  "agent",
		TargetID:    "0xunknown",
		Metric:      "tx_count",
		Operator:    ">",
		TargetValue: 100,
		ResolvesAt:  time.Now().Add(-time.Minute),
	})

	svc.ResolvePredictions(ctx)

	updated, _ := store.Get(ctx, pred.ID)
	if updated.Status != StatusVoid {
		t.Errorf("Expected void status on metric error, got %s", updated.Status)
	}
}

func TestResolvePredictions_VoidOnUnknownTargetType(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	pred := createTestPred(store, &Prediction{
		AuthorAddr: "0xoracle",
		Statement:  "Test",
		TargetType: "unknown_type",
		Metric:     "something",
		ResolvesAt: time.Now().Add(-time.Minute),
	})

	svc.ResolvePredictions(ctx)

	updated, _ := store.Get(ctx, pred.ID)
	if updated.Status != StatusVoid {
		t.Errorf("Expected void on unknown target type, got %s", updated.Status)
	}
}

func TestResolvePredictions_ServiceTypeMetric(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.serviceMetrics["translation:avg_price"] = 0.003
	svc := NewService(store, metrics)
	ctx := context.Background()

	createTestPred(store, &Prediction{
		AuthorAddr:  "0xoracle",
		Statement:   "Translation prices drop",
		TargetType:  "service_type",
		TargetID:    "translation",
		Metric:      "avg_price",
		Operator:    "<",
		TargetValue: 0.005,
		ResolvesAt:  time.Now().Add(-time.Minute),
	})

	resolved, _ := svc.ResolvePredictions(ctx)
	if resolved != 1 {
		t.Errorf("Expected 1 resolved, got %d", resolved)
	}
}

func TestResolvePredictions_MarketMetric(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.marketMetrics["total_volume"] = 50000
	svc := NewService(store, metrics)
	ctx := context.Background()

	createTestPred(store, &Prediction{
		AuthorAddr:  "0xoracle",
		Statement:   "Market volume exceeds 40k",
		TargetType:  "market",
		Metric:      "total_volume",
		Operator:    ">=",
		TargetValue: 40000,
		ResolvesAt:  time.Now().Add(-time.Minute),
	})

	resolved, _ := svc.ResolvePredictions(ctx)
	if resolved != 1 {
		t.Errorf("Expected 1 resolved, got %d", resolved)
	}
}

// ---------------------------------------------------------------------------
// Streak tests
// ---------------------------------------------------------------------------

func TestStreakTracking(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.agentMetrics["0xt:tx_count"] = 100
	svc := NewService(store, metrics)
	ctx := context.Background()

	makePred := func(op string, target float64) {
		createTestPred(store, &Prediction{
			AuthorAddr: "0xoracle", Statement: "test", TargetType: "agent",
			TargetID: "0xt", Metric: "tx_count", Operator: op,
			TargetValue: target, ResolvesAt: time.Now().Add(-time.Minute),
		})
	}

	// 3 correct in a row
	makePred(">", 50)
	makePred(">", 60)
	makePred(">", 70)
	svc.ResolvePredictions(ctx)

	stats, _ := store.GetPredictorStats(ctx, "0xoracle")
	if stats.Streak != 3 {
		t.Errorf("Expected streak 3, got %d", stats.Streak)
	}
	if stats.BestStreak != 3 {
		t.Errorf("Expected best streak 3, got %d", stats.BestStreak)
	}

	// Then 1 wrong
	makePred(">", 200) // 100 > 200 is false
	svc.ResolvePredictions(ctx)

	stats, _ = store.GetPredictorStats(ctx, "0xoracle")
	if stats.Streak != -1 {
		t.Errorf("Expected streak -1 after wrong, got %d", stats.Streak)
	}
	if stats.BestStreak != 3 {
		t.Errorf("Best streak should still be 3, got %d", stats.BestStreak)
	}
}

// ---------------------------------------------------------------------------
// Vote tests
// ---------------------------------------------------------------------------

func TestVote_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	pred, _ := svc.MakePrediction(ctx, &Prediction{
		AuthorAddr: "0xoracle", Statement: "test", TargetType: "agent",
		Metric: "tx_count", ResolvesAt: time.Now().Add(time.Hour),
	})

	err := svc.Vote(ctx, pred.ID, "0xVoter1", true)
	if err != nil {
		t.Fatalf("Vote failed: %v", err)
	}

	updated, _ := store.Get(ctx, pred.ID)
	if updated.Agrees != 1 {
		t.Errorf("Expected 1 agree, got %d", updated.Agrees)
	}
}

func TestVote_ChangeVote(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store, newMockMetrics())
	ctx := context.Background()

	pred, _ := svc.MakePrediction(ctx, &Prediction{
		AuthorAddr: "0xoracle", Statement: "test", TargetType: "agent",
		Metric: "tx_count", ResolvesAt: time.Now().Add(time.Hour),
	})

	svc.Vote(ctx, pred.ID, "0xVoter", true)
	svc.Vote(ctx, pred.ID, "0xVoter", false) // Change to disagree

	updated, _ := store.Get(ctx, pred.ID)
	if updated.Agrees != 0 {
		t.Errorf("Expected 0 agrees after vote change, got %d", updated.Agrees)
	}
	if updated.Disagrees != 1 {
		t.Errorf("Expected 1 disagree after vote change, got %d", updated.Disagrees)
	}
}

func TestVote_OnResolvedPrediction(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.agentMetrics["0xt:tx_count"] = 100
	svc := NewService(store, metrics)
	ctx := context.Background()

	// Create directly via store (needs past ResolvesAt)
	pred := createTestPred(store, &Prediction{
		AuthorAddr: "0xoracle", Statement: "test", TargetType: "agent",
		TargetID: "0xt", Metric: "tx_count", Operator: ">", TargetValue: 50,
		ResolvesAt: time.Now().Add(-time.Minute),
	})
	svc.ResolvePredictions(ctx)

	err := svc.Vote(ctx, pred.ID, "0xVoter", true)
	if err == nil {
		t.Error("Expected error voting on resolved prediction")
	}
}

// ---------------------------------------------------------------------------
// Leaderboard tests
// ---------------------------------------------------------------------------

func TestGetLeaderboard_MinimumThreshold(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.agentMetrics["0xt:m"] = 100
	svc := NewService(store, metrics)
	ctx := context.Background()

	// Make only 3 predictions (below threshold of 5)
	for i := 0; i < 3; i++ {
		createTestPred(store, &Prediction{
			AuthorAddr: "0xfew", Statement: "test", TargetType: "agent",
			TargetID: "0xt", Metric: "m", Operator: ">", TargetValue: 50,
			ResolvesAt: time.Now().Add(-time.Minute),
		})
	}
	svc.ResolvePredictions(ctx)

	leaderboard, _ := svc.GetLeaderboard(ctx, 10)
	if len(leaderboard) != 0 {
		t.Errorf("Expected empty leaderboard (below 5 prediction threshold), got %d", len(leaderboard))
	}
}

func TestGetLeaderboard_WithEntries(t *testing.T) {
	store := NewMemoryStore()
	metrics := newMockMetrics()
	metrics.agentMetrics["0xt:m"] = 100
	svc := NewService(store, metrics)
	ctx := context.Background()

	// Make 6 predictions
	for i := 0; i < 6; i++ {
		createTestPred(store, &Prediction{
			AuthorAddr: "0xactive", Statement: "test", TargetType: "agent",
			TargetID: "0xt", Metric: "m", Operator: ">", TargetValue: 50,
			ResolvesAt: time.Now().Add(-time.Minute),
		})
	}
	svc.ResolvePredictions(ctx)

	leaderboard, _ := svc.GetLeaderboard(ctx, 10)
	if len(leaderboard) != 1 {
		t.Errorf("Expected 1 entry on leaderboard, got %d", len(leaderboard))
	}
}

// ---------------------------------------------------------------------------
// Store tests
// ---------------------------------------------------------------------------

func TestMemoryStore_Get_NotFound(t *testing.T) {
	store := NewMemoryStore()

	_, err := store.Get(context.Background(), "nonexistent")
	if err != ErrPredictionNotFound {
		t.Errorf("Expected ErrPredictionNotFound, got %v", err)
	}
}

func TestMemoryStore_ListByAuthor(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &Prediction{ID: "p1", AuthorAddr: "0xA", Statement: "1", TargetType: "a", Metric: "m"})
	store.Create(ctx, &Prediction{ID: "p2", AuthorAddr: "0xB", Statement: "2", TargetType: "a", Metric: "m"})
	store.Create(ctx, &Prediction{ID: "p3", AuthorAddr: "0xA", Statement: "3", TargetType: "a", Metric: "m"})

	preds, _ := store.ListByAuthor(ctx, "0xa", 10)
	if len(preds) != 2 {
		t.Errorf("Expected 2 predictions by 0xa, got %d", len(preds))
	}
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
