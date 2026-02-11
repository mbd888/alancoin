package ledger

import (
	"context"
	"testing"
)

func TestReversal_ReverseDeposit(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	l := New(store)

	agent := "0x1234567890123456789012345678901234567890"

	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")

	// Find the deposit entry
	entries, _ := l.GetHistory(ctx, agent, 10)
	var depositEntryID string
	for _, e := range entries {
		if e.Type == "deposit" {
			depositEntryID = e.ID
			break
		}
	}
	if depositEntryID == "" {
		t.Fatal("deposit entry not found")
	}

	// Reverse it
	err := l.Reverse(ctx, depositEntryID, "incorrect deposit", "admin_1")
	if err != nil {
		t.Fatalf("Reverse failed: %v", err)
	}

	// Balance should be 0
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "0.000000" {
		t.Errorf("expected available 0.000000 after reversal, got %s", bal.Available)
	}
}

func TestReversal_ReverseSpend(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	l := New(store)

	agent := "0x1234567890123456789012345678901234567890"

	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")
	_ = l.Spend(ctx, agent, "3.000000", "sk_1")

	// Find the spend entry
	entries, _ := l.GetHistory(ctx, agent, 10)
	var spendEntryID string
	for _, e := range entries {
		if e.Type == "spend" {
			spendEntryID = e.ID
			break
		}
	}
	if spendEntryID == "" {
		t.Fatal("spend entry not found")
	}

	// Reverse the spend
	err := l.Reverse(ctx, spendEntryID, "incorrect charge", "admin_1")
	if err != nil {
		t.Fatalf("Reverse failed: %v", err)
	}

	// Balance should be back to 10
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "10.000000" {
		t.Errorf("expected available 10.000000 after spend reversal, got %s", bal.Available)
	}
}

func TestReversal_DoubleReversePrevention(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	l := New(store)

	agent := "0x1234567890123456789012345678901234567890"

	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")

	entries, _ := l.GetHistory(ctx, agent, 10)
	var depositID string
	for _, e := range entries {
		if e.Type == "deposit" {
			depositID = e.ID
			break
		}
	}

	// First reversal should succeed
	err := l.Reverse(ctx, depositID, "reason1", "admin_1")
	if err != nil {
		t.Fatalf("First reverse failed: %v", err)
	}

	// Second reversal should fail
	err = l.Reverse(ctx, depositID, "reason2", "admin_1")
	if err != ErrAlreadyReversed {
		t.Errorf("expected ErrAlreadyReversed, got %v", err)
	}
}

func TestReversal_NotFound(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	l := New(store)

	err := l.Reverse(ctx, "nonexistent_entry", "reason", "admin_1")
	if err != ErrEntryNotFound {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}
}

func TestReversal_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	l := New(store)

	agent := "0x1234567890123456789012345678901234567890"

	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")
	_ = l.Spend(ctx, agent, "8.000000", "sk_1") // Only 2.000000 remaining

	// Find the deposit entry
	entries, _ := l.GetHistory(ctx, agent, 10)
	var depositID string
	for _, e := range entries {
		if e.Type == "deposit" {
			depositID = e.ID
			break
		}
	}

	// Reversing the deposit should fail (insufficient balance to debit)
	err := l.Reverse(ctx, depositID, "reason", "admin_1")
	if err != ErrInsufficientBalance {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestReversal_ReversalEntry(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	l := New(store)

	agent := "0x1234567890123456789012345678901234567890"

	_ = l.Deposit(ctx, agent, "10.000000", "0xtx1")
	_ = l.Spend(ctx, agent, "3.000000", "sk_1")

	entries, _ := l.GetHistory(ctx, agent, 10)
	var spendID string
	for _, e := range entries {
		if e.Type == "spend" {
			spendID = e.ID
			break
		}
	}

	_ = l.Reverse(ctx, spendID, "incorrect", "admin_1")

	// Check that a reversal entry was created
	allEntries, _ := l.GetHistory(ctx, agent, 50)
	var found bool
	for _, e := range allEntries {
		if e.ReversalOf == spendID {
			found = true
			if e.Type != "reversal_spend" {
				t.Errorf("expected type 'reversal_spend', got %q", e.Type)
			}
			break
		}
	}
	if !found {
		t.Error("reversal entry not found in history")
	}
}
