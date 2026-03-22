package supervisor

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Supervisor: settlement operations (SettleHold, SettleHoldWithFee, etc.)
// ---------------------------------------------------------------------------

func TestSettleHold_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	// Create a hold first
	_ = sv.Hold(ctx, "0xBuyer", "10.00", "ref1")

	// Settle it
	err := sv.SettleHold(ctx, "0xBuyer", "0xSeller", "10.00", "ref1")
	if err != nil {
		t.Fatalf("SettleHold should succeed: %v", err)
	}

	calls := mock.getCalls()
	expected := []string{"Hold", "SettleHold"}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(calls), calls)
	}

	// After settle, active holds should be back to 0
	snap := sv.graph.GetNode("0xbuyer")
	if snap != nil && snap.ActiveHolds != 0 {
		t.Errorf("expected 0 active holds after settle, got %d", snap.ActiveHolds)
	}
}

func TestSettleHold_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.SettleHold(ctx, "0xBuyer", "0xSeller", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestSettleHoldWithFee_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	// Create a hold
	_ = sv.Hold(ctx, "0xBuyer", "10.00", "ref1")

	// Settle with fee
	err := sv.SettleHoldWithFee(ctx, "0xBuyer", "0xSeller", "9.00", "0xPlatform", "1.00", "ref1")
	if err != nil {
		t.Fatalf("SettleHoldWithFee should succeed: %v", err)
	}

	calls := mock.getCalls()
	if calls[1] != "SettleHoldWithFee" {
		t.Fatalf("expected SettleHoldWithFee call, got %s", calls[1])
	}
}

func TestSettleHoldWithFee_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.SettleHoldWithFee(ctx, "0xBuyer", "0xSeller", "9.00", "0xPlatform", "1.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestSettleHoldWithFee_InvalidFee(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	_ = sv.Hold(ctx, "0xBuyer", "10.00", "ref1")

	// Use invalid fee amount — the settle still succeeds, just the persistSpend
	// uses sellerAmount as fallback
	err := sv.SettleHoldWithFee(ctx, "0xBuyer", "0xSeller", "9.00", "0xPlatform", "invalid", "ref1")
	if err != nil {
		t.Fatalf("should succeed (invalid fee only affects baseline): %v", err)
	}
}

func TestReleaseEscrow_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	_ = sv.EscrowLock(ctx, "0xBuyer", "10.00", "ref1")

	err := sv.ReleaseEscrow(ctx, "0xBuyer", "0xSeller", "10.00", "ref1")
	if err != nil {
		t.Fatalf("ReleaseEscrow should succeed: %v", err)
	}

	snap := sv.graph.GetNode("0xbuyer")
	if snap != nil && snap.ActiveEscrows != 0 {
		t.Errorf("expected 0 active escrows after release, got %d", snap.ActiveEscrows)
	}
}

func TestReleaseEscrow_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.ReleaseEscrow(ctx, "0xBuyer", "0xSeller", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestPartialEscrowSettle_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	_ = sv.EscrowLock(ctx, "0xBuyer", "10.00", "ref1")

	err := sv.PartialEscrowSettle(ctx, "0xBuyer", "0xSeller", "7.00", "3.00", "ref1")
	if err != nil {
		t.Fatalf("PartialEscrowSettle should succeed: %v", err)
	}

	calls := mock.getCalls()
	found := false
	for _, c := range calls {
		if c == "PartialEscrowSettle" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected PartialEscrowSettle call in %v", calls)
	}
}

func TestPartialEscrowSettle_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.PartialEscrowSettle(ctx, "0xBuyer", "0xSeller", "7.00", "3.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestRefundEscrow_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	_ = sv.EscrowLock(ctx, "0xBuyer", "10.00", "ref1")

	err := sv.RefundEscrow(ctx, "0xBuyer", "10.00", "ref1")
	if err != nil {
		t.Fatalf("RefundEscrow should succeed: %v", err)
	}

	snap := sv.graph.GetNode("0xbuyer")
	if snap != nil && snap.ActiveEscrows != 0 {
		t.Errorf("expected 0 active escrows after refund, got %d", snap.ActiveEscrows)
	}
}

func TestRefundEscrow_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.RefundEscrow(ctx, "0xBuyer", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: evaluated operations error paths
// ---------------------------------------------------------------------------

func TestSpend_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Spend(ctx, "0xAlice", "10.00", "ref1")
	if err != nil {
		t.Fatalf("Spend should succeed: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "Spend" {
		t.Fatalf("expected [Spend], got %v", calls)
	}
}

func TestSpend_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Spend(ctx, "0xAlice", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestSpend_EvalDenied(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"}
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// $6 exceeds new agent per-tx limit of $5
	err := sv.Spend(ctx, "0xAlice", "6.00", "ref1")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
}

func TestTransfer_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Transfer(ctx, "0xAlice", "0xBob", "10.00", "ref1")
	if err != nil {
		t.Fatalf("Transfer should succeed: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "Transfer" {
		t.Fatalf("expected [Transfer], got %v", calls)
	}
}

func TestTransfer_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Transfer(ctx, "0xAlice", "0xBob", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestWithdraw_Success(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Withdraw(ctx, "0xAlice", "10.00", "0xTxHash")
	if err != nil {
		t.Fatalf("Withdraw should succeed: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "Withdraw" {
		t.Fatalf("expected [Withdraw], got %v", calls)
	}
}

func TestWithdraw_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Withdraw(ctx, "0xAlice", "10.00", "0xTxHash")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestWithdraw_EvalDenied(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"}
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// $6 exceeds new agent per-tx limit of $5
	err := sv.Withdraw(ctx, "0xAlice", "6.00", "0xTxHash")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Hold inner error rollback
// ---------------------------------------------------------------------------

func TestHold_InnerError_RollsBackCounter(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Hold(ctx, "0xAlice", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}

	// Counter should be rolled back
	snap := sv.graph.GetNode("0xalice")
	if snap != nil && snap.ActiveHolds != 0 {
		t.Errorf("hold counter should be rolled back to 0, got %d", snap.ActiveHolds)
	}
}

func TestEscrowLock_InnerError_RollsBackCounter(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.EscrowLock(ctx, "0xAlice", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}

	// Counter should be rolled back
	snap := sv.graph.GetNode("0xalice")
	if snap != nil && snap.ActiveEscrows != 0 {
		t.Errorf("escrow counter should be rolled back to 0, got %d", snap.ActiveEscrows)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: evaluate with invalid amount
// ---------------------------------------------------------------------------

func TestEvaluate_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Hold(ctx, "0xAlice", "not_a_number", "ref1")
	if err == nil {
		t.Fatal("expected error for invalid amount")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: options and wiring
// ---------------------------------------------------------------------------

func TestSetReputation(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)

	// Initially no reputation provider
	tier := sv.getTier(context.Background(), "0xAlice")
	if tier != "established" {
		t.Fatalf("expected 'established' without reputation, got %s", tier)
	}

	// Wire a reputation provider
	rep := &mockReputation{tier: "trusted"}
	sv.SetReputation(rep)

	tier = sv.getTier(context.Background(), "0xAlice")
	if tier != "trusted" {
		t.Fatalf("expected 'trusted' after SetReputation, got %s", tier)
	}
}

func TestGetTier_EmptyTier(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: ""}
	sv := New(mock, WithReputation(rep))

	tier := sv.getTier(context.Background(), "0xAlice")
	if tier != "established" {
		t.Fatalf("expected 'established' for empty tier, got %s", tier)
	}
}

func TestConcurrencyLimitForTier(t *testing.T) {
	tests := []struct {
		tier     string
		expected int
	}{
		{"new", 3},
		{"emerging", 10},
		{"established", 25},
		{"trusted", 50},
		{"elite", 100},
		{"unknown", 25}, // defaults to established
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			limit := concurrencyLimitForTier(tt.tier)
			if limit != tt.expected {
				t.Errorf("tier %s: expected %d, got %d", tt.tier, tt.expected, limit)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Supervisor: baseline cache operations
// ---------------------------------------------------------------------------

func TestGetCachedBaseline_NilCache(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock) // no baseline store — cache is nil

	b := sv.GetCachedBaseline("0xAlice")
	if b != nil {
		t.Fatal("expected nil for nil cache")
	}
}

func TestGetCachedBaseline_CaseInsensitive(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))

	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xalice": {AgentAddr: "0xalice", HourlyMean: big.NewInt(100)},
	})

	b := sv.GetCachedBaseline("0xALICE")
	if b == nil {
		t.Fatal("expected baseline for case-insensitive lookup")
	}
}

func TestRefreshBaselines_MergesIntoExisting(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))

	// First refresh
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xa": {AgentAddr: "0xa", HourlyMean: big.NewInt(100)},
	})
	// Second refresh — should merge, not replace
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xb": {AgentAddr: "0xb", HourlyMean: big.NewInt(200)},
	})

	if sv.GetCachedBaseline("0xa") == nil {
		t.Fatal("first agent should still be in cache")
	}
	if sv.GetCachedBaseline("0xb") == nil {
		t.Fatal("second agent should be in cache")
	}
}

func TestRefreshBaselines_NilCacheCreatesOne(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock) // no WithBaselineStore

	// This should not panic even when baselineCache is nil
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xa": {AgentAddr: "0xa", HourlyMean: big.NewInt(100)},
	})

	b := sv.GetCachedBaseline("0xa")
	if b == nil {
		t.Fatal("baseline should exist after RefreshBaselines on nil cache")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: persistSpend with event writer
// ---------------------------------------------------------------------------

func TestPersistSpend_WithEventWriter(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	// Spend should persist via event writer
	_ = sv.Spend(ctx, "0xAlice", "10.00", "ref1")

	// Wait for flush
	time.Sleep(700 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)

	events := store.GetEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 persisted event, got %d", len(events))
	}
}

func TestPersistSpend_NilEventWriter(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)

	// Should not panic without event writer
	sv.persistSpend("0xAlice", "0xBob", "10.00")
}

func TestPersistSpend_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))
	w := NewEventWriter(store, testLogger())
	sv.SetEventWriter(w)

	// Invalid amount should be silently ignored
	sv.persistSpend("0xAlice", "0xBob", "not_a_number")
}

// ---------------------------------------------------------------------------
// Supervisor: EscrowLock velocity deny
// ---------------------------------------------------------------------------

func TestEscrowLock_VelocityDeny(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // $50/hr limit
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Fill up velocity: 10 * $5 = $50
	for i := 0; i < 10; i++ {
		err := sv.EscrowLock(ctx, "0xAlice", "5.00", fmt.Sprintf("ref%d", i))
		if err != nil {
			t.Fatalf("escrow %d should succeed: %v", i, err)
		}
		_ = sv.RefundEscrow(ctx, "0xAlice", "5.00", fmt.Sprintf("ref%d", i))
	}

	// 11th should be denied by velocity
	err := sv.EscrowLock(ctx, "0xAlice", "5.00", "ref10")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Supervisor: Deposit passthrough (no evaluation)
// ---------------------------------------------------------------------------

func TestDeposit_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.Deposit(ctx, "0xAlice", "1000.00", "0xTxHash")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: ConfirmHold/ReleaseHold inner errors
// ---------------------------------------------------------------------------

func TestConfirmHold_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.ConfirmHold(ctx, "0xAlice", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

func TestReleaseHold_InnerError(t *testing.T) {
	mock := &mockLedgerService{err: errors.New("ledger error")}
	sv := New(mock)
	ctx := context.Background()

	err := sv.ReleaseHold(ctx, "0xAlice", "10.00", "ref1")
	if err == nil {
		t.Fatal("expected error from inner service")
	}
}

// ---------------------------------------------------------------------------
// Supervisor: record/recordEdge with invalid amounts
// ---------------------------------------------------------------------------

func TestRecord_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)

	// Should not panic on invalid amount
	sv.record("0xAlice", "0xBob", "invalid")

	// No node should be created
	if sv.graph.GetNode("0xalice") != nil {
		t.Fatal("no node should be created for invalid amount")
	}
}

func TestRecordEdge_InvalidAmount(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)

	// Should not panic on invalid amount
	sv.recordEdge("0xAlice", "0xBob", "invalid")
}

// ---------------------------------------------------------------------------
// RuleEngine: edge cases
// ---------------------------------------------------------------------------

func TestRuleEngine_AllRulesAllow(t *testing.T) {
	engine := NewRuleEngine() // no rules
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1000000),
		OpType:    "spend",
		Tier:      "established",
	}

	verdict := engine.Evaluate(context.Background(), graph, ec)
	if verdict.Action != Allow {
		t.Fatalf("expected Allow, got %d", verdict.Action)
	}
}

func TestRuleEngine_FlagDoesNotBlock(t *testing.T) {
	// Create a custom rule that always flags
	engine := NewRuleEngine(&alwaysFlagRule{})
	graph := NewSpendGraph()
	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(1000000),
		OpType:    "spend",
		Tier:      "established",
	}

	verdict := engine.Evaluate(context.Background(), graph, ec)
	if verdict.Action != Flag {
		t.Fatalf("expected Flag, got %d", verdict.Action)
	}
}

type alwaysFlagRule struct{}

func (r *alwaysFlagRule) Name() string { return "always_flag" }
func (r *alwaysFlagRule) Evaluate(_ context.Context, _ *SpendGraph, _ *EvalContext) *Verdict {
	return &Verdict{Action: Flag, Rule: r.Name(), Reason: "test flag"}
}

// ---------------------------------------------------------------------------
// VelocityRule: unknown tier defaults to established
// ---------------------------------------------------------------------------

func TestVelocityRule_UnknownTier(t *testing.T) {
	graph := NewSpendGraph()
	now := time.Now()
	graph.RecordEvent("0xalice", "", big.NewInt(4999_000000), now)

	rule := &VelocityRule{}
	ec := &EvalContext{
		AgentAddr: "0xalice",
		Amount:    big.NewInt(1_000000),
		OpType:    "spend",
		Tier:      "unknown_tier",
	}

	// Should use established limit ($5000/hr), so $4999 + $1 = $5000 is at the limit
	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil && verdict.Action == Deny {
		t.Fatal("$5000 should not be denied for established (default) tier")
	}
}

// ---------------------------------------------------------------------------
// NewAgentRule: non-new tier is ignored
// ---------------------------------------------------------------------------

func TestNewAgentRule_NonNewTier(t *testing.T) {
	rule := &NewAgentRule{}
	ec := &EvalContext{
		AgentAddr: "0xAlice",
		Amount:    big.NewInt(100_000000), // $100 — way above $5 limit
		OpType:    "hold",
		Tier:      "trusted",
	}

	verdict := rule.Evaluate(context.Background(), nil, ec)
	if verdict != nil {
		t.Fatal("NewAgentRule should return nil for non-new tier")
	}
}

// ---------------------------------------------------------------------------
// CircularFlowRule: no counterparty returns nil
// ---------------------------------------------------------------------------

func TestCircularFlowRule_NoCounterparty(t *testing.T) {
	rule := &CircularFlowRule{}
	ec := &EvalContext{
		AgentAddr:    "0xAlice",
		Counterparty: "",
		Amount:       big.NewInt(1000000),
		OpType:       "spend",
		Tier:         "established",
	}

	verdict := rule.Evaluate(context.Background(), NewSpendGraph(), ec)
	if verdict != nil {
		t.Fatal("CircularFlowRule should return nil when no counterparty")
	}
}

// ---------------------------------------------------------------------------
// CounterpartyConcentrationRule: edge cases
// ---------------------------------------------------------------------------

func TestCounterpartyConcentrationRule_NoHistory(t *testing.T) {
	rule := &CounterpartyConcentrationRule{}
	ec := &EvalContext{
		AgentAddr:    "0xAlice",
		Counterparty: "0xBob",
		Amount:       big.NewInt(1000000),
		Tier:         "established",
	}

	verdict := rule.Evaluate(context.Background(), NewSpendGraph(), ec)
	if verdict != nil {
		t.Fatal("should return nil for no history")
	}
}

func TestCounterpartyConcentrationRule_NoCounterparty(t *testing.T) {
	rule := &CounterpartyConcentrationRule{}
	graph := NewSpendGraph()
	graph.RecordEvent("0xalice", "0xbob", big.NewInt(1000000), time.Now())

	ec := &EvalContext{
		AgentAddr:    "0xalice",
		Counterparty: "",
		Amount:       big.NewInt(1000000),
		Tier:         "established",
	}

	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil {
		t.Fatal("should return nil when no counterparty")
	}
}

func TestCounterpartyConcentrationRule_LowConcentration(t *testing.T) {
	rule := &CounterpartyConcentrationRule{}
	graph := NewSpendGraph()
	now := time.Now()

	// Split evenly between 5 counterparties — 20% each
	for i := 0; i < 5; i++ {
		graph.RecordEvent("0xalice", fmt.Sprintf("0x%d", i), big.NewInt(1000000), now)
	}

	ec := &EvalContext{
		AgentAddr:    "0xalice",
		Counterparty: "0x0",
		Amount:       big.NewInt(1000000),
		Tier:         "established",
	}

	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil && verdict.Action == Flag {
		t.Fatal("20% concentration should not be flagged")
	}
}

// ---------------------------------------------------------------------------
// EventWriter: Stop and Running
// ---------------------------------------------------------------------------

func TestEventWriter_StopAndRunning(t *testing.T) {
	store := NewMemoryBaselineStore()
	w := NewEventWriter(store, testLogger())

	if w.Running() {
		t.Fatal("should not be running before Start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go w.Start(ctx)

	// Give time to start
	time.Sleep(50 * time.Millisecond)
	if !w.Running() {
		t.Fatal("should be running after Start")
	}

	w.Stop()
	time.Sleep(100 * time.Millisecond)
	if w.Running() {
		t.Fatal("should not be running after Stop")
	}
	cancel()
}

// ---------------------------------------------------------------------------
// BaselineTimer: compute with agents
// ---------------------------------------------------------------------------

func TestBaselineTimer_Compute(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))
	ctx := context.Background()

	// Seed events for agent across 25 different hours (meets min 24 sample hours)
	now := time.Now()
	for i := 0; i < 25; i++ {
		_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
			AgentAddr:    "0xcomputer",
			Counterparty: "0xseller",
			Amount:       big.NewInt(10_000000), // $10
			CreatedAt:    now.Add(-time.Duration(i) * time.Hour),
		})
	}

	timer := NewBaselineTimer(store, sv, testLogger())
	timer.compute(ctx)

	// Baseline should now be computed and cached
	b := sv.GetCachedBaseline("0xcomputer")
	if b == nil {
		t.Fatal("expected baseline to be computed")
	}
	if b.SampleHours < 24 {
		t.Fatalf("expected at least 24 sample hours, got %d", b.SampleHours)
	}
	// Mean should be ~$10 (all events are $10/hr)
	if b.HourlyMean.Int64() < 9_000000 || b.HourlyMean.Int64() > 11_000000 {
		t.Fatalf("expected mean ~10000000, got %d", b.HourlyMean.Int64())
	}
}

func TestBaselineTimer_ComputeInsufficientSamples(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))
	ctx := context.Background()

	// Only 5 events in 5 different hours — below minimum 24
	now := time.Now()
	for i := 0; i < 5; i++ {
		_ = store.AppendSpendEvent(ctx, &SpendEventRecord{
			AgentAddr: "0xfew",
			Amount:    big.NewInt(10_000000),
			CreatedAt: now.Add(-time.Duration(i) * time.Hour),
		})
	}

	timer := NewBaselineTimer(store, sv, testLogger())
	timer.compute(ctx)

	// Should not compute baseline for insufficient samples
	b := sv.GetCachedBaseline("0xfew")
	if b != nil {
		t.Fatal("should not compute baseline with fewer than 24 sample hours")
	}
}

func TestBaselineTimer_Running(t *testing.T) {
	store := NewMemoryBaselineStore()
	mock := &mockLedgerService{}
	sv := New(mock, WithBaselineStore(store))

	timer := NewBaselineTimer(store, sv, testLogger())
	if timer.Running() {
		t.Fatal("should not be running before Start")
	}
}

// ---------------------------------------------------------------------------
// MemoryBaselineStore: ListDenials
// ---------------------------------------------------------------------------

func TestMemoryBaselineStore_ListDenials(t *testing.T) {
	store := NewMemoryBaselineStore()
	ctx := context.Background()
	now := time.Now()

	// Log some denials at different times
	for i := 0; i < 5; i++ {
		_ = store.LogDenial(ctx, &DenialRecord{
			AgentAddr: fmt.Sprintf("0xagent%d", i),
			RuleName:  "velocity",
			Amount:    big.NewInt(1000000),
			CreatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}

	// List all
	denials, err := store.ListDenials(ctx, now.Add(-1*time.Hour), 10)
	if err != nil {
		t.Fatalf("ListDenials failed: %v", err)
	}
	if len(denials) != 5 {
		t.Fatalf("expected 5 denials, got %d", len(denials))
	}

	// List with limit
	denials, err = store.ListDenials(ctx, now.Add(-1*time.Hour), 2)
	if err != nil {
		t.Fatalf("ListDenials with limit failed: %v", err)
	}
	if len(denials) != 2 {
		t.Fatalf("expected 2 denials (limited), got %d", len(denials))
	}
}

// ---------------------------------------------------------------------------
// BuildDenialRecord
// ---------------------------------------------------------------------------

func TestBuildDenialRecord_WithBaselineAndGraph(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock,
		WithReputation(&mockReputation{tier: "new"}),
		WithBaselineStore(store),
	)
	ctx := context.Background()

	// Create graph data
	_ = sv.Hold(ctx, "0xAgent", "5.00", "ref")
	_ = sv.ReleaseHold(ctx, "0xAgent", "5.00", "ref")

	// Set baseline
	sv.RefreshBaselines(map[string]*AgentBaseline{
		"0xagent": {
			AgentAddr:    "0xagent",
			HourlyMean:   big.NewInt(10_000000),
			HourlyStddev: big.NewInt(2_000000),
			SampleHours:  48,
		},
	})

	verdict := &Verdict{
		Action: Deny,
		Rule:   "velocity",
		Reason: "test reason",
	}

	rec := sv.buildDenialRecord("0xAgent", "0xCounterparty", big.NewInt(5_000000), "hold", "new", verdict)

	if rec.AgentAddr != "0xAgent" {
		t.Errorf("expected agent 0xAgent, got %s", rec.AgentAddr)
	}
	if rec.RuleName != "velocity" {
		t.Errorf("expected rule velocity, got %s", rec.RuleName)
	}
	if rec.BaselineMean.Int64() != 10_000000 {
		t.Errorf("expected baseline mean 10000000, got %d", rec.BaselineMean.Int64())
	}
	if rec.HourlyTotal.Int64() == 0 {
		t.Error("expected non-zero hourly total from graph")
	}
}

func TestBuildDenialRecord_NoBaseline(t *testing.T) {
	mock := &mockLedgerService{}
	store := NewMemoryBaselineStore()
	sv := New(mock, WithBaselineStore(store))

	verdict := &Verdict{
		Action: Deny,
		Rule:   "velocity",
		Reason: "test reason",
	}

	rec := sv.buildDenialRecord("0xNew", "", big.NewInt(5_000000), "spend", "new", verdict)

	// BaselineMean and Stddev should be zero (no baseline)
	if rec.BaselineMean.Int64() != 0 {
		t.Errorf("expected 0 baseline mean, got %d", rec.BaselineMean.Int64())
	}
	// HourlyTotal should be zero (no graph data)
	if rec.HourlyTotal.Int64() != 0 {
		t.Errorf("expected 0 hourly total, got %d", rec.HourlyTotal.Int64())
	}
}
