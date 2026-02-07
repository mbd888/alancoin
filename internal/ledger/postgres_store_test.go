//go:build integration

package ledger

import (
	"context"
	"database/sql"
	"os"
	"sync"
	"testing"

	_ "github.com/lib/pq"
)

func setupTestDB(t *testing.T) (*PostgresStore, func()) {
	t.Helper()

	dbURL := os.Getenv("POSTGRES_URL")
	if dbURL == "" {
		t.Skip("POSTGRES_URL not set, skipping integration test")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	if err := db.Ping(); err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	store := NewPostgresStore(db)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	// Add escrowed column if not present (from migration 003)
	db.ExecContext(ctx, `ALTER TABLE agent_balances ADD COLUMN IF NOT EXISTS escrowed NUMERIC(20,6) NOT NULL DEFAULT 0`)

	cleanup := func() {
		db.ExecContext(ctx, "DELETE FROM ledger_entries")
		db.ExecContext(ctx, "DELETE FROM agent_balances")
		db.Close()
	}

	return store, cleanup
}

func TestPostgres_CreditAndGetBalance(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000001"

	err := store.Credit(ctx, addr, "10.500000", "0xabc123", "test deposit")
	if err != nil {
		t.Fatalf("Credit failed: %v", err)
	}

	bal, err := store.GetBalance(ctx, addr)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "10.500000" {
		t.Errorf("Expected available 10.500000, got %s", bal.Available)
	}
	if bal.TotalIn != "10.500000" {
		t.Errorf("Expected totalIn 10.500000, got %s", bal.TotalIn)
	}
}

func TestPostgres_CreditThenDebit(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000002"

	store.Credit(ctx, addr, "100.000000", "", "deposit")

	err := store.Debit(ctx, addr, "30.000000", "ref1", "payment")
	if err != nil {
		t.Fatalf("Debit failed: %v", err)
	}

	bal, err := store.GetBalance(ctx, addr)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "70.000000" {
		t.Errorf("Expected available 70.000000, got %s", bal.Available)
	}
	if bal.TotalOut != "30.000000" {
		t.Errorf("Expected totalOut 30.000000, got %s", bal.TotalOut)
	}
}

func TestPostgres_OverdraftPrevention(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000003"

	store.Credit(ctx, addr, "5.000000", "", "deposit")

	// Try to debit more than available — should fail via CHECK constraint
	err := store.Debit(ctx, addr, "10.000000", "ref1", "overdraft attempt")
	if err == nil {
		t.Fatal("Expected overdraft to fail, but it succeeded")
	}

	// Balance should be unchanged
	bal, _ := store.GetBalance(ctx, addr)
	if bal.Available != "5.000000" {
		t.Errorf("Expected available 5.000000 after failed overdraft, got %s", bal.Available)
	}
}

func TestPostgres_HoldConfirmRelease(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000004"

	store.Credit(ctx, addr, "50.000000", "", "deposit")

	// Hold
	err := store.Hold(ctx, addr, "20.000000", "hold1")
	if err != nil {
		t.Fatalf("Hold failed: %v", err)
	}

	bal, _ := store.GetBalance(ctx, addr)
	if bal.Available != "30.000000" {
		t.Errorf("After hold: expected available 30, got %s", bal.Available)
	}
	if bal.Pending != "20.000000" {
		t.Errorf("After hold: expected pending 20, got %s", bal.Pending)
	}

	// Confirm
	err = store.ConfirmHold(ctx, addr, "20.000000", "hold1")
	if err != nil {
		t.Fatalf("ConfirmHold failed: %v", err)
	}

	bal, _ = store.GetBalance(ctx, addr)
	if bal.Available != "30.000000" {
		t.Errorf("After confirm: expected available 30, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("After confirm: expected pending 0, got %s", bal.Pending)
	}
	if bal.TotalOut != "20.000000" {
		t.Errorf("After confirm: expected totalOut 20, got %s", bal.TotalOut)
	}
}

func TestPostgres_HoldRelease(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000005"

	store.Credit(ctx, addr, "50.000000", "", "deposit")

	store.Hold(ctx, addr, "20.000000", "hold1")

	// Release (transfer failed)
	err := store.ReleaseHold(ctx, addr, "20.000000", "hold1")
	if err != nil {
		t.Fatalf("ReleaseHold failed: %v", err)
	}

	bal, _ := store.GetBalance(ctx, addr)
	if bal.Available != "50.000000" {
		t.Errorf("After release: expected available 50, got %s", bal.Available)
	}
	if bal.Pending != "0.000000" {
		t.Errorf("After release: expected pending 0, got %s", bal.Pending)
	}
}

func TestPostgres_EscrowLockAndRelease(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	buyer := "0xaaaa000000000000000000000000000000000006"
	seller := "0xbbbb000000000000000000000000000000000007"

	store.Credit(ctx, buyer, "100.000000", "", "deposit")

	// Lock
	err := store.EscrowLock(ctx, buyer, "25.000000", "esc1")
	if err != nil {
		t.Fatalf("EscrowLock failed: %v", err)
	}

	bal, _ := store.GetBalance(ctx, buyer)
	if bal.Available != "75.000000" {
		t.Errorf("After lock: expected available 75, got %s", bal.Available)
	}
	if bal.Escrowed != "25.000000" {
		t.Errorf("After lock: expected escrowed 25, got %s", bal.Escrowed)
	}

	// Release to seller
	err = store.ReleaseEscrow(ctx, buyer, seller, "25.000000", "esc1")
	if err != nil {
		t.Fatalf("ReleaseEscrow failed: %v", err)
	}

	buyerBal, _ := store.GetBalance(ctx, buyer)
	sellerBal, _ := store.GetBalance(ctx, seller)

	if buyerBal.Escrowed != "0.000000" {
		t.Errorf("Buyer escrowed should be 0, got %s", buyerBal.Escrowed)
	}
	if buyerBal.TotalOut != "25.000000" {
		t.Errorf("Buyer totalOut should be 25, got %s", buyerBal.TotalOut)
	}
	if sellerBal.Available != "25.000000" {
		t.Errorf("Seller available should be 25, got %s", sellerBal.Available)
	}
}

func TestPostgres_EscrowRefund(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	buyer := "0xaaaa000000000000000000000000000000000008"

	store.Credit(ctx, buyer, "100.000000", "", "deposit")
	store.EscrowLock(ctx, buyer, "30.000000", "esc2")

	// Refund (dispute)
	err := store.RefundEscrow(ctx, buyer, "30.000000", "esc2")
	if err != nil {
		t.Fatalf("RefundEscrow failed: %v", err)
	}

	bal, _ := store.GetBalance(ctx, buyer)
	if bal.Available != "100.000000" {
		t.Errorf("After refund: expected available 100, got %s", bal.Available)
	}
	if bal.Escrowed != "0.000000" {
		t.Errorf("After refund: expected escrowed 0, got %s", bal.Escrowed)
	}
}

func TestPostgres_History(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa000000000000000000000000000000000009"

	store.Credit(ctx, addr, "100.000000", "0xhash1", "deposit 1")
	store.Debit(ctx, addr, "10.000000", "ref1", "payment 1")
	store.Debit(ctx, addr, "20.000000", "ref2", "payment 2")

	entries, err := store.GetHistory(ctx, addr, 10)
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("Expected 3 entries, got %d", len(entries))
	}

	// Most recent first
	if entries[0].Type != "spend" {
		t.Errorf("Expected first entry type 'spend', got %s", entries[0].Type)
	}
}

func TestPostgres_HasDeposit(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa00000000000000000000000000000000000a"

	store.Credit(ctx, addr, "10.000000", "0xuniquehash123", "test deposit")

	has, err := store.HasDeposit(ctx, "0xuniquehash123")
	if err != nil {
		t.Fatalf("HasDeposit failed: %v", err)
	}
	if !has {
		t.Error("Expected HasDeposit to return true")
	}

	has, _ = store.HasDeposit(ctx, "0xnonexistent")
	if has {
		t.Error("Expected HasDeposit to return false for nonexistent hash")
	}
}

func TestPostgres_ConcurrentCredits(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa00000000000000000000000000000000000b"

	// 10 concurrent credits of 1.00 each
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Credit(ctx, addr, "1.000000", "", "concurrent deposit")
		}()
	}
	wg.Wait()

	bal, err := store.GetBalance(ctx, addr)
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}

	if bal.Available != "10.000000" {
		t.Errorf("Expected available 10.000000 after 10 concurrent credits, got %s", bal.Available)
	}
}

func TestPostgres_ConcurrentDebits_NoOverdraft(t *testing.T) {
	store, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	addr := "0xaaaa00000000000000000000000000000000000c"

	store.Credit(ctx, addr, "5.000000", "", "deposit")

	// 10 concurrent debits of 1.00 each — only 5 should succeed
	var wg sync.WaitGroup
	var successCount int32
	var mu sync.Mutex
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := store.Debit(ctx, addr, "1.000000", "ref", "concurrent spend")
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if successCount != 5 {
		t.Errorf("Expected exactly 5 successful debits, got %d", successCount)
	}

	bal, _ := store.GetBalance(ctx, addr)
	if bal.Available != "0.000000" {
		t.Errorf("Expected available 0 after draining, got %s", bal.Available)
	}
}
