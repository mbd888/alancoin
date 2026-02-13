package supervisor

import (
	"context"
	"errors"
	"fmt"
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
	rep := &mockReputation{tier: "new"} // $50/hr limit, $5/tx limit
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// Each hold is $5.00 (at the per-tx limit). 10 holds = $50 (at velocity limit).
	// We release each hold after to avoid concurrency limit (max 3 for new).
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
	rep := &mockReputation{tier: "trusted"} // $25,000/hr limit
	sv := New(mock, WithReputation(rep))
	ctx := context.Background()

	// $500 hold — well within trusted limits
	err := sv.Hold(ctx, "0xAlice", "500.00", "ref1")
	if err != nil {
		t.Fatalf("$500 hold for trusted agent should succeed: %v", err)
	}

	// Trusted also has 50 concurrent hold limit — should not hit it with 1
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
