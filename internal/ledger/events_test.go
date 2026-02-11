package ledger

import (
	"context"
	"testing"
	"time"
)

func TestRebuildBalance_Deposits(t *testing.T) {
	events := []*Event{
		{AgentAddr: "0xA", EventType: "deposit", Amount: "10.000000"},
		{AgentAddr: "0xA", EventType: "deposit", Amount: "5.000000"},
	}

	bal := RebuildBalance("0xA", events)

	if bal.Available != "15.000000" {
		t.Errorf("expected available 15.000000, got %s", bal.Available)
	}
	if bal.TotalIn != "15.000000" {
		t.Errorf("expected totalIn 15.000000, got %s", bal.TotalIn)
	}
}

func TestRebuildBalance_DepositAndSpend(t *testing.T) {
	events := []*Event{
		{AgentAddr: "0xA", EventType: "deposit", Amount: "10.000000"},
		{AgentAddr: "0xA", EventType: "spend", Amount: "3.000000"},
	}

	bal := RebuildBalance("0xA", events)

	if bal.Available != "7.000000" {
		t.Errorf("expected available 7.000000, got %s", bal.Available)
	}
	if bal.TotalOut != "3.000000" {
		t.Errorf("expected totalOut 3.000000, got %s", bal.TotalOut)
	}
}

func TestRebuildBalance_HoldAndConfirm(t *testing.T) {
	events := []*Event{
		{AgentAddr: "0xA", EventType: "deposit", Amount: "10.000000"},
		{AgentAddr: "0xA", EventType: "hold", Amount: "4.000000"},
		{AgentAddr: "0xA", EventType: "confirm_hold", Amount: "4.000000"},
	}

	bal := RebuildBalance("0xA", events)

	if bal.Available != "6.000000" {
		t.Errorf("expected available 6.000000, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("expected pending 0.000000, got %s", bal.Pending)
	}
	if bal.TotalOut != "4.000000" {
		t.Errorf("expected totalOut 4.000000, got %s", bal.TotalOut)
	}
}

func TestRebuildBalance_HoldAndRelease(t *testing.T) {
	events := []*Event{
		{AgentAddr: "0xA", EventType: "deposit", Amount: "10.000000"},
		{AgentAddr: "0xA", EventType: "hold", Amount: "4.000000"},
		{AgentAddr: "0xA", EventType: "release_hold", Amount: "4.000000"},
	}

	bal := RebuildBalance("0xA", events)

	if bal.Available != "10.000000" {
		t.Errorf("expected available 10.000000, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("expected pending 0.000000, got %s", bal.Pending)
	}
}

func TestRebuildBalance_EscrowCycle(t *testing.T) {
	events := []*Event{
		{AgentAddr: "0xBuyer", EventType: "deposit", Amount: "10.000000"},
		{AgentAddr: "0xBuyer", EventType: "escrow_lock", Amount: "5.000000"},
		{AgentAddr: "0xBuyer", EventType: "escrow_release", Amount: "5.000000"},
	}

	bal := RebuildBalance("0xBuyer", events)

	if bal.Available != "5.000000" {
		t.Errorf("expected available 5.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("expected escrowed 0.000000, got %s", bal.Escrowed)
	}
	if bal.TotalOut != "5.000000" {
		t.Errorf("expected totalOut 5.000000, got %s", bal.TotalOut)
	}
}

func TestRebuildBalance_EscrowRefund(t *testing.T) {
	events := []*Event{
		{AgentAddr: "0xBuyer", EventType: "deposit", Amount: "10.000000"},
		{AgentAddr: "0xBuyer", EventType: "escrow_lock", Amount: "5.000000"},
		{AgentAddr: "0xBuyer", EventType: "escrow_refund", Amount: "5.000000"},
	}

	bal := RebuildBalance("0xBuyer", events)

	if bal.Available != "10.000000" {
		t.Errorf("expected available 10.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("expected escrowed 0.000000, got %s", bal.Escrowed)
	}
}

func TestMemoryEventStore_AppendAndGet(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	err := es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "deposit", Amount: "10.000000"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	err = es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "spend", Amount: "3.000000"})
	if err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	events, err := es.GetEvents(ctx, "0xA", time.Time{})
	if err != nil {
		t.Fatalf("GetEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].ID != 1 || events[1].ID != 2 {
		t.Errorf("expected IDs 1,2, got %d,%d", events[0].ID, events[1].ID)
	}
}

func TestMemoryEventStore_GetAllAgents(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	_ = es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "deposit", Amount: "1.000000"})
	_ = es.AppendEvent(ctx, &Event{AgentAddr: "0xB", EventType: "deposit", Amount: "2.000000"})
	_ = es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "spend", Amount: "0.500000"})

	agents, err := es.GetAllAgents(ctx)
	if err != nil {
		t.Fatalf("GetAllAgents failed: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

func TestBalanceAtTime(t *testing.T) {
	ctx := context.Background()
	es := NewMemoryEventStore()

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 3, 0, 0, 0, 0, time.UTC)

	_ = es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "deposit", Amount: "10.000000", CreatedAt: t1})
	_ = es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "spend", Amount: "3.000000", CreatedAt: t2})
	_ = es.AppendEvent(ctx, &Event{AgentAddr: "0xA", EventType: "spend", Amount: "2.000000", CreatedAt: t3})

	// Balance at t1 should show just the deposit
	bal, err := BalanceAtTime(ctx, es, "0xA", t1)
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal.Available != "10.000000" {
		t.Errorf("expected available 10.000000 at t1, got %s", bal.Available)
	}

	// Balance at t2 should show deposit - first spend
	bal, err = BalanceAtTime(ctx, es, "0xA", t2)
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal.Available != "7.000000" {
		t.Errorf("expected available 7.000000 at t2, got %s", bal.Available)
	}

	// Balance at t3 should show all events
	bal, err = BalanceAtTime(ctx, es, "0xA", t3)
	if err != nil {
		t.Fatalf("BalanceAtTime failed: %v", err)
	}
	if bal.Available != "5.000000" {
		t.Errorf("expected available 5.000000 at t3, got %s", bal.Available)
	}
}

func TestReconcileAgent_Match(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)

	agent := "0x1234567890123456789012345678901234567890"

	if err := l.Deposit(ctx, agent, "10.000000", "0xtx1"); err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}
	if err := l.Spend(ctx, agent, "3.000000", "sk_1"); err != nil {
		t.Fatalf("Spend failed: %v", err)
	}

	result, err := ReconcileAgent(ctx, es, store, agent)
	if err != nil {
		t.Fatalf("ReconcileAgent failed: %v", err)
	}
	if !result.Match {
		t.Errorf("expected match, got mismatch: replay=%s actual=%s",
			result.ReplayAvailable, result.ActualAvailable)
	}
}

func TestReconcileAll(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)

	a1 := "0x1111111111111111111111111111111111111111"
	a2 := "0x2222222222222222222222222222222222222222"

	_ = l.Deposit(ctx, a1, "10.000000", "0xtx1")
	_ = l.Deposit(ctx, a2, "5.000000", "0xtx2")
	_ = l.Spend(ctx, a1, "2.000000", "sk_1")

	results, err := ReconcileAll(ctx, es, store)
	if err != nil {
		t.Fatalf("ReconcileAll failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Match {
			t.Errorf("agent %s mismatch: replay=%s actual=%s",
				r.AgentAddr, r.ReplayAvailable, r.ActualAvailable)
		}
	}
}
