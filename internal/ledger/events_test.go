package ledger

import (
	"context"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// RebuildBalance Tests
// ---------------------------------------------------------------------------

func TestRebuildBalance_SingleAgent_DepositAndSpend(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "10.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "3.50"},
		{AgentAddr: agent, EventType: "deposit", Amount: "5.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "2.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "9.500000" {
		t.Errorf("Expected available 9.500000, got %s", balance.Available)
	}
	if balance.TotalIn != "15.000000" {
		t.Errorf("Expected totalIn 15.000000, got %s", balance.TotalIn)
	}
	if balance.TotalOut != "5.500000" {
		t.Errorf("Expected totalOut 5.500000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_HoldConfirmRelease(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "10.00"},
		{AgentAddr: agent, EventType: "hold", Amount: "3.00"},
		{AgentAddr: agent, EventType: "confirm_hold", Amount: "3.00"},
		{AgentAddr: agent, EventType: "hold", Amount: "2.00"},
		{AgentAddr: agent, EventType: "release_hold", Amount: "2.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "7.000000" {
		t.Errorf("Expected available 7.000000, got %s", balance.Available)
	}
	if balance.Pending != "0.000000" {
		t.Errorf("Expected pending 0.000000, got %s", balance.Pending)
	}
	if balance.TotalOut != "3.000000" {
		t.Errorf("Expected totalOut 3.000000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_EscrowLockReleaseRefund(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "20.00"},
		{AgentAddr: agent, EventType: "escrow_lock", Amount: "5.00"},
		{AgentAddr: agent, EventType: "escrow_release", Amount: "5.00"},
		{AgentAddr: agent, EventType: "escrow_lock", Amount: "3.00"},
		{AgentAddr: agent, EventType: "escrow_refund", Amount: "3.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "15.000000" {
		t.Errorf("Expected available 15.000000, got %s", balance.Available)
	}
	if balance.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", balance.Escrowed)
	}
	if balance.TotalOut != "5.000000" {
		t.Errorf("Expected totalOut 5.000000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_EscrowReceive(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "escrow_receive", Amount: "5.00"},
		{AgentAddr: agent, EventType: "escrow_receive", Amount: "3.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "8.000000" {
		t.Errorf("Expected available 8.000000, got %s", balance.Available)
	}
	if balance.TotalIn != "8.000000" {
		t.Errorf("Expected totalIn 8.000000, got %s", balance.TotalIn)
	}
}

func TestRebuildBalance_Withdrawal(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "100.00"},
		{AgentAddr: agent, EventType: "withdrawal", Amount: "30.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "10.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "60.000000" {
		t.Errorf("Expected available 60.000000, got %s", balance.Available)
	}
	if balance.TotalOut != "40.000000" {
		t.Errorf("Expected totalOut 40.000000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_Refund(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "10.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "5.00"},
		{AgentAddr: agent, EventType: "refund", Amount: "2.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "7.000000" {
		t.Errorf("Expected available 7.000000, got %s", balance.Available)
	}
	if balance.TotalOut != "5.000000" {
		t.Errorf("Expected totalOut 5.000000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_EmptyEvents(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{}

	balance := RebuildBalance(agent, events)

	if balance.Available != "0.000000" {
		t.Errorf("Expected available 0.000000, got %s", balance.Available)
	}
	if balance.Pending != "0.000000" {
		t.Errorf("Expected pending 0.000000, got %s", balance.Pending)
	}
	if balance.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", balance.Escrowed)
	}
	if balance.TotalIn != "0.000000" {
		t.Errorf("Expected totalIn 0.000000, got %s", balance.TotalIn)
	}
	if balance.TotalOut != "0.000000" {
		t.Errorf("Expected totalOut 0.000000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_InvalidAmount(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "10.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "invalid"},
		{AgentAddr: agent, EventType: "deposit", Amount: "5.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "15.000000" {
		t.Errorf("Expected available 15.000000, got %s", balance.Available)
	}
}

func TestRebuildBalance_ComplexScenario(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "100.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "10.00"},
		{AgentAddr: agent, EventType: "hold", Amount: "20.00"},
		{AgentAddr: agent, EventType: "escrow_lock", Amount: "15.00"},
		{AgentAddr: agent, EventType: "withdrawal", Amount: "5.00"},
		{AgentAddr: agent, EventType: "confirm_hold", Amount: "20.00"},
		{AgentAddr: agent, EventType: "escrow_release", Amount: "15.00"},
		{AgentAddr: agent, EventType: "refund", Amount: "3.00"},
	}

	balance := RebuildBalance(agent, events)

	if balance.Available != "53.000000" {
		t.Errorf("Expected available 53.000000, got %s", balance.Available)
	}
	if balance.Pending != "0.000000" {
		t.Errorf("Expected pending 0.000000, got %s", balance.Pending)
	}
	if balance.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", balance.Escrowed)
	}
	if balance.TotalIn != "100.000000" {
		t.Errorf("Expected totalIn 100.000000, got %s", balance.TotalIn)
	}
	if balance.TotalOut != "50.000000" {
		t.Errorf("Expected totalOut 50.000000, got %s", balance.TotalOut)
	}
}

func TestRebuildBalance_FundConservation(t *testing.T) {
	agent := "0x1234567890123456789012345678901234567890"
	events := []*Event{
		{AgentAddr: agent, EventType: "deposit", Amount: "100.00"},
		{AgentAddr: agent, EventType: "spend", Amount: "20.00"},
		{AgentAddr: agent, EventType: "hold", Amount: "10.00"},
		{AgentAddr: agent, EventType: "escrow_lock", Amount: "15.00"},
		{AgentAddr: agent, EventType: "withdrawal", Amount: "5.00"},
		{AgentAddr: agent, EventType: "confirm_hold", Amount: "10.00"},
		{AgentAddr: agent, EventType: "escrow_release", Amount: "15.00"},
	}

	balance := RebuildBalance(agent, events)

	// Verify fund conservation without refunds
	// in=100, out=50, avail+pend+esc should equal 50
	assertFundConservation(t, balance, "RebuildBalance fund conservation")
}

// ---------------------------------------------------------------------------
// BalanceAtTime Tests
// ---------------------------------------------------------------------------

func TestBalanceAtTime_SinglePoint(t *testing.T) {
	ctx := context.Background()
	eventStore := NewMemoryEventStore()
	agent := "0x1234567890123456789012345678901234567890"

	baseTime := time.Now()

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "deposit",
		Amount:    "10.00",
		CreatedAt: baseTime,
	})

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "spend",
		Amount:    "3.00",
		CreatedAt: baseTime.Add(1 * time.Hour),
	})

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "deposit",
		Amount:    "5.00",
		CreatedAt: baseTime.Add(2 * time.Hour),
	})

	bal1, err := BalanceAtTime(ctx, eventStore, agent, baseTime.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal1.Available != "10.000000" {
		t.Errorf("At 30min: expected available 10.000000, got %s", bal1.Available)
	}

	bal2, err := BalanceAtTime(ctx, eventStore, agent, baseTime.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal2.Available != "7.000000" {
		t.Errorf("At 90min: expected available 7.000000, got %s", bal2.Available)
	}

	bal3, err := BalanceAtTime(ctx, eventStore, agent, baseTime.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal3.Available != "12.000000" {
		t.Errorf("At 3hr: expected available 12.000000, got %s", bal3.Available)
	}
}

func TestBalanceAtTime_BeforeFirstEvent(t *testing.T) {
	ctx := context.Background()
	eventStore := NewMemoryEventStore()
	agent := "0x1234567890123456789012345678901234567890"

	baseTime := time.Now()

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "deposit",
		Amount:    "10.00",
		CreatedAt: baseTime,
	})

	bal, err := BalanceAtTime(ctx, eventStore, agent, baseTime.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}

	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000 before first event, got %s", bal.Available)
	}
}

func TestBalanceAtTime_ExactEventTime(t *testing.T) {
	ctx := context.Background()
	eventStore := NewMemoryEventStore()
	agent := "0x1234567890123456789012345678901234567890"

	baseTime := time.Now()

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "deposit",
		Amount:    "10.00",
		CreatedAt: baseTime,
	})

	bal, err := BalanceAtTime(ctx, eventStore, agent, baseTime)
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}

	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000 at exact event time, got %s", bal.Available)
	}
}

func TestBalanceAtTime_UnknownAgent(t *testing.T) {
	ctx := context.Background()
	eventStore := NewMemoryEventStore()

	agent1 := "0x1111111111111111111111111111111111111111"
	agent2 := "0x2222222222222222222222222222222222222222"

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent1,
		EventType: "deposit",
		Amount:    "10.00",
		CreatedAt: time.Now(),
	})

	bal, err := BalanceAtTime(ctx, eventStore, agent2, time.Now())
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}

	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000 for unknown agent, got %s", bal.Available)
	}
}

func TestBalanceAtTime_WithHoldAndEscrow(t *testing.T) {
	ctx := context.Background()
	eventStore := NewMemoryEventStore()
	agent := "0x1234567890123456789012345678901234567890"

	baseTime := time.Now()

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "deposit",
		Amount:    "20.00",
		CreatedAt: baseTime,
	})

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "hold",
		Amount:    "5.00",
		CreatedAt: baseTime.Add(1 * time.Hour),
	})

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "escrow_lock",
		Amount:    "3.00",
		CreatedAt: baseTime.Add(2 * time.Hour),
	})

	bal, err := BalanceAtTime(ctx, eventStore, agent, baseTime.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal.Available != "15.000000" {
		t.Errorf("Expected available 15.000000, got %s", bal.Available)
	}
	if bal.Pending != "5.000000" {
		t.Errorf("Expected pending 5.000000, got %s", bal.Pending)
	}

	bal2, err := BalanceAtTime(ctx, eventStore, agent, baseTime.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal2.Available != "12.000000" {
		t.Errorf("Expected available 12.000000, got %s", bal2.Available)
	}
	if bal2.Pending != "5.000000" {
		t.Errorf("Expected pending 5.000000, got %s", bal2.Pending)
	}
	if bal2.Escrowed != "3.000000" {
		t.Errorf("Expected escrowed 3.000000, got %s", bal2.Escrowed)
	}
}

// ---------------------------------------------------------------------------
// ReconcileAgent Tests
// ---------------------------------------------------------------------------

func TestReconcileAgent_MatchingBalances(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent := "0x1234567890123456789012345678901234567890"

	ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	ledger.Spend(ctx, agent, "3.00", "sk_1")
	ledger.Deposit(ctx, agent, "5.00", "0xtx2")

	result, err := ReconcileAgent(ctx, eventStore, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}

	if !result.Match {
		t.Errorf("Expected Match=true, got false. Replay: avail=%s pend=%s esc=%s, Actual: avail=%s pend=%s esc=%s",
			result.ReplayAvailable, result.ReplayPending, result.ReplayEscrowed,
			result.ActualAvailable, result.ActualPending, result.ActualEscrowed)
	}

	if result.ReplayAvailable != "12.000000" {
		t.Errorf("Expected replay available 12.000000, got %s", result.ReplayAvailable)
	}
	if result.ActualAvailable != "12.000000" {
		t.Errorf("Expected actual available 12.000000, got %s", result.ActualAvailable)
	}
}

func TestReconcileAgent_WithHoldAndEscrow(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent := "0x1234567890123456789012345678901234567890"

	ledger.Deposit(ctx, agent, "20.00", "0xtx1")
	ledger.Hold(ctx, agent, "5.00", "hold_1")
	ledger.EscrowLock(ctx, agent, "3.00", "esc_1")

	result, err := ReconcileAgent(ctx, eventStore, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}

	if !result.Match {
		t.Errorf("Expected Match=true, got false")
	}

	if result.ReplayAvailable != "12.000000" {
		t.Errorf("Expected replay available 12.000000, got %s", result.ReplayAvailable)
	}
	if result.ReplayPending != "5.000000" {
		t.Errorf("Expected replay pending 5.000000, got %s", result.ReplayPending)
	}
	if result.ReplayEscrowed != "3.000000" {
		t.Errorf("Expected replay escrowed 3.000000, got %s", result.ReplayEscrowed)
	}
}

func TestReconcileAgent_Mismatch(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent := "0x1234567890123456789012345678901234567890"

	ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	ledger.Spend(ctx, agent, "3.00", "sk_1")

	store.Credit(ctx, agent, "5.00", "tampering", "manual_adjustment")

	result, err := ReconcileAgent(ctx, eventStore, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}

	if result.Match {
		t.Error("Expected Match=false due to tampering, got true")
	}

	if result.ReplayAvailable != "7.000000" {
		t.Errorf("Expected replay available 7.000000, got %s", result.ReplayAvailable)
	}
	if result.ActualAvailable != "12.000000" {
		t.Errorf("Expected actual available 12.000000 (tampered), got %s", result.ActualAvailable)
	}
}

func TestReconcileAgent_UnknownAgent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()

	agent := "0x1234567890123456789012345678901234567890"

	result, err := ReconcileAgent(ctx, eventStore, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}

	if !result.Match {
		t.Error("Expected Match=true for unknown agent (all zeros)")
	}

	if result.ReplayAvailable != "0.000000" {
		t.Errorf("Expected replay available 0.000000, got %s", result.ReplayAvailable)
	}
	// Actual balance for non-existent agent is normalized to "0.000000"
	if result.ActualAvailable != "0" && result.ActualAvailable != "0.000000" {
		t.Errorf("Expected actual available 0 or 0.000000, got %s", result.ActualAvailable)
	}
}

func TestReconcileAgent_EmptyEventHistory(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()

	agent := "0x1234567890123456789012345678901234567890"

	store.Credit(ctx, agent, "10.00", "0xtx1", "deposit")

	result, err := ReconcileAgent(ctx, eventStore, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}

	if result.Match {
		t.Error("Expected Match=false (no events but balance exists)")
	}

	if result.ReplayAvailable != "0.000000" {
		t.Errorf("Expected replay available 0.000000 (no events), got %s", result.ReplayAvailable)
	}
	if result.ActualAvailable != "10.000000" {
		t.Errorf("Expected actual available 10.000000, got %s", result.ActualAvailable)
	}
}

func TestReconcileAgent_CompleteLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent := "0x1234567890123456789012345678901234567890"
	seller := "0x9999999999999999999999999999999999999999"

	ledger.Deposit(ctx, agent, "100.00", "0xtx1")
	ledger.Hold(ctx, agent, "20.00", "hold_1")
	ledger.ConfirmHold(ctx, agent, "20.00", "hold_1")
	ledger.EscrowLock(ctx, agent, "15.00", "esc_1")
	ledger.ReleaseEscrow(ctx, agent, seller, "15.00", "esc_1")
	ledger.Spend(ctx, agent, "10.00", "sk_1")
	ledger.Refund(ctx, agent, "3.00", "ref_1")
	ledger.Withdraw(ctx, agent, "5.00", "0xwithdraw")

	result, err := ReconcileAgent(ctx, eventStore, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}

	if !result.Match {
		t.Errorf("Expected Match=true after full lifecycle, got false. Replay: avail=%s, Actual: avail=%s",
			result.ReplayAvailable, result.ActualAvailable)
	}
}

// ---------------------------------------------------------------------------
// ReconcileAll Tests
// ---------------------------------------------------------------------------

func TestReconcileAll_MultipleAgents(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent1 := "0x1111111111111111111111111111111111111111"
	agent2 := "0x2222222222222222222222222222222222222222"
	agent3 := "0x3333333333333333333333333333333333333333"

	ledger.Deposit(ctx, agent1, "10.00", "0xtx1")
	ledger.Spend(ctx, agent1, "3.00", "sk_1")

	ledger.Deposit(ctx, agent2, "20.00", "0xtx2")
	ledger.Hold(ctx, agent2, "5.00", "hold_1")

	ledger.Deposit(ctx, agent3, "15.00", "0xtx3")
	ledger.EscrowLock(ctx, agent3, "7.00", "esc_1")

	results, err := ReconcileAll(ctx, eventStore, store)
	if err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("Expected 3 reconciliation results, got %d", len(results))
	}

	for _, result := range results {
		if !result.Match {
			t.Errorf("Expected all agents to match. Agent %s did not match", result.AgentAddr)
		}
	}

	var r1, r2, r3 *ReconciliationResult
	for _, r := range results {
		switch r.AgentAddr {
		case agent1:
			r1 = r
		case agent2:
			r2 = r
		case agent3:
			r3 = r
		}
	}

	if r1 == nil || r2 == nil || r3 == nil {
		t.Fatal("Not all agents found in reconciliation results")
	}

	if r1.ReplayAvailable != "7.000000" {
		t.Errorf("Agent1: expected available 7.000000, got %s", r1.ReplayAvailable)
	}
	if r2.ReplayAvailable != "15.000000" {
		t.Errorf("Agent2: expected available 15.000000, got %s", r2.ReplayAvailable)
	}
	if r2.ReplayPending != "5.000000" {
		t.Errorf("Agent2: expected pending 5.000000, got %s", r2.ReplayPending)
	}
	if r3.ReplayAvailable != "8.000000" {
		t.Errorf("Agent3: expected available 8.000000, got %s", r3.ReplayAvailable)
	}
	if r3.ReplayEscrowed != "7.000000" {
		t.Errorf("Agent3: expected escrowed 7.000000, got %s", r3.ReplayEscrowed)
	}
}

func TestReconcileAll_WithMismatch(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent1 := "0x1111111111111111111111111111111111111111"
	agent2 := "0x2222222222222222222222222222222222222222"

	ledger.Deposit(ctx, agent1, "10.00", "0xtx1")
	ledger.Deposit(ctx, agent2, "20.00", "0xtx2")

	store.Credit(ctx, agent2, "5.00", "tamper", "manual")

	results, err := ReconcileAll(ctx, eventStore, store)
	if err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 reconciliation results, got %d", len(results))
	}

	matchCount := 0
	mismatchCount := 0
	for _, r := range results {
		if r.Match {
			matchCount++
			if r.AgentAddr != agent1 {
				t.Errorf("Expected only agent1 to match, but %s matched", r.AgentAddr)
			}
		} else {
			mismatchCount++
			if r.AgentAddr != agent2 {
				t.Errorf("Expected agent2 to mismatch, but %s mismatched", r.AgentAddr)
			}
		}
	}

	if matchCount != 1 {
		t.Errorf("Expected 1 matching agent, got %d", matchCount)
	}
	if mismatchCount != 1 {
		t.Errorf("Expected 1 mismatching agent, got %d", mismatchCount)
	}
}

func TestReconcileAll_NoAgents(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()

	results, err := ReconcileAll(ctx, eventStore, store)
	if err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}

	if len(results) != 0 {
		t.Errorf("Expected 0 results for empty store, got %d", len(results))
	}
}

func TestReconcileAll_OnlyEventsNoBalance(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()

	agent := "0x1234567890123456789012345678901234567890"

	eventStore.AppendEvent(ctx, &Event{
		AgentAddr: agent,
		EventType: "deposit",
		Amount:    "10.00",
	})

	results, err := ReconcileAll(ctx, eventStore, store)
	if err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}

	if results[0].Match {
		t.Error("Expected mismatch when events exist but balance is zero")
	}

	if results[0].ReplayAvailable != "10.000000" {
		t.Errorf("Expected replay available 10.000000, got %s", results[0].ReplayAvailable)
	}
	// Normalized value for non-existent balance
	if results[0].ActualAvailable != "0" && results[0].ActualAvailable != "0.000000" {
		t.Errorf("Expected actual available 0 or 0.000000, got %s", results[0].ActualAvailable)
	}
}

// ---------------------------------------------------------------------------
// Ledger Integration Tests
// ---------------------------------------------------------------------------

func TestLedger_BalanceAtTime_Integration(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent := "0x1234567890123456789012345678901234567890"

	ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	time.Sleep(10 * time.Millisecond)
	midTime := time.Now()
	time.Sleep(10 * time.Millisecond)
	ledger.Spend(ctx, agent, "3.00", "sk_1")

	bal, err := ledger.BalanceAtTime(ctx, agent, midTime)
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}

	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000 at mid time, got %s", bal.Available)
	}

	bal2, err := ledger.BalanceAtTime(ctx, agent, time.Now())
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}

	if bal2.Available != "7.000000" {
		t.Errorf("Expected available 7.000000 at current time, got %s", bal2.Available)
	}
}

func TestLedger_BalanceAtTime_NoEventStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	ledger := New(store)

	agent := "0x1234567890123456789012345678901234567890"

	_, err := ledger.BalanceAtTime(ctx, agent, time.Now())
	if err == nil {
		t.Error("Expected error when event store not configured")
	}
}

func TestLedger_ReconcileAll_Integration(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	eventStore := NewMemoryEventStore()
	ledger := NewWithEvents(store, eventStore)

	agent1 := "0x1111111111111111111111111111111111111111"
	agent2 := "0x2222222222222222222222222222222222222222"

	ledger.Deposit(ctx, agent1, "10.00", "0xtx1")
	ledger.Spend(ctx, agent1, "3.00", "sk_1")

	ledger.Deposit(ctx, agent2, "20.00", "0xtx2")

	results, err := ledger.ReconcileAll(ctx)
	if err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	for _, r := range results {
		if !r.Match {
			t.Errorf("Expected all to match, agent %s did not", r.AgentAddr)
		}
	}
}

func TestLedger_ReconcileAll_NoEventStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	ledger := New(store)

	_, err := ledger.ReconcileAll(ctx)
	if err == nil {
		t.Error("Expected error when event store not configured")
	}
}

// assertFundConservation is already defined in ledger_test.go
