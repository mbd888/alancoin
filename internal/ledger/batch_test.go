package ledger

import (
	"context"
	"testing"
)

func TestComputeNetSettlements_SimpleNet(t *testing.T) {
	transfers := []Transfer{
		{From: "0xA", To: "0xB", Amount: "5.000000"},
		{From: "0xB", To: "0xA", Amount: "3.000000"},
	}

	settlements := ComputeNetSettlements(transfers)

	if len(settlements) != 1 {
		t.Fatalf("expected 1 settlement, got %d", len(settlements))
	}

	s := settlements[0]
	if s.Amount != "2.000000" {
		t.Errorf("expected net amount 2.000000, got %s", s.Amount)
	}
	// The direction should be A→B (net 2)
	if (s.From != "0xA" || s.To != "0xB") && (s.From != "0xB" || s.To != "0xA") {
		t.Errorf("unexpected settlement direction: %s → %s", s.From, s.To)
	}
}

func TestComputeNetSettlements_ZeroNet(t *testing.T) {
	transfers := []Transfer{
		{From: "0xA", To: "0xB", Amount: "5.000000"},
		{From: "0xB", To: "0xA", Amount: "5.000000"},
	}

	settlements := ComputeNetSettlements(transfers)

	if len(settlements) != 0 {
		t.Errorf("expected 0 settlements (zero net), got %d", len(settlements))
	}
}

func TestComputeNetSettlements_MultiplePairs(t *testing.T) {
	transfers := []Transfer{
		{From: "0xA", To: "0xB", Amount: "10.000000"},
		{From: "0xA", To: "0xC", Amount: "5.000000"},
		{From: "0xB", To: "0xC", Amount: "3.000000"},
	}

	settlements := ComputeNetSettlements(transfers)

	if len(settlements) != 3 {
		t.Fatalf("expected 3 settlements, got %d", len(settlements))
	}
}

func TestComputeNetSettlements_InvalidAmounts(t *testing.T) {
	transfers := []Transfer{
		{From: "0xA", To: "0xB", Amount: "invalid"},
		{From: "0xA", To: "0xB", Amount: "0.000000"}, // zero — skipped
		{From: "0xA", To: "0xB", Amount: "5.000000"},
	}

	settlements := ComputeNetSettlements(transfers)

	if len(settlements) != 1 {
		t.Fatalf("expected 1 settlement, got %d", len(settlements))
	}
	if settlements[0].Amount != "5.000000" {
		t.Errorf("expected 5.000000, got %s", settlements[0].Amount)
	}
}

func TestMemoryBatchStore_BatchDebit(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Fund two agents
	_ = store.Credit(ctx, "0xA", "100.000000", "tx1", "deposit")
	_ = store.Credit(ctx, "0xB", "50.000000", "tx2", "deposit")

	batch := NewMemoryBatchStore(store)
	errs := batch.BatchDebit(ctx, []BatchDebitRequest{
		{AgentAddr: "0xA", Amount: "10.000000", Reference: "ref1", Description: "test"},
		{AgentAddr: "0xB", Amount: "20.000000", Reference: "ref2", Description: "test"},
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("BatchDebit[%d] failed: %v", i, err)
		}
	}

	// Verify balances
	balA, _ := store.GetBalance(ctx, "0xA")
	if balA.Available != "90.000000" {
		t.Errorf("expected 0xA available 90.000000, got %s", balA.Available)
	}

	balB, _ := store.GetBalance(ctx, "0xB")
	if balB.Available != "30.000000" {
		t.Errorf("expected 0xB available 30.000000, got %s", balB.Available)
	}
}

func TestMemoryBatchStore_BatchDebit_Atomicity(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Fund only 0xA
	_ = store.Credit(ctx, "0xA", "100.000000", "tx1", "deposit")
	_ = store.Credit(ctx, "0xB", "5.000000", "tx2", "deposit")

	batch := NewMemoryBatchStore(store)
	errs := batch.BatchDebit(ctx, []BatchDebitRequest{
		{AgentAddr: "0xA", Amount: "10.000000", Reference: "ref1", Description: "test"},
		{AgentAddr: "0xB", Amount: "50.000000", Reference: "ref2", Description: "test"}, // insufficient
	})

	if errs[0] != nil {
		t.Errorf("expected first debit to succeed, got %v", errs[0])
	}
	if errs[1] != ErrInsufficientBalance {
		t.Errorf("expected ErrInsufficientBalance for second debit, got %v", errs[1])
	}

	// Both should be unchanged since batch is atomic
	balA, _ := store.GetBalance(ctx, "0xA")
	if balA.Available != "100.000000" {
		t.Errorf("expected 0xA available unchanged at 100.000000, got %s (atomicity failed)", balA.Available)
	}
}

func TestMemoryBatchStore_BatchDeposit(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	batch := NewMemoryBatchStore(store)
	errs := batch.BatchDeposit(ctx, []BatchDepositRequest{
		{AgentAddr: "0xA", Amount: "25.000000", TxHash: "tx1", Description: "test"},
		{AgentAddr: "0xB", Amount: "50.000000", TxHash: "tx2", Description: "test"},
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("BatchDeposit[%d] failed: %v", i, err)
		}
	}

	balA, _ := store.GetBalance(ctx, "0xA")
	if balA.Available != "25.000000" {
		t.Errorf("expected 0xA available 25.000000, got %s", balA.Available)
	}

	balB, _ := store.GetBalance(ctx, "0xB")
	if balB.Available != "50.000000" {
		t.Errorf("expected 0xB available 50.000000, got %s", balB.Available)
	}
}

func TestExecuteSettlement(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Fund agents
	_ = store.Credit(ctx, "0xA", "100.000000", "tx1", "deposit")
	_ = store.Credit(ctx, "0xB", "50.000000", "tx2", "deposit")

	settlements := []NetSettlement{
		{From: "0xA", To: "0xB", Amount: "15.000000"},
	}

	err := ExecuteSettlement(ctx, store, settlements)
	if err != nil {
		t.Fatalf("ExecuteSettlement failed: %v", err)
	}

	balA, _ := store.GetBalance(ctx, "0xA")
	balB, _ := store.GetBalance(ctx, "0xB")

	if balA.Available != "85.000000" {
		t.Errorf("expected 0xA available 85.000000, got %s", balA.Available)
	}
	if balB.Available != "65.000000" {
		t.Errorf("expected 0xB available 65.000000, got %s", balB.Available)
	}
}
