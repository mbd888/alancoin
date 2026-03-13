package ledger

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/mbd888/alancoin/internal/usdc"
)

// ---------- BatchDebit tests ----------

func TestBatchDebit_MultipleAgents(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agentA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agentB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Seed balances
	if err := l.Deposit(ctx, agentA, "100.00", "0xtxA"); err != nil {
		t.Fatalf("Deposit agentA: %v", err)
	}
	if err := l.Deposit(ctx, agentB, "50.00", "0xtxB"); err != nil {
		t.Fatalf("Deposit agentB: %v", err)
	}

	// Batch debit both agents
	err := l.BatchDebit(ctx, []DebitRequest{
		{AgentAddr: agentA, Amount: "10.00", Reference: "ref1", Description: "svc1"},
		{AgentAddr: agentB, Amount: "5.00", Reference: "ref2", Description: "svc2"},
	})
	if err != nil {
		t.Fatalf("BatchDebit: %v", err)
	}

	// Verify balances
	balA, _ := l.GetBalance(ctx, agentA)
	balB, _ := l.GetBalance(ctx, agentB)

	if balA.Available != "90.000000" {
		t.Errorf("agentA available = %s, want 90.000000", balA.Available)
	}
	if balB.Available != "45.000000" {
		t.Errorf("agentB available = %s, want 45.000000", balB.Available)
	}
}

func TestBatchDebit_RollbackOnInsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agentA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agentB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Give agentA enough, but agentB too little
	if err := l.Deposit(ctx, agentA, "100.00", "0xtxA"); err != nil {
		t.Fatalf("Deposit agentA: %v", err)
	}
	if err := l.Deposit(ctx, agentB, "2.00", "0xtxB"); err != nil {
		t.Fatalf("Deposit agentB: %v", err)
	}

	err := l.BatchDebit(ctx, []DebitRequest{
		{AgentAddr: agentA, Amount: "10.00", Reference: "ref1", Description: "svc1"},
		{AgentAddr: agentB, Amount: "5.00", Reference: "ref2", Description: "svc2"}, // will fail
	})

	if !errors.Is(err, ErrBatchDebitFailed) {
		t.Fatalf("expected ErrBatchDebitFailed, got %v", err)
	}

	// agentA balance should be restored (rollback refund)
	balA, _ := l.GetBalance(ctx, agentA)
	if balA.Available != "100.000000" {
		t.Errorf("agentA available after rollback = %s, want 100.000000", balA.Available)
	}

	// agentB balance should be unchanged
	balB, _ := l.GetBalance(ctx, agentB)
	if balB.Available != "2.000000" {
		t.Errorf("agentB available after rollback = %s, want 2.000000", balB.Available)
	}
}

func TestBatchDebit_EmptyBatch(t *testing.T) {
	l := New(NewMemoryStore())
	err := l.BatchDebit(context.Background(), nil)
	if !errors.Is(err, ErrEmptyBatch) {
		t.Errorf("expected ErrEmptyBatch, got %v", err)
	}
}

func TestBatchDebit_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	err := l.BatchDebit(context.Background(), []DebitRequest{
		{AgentAddr: "0xaaa", Amount: "-1", Reference: "r", Description: "d"},
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("expected ErrInvalidAmount, got %v", err)
	}
}

// ---------- BatchCredit tests ----------

func TestBatchCredit_WithDedup(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agentA := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agentB := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Credit two agents
	applied, err := l.BatchCredit(ctx, []CreditRequest{
		{AgentAddr: agentA, Amount: "20.00", TxHash: "0xhash1", Description: "deposit"},
		{AgentAddr: agentB, Amount: "30.00", TxHash: "0xhash2", Description: "deposit"},
	})
	if err != nil {
		t.Fatalf("BatchCredit: %v", err)
	}
	if applied != 2 {
		t.Errorf("applied = %d, want 2", applied)
	}

	// Repeat the same batch -- both should be skipped (dedup)
	applied, err = l.BatchCredit(ctx, []CreditRequest{
		{AgentAddr: agentA, Amount: "20.00", TxHash: "0xhash1", Description: "deposit"},
		{AgentAddr: agentB, Amount: "30.00", TxHash: "0xhash2", Description: "deposit"},
	})
	if err != nil {
		t.Fatalf("BatchCredit duplicate: %v", err)
	}
	if applied != 0 {
		t.Errorf("applied on dup = %d, want 0", applied)
	}

	// Balances should reflect only the first batch
	balA, _ := l.GetBalance(ctx, agentA)
	balB, _ := l.GetBalance(ctx, agentB)

	if balA.Available != "20.000000" {
		t.Errorf("agentA available = %s, want 20.000000", balA.Available)
	}
	if balB.Available != "30.000000" {
		t.Errorf("agentB available = %s, want 30.000000", balB.Available)
	}
}

func TestBatchCredit_EmptyBatch(t *testing.T) {
	l := New(NewMemoryStore())
	_, err := l.BatchCredit(context.Background(), nil)
	if !errors.Is(err, ErrEmptyBatch) {
		t.Errorf("expected ErrEmptyBatch, got %v", err)
	}
}

func TestBatchCredit_PartialDedup(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// First credit
	_, err := l.BatchCredit(ctx, []CreditRequest{
		{AgentAddr: agent, Amount: "10.00", TxHash: "0xhash1", Description: "deposit"},
	})
	if err != nil {
		t.Fatalf("first BatchCredit: %v", err)
	}

	// Batch with one dup and one new
	applied, err := l.BatchCredit(ctx, []CreditRequest{
		{AgentAddr: agent, Amount: "10.00", TxHash: "0xhash1", Description: "deposit"}, // dup
		{AgentAddr: agent, Amount: "5.00", TxHash: "0xhash3", Description: "deposit"},  // new
	})
	if err != nil {
		t.Fatalf("second BatchCredit: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "15.000000" {
		t.Errorf("available = %s, want 15.000000", bal.Available)
	}
}

// ---------- NetSettle tests ----------

func TestBatchNetSettle_Basic(t *testing.T) {
	// A->B $5, B->A $3 => net A->B $2
	result, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xBBB", Amount: "5.00"},
		{From: "0xBBB", To: "0xAAA", Amount: "3.00"},
	})
	if err != nil {
		t.Fatalf("NetSettle: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 net transfer, got %d", len(result))
	}

	nt := result[0]
	if nt.From != "0xaaa" || nt.To != "0xbbb" {
		t.Errorf("direction: from=%s to=%s, want 0xaaa->0xbbb", nt.From, nt.To)
	}

	amt, ok := usdc.Parse(nt.Amount)
	if !ok {
		t.Fatalf("could not parse net amount %q", nt.Amount)
	}
	expected, _ := usdc.Parse("2.00")
	if amt.Cmp(expected) != 0 {
		t.Errorf("net amount = %s, want %s", usdc.Format(amt), usdc.Format(expected))
	}
}

func TestBatchNetSettle_NoTransfersWhenBalanced(t *testing.T) {
	// A->B $10, B->A $10 => nothing
	result, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xBBB", Amount: "10.00"},
		{From: "0xBBB", To: "0xAAA", Amount: "10.00"},
	})
	if err != nil {
		t.Fatalf("NetSettle: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 net transfers, got %d", len(result))
	}
}

func TestBatchNetSettle_CircularFlows(t *testing.T) {
	// A->B $10, B->C $10, C->A $10 => nothing (circular, nets to zero)
	result, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xBBB", Amount: "10.00"},
		{From: "0xBBB", To: "0xCCC", Amount: "10.00"},
		{From: "0xCCC", To: "0xAAA", Amount: "10.00"},
	})
	if err != nil {
		t.Fatalf("NetSettle: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 net transfers for circular flow, got %d", len(result))
	}
}

func TestBatchNetSettle_CircularWithResidue(t *testing.T) {
	// A->B $10, B->C $7, C->A $4
	// Net positions: A = -10+4 = -6, B = 10-7 = 3, C = 7-4 = 3
	// Result: A pays B $3, A pays C $3 (or equivalent)
	result, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xBBB", Amount: "10.00"},
		{From: "0xBBB", To: "0xCCC", Amount: "7.00"},
		{From: "0xCCC", To: "0xAAA", Amount: "4.00"},
	})
	if err != nil {
		t.Fatalf("NetSettle: %v", err)
	}

	// Should produce exactly 2 transfers totalling $6 from A
	totalMoved := big.NewInt(0)
	for _, nt := range result {
		amt, ok := usdc.Parse(nt.Amount)
		if !ok {
			t.Fatalf("cannot parse %q", nt.Amount)
		}
		totalMoved.Add(totalMoved, amt)
		if nt.From != "0xaaa" {
			t.Errorf("expected debtor 0xaaa, got %s", nt.From)
		}
	}

	expectedTotal, _ := usdc.Parse("6.00")
	if totalMoved.Cmp(expectedTotal) != 0 {
		t.Errorf("total moved = %s, want %s", usdc.Format(totalMoved), usdc.Format(expectedTotal))
	}
}

func TestBatchNetSettle_MultiParty(t *testing.T) {
	// A->B $5, A->C $3, B->C $2
	// Net: A = -8, B = +3, C = +5
	result, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xBBB", Amount: "5.00"},
		{From: "0xAAA", To: "0xCCC", Amount: "3.00"},
		{From: "0xBBB", To: "0xCCC", Amount: "2.00"},
	})
	if err != nil {
		t.Fatalf("NetSettle: %v", err)
	}

	// Total transfers should equal $8 (A's net debt)
	totalMoved := big.NewInt(0)
	for _, nt := range result {
		amt, ok := usdc.Parse(nt.Amount)
		if !ok {
			t.Fatalf("cannot parse %q", nt.Amount)
		}
		totalMoved.Add(totalMoved, amt)
	}

	expectedTotal, _ := usdc.Parse("8.00")
	if totalMoved.Cmp(expectedTotal) != 0 {
		t.Errorf("total moved = %s, want %s", usdc.Format(totalMoved), usdc.Format(expectedTotal))
	}
}

func TestBatchNetSettle_Empty(t *testing.T) {
	result, err := NetSettle(nil)
	if err != nil {
		t.Fatalf("NetSettle nil: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %v", result)
	}
}

func TestBatchNetSettle_InvalidAmount(t *testing.T) {
	_, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xBBB", Amount: "bad"},
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestBatchNetSettle_SelfTransfer(t *testing.T) {
	// Self-transfer should be a no-op
	result, err := NetSettle([]Transfer{
		{From: "0xAAA", To: "0xAAA", Amount: "10.00"},
	})
	if err != nil {
		t.Fatalf("NetSettle: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0 net transfers for self-transfer, got %d", len(result))
	}
}
