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

func TestExecuteSettlement(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	// Fund agents (use lowercase — Ledger.Transfer lowercases addresses)
	_ = store.Credit(ctx, "0xa", "100.000000", "tx1", "deposit")
	_ = store.Credit(ctx, "0xb", "50.000000", "tx2", "deposit")

	l := New(store)

	settlements := []NetSettlement{
		{From: "0xA", To: "0xB", Amount: "15.000000"},
	}

	err := ExecuteSettlement(ctx, l, settlements)
	if err != nil {
		t.Fatalf("ExecuteSettlement failed: %v", err)
	}

	balA, _ := store.GetBalance(ctx, "0xa")
	balB, _ := store.GetBalance(ctx, "0xb")

	if balA.Available != "85.000000" {
		t.Errorf("expected 0xa available 85.000000, got %s", balA.Available)
	}
	if balB.Available != "65.000000" {
		t.Errorf("expected 0xb available 65.000000, got %s", balB.Available)
	}
}
