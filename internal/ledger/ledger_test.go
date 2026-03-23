package ledger

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Tests

func TestLedger_Deposit(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	txHash := "0xabc123"

	// Deposit
	err := ledger.Deposit(ctx, agent, "10.00", txHash)
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	// Check balance
	bal, err := ledger.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000, got %s", bal.Available)
	}
}

func TestLedger_DuplicateDeposit(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	txHash := "0xabc123"

	// First deposit
	err := ledger.Deposit(ctx, agent, "10.00", txHash)
	if err != nil {
		t.Fatalf("First deposit failed: %v", err)
	}

	// Duplicate deposit should fail
	err = ledger.Deposit(ctx, agent, "10.00", txHash)
	if err != ErrDuplicateDeposit {
		t.Errorf("Expected ErrDuplicateDeposit, got %v", err)
	}
}

func TestLedger_Spend(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit first
	err := ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	// Spend
	err = ledger.Spend(ctx, agent, "3.50", "sk_123")
	if err != nil {
		t.Fatalf("Spend failed: %v", err)
	}

	// Check balance
	bal, err := ledger.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "6.500000" {
		t.Errorf("Expected available 6.500000, got %s", bal.Available)
	}
}

func TestLedger_SpendInsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit first
	err := ledger.Deposit(ctx, agent, "5.00", "0xtx1")
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	// Try to spend more than available
	err = ledger.Spend(ctx, agent, "10.00", "sk_123")
	if err != ErrInsufficientBalance {
		t.Errorf("Expected ErrInsufficientBalance, got %v", err)
	}
}

func TestLedger_CanSpend(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit
	ledger.Deposit(ctx, agent, "10.00", "0xtx1")

	// Can spend less than balance
	canSpend, err := ledger.CanSpend(ctx, agent, "5.00")
	if err != nil {
		t.Fatalf("CanSpend failed: %v", err)
	}
	if !canSpend {
		t.Error("Expected CanSpend to return true")
	}

	// Cannot spend more than balance
	canSpend, err = ledger.CanSpend(ctx, agent, "15.00")
	if err != nil {
		t.Fatalf("CanSpend failed: %v", err)
	}
	if canSpend {
		t.Error("Expected CanSpend to return false")
	}
}

func TestLedger_Withdraw(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Deposit
	ledger.Deposit(ctx, agent, "10.00", "0xtx1")

	// Withdraw
	err := ledger.Withdraw(ctx, agent, "4.00", "0xwithdraw1")
	if err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}

	// Check balance
	bal, err := ledger.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "6.000000" {
		t.Errorf("Expected available 6.000000, got %s", bal.Available)
	}
}

func TestLedger_GetHistory(t *testing.T) {
	store := NewMemoryStore()
	ledger := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"

	// Multiple operations
	ledger.Deposit(ctx, agent, "10.00", "0xtx1")
	ledger.Spend(ctx, agent, "2.00", "sk_1")
	ledger.Spend(ctx, agent, "1.00", "sk_2")

	// Get history
	entries, err := ledger.GetHistory(ctx, agent, 10)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}
}

func TestLedger_HoldConfirmRelease(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"

	// Setup: deposit $10
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	// Hold $3 — should move from available to pending
	err := l.Hold(ctx, agent, "3.00", "sk_hold1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "7.000000" {
		t.Errorf("After hold: expected available 7.000000, got %s", bal.Available)
	}
	if bal.Pending != "3.000000" {
		t.Errorf("After hold: expected pending 3.000000, got %s", bal.Pending)
	}

	// Confirm hold — should move from pending to total_out
	err = l.ConfirmHold(ctx, agent, "3.00", "sk_hold1")
	if err != nil {
		t.Fatalf("ConfirmHold failed: %v", err)
	}
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "7.000000" {
		t.Errorf("After confirm: expected available 7.000000, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("After confirm: expected pending 0.000000, got %s", bal.Pending)
	}
	if bal.TotalOut != "3.000000" {
		t.Errorf("After confirm: expected total_out 3.000000, got %s", bal.TotalOut)
	}

	// Hold $5 then release — should return to available
	l.Hold(ctx, agent, "5.00", "sk_hold2")
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "2.000000" {
		t.Errorf("After second hold: expected available 2.000000, got %s", bal.Available)
	}

	err = l.ReleaseHold(ctx, agent, "5.00", "sk_hold2")
	if err != nil {
		t.Fatalf("ReleaseHold failed: %v", err)
	}
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "7.000000" {
		t.Errorf("After release: expected available 7.000000, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("After release: expected pending 0.000000, got %s", bal.Pending)
	}
}

func TestLedger_HoldInsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"

	l.Deposit(ctx, agent, "5.00", "0xtx1")

	// Try to hold more than available
	err := l.Hold(ctx, agent, "10.00", "sk_big")
	if err == nil {
		t.Error("Expected error when holding more than available balance")
	}

	// Balance should be unchanged
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "5.000000" {
		t.Errorf("Balance should be unchanged, got %s", bal.Available)
	}
}

func TestLedger_EscrowLockAndRelease(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0x1111111111111111111111111111111111111111"
	seller := "0x2222222222222222222222222222222222222222"

	// Setup: deposit $10 to buyer
	l.Deposit(ctx, buyer, "10.00", "0xtx_buyer")

	// Lock $3 in escrow
	err := l.EscrowLock(ctx, buyer, "3.00", "esc_1")
	if err != nil {
		t.Fatalf("EscrowLock failed: %v", err)
	}
	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "7.000000" {
		t.Errorf("After escrow lock: expected available 7.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("After escrow lock: expected escrowed 3.000000, got %s", bal.Escrowed)
	}

	// Release escrow to seller
	err = l.ReleaseEscrow(ctx, buyer, seller, "3.00", "esc_1")
	if err != nil {
		t.Fatalf("ReleaseEscrow failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "7.000000" {
		t.Errorf("Buyer after release: expected available 7.000000, got %s", buyerBal.Available)
	}
	if buyerBal.Escrowed != "0.000000" {
		t.Errorf("Buyer after release: expected escrowed 0.000000, got %s", buyerBal.Escrowed)
	}
	if buyerBal.TotalOut != "3.000000" {
		t.Errorf("Buyer after release: expected totalOut 3.000000, got %s", buyerBal.TotalOut)
	}

	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "3.000000" {
		t.Errorf("Seller after release: expected available 3.000000, got %s", sellerBal.Available)
	}
}

func TestLedger_EscrowLockAndRefund(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0x1111111111111111111111111111111111111111"

	// Setup: deposit $10
	l.Deposit(ctx, buyer, "10.00", "0xtx1")

	// Lock $5 in escrow
	err := l.EscrowLock(ctx, buyer, "5.00", "esc_2")
	if err != nil {
		t.Fatalf("EscrowLock failed: %v", err)
	}

	// Refund (dispute path)
	err = l.RefundEscrow(ctx, buyer, "5.00", "esc_2")
	if err != nil {
		t.Fatalf("RefundEscrow failed: %v", err)
	}

	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "10.000000" {
		t.Errorf("After refund: expected available 10.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("After refund: expected escrowed 0.000000, got %s", bal.Escrowed)
	}
}

func TestLedger_EscrowInsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0x1234567890123456789012345678901234567890"

	l.Deposit(ctx, agent, "5.00", "0xtx1")

	// Try to lock more than available
	err := l.EscrowLock(ctx, agent, "10.00", "esc_big")
	if err == nil {
		t.Error("Expected error when escrowing more than available balance")
	}

	// Balance should be unchanged
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "5.000000" {
		t.Errorf("Balance should be unchanged, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" && bal.Escrowed != "0" {
		t.Errorf("Escrowed should be 0, got %s", bal.Escrowed)
	}
}

// ---------------------------------------------------------------------------
// Escrow edge cases: fund conservation
// ---------------------------------------------------------------------------

func TestLedger_EscrowFundConservation(t *testing.T) {
	// Verify no money is created or destroyed in the escrow cycle.
	// totalIn - totalOut = available + pending + escrowed (for buyer)
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seller := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Deposit $20 to buyer
	l.Deposit(ctx, buyer, "20.00", "0xtx1")

	// Lock $7 in escrow
	l.EscrowLock(ctx, buyer, "7.00", "esc1")

	// Verify fund conservation: 20 = 13 + 0 + 7
	bal, _ := l.GetBalance(ctx, buyer)
	assertFundConservation(t, bal, "after escrow lock")

	// Release $7 from escrow to seller
	l.ReleaseEscrow(ctx, buyer, seller, "7.00", "esc1")

	// Buyer: totalIn=20, totalOut=7, available=13, pending=0, escrowed=0 → 20-7=13 ✓
	bal, _ = l.GetBalance(ctx, buyer)
	assertFundConservation(t, bal, "buyer after release")

	if bal.Available != "13.000000" {
		t.Errorf("Buyer available after release: expected 13.000000, got %s", bal.Available)
	}

	// Seller: totalIn=7, totalOut=0, available=7, pending=0, escrowed=0 → 7-0=7 ✓
	sellerBal, _ := l.GetBalance(ctx, seller)
	assertFundConservation(t, sellerBal, "seller after release")

	if sellerBal.Available != "7.000000" {
		t.Errorf("Seller available: expected 7.000000, got %s", sellerBal.Available)
	}
}

func TestLedger_EscrowRefundFundConservation(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, buyer, "15.00", "0xtx1")

	// Lock, then refund
	l.EscrowLock(ctx, buyer, "6.00", "esc1")
	l.RefundEscrow(ctx, buyer, "6.00", "esc1")

	// Should be back to full amount
	bal, _ := l.GetBalance(ctx, buyer)
	assertFundConservation(t, bal, "after lock+refund")

	if bal.Available != "15.000000" {
		t.Errorf("Available should be back to 15.000000 after refund, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Escrowed should be 0.000000 after refund, got %s", bal.Escrowed)
	}
}

func TestLedger_EscrowAndHoldCoexistence(t *testing.T) {
	// Verify that escrow and hold (two-phase on-chain) don't interfere
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "20.00", "0xtx1")

	// Hold $5 for an on-chain transfer
	l.Hold(ctx, agent, "5.00", "hold1")

	// Lock $3 in escrow
	l.EscrowLock(ctx, agent, "3.00", "esc1")

	// Should have: available=12, pending=5, escrowed=3
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "12.000000" {
		t.Errorf("Expected available 12.000000, got %s", bal.Available)
	}
	if bal.Pending != "5.000000" {
		t.Errorf("Expected pending 5.000000, got %s", bal.Pending)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("Expected escrowed 3.000000, got %s", bal.Escrowed)
	}

	// Confirm the hold (on-chain transfer done)
	l.ConfirmHold(ctx, agent, "5.00", "hold1")

	// available=12, pending=0, escrowed=3, totalOut=5
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "12.000000" {
		t.Errorf("Expected available 12.000000 after confirm hold, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("Expected pending 0.000000 after confirm hold, got %s", bal.Pending)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("Expected escrowed 3.000000 (unchanged), got %s", bal.Escrowed)
	}
	if bal.TotalOut != "5.000000" {
		t.Errorf("Expected totalOut 5.000000, got %s", bal.TotalOut)
	}

	// Refund the escrow
	l.RefundEscrow(ctx, agent, "3.00", "esc1")

	// available=15, pending=0, escrowed=0, totalOut=5, totalIn=20
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "15.000000" {
		t.Errorf("Expected available 15.000000 after escrow refund, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000 after refund, got %s", bal.Escrowed)
	}
}

func TestLedger_EscrowMultiplePartialOperations(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seller1 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller2 := "0xcccccccccccccccccccccccccccccccccccccccc"

	l.Deposit(ctx, buyer, "10.00", "0xtx1")

	// Lock $3 + $4 in two separate escrows
	l.EscrowLock(ctx, buyer, "3.00", "esc1")
	l.EscrowLock(ctx, buyer, "4.00", "esc2")

	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "3.000000" {
		t.Errorf("Expected available 3.000000 after two locks, got %s", bal.Available)
	}
	if bal.Escrowed != "7.000000" {
		t.Errorf("Expected escrowed 7.000000, got %s", bal.Escrowed)
	}

	// Release first escrow to seller1
	l.ReleaseEscrow(ctx, buyer, seller1, "3.00", "esc1")

	bal, _ = l.GetBalance(ctx, buyer)
	if bal.Available != "3.000000" {
		t.Errorf("Expected available 3.000000 after partial release, got %s", bal.Available)
	}
	if bal.Escrowed != "4.000000" {
		t.Errorf("Expected escrowed 4.000000, got %s", bal.Escrowed)
	}

	// Release second escrow to seller2
	l.ReleaseEscrow(ctx, buyer, seller2, "4.00", "esc2")

	bal, _ = l.GetBalance(ctx, buyer)
	if bal.Available != "3.000000" {
		t.Errorf("Expected available 3.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", bal.Escrowed)
	}
	if bal.TotalOut != "7.000000" {
		t.Errorf("Expected totalOut 7.000000, got %s", bal.TotalOut)
	}
	assertFundConservation(t, bal, "buyer after all releases")

	// Verify sellers got their money
	s1Bal, _ := l.GetBalance(ctx, seller1)
	if s1Bal.Available != "3.000000" {
		t.Errorf("Seller1 available: expected 3.000000, got %s", s1Bal.Available)
	}
	s2Bal, _ := l.GetBalance(ctx, seller2)
	if s2Bal.Available != "4.000000" {
		t.Errorf("Seller2 available: expected 4.000000, got %s", s2Bal.Available)
	}
}

func TestLedger_EscrowRefundMoreThanEscrowed(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.EscrowLock(ctx, agent, "3.00", "esc1")

	// Try to refund more than escrowed
	err := l.RefundEscrow(ctx, agent, "5.00", "esc1")
	if err == nil {
		t.Error("Expected error when refunding more than escrowed")
	}

	// Balance should be unchanged
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "7.000000" {
		t.Errorf("Available should be unchanged at 7.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("Escrowed should be unchanged at 3.000000, got %s", bal.Escrowed)
	}
}

func TestLedger_EscrowReleaseMoreThanEscrowed(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seller := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, buyer, "10.00", "0xtx1")
	l.EscrowLock(ctx, buyer, "3.00", "esc1")

	// Try to release more than escrowed
	err := l.ReleaseEscrow(ctx, buyer, seller, "5.00", "esc1")
	if err == nil {
		t.Error("Expected error when releasing more than escrowed")
	}

	// Balance should be unchanged
	bal, _ := l.GetBalance(ctx, buyer)
	if bal.Available != "7.000000" {
		t.Errorf("Available should be unchanged at 7.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("Escrowed should be unchanged at 3.000000, got %s", bal.Escrowed)
	}
}

func TestLedger_EscrowLockNonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	err := l.EscrowLock(ctx, "0xghost", "1.00", "esc1")
	if err == nil {
		t.Error("Expected error when escrowing for nonexistent agent")
	}
}

func TestLedger_EscrowLockInvalidAmount(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	// Zero amount
	err := l.EscrowLock(ctx, "0xagent", "0", "esc1")
	if err == nil {
		t.Error("Expected error for zero escrow amount")
	}

	// Negative amount
	err = l.EscrowLock(ctx, "0xagent", "-1.00", "esc1")
	if err == nil {
		t.Error("Expected error for negative escrow amount")
	}
}

func TestLedger_EscrowLockEntireBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "5.00", "0xtx1")

	// Lock entire balance
	err := l.EscrowLock(ctx, agent, "5.00", "esc1")
	if err != nil {
		t.Fatalf("Locking entire balance should work: %v", err)
	}

	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "5.000000" {
		t.Errorf("Expected escrowed 5.000000, got %s", bal.Escrowed)
	}

	// Should not be able to spend or lock more
	err = l.EscrowLock(ctx, agent, "0.01", "esc2")
	if err == nil {
		t.Error("Expected error when no available balance left")
	}
}

func TestLedger_EscrowHistoryEntries(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seller := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, buyer, "10.00", "0xtx1")
	l.EscrowLock(ctx, buyer, "3.00", "esc1")
	l.ReleaseEscrow(ctx, buyer, seller, "3.00", "esc1")

	entries, err := l.GetHistory(ctx, buyer, 100)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	// Should have: deposit, escrow_lock, escrow_release (3 entries, reverse order)
	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}
	// Most recent first
	if entries[0].Type != "escrow_release" {
		t.Errorf("Expected escrow_release entry, got %s", entries[0].Type)
	}
	if entries[1].Type != "escrow_lock" {
		t.Errorf("Expected escrow_lock entry, got %s", entries[1].Type)
	}
	if entries[2].Type != "deposit" {
		t.Errorf("Expected deposit entry, got %s", entries[2].Type)
	}
}

// assertFundConservation delegates to the production CheckInvariant function.
func assertFundConservation(t *testing.T, bal *Balance, ctx string) {
	t.Helper()
	if err := CheckInvariant(bal); err != nil {
		t.Errorf("%s: %v (A=%s P=%s E=%s In=%s Out=%s Credit=%s)",
			ctx, err, bal.Available, bal.Pending, bal.Escrowed,
			bal.TotalIn, bal.TotalOut, bal.CreditUsed)
	}
}

// ---------------------------------------------------------------------------
// More escrow edge cases
// ---------------------------------------------------------------------------

func TestLedger_EscrowReleaseSameSellerTwice(t *testing.T) {
	// Verify that releasing to the same seller accumulates correctly
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	buyer := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seller := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, buyer, "10.00", "0xtx1")

	l.EscrowLock(ctx, buyer, "3.00", "esc1")
	l.EscrowLock(ctx, buyer, "2.00", "esc2")

	l.ReleaseEscrow(ctx, buyer, seller, "3.00", "esc1")
	l.ReleaseEscrow(ctx, buyer, seller, "2.00", "esc2")

	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "5.000000" {
		t.Errorf("Seller should have 5.000000 after two releases, got %s", sellerBal.Available)
	}
	if sellerBal.TotalIn != "5.000000" {
		t.Errorf("Seller totalIn should be 5.000000, got %s", sellerBal.TotalIn)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "5.000000" {
		t.Errorf("Buyer should have 5.000000, got %s", buyerBal.Available)
	}
	if buyerBal.TotalOut != "5.000000" {
		t.Errorf("Buyer totalOut should be 5.000000, got %s", buyerBal.TotalOut)
	}
	assertFundConservation(t, buyerBal, "buyer after two releases")
	assertFundConservation(t, sellerBal, "seller after two releases")
}

func TestLedger_EscrowReleaseToSelf(t *testing.T) {
	// Edge case: buyer releases to themselves (same address)
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.EscrowLock(ctx, agent, "3.00", "esc1")

	// Release to self
	err := l.ReleaseEscrow(ctx, agent, agent, "3.00", "esc1")
	if err != nil {
		t.Fatalf("ReleaseEscrow to self failed: %v", err)
	}

	// escrowed should be 0, available should be original minus escrow plus release
	// Lock: available 10->7, escrowed 0->3
	// Release: escrowed 3->0, totalOut +=3, THEN credit self: available 7->10, totalIn +=3
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Escrowed != "0.000000" {
		t.Errorf("Expected escrowed 0.000000, got %s", bal.Escrowed)
	}
	// After release to self: totalIn=10+3=13, totalOut=3, available=10
	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000 after self-release, got %s", bal.Available)
	}
	assertFundConservation(t, bal, "after self-release")
}

func TestLedger_RefundEscrowNonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	err := l.RefundEscrow(ctx, "0xghost", "1.00", "esc1")
	if err == nil {
		t.Error("Expected error when refunding nonexistent agent")
	}
}

func TestLedger_ReleaseEscrowNonexistentBuyer(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	err := l.ReleaseEscrow(ctx, "0xghost_buyer", "0xghost_seller", "1.00", "esc1")
	if err == nil {
		t.Error("Expected error when releasing escrow for nonexistent buyer")
	}
}

func TestLedger_EscrowLockCaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	// Deposit with lowercase
	l.Deposit(ctx, "0xAABBCCDDEEFF00112233445566778899aabbccdd", "10.00", "0xtx1")

	// Lock with same address (Ledger lowercases it)
	err := l.EscrowLock(ctx, "0xAABBCCDDEEFF00112233445566778899AABBCCDD", "3.00", "esc1")
	if err != nil {
		t.Fatalf("EscrowLock with mixed case should work: %v", err)
	}

	bal, _ := l.GetBalance(ctx, "0xaabbccddeeff00112233445566778899aabbccdd")
	if bal.Escrowed != "3.000000" {
		t.Errorf("Expected escrowed 3.000000, got %s", bal.Escrowed)
	}
}

func TestLedger_EscrowGetBalanceShowsEscrowed(t *testing.T) {
	// Verify GetBalance returns the escrowed field properly
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Before any deposit
	bal, err := l.GetBalance(ctx, agent)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}
	if bal.Escrowed != "0" {
		t.Errorf("Expected escrowed 0 for new agent, got %s", bal.Escrowed)
	}

	// After deposit + escrow lock
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.EscrowLock(ctx, agent, "4.00", "esc1")

	bal, _ = l.GetBalance(ctx, agent)
	if bal.Escrowed != "4.000000" {
		t.Errorf("Expected escrowed 4.000000, got %s", bal.Escrowed)
	}
	if bal.Available != "6.000000" {
		t.Errorf("Expected available 6.000000, got %s", bal.Available)
	}
}

func TestLedger_EscrowRefundHistory(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.EscrowLock(ctx, agent, "3.00", "esc1")
	l.RefundEscrow(ctx, agent, "3.00", "esc1")

	entries, _ := l.GetHistory(ctx, agent, 100)
	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}

	// Most recent first
	if entries[0].Type != "escrow_refund" {
		t.Errorf("Expected escrow_refund, got %s", entries[0].Type)
	}
	if entries[1].Type != "escrow_lock" {
		t.Errorf("Expected escrow_lock, got %s", entries[1].Type)
	}
}

func TestLedger_EscrowAndSpendCombined(t *testing.T) {
	// Test that escrow and regular spend work together correctly
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seller := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, agent, "20.00", "0xtx1")

	// Regular spend
	l.Spend(ctx, agent, "5.00", "sk_1")

	// Escrow lock
	l.EscrowLock(ctx, agent, "3.00", "esc1")

	// Hold (for on-chain transfer)
	l.Hold(ctx, agent, "2.00", "hold1")

	// available = 20 - 5 - 3 - 2 = 10
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000, got %s", bal.Available)
	}
	if bal.Escrowed != "3.000000" {
		t.Errorf("Expected escrowed 3.000000, got %s", bal.Escrowed)
	}
	if bal.Pending != "2.000000" {
		t.Errorf("Expected pending 2.000000, got %s", bal.Pending)
	}

	// Can't escrow more than available (even though total balance > amount)
	err := l.EscrowLock(ctx, agent, "11.00", "esc2")
	if err == nil {
		t.Error("Expected error when escrowing more than available")
	}

	// Release escrow
	l.ReleaseEscrow(ctx, agent, seller, "3.00", "esc1")

	// Confirm hold
	l.ConfirmHold(ctx, agent, "2.00", "hold1")

	// Final: available=10, pending=0, escrowed=0, totalOut=5+3+2=10
	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000, got %s", bal.Available)
	}
	if bal.TotalOut != "10.000000" {
		t.Errorf("Expected totalOut 10.000000, got %s", bal.TotalOut)
	}
	assertFundConservation(t, bal, "after combined operations")
}

// ---------------------------------------------------------------------------
// Hold + Credit draw tracking
// ---------------------------------------------------------------------------

func TestLedger_HoldWithCreditDraw(t *testing.T) {
	// When available < hold amount, the gap is drawn from credit.
	// ReleaseHold must reverse the credit draw, not return the full amount to available.
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Setup: $3 available + $10 credit line
	l.Deposit(ctx, agent, "3.00", "0xtx1")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Hold $5 — should take $3 from available, $2 from credit
	err := l.Hold(ctx, agent, "5.00", "hold_credit")
	if err != nil {
		t.Fatalf("Hold with credit failed: %v", err)
	}

	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0.000000, got %s", bal.Available)
	}
	if bal.Pending != "5.000000" {
		t.Errorf("Expected pending 5.000000, got %s", bal.Pending)
	}
	if bal.CreditUsed != "2.000000" {
		t.Errorf("Expected creditUsed 2.000000, got %s", bal.CreditUsed)
	}

	// Release the hold — credit draw of $2 must be reversed
	err = l.ReleaseHold(ctx, agent, "5.00", "hold_credit")
	if err != nil {
		t.Fatalf("ReleaseHold failed: %v", err)
	}

	bal, _ = l.GetBalance(ctx, agent)
	if bal.Available != "3.000000" {
		t.Errorf("After release: expected available 3.000000, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("After release: expected pending 0.000000, got %s", bal.Pending)
	}
	if bal.CreditUsed != "0.000000" {
		t.Errorf("After release: expected creditUsed 0.000000, got %s", bal.CreditUsed)
	}
	assertFundConservation(t, bal, "after release with credit reversal")
}

func TestLedger_ConfirmHoldCleansUpCreditTracking(t *testing.T) {
	// ConfirmHold should clean up the credit draw tracking entry.
	// A subsequent ReleaseHold with the same ref should not crash.
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "2.00", "0xtx1")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Hold $5 ($2 avail, $3 credit)
	l.Hold(ctx, agent, "5.00", "hold_confirm")
	bal, _ := l.GetBalance(ctx, agent)
	if bal.CreditUsed != "3.000000" {
		t.Fatalf("Expected creditUsed 3.000000, got %s", bal.CreditUsed)
	}

	// Confirm — credit stays used, tracking cleaned up
	err := l.ConfirmHold(ctx, agent, "5.00", "hold_confirm")
	if err != nil {
		t.Fatalf("ConfirmHold failed: %v", err)
	}

	bal, _ = l.GetBalance(ctx, agent)
	if bal.CreditUsed != "3.000000" {
		t.Errorf("CreditUsed should remain 3.000000 after confirm, got %s", bal.CreditUsed)
	}
	if bal.TotalOut != "5.000000" {
		t.Errorf("TotalOut should be 5.000000, got %s", bal.TotalOut)
	}
}

// ---------------------------------------------------------------------------
// Refund edge cases
// ---------------------------------------------------------------------------

func TestLedger_RefundTotalOutUnderflow(t *testing.T) {
	// Refund should not make TotalOut negative.
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Deposit $10, spend $2, then refund $5 (more than totalOut)
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.Spend(ctx, agent, "2.00", "sk_1")

	// TotalOut is now 2.00
	bal, _ := l.GetBalance(ctx, agent)
	if bal.TotalOut != "2.000000" {
		t.Fatalf("Expected TotalOut 2.000000, got %s", bal.TotalOut)
	}

	// Refund $5 — should cap totalOut reduction at 2, not go negative
	err := l.Refund(ctx, agent, "5.00", "ref_overflow")
	if err != nil {
		t.Fatalf("Refund failed: %v", err)
	}

	bal, _ = l.GetBalance(ctx, agent)
	totalOut, _ := usdc.Parse(bal.TotalOut)
	if totalOut.Sign() < 0 {
		t.Errorf("TotalOut should not be negative, got %s", bal.TotalOut)
	}
}

func TestLedger_RefundIdempotent(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()
	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.Spend(ctx, agent, "3.00", "sk_1")

	// First refund should succeed
	err := l.Refund(ctx, agent, "3.00", "ref_dup")
	if err != nil {
		t.Fatalf("First refund failed: %v", err)
	}

	// Second refund with same reference should fail
	err = l.Refund(ctx, agent, "3.00", "ref_dup")
	if err != ErrDuplicateRefund {
		t.Errorf("Expected ErrDuplicateRefund, got: %v", err)
	}

	// Balance should only reflect one refund
	bal, _ := l.GetBalance(ctx, agent)
	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000 after one refund, got %s", bal.Available)
	}
}

func TestParseUSDC(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"1.00", "1000000"},
		{"0.50", "500000"},
		{"10", "10000000"},
		{"0.000001", "1"},
		{"100.123456", "100123456"},
	}

	for _, tt := range tests {
		result, ok := usdc.Parse(tt.input)
		if !ok {
			t.Errorf("usdc.Parse(%s) failed", tt.input)
			continue
		}
		if result.String() != tt.expected {
			t.Errorf("usdc.Parse(%s) = %s, want %s", tt.input, result.String(), tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// SettleHoldWithFee tests
// ---------------------------------------------------------------------------

func TestSettleHoldWithFee(t *testing.T) {
	tests := []struct {
		name                string
		depositAmount       string
		holdAmount          string
		sellerAmount        string
		feeAmount           string
		wantErr             error
		wantBuyerPending    string
		wantBuyerTotalOut   string
		wantSellerAvailable string
		wantPlatformAvail   string
	}{
		{
			name:                "basic_fee_split",
			depositAmount:       "10.00",
			holdAmount:          "5.00",
			sellerAmount:        "4.50",
			feeAmount:           "0.50",
			wantErr:             nil,
			wantBuyerPending:    "0.000000",
			wantBuyerTotalOut:   "5.000000",
			wantSellerAvailable: "4.500000",
			wantPlatformAvail:   "0.500000",
		},
		{
			name:                "zero_fee",
			depositAmount:       "10.00",
			holdAmount:          "5.00",
			sellerAmount:        "5.00",
			feeAmount:           "0.00",
			wantErr:             nil,
			wantBuyerPending:    "0.000000",
			wantBuyerTotalOut:   "5.000000",
			wantSellerAvailable: "5.000000",
			wantPlatformAvail:   "0.000000",
		},
		{
			name:          "insufficient_pending",
			depositAmount: "10.00",
			holdAmount:    "3.00",
			sellerAmount:  "4.00",
			feeAmount:     "1.00",
			wantErr:       ErrInsufficientBalance,
		},
		{
			name:                "fee_larger_than_price",
			depositAmount:       "10.00",
			holdAmount:          "5.00",
			sellerAmount:        "1.00",
			feeAmount:           "4.00",
			wantErr:             nil,
			wantBuyerPending:    "0.000000",
			wantBuyerTotalOut:   "5.000000",
			wantSellerAvailable: "1.000000",
			wantPlatformAvail:   "4.000000",
		},
		{
			name:                "first_settlement",
			depositAmount:       "20.00",
			holdAmount:          "6.00",
			sellerAmount:        "5.70",
			feeAmount:           "0.30",
			wantErr:             nil,
			wantBuyerPending:    "0.000000",
			wantBuyerTotalOut:   "6.000000",
			wantSellerAvailable: "5.700000",
			wantPlatformAvail:   "0.300000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			l := New(store)
			ctx := context.Background()

			buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			seller := "0xssssssssssssssssssssssssssssssssssssssss"
			platform := "0xpppppppppppppppppppppppppppppppppppppppp"

			// Setup: deposit to buyer
			err := l.Deposit(ctx, buyer, tt.depositAmount, "0xtx_buyer")
			if err != nil {
				t.Fatalf("Deposit failed: %v", err)
			}

			// Hold funds
			err = l.Hold(ctx, buyer, tt.holdAmount, "hold_ref1")
			if err != nil {
				t.Fatalf("Hold failed: %v", err)
			}

			// Settle hold with fee
			err = l.SettleHoldWithFee(ctx, buyer, seller, tt.sellerAmount, platform, tt.feeAmount, "hold_ref1")
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("Expected error %v, got nil", tt.wantErr)
				}
				if err != tt.wantErr {
					t.Errorf("Expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SettleHoldWithFee failed: %v", err)
			}

			// Verify buyer balance
			buyerBal, _ := l.GetBalance(ctx, buyer)
			if buyerBal.Pending != tt.wantBuyerPending {
				t.Errorf("Buyer pending: expected %s, got %s", tt.wantBuyerPending, buyerBal.Pending)
			}
			if buyerBal.TotalOut != tt.wantBuyerTotalOut {
				t.Errorf("Buyer totalOut: expected %s, got %s", tt.wantBuyerTotalOut, buyerBal.TotalOut)
			}

			// Verify seller balance
			sellerBal, _ := l.GetBalance(ctx, seller)
			if sellerBal.Available != tt.wantSellerAvailable {
				t.Errorf("Seller available: expected %s, got %s", tt.wantSellerAvailable, sellerBal.Available)
			}

			// Verify platform balance
			platformBal, _ := l.GetBalance(ctx, platform)
			// Handle both "0" and "0.000000" format
			platformAvail := platformBal.Available
			if platformAvail == "0" && tt.wantPlatformAvail == "0.000000" {
				platformAvail = "0.000000"
			}
			if platformAvail != tt.wantPlatformAvail {
				t.Errorf("Platform available: expected %s, got %s", tt.wantPlatformAvail, platformBal.Available)
			}
		})
	}
}

func TestSettleHoldWithFee_MultipleSettlements(t *testing.T) {
	// Verify that multiple sequential settlements accumulate correctly
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"
	platform := "0xpppppppppppppppppppppppppppppppppppppppp"

	// Setup: deposit $20 to buyer
	l.Deposit(ctx, buyer, "20.00", "0xtx1")

	// First settlement: hold $6, settle with $5.70 to seller, $0.30 fee
	l.Hold(ctx, buyer, "6.00", "hold1")
	err := l.SettleHoldWithFee(ctx, buyer, seller, "5.70", platform, "0.30", "hold1")
	if err != nil {
		t.Fatalf("First settlement failed: %v", err)
	}

	// Second settlement: hold $4, settle with $3.60 to seller, $0.40 fee
	l.Hold(ctx, buyer, "4.00", "hold2")
	err = l.SettleHoldWithFee(ctx, buyer, seller, "3.60", platform, "0.40", "hold2")
	if err != nil {
		t.Fatalf("Second settlement failed: %v", err)
	}

	// Verify final balances
	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "10.000000" {
		t.Errorf("Buyer available: expected 10.000000, got %s", buyerBal.Available)
	}
	if buyerBal.Pending != "0.000000" {
		t.Errorf("Buyer pending: expected 0.000000, got %s", buyerBal.Pending)
	}
	if buyerBal.TotalOut != "10.000000" {
		t.Errorf("Buyer totalOut: expected 10.000000, got %s", buyerBal.TotalOut)
	}

	// Seller should have received cumulative $5.70 + $3.60 = $9.30
	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "9.300000" {
		t.Errorf("Seller available: expected 9.300000, got %s", sellerBal.Available)
	}
	if sellerBal.TotalIn != "9.300000" {
		t.Errorf("Seller totalIn: expected 9.300000, got %s", sellerBal.TotalIn)
	}

	// Platform should have received cumulative $0.30 + $0.40 = $0.70
	platformBal, _ := l.GetBalance(ctx, platform)
	if platformBal.Available != "0.700000" {
		t.Errorf("Platform available: expected 0.700000, got %s", platformBal.Available)
	}
	if platformBal.TotalIn != "0.700000" {
		t.Errorf("Platform totalIn: expected 0.700000, got %s", platformBal.TotalIn)
	}

	// Verify fund conservation
	assertFundConservation(t, buyerBal, "buyer after two settlements")
	assertFundConservation(t, sellerBal, "seller after two settlements")
	assertFundConservation(t, platformBal, "platform after two settlements")
}

func TestSettleHoldWithFee_FundConservation(t *testing.T) {
	// Verify no money is created or destroyed in the fee settlement
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"
	platform := "0xpppppppppppppppppppppppppppppppppppppppp"

	l.Deposit(ctx, buyer, "100.00", "0xtx1")
	l.Hold(ctx, buyer, "15.00", "hold1")

	// Before settlement: get all balances
	buyerBefore, _ := l.GetBalance(ctx, buyer)
	sellerBefore, _ := l.GetBalance(ctx, seller)
	platformBefore, _ := l.GetBalance(ctx, platform)

	totalAvailBefore, _ := usdc.Parse(buyerBefore.Available)
	totalAvailBefore.Add(totalAvailBefore, mustParse(sellerBefore.Available))
	totalAvailBefore.Add(totalAvailBefore, mustParse(platformBefore.Available))

	totalPendingBefore, _ := usdc.Parse(buyerBefore.Pending)
	totalPendingBefore.Add(totalPendingBefore, mustParse(sellerBefore.Pending))
	totalPendingBefore.Add(totalPendingBefore, mustParse(platformBefore.Pending))

	// Settle: $14.25 to seller, $0.75 fee
	err := l.SettleHoldWithFee(ctx, buyer, seller, "14.25", platform, "0.75", "hold1")
	if err != nil {
		t.Fatalf("SettleHoldWithFee failed: %v", err)
	}

	// After settlement: get all balances
	buyerAfter, _ := l.GetBalance(ctx, buyer)
	sellerAfter, _ := l.GetBalance(ctx, seller)
	platformAfter, _ := l.GetBalance(ctx, platform)

	totalAvailAfter, _ := usdc.Parse(buyerAfter.Available)
	totalAvailAfter.Add(totalAvailAfter, mustParse(sellerAfter.Available))
	totalAvailAfter.Add(totalAvailAfter, mustParse(platformAfter.Available))

	totalPendingAfter, _ := usdc.Parse(buyerAfter.Pending)
	totalPendingAfter.Add(totalPendingAfter, mustParse(sellerAfter.Pending))
	totalPendingAfter.Add(totalPendingAfter, mustParse(platformAfter.Pending))

	// Pending should decrease by $15.00, available should increase by $15.00
	pendingDelta := new(big.Int).Sub(totalPendingBefore, totalPendingAfter)
	availDelta := new(big.Int).Sub(totalAvailAfter, totalAvailBefore)

	if pendingDelta.Cmp(mustParse("15.00")) != 0 {
		t.Errorf("Expected pending to decrease by 15.00, got %s", usdc.Format(pendingDelta))
	}
	if availDelta.Cmp(mustParse("15.00")) != 0 {
		t.Errorf("Expected available to increase by 15.00, got %s", usdc.Format(availDelta))
	}

	// Verify individual fund conservation
	assertFundConservation(t, buyerAfter, "buyer")
	assertFundConservation(t, sellerAfter, "seller")
	assertFundConservation(t, platformAfter, "platform")
}

func TestSettleHoldWithFee_NonexistentBuyer(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	err := l.SettleHoldWithFee(ctx, "0xghost", "0xseller", "1.00", "0xplatform", "0.10", "ref1")
	if err == nil {
		t.Error("Expected error when settling for nonexistent buyer")
	}
}

func TestSettleHoldWithFee_NewSellerAndPlatform(t *testing.T) {
	// Verify that seller and platform balances are created if they don't exist
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xnew_seller_never_seen_before_1234567890"
	platform := "0xnew_platform_never_seen_before_1234567"

	l.Deposit(ctx, buyer, "10.00", "0xtx1")
	l.Hold(ctx, buyer, "5.00", "hold1")

	err := l.SettleHoldWithFee(ctx, buyer, seller, "4.75", platform, "0.25", "hold1")
	if err != nil {
		t.Fatalf("SettleHoldWithFee failed: %v", err)
	}

	// Seller should have $4.75 even though they never existed before
	sellerBal, _ := l.GetBalance(ctx, seller)
	if sellerBal.Available != "4.750000" {
		t.Errorf("New seller available: expected 4.750000, got %s", sellerBal.Available)
	}

	// Platform should have $0.25
	platformBal, _ := l.GetBalance(ctx, platform)
	if platformBal.Available != "0.250000" {
		t.Errorf("New platform available: expected 0.250000, got %s", platformBal.Available)
	}
}

func TestSettleHoldWithFee_HistoryEntries(t *testing.T) {
	// Verify correct ledger entries are created
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"
	platform := "0xpppppppppppppppppppppppppppppppppppppppp"

	l.Deposit(ctx, buyer, "10.00", "0xtx1")
	l.Hold(ctx, buyer, "5.00", "hold_ref_fee")
	l.SettleHoldWithFee(ctx, buyer, seller, "4.50", platform, "0.50", "hold_ref_fee")

	// Check buyer entries
	buyerEntries, _ := l.GetHistory(ctx, buyer, 100)
	// Expect 3 entries: deposit, hold, spend (most recent first)
	if len(buyerEntries) < 3 {
		t.Fatalf("Expected at least 3 buyer entries, got %d", len(buyerEntries))
	}
	if buyerEntries[0].Type != "spend" {
		t.Errorf("Expected most recent entry type 'spend', got %s", buyerEntries[0].Type)
	}
	if buyerEntries[0].Amount != "5.000000" {
		t.Errorf("Expected spend amount 5.000000 (total), got %s", buyerEntries[0].Amount)
	}

	// Check seller entries
	sellerEntries, _ := l.GetHistory(ctx, seller, 100)
	if len(sellerEntries) != 1 {
		t.Fatalf("Expected 1 seller entry, got %d", len(sellerEntries))
	}
	if sellerEntries[0].Type != "deposit" {
		t.Errorf("Expected seller entry type 'deposit', got %s", sellerEntries[0].Type)
	}
	// Amount may be stored in original format ("4.50") or normalized ("4.500000")
	sellerAmt, _ := usdc.Parse(sellerEntries[0].Amount)
	expectedSellerAmt, _ := usdc.Parse("4.50")
	if sellerAmt.Cmp(expectedSellerAmt) != 0 {
		t.Errorf("Expected seller deposit amount 4.50, got %s", sellerEntries[0].Amount)
	}

	// Check platform entries
	platformEntries, _ := l.GetHistory(ctx, platform, 100)
	if len(platformEntries) != 1 {
		t.Fatalf("Expected 1 platform entry, got %d", len(platformEntries))
	}
	if platformEntries[0].Type != "deposit" {
		t.Errorf("Expected platform entry type 'deposit', got %s", platformEntries[0].Type)
	}
	// Amount may be stored in original format ("0.50") or normalized ("0.500000")
	platformAmt, _ := usdc.Parse(platformEntries[0].Amount)
	expectedPlatformAmt, _ := usdc.Parse("0.50")
	if platformAmt.Cmp(expectedPlatformAmt) != 0 {
		t.Errorf("Expected platform deposit amount 0.50, got %s", platformEntries[0].Amount)
	}
}

// Helper function for parsing USDC amounts in tests
func mustParse(s string) *big.Int {
	val, ok := usdc.Parse(s)
	if !ok {
		panic("failed to parse USDC amount: " + s)
	}
	return val
}

// ---------------------------------------------------------------------------
// Transfer tests
// ---------------------------------------------------------------------------

func TestLedger_Transfer_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	from := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	to := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, from, "20.00", "0xtx1")

	err := l.Transfer(ctx, from, to, "7.00", "transfer_ref_1")
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	fromBal, _ := l.GetBalance(ctx, from)
	toBal, _ := l.GetBalance(ctx, to)

	if fromBal.Available != "13.000000" {
		t.Errorf("from available: expected 13.000000, got %s", fromBal.Available)
	}
	if fromBal.TotalOut != "7.000000" {
		t.Errorf("from totalOut: expected 7.000000, got %s", fromBal.TotalOut)
	}
	if toBal.Available != "7.000000" {
		t.Errorf("to available: expected 7.000000, got %s", toBal.Available)
	}
	if toBal.TotalIn != "7.000000" {
		t.Errorf("to totalIn: expected 7.000000, got %s", toBal.TotalIn)
	}

	assertFundConservation(t, fromBal, "transfer sender")
	assertFundConservation(t, toBal, "transfer receiver")
}

func TestLedger_Transfer_InsufficientBalance(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	from := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	to := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, from, "5.00", "0xtx1")

	err := l.Transfer(ctx, from, to, "10.00", "transfer_ref_1")
	if err == nil {
		t.Fatal("expected error for insufficient balance")
	}
}

func TestLedger_Transfer_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	tests := []struct {
		name   string
		amount string
	}{
		{"zero", "0"},
		{"negative", "-1.00"},
		{"invalid_string", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.Transfer(ctx, "0xfrom", "0xto", tt.amount, "ref")
			if err != ErrInvalidAmount {
				t.Errorf("expected ErrInvalidAmount, got %v", err)
			}
		})
	}
}

func TestLedger_Transfer_NonexistentSender(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.Transfer(ctx, "0xghost", "0xto", "1.00", "ref")
	if err == nil {
		t.Fatal("expected error for nonexistent sender")
	}
}

func TestLedger_Transfer_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	from := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	to := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, from, "20.00", "0xtx1")
	err := l.Transfer(ctx, from, to, "7.00", "transfer_ref")
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	// Events should include transfer_out and transfer_in
	fromEvents, _ := es.GetEvents(ctx, from, time.Time{})
	toEvents, _ := es.GetEvents(ctx, to, time.Time{})

	var foundOut, foundIn bool
	for _, e := range fromEvents {
		if e.EventType == "transfer_out" {
			foundOut = true
		}
	}
	for _, e := range toEvents {
		if e.EventType == "transfer_in" {
			foundIn = true
		}
	}

	if !foundOut {
		t.Error("expected transfer_out event for sender")
	}
	if !foundIn {
		t.Error("expected transfer_in event for receiver")
	}
}

// ---------------------------------------------------------------------------
// SettleHold tests (Ledger layer)
// ---------------------------------------------------------------------------

func TestLedger_SettleHold_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"

	l.Deposit(ctx, buyer, "20.00", "0xtx1")
	l.Hold(ctx, buyer, "8.00", "hold1")

	err := l.SettleHold(ctx, buyer, seller, "8.00", "hold1")
	if err != nil {
		t.Fatalf("SettleHold failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	sellerBal, _ := l.GetBalance(ctx, seller)

	if buyerBal.Pending != "0.000000" {
		t.Errorf("buyer pending: expected 0.000000, got %s", buyerBal.Pending)
	}
	if buyerBal.TotalOut != "8.000000" {
		t.Errorf("buyer totalOut: expected 8.000000, got %s", buyerBal.TotalOut)
	}
	if sellerBal.Available != "8.000000" {
		t.Errorf("seller available: expected 8.000000, got %s", sellerBal.Available)
	}
	if sellerBal.TotalIn != "8.000000" {
		t.Errorf("seller totalIn: expected 8.000000, got %s", sellerBal.TotalIn)
	}

	assertFundConservation(t, buyerBal, "settle hold buyer")
	assertFundConservation(t, sellerBal, "settle hold seller")
}

func TestLedger_SettleHold_InsufficientPending(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"

	l.Deposit(ctx, buyer, "20.00", "0xtx1")
	l.Hold(ctx, buyer, "3.00", "hold1")

	err := l.SettleHold(ctx, buyer, seller, "5.00", "hold1")
	if err == nil {
		t.Fatal("expected error for insufficient pending")
	}
}

func TestLedger_SettleHold_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.SettleHold(ctx, "0xbuyer", "0xseller", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount, got %v", err)
	}

	err = l.SettleHold(ctx, "0xbuyer", "0xseller", "-1", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for negative, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// PartialEscrowSettle tests
// ---------------------------------------------------------------------------

func TestLedger_PartialEscrowSettle_Success(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"

	l.Deposit(ctx, buyer, "20.00", "0xtx1")
	l.EscrowLock(ctx, buyer, "10.00", "esc1")

	// Settle: release $6 to seller, refund $4 to buyer
	err := l.PartialEscrowSettle(ctx, buyer, seller, "6.00", "4.00", "esc1")
	if err != nil {
		t.Fatalf("PartialEscrowSettle failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	sellerBal, _ := l.GetBalance(ctx, seller)

	if buyerBal.Escrowed != "0.000000" {
		t.Errorf("buyer escrowed: expected 0.000000, got %s", buyerBal.Escrowed)
	}
	if buyerBal.Available != "14.000000" {
		t.Errorf("buyer available: expected 14.000000 (10 remaining + 4 refund), got %s", buyerBal.Available)
	}
	if buyerBal.TotalOut != "6.000000" {
		t.Errorf("buyer totalOut: expected 6.000000, got %s", buyerBal.TotalOut)
	}
	if sellerBal.Available != "6.000000" {
		t.Errorf("seller available: expected 6.000000, got %s", sellerBal.Available)
	}
	if sellerBal.TotalIn != "6.000000" {
		t.Errorf("seller totalIn: expected 6.000000, got %s", sellerBal.TotalIn)
	}

	assertFundConservation(t, buyerBal, "partial escrow buyer")
	assertFundConservation(t, sellerBal, "partial escrow seller")
}

func TestLedger_PartialEscrowSettle_InsufficientEscrow(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"

	l.Deposit(ctx, buyer, "20.00", "0xtx1")
	l.EscrowLock(ctx, buyer, "5.00", "esc1")

	// Try to settle more than escrowed
	err := l.PartialEscrowSettle(ctx, buyer, seller, "4.00", "4.00", "esc1")
	if err == nil {
		t.Fatal("expected error for insufficient escrowed")
	}
}

func TestLedger_PartialEscrowSettle_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	// Both zero
	err := l.PartialEscrowSettle(ctx, "0xbuyer", "0xseller", "0", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero amounts, got %v", err)
	}

	// Negative
	err = l.PartialEscrowSettle(ctx, "0xbuyer", "0xseller", "-1", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for negative, got %v", err)
	}
}

func TestLedger_PartialEscrowSettle_FullRelease(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"

	l.Deposit(ctx, buyer, "20.00", "0xtx1")
	l.EscrowLock(ctx, buyer, "10.00", "esc1")

	// Full release, no refund
	err := l.PartialEscrowSettle(ctx, buyer, seller, "10.00", "0.00", "esc1")
	if err != nil {
		t.Fatalf("PartialEscrowSettle failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Escrowed != "0.000000" {
		t.Errorf("buyer escrowed: expected 0.000000, got %s", buyerBal.Escrowed)
	}
	if buyerBal.Available != "10.000000" {
		t.Errorf("buyer available: expected 10.000000, got %s", buyerBal.Available)
	}
}

func TestLedger_PartialEscrowSettle_FullRefund(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	buyer := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	seller := "0xssssssssssssssssssssssssssssssssssssssss"

	l.Deposit(ctx, buyer, "20.00", "0xtx1")
	l.EscrowLock(ctx, buyer, "10.00", "esc1")

	// Full refund, no release
	err := l.PartialEscrowSettle(ctx, buyer, seller, "0.00", "10.00", "esc1")
	if err != nil {
		t.Fatalf("PartialEscrowSettle failed: %v", err)
	}

	buyerBal, _ := l.GetBalance(ctx, buyer)
	if buyerBal.Available != "20.000000" {
		t.Errorf("buyer available: expected 20.000000, got %s", buyerBal.Available)
	}
}

// ---------------------------------------------------------------------------
// GetHistoryPage tests
// ---------------------------------------------------------------------------

func TestLedger_GetHistoryPage_DefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	entries, err := l.GetHistoryPage(ctx, agent, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("GetHistoryPage failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}
}

func TestLedger_GetHistoryPage_WithLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")
	l.Spend(ctx, agent, "1.00", "sk_1")
	l.Spend(ctx, agent, "2.00", "sk_2")

	entries, err := l.GetHistoryPage(ctx, agent, 2, time.Time{}, "")
	if err != nil {
		t.Fatalf("GetHistoryPage failed: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries with limit 2, got %d", len(entries))
	}
}

func TestLedger_GetHistoryPage_CaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0xAABBCCDDEEFF00112233445566778899aabbccdd"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	// Query with different case
	entries, err := l.GetHistoryPage(ctx, "0xAABBCCDDEEFF00112233445566778899AABBCCDD", 10, time.Time{}, "")
	if err != nil {
		t.Fatalf("GetHistoryPage failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry (case insensitive), got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Additional validation coverage
// ---------------------------------------------------------------------------

func TestLedger_Deposit_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	tests := []struct {
		name   string
		amount string
	}{
		{"zero", "0"},
		{"negative", "-1.00"},
		{"invalid_string", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := l.Deposit(ctx, "0xagent", tt.amount, "0xtx_"+tt.name)
			if err != ErrInvalidAmount {
				t.Errorf("expected ErrInvalidAmount, got %v", err)
			}
		})
	}
}

func TestLedger_Spend_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.Spend(ctx, "0xagent", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}

	err = l.Spend(ctx, "0xagent", "abc", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for invalid string, got %v", err)
	}
}

func TestLedger_Withdraw_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.Withdraw(ctx, "0xagent", "0", "0xhash")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}

	err = l.Withdraw(ctx, "0xagent", "-5", "0xhash")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for negative, got %v", err)
	}
}

func TestLedger_Refund_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.Refund(ctx, "0xagent", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}
}

func TestLedger_Hold_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.Hold(ctx, "0xagent", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}
}

func TestLedger_ConfirmHold_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.ConfirmHold(ctx, "0xagent", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}
}

func TestLedger_ReleaseHold_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.ReleaseHold(ctx, "0xagent", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}
}

func TestLedger_EscrowLock_InvalidAmount_Comprehensive(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.EscrowLock(ctx, "0xagent", "abc", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for invalid string, got %v", err)
	}
}

func TestLedger_ReleaseEscrow_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.ReleaseEscrow(ctx, "0xbuyer", "0xseller", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}
}

func TestLedger_RefundEscrow_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	err := l.RefundEscrow(ctx, "0xagent", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero, got %v", err)
	}
}

func TestLedger_SettleHoldWithFee_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	// invalid seller amount
	err := l.SettleHoldWithFee(ctx, "0xbuyer", "0xseller", "abc", "0xplatform", "1.00", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for invalid seller amount, got %v", err)
	}

	// invalid fee amount
	err = l.SettleHoldWithFee(ctx, "0xbuyer", "0xseller", "1.00", "0xplatform", "abc", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for invalid fee amount, got %v", err)
	}

	// zero seller amount
	err = l.SettleHoldWithFee(ctx, "0xbuyer", "0xseller", "0", "0xplatform", "0", "ref")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount for zero amounts, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// StoreRef and EventStoreRef
// ---------------------------------------------------------------------------

func TestLedger_StoreRef(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	if l.StoreRef() != store {
		t.Error("StoreRef should return the underlying store")
	}
}

func TestLedger_EventStoreRef(t *testing.T) {
	es := NewMemoryEventStore()
	l := NewWithEvents(NewMemoryStore(), es)
	if l.EventStoreRef() != es {
		t.Error("EventStoreRef should return the event store")
	}

	l2 := New(NewMemoryStore())
	if l2.EventStoreRef() != nil {
		t.Error("EventStoreRef should return nil when no event store")
	}
}

// ---------------------------------------------------------------------------
// CanSpend with credit
// ---------------------------------------------------------------------------

func TestLedger_CanSpend_WithCredit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "5.00", "0xtx1")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Can spend up to available + credit = 15
	canSpend, err := l.CanSpend(ctx, agent, "12.00")
	if err != nil {
		t.Fatalf("CanSpend failed: %v", err)
	}
	if !canSpend {
		t.Error("should be able to spend 12 with 5 available + 10 credit")
	}

	// Cannot exceed total
	canSpend, err = l.CanSpend(ctx, agent, "16.00")
	if err != nil {
		t.Fatalf("CanSpend failed: %v", err)
	}
	if canSpend {
		t.Error("should not be able to spend 16 with 5 available + 10 credit")
	}
}

func TestLedger_CanSpend_InvalidAmount(t *testing.T) {
	l := New(NewMemoryStore())
	ctx := context.Background()

	_, err := l.CanSpend(ctx, "0xagent", "abc")
	if err != ErrInvalidAmount {
		t.Errorf("expected ErrInvalidAmount, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetHistory default limit
// ---------------------------------------------------------------------------

func TestLedger_GetHistory_DefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	entries, err := l.GetHistory(ctx, agent, -1)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry with default limit, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Audit context helpers
// ---------------------------------------------------------------------------

func TestWithAuditRequestID(t *testing.T) {
	ctx := context.Background()
	ctx = WithAuditRequestID(ctx, "req_12345")

	_, _, _, reqID := actorFromCtx(ctx)
	if reqID != "req_12345" {
		t.Errorf("expected requestID req_12345, got %q", reqID)
	}
}

func TestActorFromCtx_Defaults(t *testing.T) {
	ctx := context.Background()

	actorType, actorID, ip, requestID := actorFromCtx(ctx)
	if actorType != "system" {
		t.Errorf("expected default actorType 'system', got %q", actorType)
	}
	if actorID != "" {
		t.Errorf("expected empty actorID, got %q", actorID)
	}
	if ip != "" {
		t.Errorf("expected empty ip, got %q", ip)
	}
	if requestID != "" {
		t.Errorf("expected empty requestID, got %q", requestID)
	}
}

func TestBalanceSnapshot_NilBalance(t *testing.T) {
	snap := balanceSnapshot(nil)
	if snap != "{}" {
		t.Errorf("expected '{}' for nil balance, got %q", snap)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore direct tests (UseCredit, RepayCredit, SumAllBalances, GetEntry)
// ---------------------------------------------------------------------------

func TestMemoryStore_UseCredit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// UseCredit for nonexistent agent
	err := store.UseCredit(ctx, agent, "1.00")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}

	// Set up agent with credit limit
	store.Credit(ctx, agent, "10.00", "0xtx1", "deposit")
	store.SetCreditLimit(ctx, agent, "5.00")

	// Use some credit
	err = store.UseCredit(ctx, agent, "3.00")
	if err != nil {
		t.Fatalf("UseCredit failed: %v", err)
	}

	_, creditUsed, _ := store.GetCreditInfo(ctx, agent)
	if creditUsed != "3.000000" {
		t.Errorf("expected creditUsed 3.000000, got %s", creditUsed)
	}

	// Exceed credit limit
	err = store.UseCredit(ctx, agent, "5.00")
	if err != ErrInsufficientBalance {
		t.Errorf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestMemoryStore_RepayCredit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// RepayCredit for nonexistent agent
	err := store.RepayCredit(ctx, agent, "1.00")
	if err != ErrAgentNotFound {
		t.Errorf("expected ErrAgentNotFound, got %v", err)
	}

	// Set up agent with credit usage
	store.Credit(ctx, agent, "10.00", "0xtx1", "deposit")
	store.SetCreditLimit(ctx, agent, "10.00")
	store.UseCredit(ctx, agent, "5.00")

	// Repay partial
	err = store.RepayCredit(ctx, agent, "2.00")
	if err != nil {
		t.Fatalf("RepayCredit failed: %v", err)
	}

	_, creditUsed, _ := store.GetCreditInfo(ctx, agent)
	if creditUsed != "3.000000" {
		t.Errorf("expected creditUsed 3.000000, got %s", creditUsed)
	}

	// Repay more than owed (should cap at what's owed)
	err = store.RepayCredit(ctx, agent, "10.00")
	if err != nil {
		t.Fatalf("RepayCredit (over) failed: %v", err)
	}

	_, creditUsed, _ = store.GetCreditInfo(ctx, agent)
	if creditUsed != "0.000000" {
		t.Errorf("expected creditUsed 0.000000 after full repay, got %s", creditUsed)
	}
}

func TestMemoryStore_SumAllBalances(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	l := New(store)

	agent1 := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	agent2 := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	l.Deposit(ctx, agent1, "10.00", "0xtx1")
	l.Deposit(ctx, agent2, "20.00", "0xtx2")
	l.Hold(ctx, agent1, "3.00", "hold1")
	l.EscrowLock(ctx, agent2, "5.00", "esc1")

	avail, pending, escrowed, err := store.SumAllBalances(ctx)
	if err != nil {
		t.Fatalf("SumAllBalances failed: %v", err)
	}

	// agent1: avail=7, pend=3, esc=0
	// agent2: avail=15, pend=0, esc=5
	// totals: avail=22, pend=3, esc=5
	if avail != "22.000000" {
		t.Errorf("expected total available 22.000000, got %s", avail)
	}
	if pending != "3.000000" {
		t.Errorf("expected total pending 3.000000, got %s", pending)
	}
	if escrowed != "5.000000" {
		t.Errorf("expected total escrowed 5.000000, got %s", escrowed)
	}
}

func TestMemoryStore_GetEntry(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	l := New(store)

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "10.00", "0xtx1")

	entries, _ := l.GetHistory(ctx, agent, 10)
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}

	// Get existing entry
	entry, err := store.GetEntry(ctx, entries[0].ID)
	if err != nil {
		t.Fatalf("GetEntry failed: %v", err)
	}
	if entry.ID != entries[0].ID {
		t.Errorf("entry ID mismatch")
	}

	// Get nonexistent entry
	_, err = store.GetEntry(ctx, "nonexistent")
	if err != ErrEntryNotFound {
		t.Errorf("expected ErrEntryNotFound, got %v", err)
	}
}

func TestMemoryStore_GetCreditInfo_NonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	limit, used, err := store.GetCreditInfo(ctx, "0xnonexistent")
	if err != nil {
		t.Fatalf("GetCreditInfo failed: %v", err)
	}
	if limit != "0" || used != "0" {
		t.Errorf("expected 0/0 for nonexistent agent, got %s/%s", limit, used)
	}
}

// --- merged from coverage_extra_test.go ---

// ---------------------------------------------------------------------------
// Ledger: NewWithEvents and EventStoreRef
// ---------------------------------------------------------------------------

func TestLedger_NewWithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)

	if l.EventStoreRef() == nil {
		t.Fatal("expected non-nil event store")
	}
	if l.StoreRef() != store {
		t.Fatal("expected matching store")
	}
}

// ---------------------------------------------------------------------------
// Ledger: WithAuditLogger
// ---------------------------------------------------------------------------

func TestLedger_WithAuditLogger(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)

	al := NewMemoryAuditLogger()
	l2 := l.WithAuditLogger(al)

	if l2 != l {
		t.Fatal("expected same ledger instance")
	}
}

// ---------------------------------------------------------------------------
// Ledger: appendEvent with nil event store is no-op
// ---------------------------------------------------------------------------

func TestLedger_AppendEvent_NilEventStore(t *testing.T) {
	store := NewMemoryStore()
	l := New(store) // no event store

	// Should not panic
	l.appendEvent(context.Background(), "0xagent", "deposit", "10.00", "ref", "")
}

// ---------------------------------------------------------------------------
// Ledger: logAudit with nil audit logger is no-op
// ---------------------------------------------------------------------------

func TestLedger_LogAudit_NilLogger(t *testing.T) {
	store := NewMemoryStore()
	l := New(store) // no audit logger

	// Should not panic
	l.logAudit(context.Background(), "0xagent", "deposit", "10.00", "ref", nil, nil)
}

// ---------------------------------------------------------------------------
// Ledger: Deposit with events and audit
// ---------------------------------------------------------------------------

func TestLedger_Deposit_WithEventsAndAudit(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	al := NewMemoryAuditLogger()
	l := NewWithEvents(store, es).WithAuditLogger(al)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	err := l.Deposit(ctx, agent, "50.00", "tx1")
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// Check event store
	events, _ := es.GetEvents(ctx, "0x1234567890123456789012345678901234567890", time.Time{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].EventType != "deposit" {
		t.Fatalf("expected deposit event, got %s", events[0].EventType)
	}

	// Check audit log
	entries := al.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Ledger: Spend with events
// ---------------------------------------------------------------------------

func TestLedger_Spend_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	err := l.Spend(ctx, agent, "10.00", "ref1")
	if err != nil {
		t.Fatalf("spend: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Ledger: Transfer with events and audit
// ---------------------------------------------------------------------------

func TestLedger_Transfer_WithEventsAndAudit(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	al := NewMemoryAuditLogger()
	l := NewWithEvents(store, es).WithAuditLogger(al)
	ctx := context.Background()

	from := "0xfrom000000000000000000000000000000000000"
	to := "0xto00000000000000000000000000000000000000"

	l.Deposit(ctx, from, "100.00", "tx1")
	err := l.Transfer(ctx, from, to, "25.00", "ref1")
	if err != nil {
		t.Fatalf("transfer: %v", err)
	}

	// Should have 2 transfer events (out + in) plus 1 deposit event
	fromEvents, _ := es.GetEvents(ctx, from, time.Time{})
	if len(fromEvents) < 2 {
		t.Fatalf("expected at least 2 events for sender, got %d", len(fromEvents))
	}

	// Audit should have entries for both sides
	entries := al.Entries()
	if len(entries) < 3 { // deposit + transfer_out + transfer_in
		t.Fatalf("expected at least 3 audit entries, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Ledger: Withdraw with events
// ---------------------------------------------------------------------------

func TestLedger_Withdraw_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	err := l.Withdraw(ctx, agent, "20.00", "0xtx_withdraw")
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "withdrawal" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected withdrawal event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: Refund with events
// ---------------------------------------------------------------------------

func TestLedger_Refund_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	l.Spend(ctx, agent, "20.00", "ref1")
	err := l.Refund(ctx, agent, "5.00", "ref_refund")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "refund" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected refund event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: Hold, ConfirmHold, ReleaseHold with events
// ---------------------------------------------------------------------------

func TestLedger_Hold_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")

	err := l.Hold(ctx, agent, "10.00", "hold_ref")
	if err != nil {
		t.Fatalf("hold: %v", err)
	}

	err = l.ConfirmHold(ctx, agent, "10.00", "hold_ref")
	if err != nil {
		t.Fatalf("confirm hold: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	types := make(map[string]bool)
	for _, e := range events {
		types[e.EventType] = true
	}
	if !types["hold"] {
		t.Fatal("expected hold event")
	}
	if !types["confirm_hold"] {
		t.Fatal("expected confirm_hold event")
	}
}

func TestLedger_ReleaseHold_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	l.Hold(ctx, agent, "10.00", "hold_ref")

	err := l.ReleaseHold(ctx, agent, "10.00", "hold_ref")
	if err != nil {
		t.Fatalf("release hold: %v", err)
	}

	events, _ := es.GetEvents(ctx, agent, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "release_hold" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected release_hold event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: EscrowLock, ReleaseEscrow, RefundEscrow with events
// ---------------------------------------------------------------------------

func TestLedger_EscrowOps_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")

	l.EscrowLock(ctx, buyer, "30.00", "escrow_ref")

	err := l.ReleaseEscrow(ctx, buyer, seller, "30.00", "escrow_ref")
	if err != nil {
		t.Fatalf("release escrow: %v", err)
	}

	events, _ := es.GetEvents(ctx, buyer, time.Time{})
	types := make(map[string]bool)
	for _, e := range events {
		types[e.EventType] = true
	}
	if !types["escrow_lock"] {
		t.Fatal("expected escrow_lock event")
	}
	if !types["escrow_release"] {
		t.Fatal("expected escrow_release event")
	}
}

func TestLedger_RefundEscrow_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.EscrowLock(ctx, buyer, "20.00", "escrow_ref")

	err := l.RefundEscrow(ctx, buyer, "20.00", "escrow_ref")
	if err != nil {
		t.Fatalf("refund escrow: %v", err)
	}

	events, _ := es.GetEvents(ctx, buyer, time.Time{})
	found := false
	for _, e := range events {
		if e.EventType == "escrow_refund" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected escrow_refund event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: SettleHold with events
// ---------------------------------------------------------------------------

func TestLedger_SettleHold_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.Hold(ctx, buyer, "20.00", "hold_ref")

	err := l.SettleHold(ctx, buyer, seller, "20.00", "hold_ref")
	if err != nil {
		t.Fatalf("settle hold: %v", err)
	}

	buyerEvents, _ := es.GetEvents(ctx, buyer, time.Time{})
	found := false
	for _, e := range buyerEvents {
		if e.EventType == "settle_hold_out" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected settle_hold_out event for buyer")
	}

	sellerEvents, _ := es.GetEvents(ctx, seller, time.Time{})
	found = false
	for _, e := range sellerEvents {
		if e.EventType == "settle_hold_in" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected settle_hold_in event for seller")
	}
}

// ---------------------------------------------------------------------------
// Ledger: SettleHoldWithFee with events
// ---------------------------------------------------------------------------

func TestLedger_SettleHoldWithFee_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	platform := "0xplatform000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.Hold(ctx, buyer, "20.00", "hold_ref")

	err := l.SettleHoldWithFee(ctx, buyer, seller, "18.00", platform, "2.00", "hold_ref")
	if err != nil {
		t.Fatalf("settle hold with fee: %v", err)
	}

	platformEvents, _ := es.GetEvents(ctx, platform, time.Time{})
	found := false
	for _, e := range platformEvents {
		if e.EventType == "fee_in" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected fee_in event for platform")
	}
}

// ---------------------------------------------------------------------------
// Ledger: PartialEscrowSettle with events
// ---------------------------------------------------------------------------

func TestLedger_PartialEscrowSettle_WithEvents(t *testing.T) {
	store := NewMemoryStore()
	es := NewMemoryEventStore()
	l := NewWithEvents(store, es)
	ctx := context.Background()

	buyer := "0xbuyer0000000000000000000000000000000000"
	seller := "0xseller000000000000000000000000000000000"
	l.Deposit(ctx, buyer, "100.00", "tx1")
	l.EscrowLock(ctx, buyer, "30.00", "escrow_ref")

	err := l.PartialEscrowSettle(ctx, buyer, seller, "20.00", "10.00", "escrow_ref")
	if err != nil {
		t.Fatalf("partial escrow settle: %v", err)
	}

	buyerEvents, _ := es.GetEvents(ctx, buyer, time.Time{})
	types := make(map[string]bool)
	for _, e := range buyerEvents {
		types[e.EventType] = true
	}
	if !types["escrow_partial_release"] {
		t.Fatal("expected escrow_partial_release event")
	}
	if !types["escrow_partial_refund"] {
		t.Fatal("expected escrow_partial_refund event")
	}
}

// ---------------------------------------------------------------------------
// Ledger: BalanceAtTime without event store
// ---------------------------------------------------------------------------

func TestLedger_BalanceAtTime_NoEventStoreConfigured(t *testing.T) {
	l := New(NewMemoryStore())
	_, err := l.BalanceAtTime(context.Background(), "0xagent", time.Now())
	if err == nil {
		t.Fatal("expected error when no event store configured")
	}
}

// ---------------------------------------------------------------------------
// Ledger: ReconcileAll without event store
// ---------------------------------------------------------------------------

func TestLedger_ReconcileAll_NoEventStoreConfigured(t *testing.T) {
	l := New(NewMemoryStore())
	_, err := l.ReconcileAll(context.Background())
	if err == nil {
		t.Fatal("expected error when no event store configured")
	}
}

// ---------------------------------------------------------------------------
// Ledger: Reverse delegates to store
// ---------------------------------------------------------------------------

func TestLedger_Reverse(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	// Deposit and spend to create an entry
	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")
	l.Spend(ctx, agent, "10.00", "ref1")

	// Get the entry ID
	entries, _ := l.GetHistory(ctx, agent, 10)
	if len(entries) == 0 {
		t.Fatal("expected entries")
	}
	spendEntry := entries[0] // most recent first

	err := l.Reverse(ctx, spendEntry.ID, "test reason", "admin1")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Audit: WithActor, WithAuditIP, WithAuditRequestID
// ---------------------------------------------------------------------------

func TestAuditContextHelpers(t *testing.T) {
	ctx := context.Background()
	ctx = WithActor(ctx, "session_key", "sk_123")
	ctx = WithAuditIP(ctx, "192.168.1.1")
	ctx = WithAuditRequestID(ctx, "req_456")

	actorType, actorID, ip, requestID := actorFromCtx(ctx)
	if actorType != "session_key" {
		t.Errorf("expected session_key, got %s", actorType)
	}
	if actorID != "sk_123" {
		t.Errorf("expected sk_123, got %s", actorID)
	}
	if ip != "192.168.1.1" {
		t.Errorf("expected 192.168.1.1, got %s", ip)
	}
	if requestID != "req_456" {
		t.Errorf("expected req_456, got %s", requestID)
	}
}

// ---------------------------------------------------------------------------
// Audit: balanceSnapshot
// ---------------------------------------------------------------------------

func TestBalanceSnapshot(t *testing.T) {
	bal := &Balance{
		Available: "100.00",
		Pending:   "10.00",
		Escrowed:  "5.00",
	}
	snap := balanceSnapshot(bal)
	var m map[string]string
	json.Unmarshal([]byte(snap), &m)
	if m["available"] != "100.00" {
		t.Errorf("expected available 100.00, got %s", m["available"])
	}
}

// ---------------------------------------------------------------------------
// Audit: MemoryAuditLogger QueryAudit with operation filter
// ---------------------------------------------------------------------------

func TestMemoryAuditLogger_QueryAudit_WithOperation(t *testing.T) {
	al := NewMemoryAuditLogger()
	ctx := context.Background()

	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "deposit", Amount: "10.00"})
	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "spend", Amount: "5.00"})
	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "deposit", Amount: "20.00"})

	entries, _ := al.QueryAudit(ctx, "0xa", time.Time{}, time.Now().Add(time.Hour), "deposit", 10)
	if len(entries) != 2 {
		t.Fatalf("expected 2 deposit entries, got %d", len(entries))
	}
}

func TestMemoryAuditLogger_QueryAudit_DifferentAgent(t *testing.T) {
	al := NewMemoryAuditLogger()
	ctx := context.Background()

	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xa", Operation: "deposit"})
	al.LogAudit(ctx, &AuditEntry{AgentAddr: "0xb", Operation: "deposit"})

	entries, _ := al.QueryAudit(ctx, "0xa", time.Time{}, time.Now().Add(time.Hour), "", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry for 0xa, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: EscrowLock and EscrowRefund
// ---------------------------------------------------------------------------

func TestMemoryStore_EscrowLock_NonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	err := store.EscrowLock(context.Background(), "nonexistent", "10.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

func TestMemoryStore_EscrowRefund_NonexistentAgent(t *testing.T) {
	store := NewMemoryStore()
	err := store.RefundEscrow(context.Background(), "nonexistent", "10.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: GetHistoryPage with cursor
// ---------------------------------------------------------------------------

func TestMemoryStore_GetHistoryPage_WithCursor(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "100.00", "tx1", "deposit")
	store.Debit(ctx, agent, "10.00", "ref1", "spend")
	store.Debit(ctx, agent, "20.00", "ref2", "spend")

	// First page — all entries
	entries, _ := store.GetHistoryPage(ctx, agent, 10, time.Time{}, "")
	if len(entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(entries))
	}

	// Page with cursor — should skip entries at or after cursor
	cursorEntry := entries[0]
	entries2, _ := store.GetHistoryPage(ctx, agent, 10, cursorEntry.CreatedAt, cursorEntry.ID)
	if len(entries2) >= len(entries) {
		t.Fatal("expected fewer entries with cursor")
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: SetCreditLimit, GetCreditInfo
// ---------------------------------------------------------------------------

func TestMemoryStore_SetAndGetCreditInfo(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "10.00", "tx1", "deposit")

	err := store.SetCreditLimit(ctx, agent, "50.00")
	if err != nil {
		t.Fatalf("SetCreditLimit: %v", err)
	}

	limit, used, err := store.GetCreditInfo(ctx, agent)
	if err != nil {
		t.Fatalf("GetCreditInfo: %v", err)
	}
	// MemoryStore stores the limit string as provided
	if limit != "50.00" {
		t.Fatalf("expected limit 50.00, got %s", limit)
	}
	if used != "0" && used != "0.000000" {
		t.Fatalf("expected used 0, got %s", used)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: HasDeposit
// ---------------------------------------------------------------------------

func TestMemoryStore_HasDeposit_NotFound(t *testing.T) {
	store := NewMemoryStore()
	exists, err := store.HasDeposit(context.Background(), "nonexistent_tx")
	if err != nil {
		t.Fatalf("HasDeposit: %v", err)
	}
	if exists {
		t.Fatal("expected false for nonexistent tx")
	}
}

// ---------------------------------------------------------------------------
// MemoryEventStore: basic operations
// ---------------------------------------------------------------------------

func TestMemoryEventStore_AppendAndGet(t *testing.T) {
	es := NewMemoryEventStore()
	ctx := context.Background()

	err := es.AppendEvent(ctx, &Event{
		AgentAddr: "0xagent",
		EventType: "deposit",
		Amount:    "10.00",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	events, _ := es.GetEvents(ctx, "0xagent", time.Time{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestMemoryEventStore_GetAllAgents(t *testing.T) {
	es := NewMemoryEventStore()
	ctx := context.Background()

	es.AppendEvent(ctx, &Event{AgentAddr: "0xa", EventType: "deposit", Amount: "1.00", CreatedAt: time.Now()})
	es.AppendEvent(ctx, &Event{AgentAddr: "0xb", EventType: "deposit", Amount: "2.00", CreatedAt: time.Now()})
	es.AppendEvent(ctx, &Event{AgentAddr: "0xa", EventType: "spend", Amount: "0.50", CreatedAt: time.Now()})

	agents, _ := es.GetAllAgents(ctx)
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: Debit with credit draw
// ---------------------------------------------------------------------------

func TestMemoryStore_Debit_WithCreditDraw(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "5.00", "tx1", "deposit")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Debit $8: $5 from available + $3 from credit
	err := store.Debit(ctx, agent, "8.00", "ref1", "spend")
	if err != nil {
		t.Fatalf("debit with credit: %v", err)
	}

	bal, _ := store.GetBalance(ctx, agent)
	if bal.Available != "0.000000" {
		t.Fatalf("expected 0 available, got %s", bal.Available)
	}
	if bal.CreditUsed != "3.000000" {
		t.Fatalf("expected 3.000000 credit used, got %s", bal.CreditUsed)
	}
}

func TestMemoryStore_Debit_InsufficientWithCredit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "5.00", "tx1", "deposit")
	store.SetCreditLimit(ctx, agent, "2.00")

	// Debit $8: $5 from available + need $3 from credit but only $2 available
	err := store.Debit(ctx, agent, "8.00", "ref1", "spend")
	if err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: Credit auto-repays credit
// ---------------------------------------------------------------------------

func TestMemoryStore_Credit_AutoRepayCredit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	agent := "0xagent"
	store.Credit(ctx, agent, "5.00", "tx1", "deposit")
	store.SetCreditLimit(ctx, agent, "20.00")

	// Draw credit
	store.Debit(ctx, agent, "10.00", "ref1", "spend") // 5 avail + 5 credit

	// Now deposit — should auto-repay credit
	store.Credit(ctx, agent, "3.00", "tx2", "deposit")

	bal, _ := store.GetBalance(ctx, agent)
	if bal.CreditUsed != "2.000000" {
		t.Fatalf("expected credit used 2.000000 after auto-repay, got %s", bal.CreditUsed)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: SettleHold nonexistent buyer
// ---------------------------------------------------------------------------

func TestMemoryStore_SettleHold_NonexistentBuyer(t *testing.T) {
	store := NewMemoryStore()
	err := store.SettleHold(context.Background(), "nonexistent", "0xseller", "10.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MemoryStore: PartialEscrowSettle nonexistent buyer
// ---------------------------------------------------------------------------

func TestMemoryStore_PartialEscrowSettle_NonexistentBuyer(t *testing.T) {
	store := NewMemoryStore()
	err := store.PartialEscrowSettle(context.Background(), "nonexistent", "0xseller", "5.00", "5.00", "ref")
	if err != ErrAgentNotFound {
		t.Fatalf("expected ErrAgentNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ledger: CanSpend with credit
// ---------------------------------------------------------------------------

func TestLedger_CanSpend_CreditBoostsEffective(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "5.00", "tx1")
	store.SetCreditLimit(ctx, agent, "10.00")

	// Agent can spend up to $15 (5 available + 10 credit)
	can, err := l.CanSpend(ctx, agent, "12.00")
	if err != nil {
		t.Fatalf("CanSpend: %v", err)
	}
	if !can {
		t.Fatal("expected can spend $12 with credit")
	}

	can, _ = l.CanSpend(ctx, agent, "16.00")
	if can {
		t.Fatal("expected cannot spend $16 with only $15 effective")
	}
}

// ---------------------------------------------------------------------------
// Ledger: GetHistory default limit
// ---------------------------------------------------------------------------

func TestLedger_GetHistory_ZeroLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")

	// Zero limit should default to 50
	entries, err := l.GetHistory(ctx, agent, 0)
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Ledger: GetHistoryPage default limit
// ---------------------------------------------------------------------------

func TestLedger_GetHistoryPage_ZeroLimit(t *testing.T) {
	store := NewMemoryStore()
	l := New(store)
	ctx := context.Background()

	agent := "0x1234567890123456789012345678901234567890"
	l.Deposit(ctx, agent, "100.00", "tx1")

	entries, err := l.GetHistoryPage(ctx, agent, 0, time.Time{}, "")
	if err != nil {
		t.Fatalf("GetHistoryPage: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

// ---------------------------------------------------------------------------
// Handler: isValidAmount
// ---------------------------------------------------------------------------

func TestIsValidAmount_Coverage(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"0", true},
		{"1.00", true},
		{"100", true},
		{"0.123456", true},
		{"", false},
		{"abc", false},
		{"-1", false},
		{"1.2.3", false},
	}
	for _, tt := range tests {
		got := isValidAmount(tt.input)
		if got != tt.expected {
			t.Errorf("isValidAmount(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
