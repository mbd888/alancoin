package ledger

import (
	"context"
	"math/big"
	"testing"

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

// assertFundConservation verifies totalIn - totalOut = available + pending + escrowed
func assertFundConservation(t *testing.T, bal *Balance, context string) {
	t.Helper()
	totalIn, _ := usdc.Parse(bal.TotalIn)
	totalOut, _ := usdc.Parse(bal.TotalOut)
	available, _ := usdc.Parse(bal.Available)
	pending, _ := usdc.Parse(bal.Pending)
	escrowed, _ := usdc.Parse(bal.Escrowed)

	// net = totalIn - totalOut
	net := new(big.Int).Sub(totalIn, totalOut)
	// sum = available + pending + escrowed
	sum := new(big.Int).Add(available, pending)
	sum.Add(sum, escrowed)

	if net.Cmp(sum) != 0 {
		t.Errorf("%s: fund conservation violated: totalIn(%s) - totalOut(%s) = %s, but available(%s) + pending(%s) + escrowed(%s) = %s",
			context, bal.TotalIn, bal.TotalOut, usdc.Format(net),
			bal.Available, bal.Pending, bal.Escrowed, usdc.Format(sum))
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
