package supervisor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/ledger"
)

// mockLedgerService records calls and returns configurable errors.
type mockLedgerService struct {
	mu    sync.Mutex
	calls []string
	err   error // if set, all money-moving ops return this
}

func (m *mockLedgerService) record(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, op)
}

func (m *mockLedgerService) getCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]string, len(m.calls))
	copy(cp, m.calls)
	return cp
}

func (m *mockLedgerService) Deposit(_ context.Context, _, _, _ string) error {
	m.record("Deposit")
	return m.err
}
func (m *mockLedgerService) Spend(_ context.Context, _, _, _ string) error {
	m.record("Spend")
	return m.err
}
func (m *mockLedgerService) Transfer(_ context.Context, _, _, _, _ string) error {
	m.record("Transfer")
	return m.err
}
func (m *mockLedgerService) Hold(_ context.Context, _, _, _ string) error {
	m.record("Hold")
	return m.err
}
func (m *mockLedgerService) ConfirmHold(_ context.Context, _, _, _ string) error {
	m.record("ConfirmHold")
	return m.err
}
func (m *mockLedgerService) ReleaseHold(_ context.Context, _, _, _ string) error {
	m.record("ReleaseHold")
	return m.err
}
func (m *mockLedgerService) SettleHold(_ context.Context, _, _, _, _ string) error {
	m.record("SettleHold")
	return m.err
}
func (m *mockLedgerService) SettleHoldWithFee(_ context.Context, _, _, _, _, _, _ string) error {
	m.record("SettleHoldWithFee")
	return m.err
}
func (m *mockLedgerService) SettleHoldWithCallback(_ context.Context, _, _, _, _ string, _ func(tx *sql.Tx) error) error {
	m.record("SettleHoldWithCallback")
	return m.err
}
func (m *mockLedgerService) SettleHoldWithFeeAndCallback(_ context.Context, _, _, _, _, _, _ string, _ func(tx *sql.Tx) error) error {
	m.record("SettleHoldWithFeeAndCallback")
	return m.err
}
func (m *mockLedgerService) EscrowLock(_ context.Context, _, _, _ string) error {
	m.record("EscrowLock")
	return m.err
}
func (m *mockLedgerService) ReleaseEscrow(_ context.Context, _, _, _, _ string) error {
	m.record("ReleaseEscrow")
	return m.err
}
func (m *mockLedgerService) RefundEscrow(_ context.Context, _, _, _ string) error {
	m.record("RefundEscrow")
	return m.err
}
func (m *mockLedgerService) PartialEscrowSettle(_ context.Context, _, _, _, _, _ string) error {
	m.record("PartialEscrowSettle")
	return m.err
}
func (m *mockLedgerService) Refund(_ context.Context, _, _, _ string) error {
	m.record("Refund")
	return m.err
}
func (m *mockLedgerService) Withdraw(_ context.Context, _, _, _ string) error {
	m.record("Withdraw")
	return m.err
}
func (m *mockLedgerService) GetBalance(_ context.Context, _ string) (*ledger.Balance, error) {
	m.record("GetBalance")
	return &ledger.Balance{Available: "100.000000"}, nil
}
func (m *mockLedgerService) CanSpend(_ context.Context, _, _ string) (bool, error) {
	m.record("CanSpend")
	return true, nil
}
func (m *mockLedgerService) GetHistory(_ context.Context, _ string, _ int) ([]*ledger.Entry, error) {
	m.record("GetHistory")
	return nil, nil
}

// mockReputation returns a fixed tier.
type mockReputation struct {
	tier string
	err  error
}

func (m *mockReputation) GetScore(_ context.Context, _ string) (float64, string, error) {
	return 50.0, m.tier, m.err
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPassthrough(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	// Read ops + refund delegate directly without evaluation
	_, _ = sv.GetBalance(ctx, "0xAlice")
	_, _ = sv.CanSpend(ctx, "0xAlice", "1.00")
	_, _ = sv.GetHistory(ctx, "0xAlice", 10)
	_ = sv.Refund(ctx, "0xAlice", "1.00", "ref1")

	calls := mock.getCalls()
	expected := []string{"GetBalance", "CanSpend", "GetHistory", "Refund"}
	if len(calls) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(calls), calls)
	}
	for i, c := range calls {
		if c != expected[i] {
			t.Errorf("call %d: expected %s, got %s", i, expected[i], c)
		}
	}
}

func TestVelocityDeny(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // $50/hr limit (geometric), $5/tx limit
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Each hold is $5.00 (at the per-tx limit). 10 holds = $50 (at velocity limit).
	// We release each hold after to avoid concurrency limit (3 for new).
	for i := 0; i < 10; i++ {
		err := sv.Hold(ctx, "0xAlice", "5.00", fmt.Sprintf("ref%d", i))
		if err != nil {
			t.Fatalf("hold %d should succeed: %v", i, err)
		}
		// Release to stay under concurrency limit
		_ = sv.ReleaseHold(ctx, "0xAlice", "5.00", fmt.Sprintf("ref%d", i))
	}

	// 11th hold: would push hourly to $55 > $50
	err := sv.Hold(ctx, "0xAlice", "5.00", "ref10")
	if err == nil {
		t.Fatal("11th hold should be denied by velocity rule")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got: %v", err)
	}
}

func TestConcurrencyDeny(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // 3 concurrent holds
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// 3 small holds should succeed
	for i := 0; i < 3; i++ {
		err := sv.Hold(ctx, "0xAlice", "1.00", fmt.Sprintf("ref%d", i))
		if err != nil {
			t.Fatalf("hold %d should succeed: %v", i, err)
		}
	}

	// 4th hold denied by atomic concurrency check
	err := sv.Hold(ctx, "0xAlice", "1.00", "ref3")
	if err == nil {
		t.Fatal("4th hold should be denied")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got: %v", err)
	}
}

func TestNewAgentPerTxLimit(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"}
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// $5 should work
	err := sv.Hold(ctx, "0xAlice", "5.00", "ref1")
	if err != nil {
		t.Fatalf("$5 hold should succeed: %v", err)
	}

	// $6 should be denied
	err = sv.Hold(ctx, "0xAlice", "6.00", "ref2")
	if err == nil {
		t.Fatal("$6 hold should be denied for new agent")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got: %v", err)
	}
}

func TestTierCalibration(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "trusted"} // geometric velocity limit (~$14,953/hr)
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// $500 hold — well within trusted limits
	err := sv.Hold(ctx, "0xAlice", "500.00", "ref1")
	if err != nil {
		t.Fatalf("$500 hold for trusted agent should succeed: %v", err)
	}

	// Trusted has geometric concurrent hold limit — should not hit it with 1
	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "Hold" {
		t.Fatalf("expected [Hold], got: %v", calls)
	}
}

func TestCircularFlowFlag(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "established"}
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Create A -> B -> C -> A cycle via transfers
	_ = sv.Transfer(ctx, "0xA", "0xB", "10.00", "ref1")
	_ = sv.Transfer(ctx, "0xB", "0xC", "10.00", "ref2")
	_ = sv.Transfer(ctx, "0xC", "0xA", "10.00", "ref3")

	// Next transfer from A should be flagged (cycle exists) but allowed
	err := sv.Transfer(ctx, "0xA", "0xB", "10.00", "ref4")
	if err != nil {
		t.Fatalf("flagged transfer should still succeed: %v", err)
	}

	// All 4 transfers should have gone through
	calls := mock.getCalls()
	if len(calls) != 4 {
		t.Fatalf("expected 4 Transfer calls, got %d: %v", len(calls), calls)
	}
}

func TestCounterpartyConcentrationFlag(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "established"}
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// 9 transfers to Bob, 1 to Carol — 90% concentration
	for i := 0; i < 9; i++ {
		_ = sv.Transfer(ctx, "0xAlice", "0xBob", "10.00", fmt.Sprintf("ref%d", i))
	}
	_ = sv.Transfer(ctx, "0xAlice", "0xCarol", "10.00", "refCarol")

	// Next transfer to Bob should be flagged but succeed
	err := sv.Transfer(ctx, "0xAlice", "0xBob", "10.00", "refFinal")
	if err != nil {
		t.Fatalf("flagged transfer should still succeed: %v", err)
	}
}

func TestReleaseDecrementsCounter(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // 3 concurrent holds
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Fill up 3 holds
	for i := 0; i < 3; i++ {
		err := sv.Hold(ctx, "0xAlice", "1.00", fmt.Sprintf("ref%d", i))
		if err != nil {
			t.Fatalf("hold %d should succeed: %v", i, err)
		}
	}

	// 4th denied
	err := sv.Hold(ctx, "0xAlice", "1.00", "ref3")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("4th hold should be denied, got: %v", err)
	}

	// Release one hold
	err = sv.ReleaseHold(ctx, "0xAlice", "1.00", "ref0")
	if err != nil {
		t.Fatalf("release should succeed: %v", err)
	}

	// Now 4th should succeed (only 2 active)
	err = sv.Hold(ctx, "0xAlice", "1.00", "ref3_retry")
	if err != nil {
		t.Fatalf("hold after release should succeed: %v", err)
	}
}

func TestGraphConcurrency(t *testing.T) {
	graph := NewSpendGraph()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			agent := fmt.Sprintf("0xagent%d", n%10)
			graph.RecordEvent(agent, "0xcounterparty", mustParse("1"), time.Now())
			graph.GetNode(agent)
			graph.TryAcquireHold(agent, 1000)
			graph.ReleaseActiveHold(agent)
		}(i)
	}

	wg.Wait()

	// Verify no panics and state is consistent
	for i := 0; i < 10; i++ {
		snap := graph.GetNode(fmt.Sprintf("0xagent%d", i))
		if snap == nil {
			t.Fatalf("agent %d should have a node", i)
		}
		if snap.ActiveHolds != 0 {
			t.Errorf("agent %d: expected 0 active holds, got %d", i, snap.ActiveHolds)
		}
	}
}

func TestNoReputationProvider(t *testing.T) {
	mock := &mockLedgerService{}
	// No reputation provider — defaults to "established"
	sv := New(mock)
	ctx := context.Background()

	// "established" tier has $5,000/hr velocity limit — $100 should be fine
	err := sv.Hold(ctx, "0xAlice", "100.00", "ref1")
	if err != nil {
		t.Fatalf("should succeed without reputation provider: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "Hold" {
		t.Fatalf("expected [Hold], got: %v", calls)
	}
}

func TestReputationProviderError(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "", err: errors.New("db down")}
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// On error, tier defaults to "new" — $5/tx limit applies
	err := sv.Hold(ctx, "0xAlice", "6.00", "ref1")
	if err == nil {
		t.Fatal("should be denied ($6 > $5 for new tier)")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got: %v", err)
	}
}

func TestDepositTracksOnly(t *testing.T) {
	mock := &mockLedgerService{}
	sv := New(mock)
	ctx := context.Background()

	// Deposit should go through without evaluation
	err := sv.Deposit(ctx, "0xAlice", "1000.00", "0xTxHash")
	if err != nil {
		t.Fatalf("deposit should succeed: %v", err)
	}

	calls := mock.getCalls()
	if len(calls) != 1 || calls[0] != "Deposit" {
		t.Fatalf("expected [Deposit], got: %v", calls)
	}
}

func TestConfirmHoldDecrementsCounter(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // 3 concurrent holds
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Fill 3 holds
	for i := 0; i < 3; i++ {
		_ = sv.Hold(ctx, "0xAlice", "1.00", fmt.Sprintf("ref%d", i))
	}

	// Confirm one
	_ = sv.ConfirmHold(ctx, "0xAlice", "1.00", "ref0")

	// Should now allow another
	err := sv.Hold(ctx, "0xAlice", "1.00", "ref3")
	if err != nil {
		t.Fatalf("hold after confirm should succeed: %v", err)
	}
}

func TestEscrowCounterManagement(t *testing.T) {
	mock := &mockLedgerService{}
	rep := &mockReputation{tier: "new"} // 3 concurrent
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Fill 3 escrows
	for i := 0; i < 3; i++ {
		_ = sv.EscrowLock(ctx, "0xAlice", "1.00", fmt.Sprintf("ref%d", i))
	}

	// 4th denied
	err := sv.EscrowLock(ctx, "0xAlice", "1.00", "ref3")
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("expected ErrDenied, got: %v", err)
	}

	// RefundEscrow decrements
	_ = sv.RefundEscrow(ctx, "0xAlice", "1.00", "ref0")

	// Now succeeds
	err = sv.EscrowLock(ctx, "0xAlice", "1.00", "ref3_retry")
	if err != nil {
		t.Fatalf("escrow after refund should succeed: %v", err)
	}
}

// --- merged from coverage_final_test.go ---

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

// --- merged from supervisor_extra_test.go ---

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
	// Geometric scaling: 3 × (100/3)^(t/4)
	// new=3, emerging≈7, established≈17, trusted≈42, elite=100
	tests := []struct {
		tier     string
		expected int
	}{
		{"new", 3},
		{"emerging", ConcurrencyLimitForTier("emerging")},
		{"established", ConcurrencyLimitForTier("established")},
		{"trusted", ConcurrencyLimitForTier("trusted")},
		{"elite", 100},
		{"unknown", ConcurrencyLimitForTier("established")}, // defaults to established
	}

	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			limit := concurrencyLimitForTier(tt.tier)
			if limit != tt.expected {
				t.Errorf("tier %s: expected %d, got %d", tt.tier, tt.expected, limit)
			}
		})
	}

	// Verify geometric properties: monotonically increasing, endpoints exact
	prev := 0
	for _, tier := range []string{"new", "emerging", "established", "trusted", "elite"} {
		limit := ConcurrencyLimitForTier(tier)
		if limit <= prev {
			t.Errorf("tier %s limit %d should be > previous %d", tier, limit, prev)
		}
		prev = limit
	}
	if ConcurrencyLimitForTier("new") != 3 {
		t.Errorf("new tier should be exactly 3")
	}
	if ConcurrencyLimitForTier("elite") != 100 {
		t.Errorf("elite tier should be exactly 100")
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
	// Unknown tier defaults to "established". Geometric limit for established
	// is ~$2,236/hr. Put total spend just under the limit.
	estLimit := VelocityLimitForTier("established")
	underLimit := new(big.Int).Sub(estLimit, big.NewInt(1_000000)) // limit - $1

	graph := NewSpendGraph()
	now := time.Now()
	graph.RecordEvent("0xalice", "", underLimit, now)

	rule := &VelocityRule{}
	ec := &EvalContext{
		AgentAddr: "0xalice",
		Amount:    big.NewInt(1_000000), // +$1 should be exactly at limit
		OpType:    "spend",
		Tier:      "unknown_tier",
	}

	// Should use established limit, so (limit - $1) + $1 = limit is at the limit
	verdict := rule.Evaluate(context.Background(), graph, ec)
	if verdict != nil && verdict.Action == Deny {
		t.Fatal("should not be denied at exactly the limit for established (default) tier")
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
