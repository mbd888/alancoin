package supervisor

import (
	"context"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// BaselineTimer: Start/Stop lifecycle, safeDoWork panic recovery
// ---------------------------------------------------------------------------

func TestBaselineTimer_StartAndStop(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go timer.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	if !timer.Running() {
		t.Fatal("expected running after Start")
	}

	timer.Stop()
	time.Sleep(100 * time.Millisecond)
	if timer.Running() {
		t.Fatal("expected not running after Stop")
	}
	cancel()
}

func TestBaselineTimer_StartContextCancellation(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go timer.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	if !timer.Running() {
		t.Fatal("expected running after Start")
	}

	cancel()
	time.Sleep(100 * time.Millisecond)
	if timer.Running() {
		t.Fatal("expected not running after context cancel")
	}
}

func TestBaselineTimer_SafeDoWorkRecoversPanic(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())

	// Should not panic
	timer.safeDoWork(context.Background(), func(ctx context.Context) {
		panic("test panic")
	})
}

// ---------------------------------------------------------------------------
// BaselineTimer: rebuildGraph with no events
// ---------------------------------------------------------------------------

func TestBaselineTimer_RebuildGraphEmpty(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())
	timer.rebuildGraph(context.Background())

	// No events to rebuild, graph should be empty
	if sv.graph.GetNode("0xanyone") != nil {
		t.Fatal("expected no nodes in empty graph")
	}
}

// ---------------------------------------------------------------------------
// BaselineTimer: compute with no agents
// ---------------------------------------------------------------------------

func TestBaselineTimer_ComputeNoAgents(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())
	timer.compute(context.Background())
	// Should complete without error
}

// ---------------------------------------------------------------------------
// BaselineTimer: loadBaselines empty store
// ---------------------------------------------------------------------------

func TestBaselineTimer_LoadBaselinesEmpty(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())
	timer.loadBaselines(context.Background())

	// No baselines loaded, cache should be empty
	if sv.GetCachedBaseline("0xanyone") != nil {
		t.Fatal("expected nil baseline from empty store")
	}
}

// ---------------------------------------------------------------------------
// EventWriter: Start and context cancel flushes remaining
// ---------------------------------------------------------------------------

func TestEventWriter_StartAndContextCancel(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	time.Sleep(50 * time.Millisecond)

	// Send some events
	w.Send("0xtest", "0xcounterparty", big.NewInt(1000000), time.Now())

	// Cancel context — should flush
	cancel()
	time.Sleep(200 * time.Millisecond)

	if w.Running() {
		t.Fatal("expected not running after cancel")
	}

	events := store.GetEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 flushed event, got %d", len(events))
	}
}

func TestEventWriter_StopFlushes(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)

	time.Sleep(100 * time.Millisecond)

	w.Send("0xtest", "", big.NewInt(500000), time.Now())
	// Allow the event to be received before stopping
	time.Sleep(50 * time.Millisecond)

	w.Stop()
	time.Sleep(300 * time.Millisecond)

	events := store.GetEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event after stop flush, got %d", len(events))
	}
}

func TestEventWriter_BatchSizeFlush(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	// Send more than batch size (100) events
	for i := 0; i < 110; i++ {
		w.Send("0xagent", "", big.NewInt(1000), time.Now())
	}

	// Wait for flush
	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) != 110 {
		t.Fatalf("expected 110 events, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// EventWriter: flush empty buffer is no-op
// ---------------------------------------------------------------------------

func TestEventWriter_FlushEmptyBuffer(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())
	// Should not panic
	w.flush(nil)
	w.flush([]*SpendEventRecord{})
}

// ---------------------------------------------------------------------------
// Supervisor: EscrowLock eval deny returns error before inner
// ---------------------------------------------------------------------------

func TestEscrowLock_EvalDeny_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.EscrowLock(ctx, "0xAlice", "not_a_number", "ref1")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Spend with invalid amount
// ---------------------------------------------------------------------------

func TestSpend_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Spend(ctx, "0xAlice", "not_a_number", "ref1")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Transfer with invalid amount
// ---------------------------------------------------------------------------

func TestTransfer_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Transfer(ctx, "0xAlice", "0xBob", "not_a_number", "ref1")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Withdraw with invalid amount
// ---------------------------------------------------------------------------

func TestWithdraw_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Withdraw(ctx, "0xAlice", "not_a_number", "ref1")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: SettleHoldWithFee with valid amounts (baseline persist path)
// ---------------------------------------------------------------------------

func TestSettleHoldWithFee_PersistSpendWithValidAmounts(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	// Hold, then settle with fee
	_ = sv.Hold(ctx, "0xBuyer", "10.00", "ref1")
	_ = sv.SettleHoldWithFee(ctx, "0xBuyer", "0xSeller", "8.00", "0xPlatform", "2.00", "ref1")

	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) < 1 {
		t.Fatalf("expected at least 1 persisted event, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Supervisor: ReleaseEscrow/PartialEscrowSettle persist spend
// ---------------------------------------------------------------------------

func TestReleaseEscrow_PersistSpend(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	_ = sv.EscrowLock(ctx, "0xBuyer", "10.00", "ref1")
	_ = sv.ReleaseEscrow(ctx, "0xBuyer", "0xSeller", "10.00", "ref1")

	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) < 1 {
		t.Fatalf("expected at least 1 persisted event, got %d", len(events))
	}
}

func TestPartialEscrowSettle_PersistSpend(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	_ = sv.EscrowLock(ctx, "0xBuyer", "10.00", "ref1")
	_ = sv.PartialEscrowSettle(ctx, "0xBuyer", "0xSeller", "7.00", "3.00", "ref1")

	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) < 1 {
		t.Fatalf("expected at least 1 persisted event, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Supervisor: WithRules option
// ---------------------------------------------------------------------------

func TestWithRulesOption(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock, WithRules(&VelocityRule{}, &NewAgentRule{}))
	ctx := context.Background()

	// Should still evaluate rules. Established tier defaults, $100 within velocity.
	err := sv.Hold(ctx, "0xAlice", "100.00", "ref1")
	if err != nil {
		t.Fatalf("hold should succeed with custom rules: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: WithLogger option
// ---------------------------------------------------------------------------

func TestWithLoggerOption(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock, WithLogger(testLogger()))
	ctx := context.Background()

	err := sv.Hold(ctx, "0xAlice", "100.00", "ref1")
	if err != nil {
		t.Fatalf("hold should succeed with custom logger: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: evaluateWithTier deny path logs denial to store
// ---------------------------------------------------------------------------

func TestEvaluateWithTier_DenialLogging(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)
	ctx := context.Background()

	// Exceed per-tx limit for new agents
	err := sv.Spend(ctx, "0xAgent", "6.00", "ref1")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}

	// Wait for async denial log
	time.Sleep(200 * time.Millisecond)

	denials := store.GetDenials()
	if len(denials) < 1 {
		t.Fatal("expected at least 1 denial logged")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: evaluateWithTier flag path (no denial log)
// ---------------------------------------------------------------------------

func TestEvaluateWithTier_FlagNoBlock(t *testing.T) {
	mock := &mockLedgerService{}
	// Use custom rules with flag-only rule
	sv := New(mock, WithRules(&alwaysFlagRule{}))
	ctx := context.Background()

	// Flag should not block
	err := sv.Spend(ctx, "0xAlice", "10.00", "ref1")
	if err != nil {
		t.Fatalf("flagged operation should succeed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: logDenialAsync handles panic
// ---------------------------------------------------------------------------

func TestLogDenialAsync_PanicRecovery(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	sv.denialStore = &panicDenialStore{}
	sv.denialSem = make(chan struct{}, 1)

	rec := &DenialRecord{
		AgentAddr:      "0xtest",
		RuleName:       "test",
		Amount:         big.NewInt(1000000),
		HourlyTotal:    new(big.Int),
		BaselineMean:   new(big.Int),
		BaselineStddev: new(big.Int),
	}
	// Should not panic
	sv.logDenialAsync(rec)
}

type panicDenialStore struct{}

func (s *panicDenialStore) LogDenial(_ context.Context, _ *DenialRecord) error {
	panic("store panic")
}
func (s *panicDenialStore) ListDenials(_ context.Context, _ time.Time, _ int) ([]*DenialRecord, error) {
	return nil, nil
}
func (s *panicDenialStore) SaveBaselineBatch(_ context.Context, _ []*AgentBaseline) error {
	return nil
}
func (s *panicDenialStore) GetAllBaselines(_ context.Context) ([]*AgentBaseline, error) {
	return nil, nil
}
func (s *panicDenialStore) AppendSpendEvent(_ context.Context, _ *SpendEventRecord) error {
	return nil
}
func (s *panicDenialStore) AppendSpendEventBatch(_ context.Context, _ []*SpendEventRecord) error {
	return nil
}
func (s *panicDenialStore) GetRecentSpendEvents(_ context.Context, _ time.Time) ([]*SpendEventRecord, error) {
	return nil, nil
}
func (s *panicDenialStore) GetAllAgentsWithEvents(_ context.Context, _ time.Time) ([]string, error) {
	return nil, nil
}
func (s *panicDenialStore) GetHourlyTotals(_ context.Context, _ string, _ time.Time) (map[time.Time]*big.Int, error) {
	return nil, nil
}
func (s *panicDenialStore) PruneOldEvents(_ context.Context, _ time.Time) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// MemoryBaselineStore: AppendSpendEvent single
// ---------------------------------------------------------------------------

func TestMemoryBaselineStore_AppendSpendEvent(t *testing.T) {
	store := NewMemoryBaselineStore()
	ctx := context.Background()

	err := store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xagent",
		Amount:    big.NewInt(1000000),
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("AppendSpendEvent failed: %v", err)
	}

	events := store.GetEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].ID == 0 {
		t.Fatal("expected non-zero ID")
	}
}

// ---------------------------------------------------------------------------
// MemoryBaselineStore: GetRecentSpendEvents filters by time
// ---------------------------------------------------------------------------

func TestMemoryBaselineStore_GetRecentSpendEvents(t *testing.T) {
	store := NewMemoryBaselineStore()
	ctx := context.Background()
	now := time.Now()

	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xa", Amount: big.NewInt(100), CreatedAt: now.Add(-2 * time.Hour),
	})
	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xa", Amount: big.NewInt(200), CreatedAt: now.Add(-30 * time.Minute),
	})

	events, err := store.GetRecentSpendEvents(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("GetRecentSpendEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 recent event, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// MemoryBaselineStore: GetAllAgentsWithEvents
// ---------------------------------------------------------------------------

func TestMemoryBaselineStore_GetAllAgentsWithEvents(t *testing.T) {
	store := NewMemoryBaselineStore()
	ctx := context.Background()
	now := time.Now()

	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xa", Amount: big.NewInt(100), CreatedAt: now,
	})
	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xb", Amount: big.NewInt(200), CreatedAt: now,
	})
	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xa", Amount: big.NewInt(300), CreatedAt: now,
	})

	agents, err := store.GetAllAgentsWithEvents(ctx, now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("GetAllAgentsWithEvents: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

// ---------------------------------------------------------------------------
// MemoryBaselineStore: GetHourlyTotals
// ---------------------------------------------------------------------------

func TestMemoryBaselineStore_GetHourlyTotals(t *testing.T) {
	store := NewMemoryBaselineStore()
	ctx := context.Background()

	// Use a fixed time that won't cross an hour boundary when adding 10 minutes
	base := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)

	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xagent", Amount: big.NewInt(1000000), CreatedAt: base,
	})
	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xagent", Amount: big.NewInt(2000000), CreatedAt: base.Add(10 * time.Minute),
	})
	_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
		AgentAddr: "0xother", Amount: big.NewInt(5000000), CreatedAt: base,
	})

	totals, err := store.GetHourlyTotals(ctx, "0xagent", base.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("GetHourlyTotals: %v", err)
	}
	if len(totals) != 1 {
		t.Fatalf("expected 1 hourly bucket, got %d", len(totals))
	}
	for _, v := range totals {
		if v.Int64() != 3000000 {
			t.Fatalf("expected total 3000000, got %d", v.Int64())
		}
	}
}

// ---------------------------------------------------------------------------
// MemoryBaselineStore: SaveBaselineBatch and GetAllBaselines
// ---------------------------------------------------------------------------

func TestMemoryBaselineStore_SaveAndGetAllBaselines(t *testing.T) {
	store := NewMemoryBaselineStore()
	ctx := context.Background()

	_ = store.SaveBaselineBatch(ctx, []*AgentBaseline{
		{AgentAddr: "0xa", HourlyMean: big.NewInt(100), HourlyStddev: big.NewInt(10), SampleHours: 48},
		{AgentAddr: "0xb", HourlyMean: big.NewInt(200), HourlyStddev: big.NewInt(20), SampleHours: 48},
	})

	baselines, err := store.GetAllBaselines(ctx)
	if err != nil {
		t.Fatalf("GetAllBaselines: %v", err)
	}
	if len(baselines) != 2 {
		t.Fatalf("expected 2 baselines, got %d", len(baselines))
	}
}

// ---------------------------------------------------------------------------
// VelocityRule: no history returns nil
// ---------------------------------------------------------------------------

func TestVelocityRule_NoHistory(t *testing.T) {
	rule := &VelocityRule{}
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xnew",
		Amount:    big.NewInt(1000000),
		OpType:    "spend",
		Tier:      "established",
	}

	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil {
		t.Fatal("expected nil verdict for no history")
	}
}

// ---------------------------------------------------------------------------
// CounterpartyConcentrationRule: no edge returns nil
// ---------------------------------------------------------------------------

func TestCounterpartyConcentrationRule_NoEdge(t *testing.T) {
	rule := &CounterpartyConcentrationRule{}
	graph := NewSpendGraph()
	now := time.Now()

	// Agent has history but no edge with the counterparty
	graph.RecordEvent("0xalice", "0xbob", big.NewInt(1000000), now)

	ec := &EvalContext{
		AgentAddr:    "0xalice",
		Counterparty: "0xcharlie",
		Amount:       big.NewInt(1000000),
		Tier:         "established",
	}

	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil {
		t.Fatal("expected nil verdict for no edge with counterparty")
	}
}

// ---------------------------------------------------------------------------
// BaselineRule: nil provider returns nil
// ---------------------------------------------------------------------------

func TestBaselineRule_NilProvider(t *testing.T) {
	rule := &BaselineRule{provider: nil}
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xalice",
		Amount:    big.NewInt(1000000),
		OpType:    "spend",
		Tier:      "established",
	}

	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil {
		t.Fatal("expected nil for nil provider")
	}
}

// ---------------------------------------------------------------------------
// BaselineRule: no snap (agent has baseline but no graph data)
// ---------------------------------------------------------------------------

func TestBaselineRule_NoGraphData(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))

	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xnograph": {
			AgentAddr:    "0xnograph",
			HourlyMean:   big.NewInt(100_000000),
			HourlyStddev: big.NewInt(10_000000),
			SampleHours:  48,
		},
	})

	rule := &BaselineRule{provider: sv}
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xnograph",
		Amount:    big.NewInt(1_000000),
		OpType:    "spend",
		Tier:      "established",
	}

	// No graph data means currentHourly=0, projected=$1, which should be within baseline
	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil && verdict.Action == Deny {
		t.Fatal("should not deny $1 with $100 mean baseline")
	}
}

// ---------------------------------------------------------------------------
// DefaultRules returns correct rules
// ---------------------------------------------------------------------------

func TestDefaultRules(t *testing.T) {
	rules := DefaultRules()
	if len(rules) != 4 {
		t.Fatalf("expected 4 default rules, got %d", len(rules))
	}
	names := make(map[string]bool)
	for _, r := range rules {
		names[r.Name()] = true
	}
	for _, expected := range []string{"velocity", "new_agent_limit", "circular_flow", "counterparty_concentration"} {
		if !names[expected] {
			t.Errorf("missing rule %q in defaults", expected)
		}
	}
}

// ---------------------------------------------------------------------------
// RuleEngine: first deny wins (multiple rules)
// ---------------------------------------------------------------------------

func TestRuleEngine_FirstDenyWins(t *testing.T) {
	engine := NewRuleEngine(&alwaysDenyRule{name: "first"}, &alwaysDenyRule{name: "second"})
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1000000),
		OpType:    "spend",
		Tier:      "established",
	}

	verdict := engine.Evaluate(context.Background(), graph, ec)
	if verdict.Action != Deny {
		t.Fatalf("expected Deny")
	}
	if verdict.Rule != "first" {
		t.Fatalf("expected first deny rule to win, got %s", verdict.Rule)
	}
}

type alwaysDenyRule struct{ name string }

func (r *alwaysDenyRule) Name() string { return r.name }
func (r *alwaysDenyRule) Evaluate(_ context.Context, _ *SpendGraph, _ *EvalContext) *Verdict {
	return &Verdict{Action: Deny, Rule: r.name, Reason: "always deny"}
}

// ---------------------------------------------------------------------------
// RuleEngine: rule returning nil is skipped
// ---------------------------------------------------------------------------

type nilRule struct{}

func (r *nilRule) Name() string { return "nil_rule" }
func (r *nilRule) Evaluate(_ context.Context, _ *SpendGraph, _ *EvalContext) *Verdict {
	return nil
}

func TestRuleEngine_NilRuleSkipped(t *testing.T) {
	engine := NewRuleEngine(&nilRule{}, &alwaysFlagRule{})
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1000000),
		OpType:    "spend",
		Tier:      "established",
	}

	verdict := engine.Evaluate(context.Background(), graph, ec)
	if verdict.Action != Flag {
		t.Fatalf("expected Flag (nil rule skipped), got %d", verdict.Action)
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: concurrent edge-only and record operations
// ---------------------------------------------------------------------------

func TestSpendGraph_ConcurrentEdgeAndRecord(t *testing.T) {
	graph := NewSpendGraph()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			graph.RecordEvent("0xagent", "0xcp", big.NewInt(100), time.Now())
		}()
		go func() {
			defer wg.Done()
			graph.RecordEdgeOnly("0xagent", "0xcp", big.NewInt(50), time.Now())
		}()
	}
	wg.Wait()

	snap := graph.GetNode("0xagent")
	if snap == nil {
		t.Fatal("expected node after concurrent operations")
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: HasCyclicFlow with isolated node
// ---------------------------------------------------------------------------

func TestSpendGraph_HasCyclicFlowIsolatedNode(t *testing.T) {
	graph := NewSpendGraph()
	graph.RecordEvent("0xisolated", "", big.NewInt(100), time.Now())

	cycle := graph.HasCyclicFlow("0xisolated", time.Hour)
	if cycle != nil {
		t.Fatalf("expected no cycle for isolated node, got %v", cycle)
	}
}

// ---------------------------------------------------------------------------
// SpendGraph: HasCyclicFlow with unknown start
// ---------------------------------------------------------------------------

func TestSpendGraph_HasCyclicFlowUnknownStart(t *testing.T) {
	graph := NewSpendGraph()
	cycle := graph.HasCyclicFlow("0xunknown", time.Hour)
	if cycle != nil {
		t.Fatalf("expected no cycle for unknown start, got %v", cycle)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Hold concurrency limit from mixed holds+escrows
// ---------------------------------------------------------------------------

func TestHold_ConcurrencyLimitWithEscrows(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // limit = 3
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// 2 escrows
	_ = sv.EscrowLock(ctx, "0xAlice", "1.00", "e1")
	_ = sv.EscrowLock(ctx, "0xAlice", "1.00", "e2")
	// 1 hold fills up to 3
	_ = sv.Hold(ctx, "0xAlice", "1.00", "h1")

	// 4th (hold) should be denied
	err := sv.Hold(ctx, "0xAlice", "1.00", "h2")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: SettleHold tracks edge for baseline
// ---------------------------------------------------------------------------

func TestSettleHold_TracksEdge(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	_ = sv.Hold(ctx, "0xBuyer", "10.00", "ref1")
	_ = sv.SettleHold(ctx, "0xBuyer", "0xSeller", "10.00", "ref1")

	edge := sv.graph.GetEdge("0xbuyer", "0xseller")
	if edge == nil {
		t.Fatal("expected edge after settle hold")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Withdraw persist spend path
// ---------------------------------------------------------------------------

func TestWithdraw_PersistSpend(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	_ = sv.Withdraw(ctx, "0xAlice", "10.00", "0xTxHash")

	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 persisted event, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Supervisor: record with no counterparty doesn't create edge
// ---------------------------------------------------------------------------

func TestRecord_NoCounterparty(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)

	sv.record("0xAlice", "", "10.00")

	snap := sv.graph.GetNode("0xalice")
	if snap == nil {
		t.Fatal("expected node for alice")
	}

	// No edge should exist
	if sv.graph.GetEdge("0xalice", "") != nil {
		t.Fatal("expected no edge for empty counterparty")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: recordEdge with no counterparty is no-op
// ---------------------------------------------------------------------------

func TestRecordEdge_NoCounterparty(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)

	sv.recordEdge("0xAlice", "", "10.00")
	// RecordEdgeOnly with empty counterparty is a no-op
	if sv.graph.GetNode("0xalice") != nil {
		t.Fatal("expected no node for edge-only with empty counterparty")
	}
}

// ---------------------------------------------------------------------------
// computeMeanStddev: uniform values (stddev = 0)
// ---------------------------------------------------------------------------

func TestComputeMeanStddev_Uniform(t *testing.T) {
	totals := map[time.Time]*big.Int{
		time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC): big.NewInt(1000000),
		time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC): big.NewInt(1000000),
		time.Date(2024, 1, 1, 2, 0, 0, 0, time.UTC): big.NewInt(1000000),
	}

	mean, stddev := computeMeanStddev(totals)
	if mean.Int64() != 1000000 {
		t.Errorf("expected mean 1000000, got %d", mean.Int64())
	}
	if stddev.Int64() != 0 {
		t.Errorf("expected stddev 0, got %d", stddev.Int64())
	}
}

// ---------------------------------------------------------------------------
// mustParse helper
// ---------------------------------------------------------------------------

func TestMustParse(t *testing.T) {
	result := mustParse("100")
	expected := big.NewInt(100_000000)
	if result.Cmp(expected) != 0 {
		t.Fatalf("expected %d, got %d", expected.Int64(), result.Int64())
	}
}

func TestMustParse_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid input")
		}
	}()
	mustParse("not_a_number")
}
