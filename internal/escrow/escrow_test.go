package escrow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// mockLedger records calls for verification.
type mockLedger struct {
	mu       sync.Mutex
	locked   map[string]string // reference -> amount
	released map[string]string
	refunded map[string]string
	partials map[string]string // reference -> releaseAmount (PartialEscrowSettle calls)
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		locked:   make(map[string]string),
		released: make(map[string]string),
		refunded: make(map[string]string),
		partials: make(map[string]string),
	}
}

func (m *mockLedger) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locked[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released[reference] = amount
	return nil
}

func (m *mockLedger) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refunded[reference] = amount
	return nil
}

func (m *mockLedger) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.partials[reference] = releaseAmount
	return nil
}

// mockRecorder captures recorded transactions.
type mockRecorder struct {
	txns []recordedTx
}

type recordedTx struct {
	from, to, amount, serviceID, status string
}

func (m *mockRecorder) RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error {
	m.txns = append(m.txns, recordedTx{from, to, amount, serviceID, status})
	return nil
}

// failingLedger returns errors on specific operations.
type failingLedger struct {
	lockErr    error
	releaseErr error
	refundErr  error
	partialErr error
	calls      []string
}

func (f *failingLedger) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	f.calls = append(f.calls, "lock")
	return f.lockErr
}

func (f *failingLedger) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	f.calls = append(f.calls, "release")
	return f.releaseErr
}

func (f *failingLedger) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	f.calls = append(f.calls, "refund")
	return f.refundErr
}

func (f *failingLedger) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	f.calls = append(f.calls, "partial_settle")
	return f.partialErr
}

func TestEscrow_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	recorder := &mockRecorder{}
	svc := NewService(store, ledger).WithRecorder(recorder)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"

	// Create escrow
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "1.00",
		ServiceID:  "svc_123",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if esc.Status != StatusPending {
		t.Errorf("Expected status pending, got %s", esc.Status)
	}
	if _, ok := ledger.locked[esc.ID]; !ok {
		t.Error("Expected ledger.EscrowLock to be called")
	}

	// Mark delivered
	esc, err = svc.MarkDelivered(ctx, esc.ID, seller)
	if err != nil {
		t.Fatalf("MarkDelivered failed: %v", err)
	}
	if esc.Status != StatusDelivered {
		t.Errorf("Expected status delivered, got %s", esc.Status)
	}
	if esc.DeliveredAt == nil {
		t.Error("Expected DeliveredAt to be set")
	}

	// Confirm (buyer releases funds to seller)
	esc, err = svc.Confirm(ctx, esc.ID, buyer)
	if err != nil {
		t.Fatalf("Confirm failed: %v", err)
	}
	if esc.Status != StatusReleased {
		t.Errorf("Expected status released, got %s", esc.Status)
	}
	if _, ok := ledger.released[esc.ID]; !ok {
		t.Error("Expected ledger.ReleaseEscrow to be called")
	}
	if esc.ResolvedAt == nil {
		t.Error("Expected ResolvedAt to be set")
	}

	// Check transaction was recorded as confirmed
	if len(recorder.txns) != 1 {
		t.Fatalf("Expected 1 recorded transaction, got %d", len(recorder.txns))
	}
	if recorder.txns[0].status != "confirmed" {
		t.Errorf("Expected tx status 'confirmed', got %s", recorder.txns[0].status)
	}
}

func TestEscrow_DisputeRefund(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	recorder := &mockRecorder{}
	svc := NewService(store, ledger).WithRecorder(recorder)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"

	// Create escrow
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "2.00",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Dispute → enters "disputed" state (funds stay locked for arbitration)
	esc, err = svc.Dispute(ctx, esc.ID, buyer, "service returned garbage")
	if err != nil {
		t.Fatalf("Dispute failed: %v", err)
	}
	if esc.Status != StatusDisputed {
		t.Errorf("Expected status disputed, got %s", esc.Status)
	}
	if esc.DisputeReason != "service returned garbage" {
		t.Errorf("Expected dispute reason, got %s", esc.DisputeReason)
	}
	// Funds remain locked (no refund until arbitration resolves)
	if len(ledger.refunded) != 0 {
		t.Error("Expected no ledger.RefundEscrow call (funds stay locked for arbitration)")
	}

	// Check transaction recorded as failed
	if len(recorder.txns) != 1 {
		t.Fatalf("Expected 1 recorded transaction, got %d", len(recorder.txns))
	}
	if recorder.txns[0].status != "failed" {
		t.Errorf("Expected tx status 'failed', got %s", recorder.txns[0].status)
	}
}

func TestEscrow_AutoRelease(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	recorder := &mockRecorder{}
	svc := NewService(store, ledger).WithRecorder(recorder)
	ctx := context.Background()

	// Create escrow with short timeout
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "0.50",
		AutoRelease: "1ms",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// List expired
	expired, err := store.ListExpired(ctx, time.Now(), 100)
	if err != nil {
		t.Fatalf("ListExpired failed: %v", err)
	}
	if len(expired) != 1 {
		t.Fatalf("Expected 1 expired escrow, got %d", len(expired))
	}

	// Auto-release
	err = svc.AutoRelease(ctx, esc)
	if err != nil {
		t.Fatalf("AutoRelease failed: %v", err)
	}

	// Verify the escrow was updated
	updated, _ := svc.Get(ctx, esc.ID)
	if updated.Status != StatusExpired {
		t.Errorf("Expected status expired, got %s", updated.Status)
	}
	if _, ok := ledger.released[esc.ID]; !ok {
		t.Error("Expected ledger.ReleaseEscrow to be called for auto-release")
	}

	// Check recorded as confirmed (auto-release = success)
	if len(recorder.txns) != 1 || recorder.txns[0].status != "confirmed" {
		t.Error("Expected auto-release to record as confirmed")
	}
}

func TestEscrow_DoubleConfirm(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "1.00",
	})

	// First confirm succeeds
	_, err := svc.Confirm(ctx, esc.ID, buyer)
	if err != nil {
		t.Fatalf("First Confirm failed: %v", err)
	}

	// Second confirm should fail (already resolved)
	_, err = svc.Confirm(ctx, esc.ID, buyer)
	if err != ErrAlreadyResolved {
		t.Errorf("Expected ErrAlreadyResolved, got %v", err)
	}

	// Dispute after confirm should also fail
	_, err = svc.Dispute(ctx, esc.ID, buyer, "too late")
	if err != ErrAlreadyResolved {
		t.Errorf("Expected ErrAlreadyResolved for dispute after confirm, got %v", err)
	}
}

func TestEscrow_Authorization(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"
	stranger := "0xstranger"

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "1.00",
	})

	// Only seller can mark delivered
	_, err := svc.MarkDelivered(ctx, esc.ID, buyer)
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized when buyer tries to deliver, got %v", err)
	}

	_, err = svc.MarkDelivered(ctx, esc.ID, stranger)
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized when stranger tries to deliver, got %v", err)
	}

	// Only buyer can confirm
	_, err = svc.Confirm(ctx, esc.ID, seller)
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized when seller tries to confirm, got %v", err)
	}

	_, err = svc.Confirm(ctx, esc.ID, stranger)
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized when stranger tries to confirm, got %v", err)
	}

	// Only buyer can dispute
	_, err = svc.Dispute(ctx, esc.ID, seller, "nope")
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized when seller tries to dispute, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: state transitions
// ---------------------------------------------------------------------------

func TestEscrow_DisputeAfterDelivery(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "1.00",
	})

	// Seller delivers
	_, err := svc.MarkDelivered(ctx, esc.ID, seller)
	if err != nil {
		t.Fatalf("MarkDelivered failed: %v", err)
	}

	// Buyer still disputes after delivery (allowed — buyer protection)
	esc, err = svc.Dispute(ctx, esc.ID, buyer, "output was wrong")
	if err != nil {
		t.Fatalf("Dispute after delivery should work: %v", err)
	}
	if esc.Status != StatusDisputed {
		t.Errorf("Expected disputed, got %s", esc.Status)
	}
}

func TestEscrow_DoubleDelivery(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  buyer,
		SellerAddr: seller,
		Amount:     "1.00",
	})

	// First delivery
	_, err := svc.MarkDelivered(ctx, esc.ID, seller)
	if err != nil {
		t.Fatalf("First MarkDelivered failed: %v", err)
	}

	// Second delivery should fail (already delivered)
	_, err = svc.MarkDelivered(ctx, esc.ID, seller)
	if err != ErrInvalidStatus {
		t.Errorf("Expected ErrInvalidStatus for double delivery, got %v", err)
	}
}

func TestEscrow_ConfirmWithoutDelivery(t *testing.T) {
	// Buyer can confirm immediately without waiting for delivery
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Confirm directly from pending (skip delivery)
	esc, err := svc.Confirm(ctx, esc.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("Confirm from pending should work: %v", err)
	}
	if esc.Status != StatusReleased {
		t.Errorf("Expected released, got %s", esc.Status)
	}
}

func TestEscrow_AutoReleaseAfterConfirm(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "1ms",
	})

	// Confirm first
	_, _ = svc.Confirm(ctx, esc.ID, "0xbuyer")

	time.Sleep(5 * time.Millisecond)

	// Re-fetch to get current state (Get returns a copy)
	updated, _ := svc.Get(ctx, esc.ID)

	// AutoRelease on already-confirmed should fail
	err := svc.AutoRelease(ctx, updated)
	if err != ErrAlreadyResolved {
		t.Errorf("Expected ErrAlreadyResolved for auto-release after confirm, got %v", err)
	}
}

func TestEscrow_AutoReleaseAfterDispute(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "1ms",
	})

	// Dispute first → status becomes "disputed"
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad service")

	time.Sleep(5 * time.Millisecond)

	// Re-fetch to get current state (Get returns a copy)
	updated, _ := svc.Get(ctx, esc.ID)

	// AutoRelease on disputed escrow should fail (funds locked for arbitration)
	err := svc.AutoRelease(ctx, updated)
	if err != ErrInvalidStatus {
		t.Errorf("Expected ErrInvalidStatus for auto-release after dispute, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: nonexistent escrow
// ---------------------------------------------------------------------------

func TestEscrow_GetNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.Get(ctx, "esc_does_not_exist")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestEscrow_ConfirmNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.Confirm(ctx, "esc_ghost", "0xbuyer")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestEscrow_DisputeNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.Dispute(ctx, "esc_ghost", "0xbuyer", "reason")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestEscrow_DeliverNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.MarkDelivered(ctx, "esc_ghost", "0xseller")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: case sensitivity
// ---------------------------------------------------------------------------

func TestEscrow_CaseInsensitiveAddresses(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	// Create with mixed case
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xBUYER",
		SellerAddr: "0xSELLER",
		Amount:     "1.00",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Addresses should be lowercased
	if esc.BuyerAddr != "0xbuyer" {
		t.Errorf("Expected buyer addr lowercased, got %s", esc.BuyerAddr)
	}
	if esc.SellerAddr != "0xseller" {
		t.Errorf("Expected seller addr lowercased, got %s", esc.SellerAddr)
	}

	// Confirm with uppercase buyer (should match after lowering)
	_, err = svc.Confirm(ctx, esc.ID, "0xBUYER")
	if err != nil {
		t.Fatalf("Confirm with uppercase buyer should work: %v", err)
	}
}

func TestEscrow_DeliverCaseInsensitive(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Deliver with uppercase seller
	_, err := svc.MarkDelivered(ctx, esc.ID, "0xSELLER")
	if err != nil {
		t.Fatalf("Deliver with uppercase seller should work: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: ledger failures
// ---------------------------------------------------------------------------

func TestEscrow_CreateFailsOnLedgerLockError(t *testing.T) {
	store := NewMemoryStore()
	fl := &failingLedger{lockErr: errors.New("insufficient balance")}
	svc := NewService(store, fl)
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "100.00",
	})
	if err == nil {
		t.Fatal("Expected error when ledger lock fails")
	}
	if len(fl.calls) != 1 || fl.calls[0] != "lock" {
		t.Errorf("Expected exactly one lock call, got %v", fl.calls)
	}
	var me *MoneyError
	if !errors.As(err, &me) {
		t.Fatalf("Expected MoneyError, got %T", err)
	}
	if me.FundsStatus != "no_change" {
		t.Errorf("Expected no_change, got %s", me.FundsStatus)
	}
}

func TestEscrow_ConfirmFailsOnLedgerReleaseError(t *testing.T) {
	store := NewMemoryStore()
	fl := &failingLedger{releaseErr: errors.New("ledger release failed")}
	svc := NewService(store, fl)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	_, err := svc.Confirm(ctx, esc.ID, "0xbuyer")
	if err == nil {
		t.Fatal("Expected error when ledger release fails")
	}

	// Escrow should still be pending (not released), so it can be retried
	got, _ := store.Get(ctx, esc.ID)
	if got.Status != StatusPending {
		t.Errorf("Escrow should remain pending after ledger failure, got %s", got.Status)
	}
}

func TestEscrow_DisputeSucceedsWithoutLedgerRefund(t *testing.T) {
	// Dispute no longer calls ledger.RefundEscrow — funds stay locked for arbitration.
	// Even with a failing refund ledger, dispute should succeed.
	store := NewMemoryStore()
	fl := &failingLedger{refundErr: errors.New("ledger refund failed")}
	svc := NewService(store, fl)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	result, err := svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	if err != nil {
		t.Fatalf("Dispute should succeed (no refund needed): %v", err)
	}
	if result.Status != StatusDisputed {
		t.Errorf("Expected disputed, got %s", result.Status)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: multiple escrows
// ---------------------------------------------------------------------------

func TestEscrow_MultipleEscrowsSameBuyer(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"

	// Create 3 escrows
	e1, _ := svc.Create(ctx, CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs1", Amount: "1.00"})
	e2, _ := svc.Create(ctx, CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs2", Amount: "2.00"})
	e3, _ := svc.Create(ctx, CreateRequest{BuyerAddr: buyer, SellerAddr: "0xs3", Amount: "3.00"})

	// All should have unique IDs
	if e1.ID == e2.ID || e2.ID == e3.ID || e1.ID == e3.ID {
		t.Error("Escrow IDs should be unique")
	}

	// List should return all 3
	list, err := svc.ListByAgent(ctx, buyer, 100)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected 3 escrows for buyer, got %d", len(list))
	}

	// Confirm one, dispute one, leave one pending
	_, _ = svc.Confirm(ctx, e1.ID, buyer)
	_, _ = svc.Dispute(ctx, e2.ID, buyer, "bad")

	// Verify states
	g1, _ := svc.Get(ctx, e1.ID)
	g2, _ := svc.Get(ctx, e2.ID)
	g3, _ := svc.Get(ctx, e3.ID)

	if g1.Status != StatusReleased {
		t.Errorf("e1 should be released, got %s", g1.Status)
	}
	if g2.Status != StatusDisputed {
		t.Errorf("e2 should be disputed, got %s", g2.Status)
	}
	if g3.Status != StatusPending {
		t.Errorf("e3 should be pending, got %s", g3.Status)
	}

	// Verify ledger was called correctly
	if len(ledger.locked) != 3 {
		t.Errorf("Expected 3 locks, got %d", len(ledger.locked))
	}
	if len(ledger.released) != 1 {
		t.Errorf("Expected 1 release, got %d", len(ledger.released))
	}
	if len(ledger.refunded) != 0 {
		t.Errorf("Expected 0 refunds (dispute keeps funds locked), got %d", len(ledger.refunded))
	}
}

func TestEscrow_ListByAgentAsSeller(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	seller := "0xseller"

	_, _ = svc.Create(ctx, CreateRequest{BuyerAddr: "0xb1", SellerAddr: seller, Amount: "1.00"})
	_, _ = svc.Create(ctx, CreateRequest{BuyerAddr: "0xb2", SellerAddr: seller, Amount: "2.00"})
	_, _ = svc.Create(ctx, CreateRequest{BuyerAddr: "0xb3", SellerAddr: "0xother", Amount: "3.00"})

	list, err := svc.ListByAgent(ctx, seller, 100)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("Expected 2 escrows for seller, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Edge cases: MemoryStore
// ---------------------------------------------------------------------------

func TestMemoryStore_ListExpiredFiltersTerminal(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-1 * time.Hour)

	// Pending expired → should be listed
	store.Create(ctx, &Escrow{
		ID: "esc_pending", Status: StatusPending,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Delivered expired → should be listed
	store.Create(ctx, &Escrow{
		ID: "esc_delivered", Status: StatusDelivered,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Released (terminal) → should NOT be listed even though past deadline
	store.Create(ctx, &Escrow{
		ID: "esc_released", Status: StatusReleased,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Refunded (terminal) → should NOT be listed
	store.Create(ctx, &Escrow{
		ID: "esc_refunded", Status: StatusRefunded,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Disputed → should NOT be listed (funds locked for arbitration)
	store.Create(ctx, &Escrow{
		ID: "esc_disputed", Status: StatusDisputed,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Arbitrating → should NOT be listed
	store.Create(ctx, &Escrow{
		ID: "esc_arbitrating", Status: StatusArbitrating,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Pending but NOT expired → should NOT be listed
	store.Create(ctx, &Escrow{
		ID: "esc_future", Status: StatusPending,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: now.Add(1 * time.Hour), CreatedAt: now, UpdatedAt: now,
	})

	expired, err := store.ListExpired(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListExpired failed: %v", err)
	}
	if len(expired) != 2 {
		t.Errorf("Expected 2 expired (pending + delivered), got %d", len(expired))
		for _, e := range expired {
			t.Logf("  got: %s (status=%s, autoRelease=%v)", e.ID, e.Status, e.AutoReleaseAt)
		}
	}
}

func TestMemoryStore_UpdateNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.Update(ctx, &Escrow{ID: "esc_ghost"})
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestMemoryStore_ListByAgentLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 10; i++ {
		store.Create(ctx, &Escrow{
			ID: "esc_" + string(rune('a'+i)), Status: StatusPending,
			BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
			AutoReleaseAt: now, CreatedAt: now, UpdatedAt: now,
		})
	}

	list, err := store.ListByAgent(ctx, "0xbuyer", 3)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected limit of 3, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Edge cases: auto-release duration parsing
// ---------------------------------------------------------------------------

func TestEscrow_CustomAutoReleaseDuration(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	before := time.Now()
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "1h",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Should auto-release ~1h from now
	expectedMin := before.Add(59 * time.Minute)
	expectedMax := before.Add(61 * time.Minute)
	if esc.AutoReleaseAt.Before(expectedMin) || esc.AutoReleaseAt.After(expectedMax) {
		t.Errorf("AutoReleaseAt should be ~1h from now, got %v (expected between %v and %v)",
			esc.AutoReleaseAt, expectedMin, expectedMax)
	}
}

func TestEscrow_InvalidAutoReleaseFallsBackToDefault(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	before := time.Now()
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "garbage",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Should use default (5 min)
	expectedMin := before.Add(4 * time.Minute)
	expectedMax := before.Add(6 * time.Minute)
	if esc.AutoReleaseAt.Before(expectedMin) || esc.AutoReleaseAt.After(expectedMax) {
		t.Errorf("Invalid auto-release should fall back to default 5m, got %v", esc.AutoReleaseAt)
	}
}

func TestEscrow_NegativeAutoReleaseFallsBackToDefault(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	before := time.Now()
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "-10m",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Should use default since negative duration is invalid
	expectedMin := before.Add(4 * time.Minute)
	expectedMax := before.Add(6 * time.Minute)
	if esc.AutoReleaseAt.Before(expectedMin) || esc.AutoReleaseAt.After(expectedMax) {
		t.Errorf("Negative auto-release should fall back to default 5m, got %v", esc.AutoReleaseAt)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: recorder not set (nil recorder)
// ---------------------------------------------------------------------------

func TestEscrow_NilRecorderDoesNotPanic(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger) // no recorder
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Confirm should not panic even without recorder
	_, err := svc.Confirm(ctx, esc.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("Confirm without recorder should not fail: %v", err)
	}

	// Create another to test dispute path
	esc2, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	_, err = svc.Dispute(ctx, esc2.ID, "0xbuyer", "reason")
	if err != nil {
		t.Fatalf("Dispute without recorder should not fail: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: ListByAgent default limit
// ---------------------------------------------------------------------------

func TestEscrow_ListByAgentDefaultLimit(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	// Create 3 escrows
	for i := 0; i < 3; i++ {
		svc.Create(ctx, CreateRequest{
			BuyerAddr:  "0xbuyer",
			SellerAddr: "0xseller",
			Amount:     "1.00",
		})
	}

	// Zero limit should default to 50
	list, err := svc.ListByAgent(ctx, "0xbuyer", 0)
	if err != nil {
		t.Fatalf("ListByAgent with 0 limit failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected 3, got %d", len(list))
	}

	// Negative limit should also default
	list, err = svc.ListByAgent(ctx, "0xbuyer", -1)
	if err != nil {
		t.Fatalf("ListByAgent with -1 limit failed: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("Expected 3, got %d", len(list))
	}
}

type failingStore struct {
	*MemoryStore
	failCreate bool
}

func (f *failingStore) Create(ctx context.Context, escrow *Escrow) error {
	if f.failCreate {
		return errors.New("store unavailable")
	}
	return f.MemoryStore.Create(ctx, escrow)
}

func TestEscrow_CreateRollsBackOnStoreFailure(t *testing.T) {
	fStore := &failingStore{MemoryStore: NewMemoryStore(), failCreate: true}
	ledger := newMockLedger()
	svc := NewService(fStore, ledger)
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "5.00",
	})
	if err == nil {
		t.Fatal("Expected error when store.Create fails")
	}

	// Ledger should have been locked AND refunded (rollback)
	if len(ledger.locked) != 1 {
		t.Errorf("Expected 1 lock call, got %d", len(ledger.locked))
	}
	if len(ledger.refunded) != 1 {
		t.Errorf("Expected 1 refund call (rollback), got %d", len(ledger.refunded))
	}
}

// ---------------------------------------------------------------------------
// Edge cases: auto-release on delivered status
// ---------------------------------------------------------------------------

func TestEscrow_AutoReleaseOnDelivered(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	recorder := &mockRecorder{}
	svc := NewService(store, ledger).WithRecorder(recorder)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "1ms",
	})

	// Mark delivered
	svc.MarkDelivered(ctx, esc.ID, "0xseller")

	time.Sleep(5 * time.Millisecond)

	// Auto-release should still work on "delivered" (it's not terminal)
	err := svc.AutoRelease(ctx, esc)
	if err != nil {
		t.Fatalf("AutoRelease on delivered should work: %v", err)
	}

	updated, _ := svc.Get(ctx, esc.ID)
	if updated.Status != StatusExpired {
		t.Errorf("Expected expired, got %s", updated.Status)
	}
}

func TestEscrow_OperationsWithEmptyCaller(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Empty caller can't confirm
	_, err := svc.Confirm(ctx, esc.ID, "")
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized for empty caller on confirm, got %v", err)
	}

	// Empty caller can't deliver
	_, err = svc.MarkDelivered(ctx, esc.ID, "")
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized for empty caller on deliver, got %v", err)
	}

	// Empty caller can't dispute
	_, err = svc.Dispute(ctx, esc.ID, "", "reason")
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized for empty caller on dispute, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: recorder error doesn't block operation
// ---------------------------------------------------------------------------

type failingRecorder struct{}

func (f *failingRecorder) RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error {
	return errors.New("recorder unavailable")
}

func TestEscrow_RecorderErrorDoesNotBlockConfirm(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger).WithRecorder(&failingRecorder{})
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Confirm should succeed even if recorder fails (fire-and-forget)
	result, err := svc.Confirm(ctx, esc.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("Confirm should succeed even with failing recorder: %v", err)
	}
	if result.Status != StatusReleased {
		t.Errorf("Expected released, got %s", result.Status)
	}
}

func TestEscrow_RecorderErrorDoesNotBlockDispute(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger).WithRecorder(&failingRecorder{})
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	result, err := svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	if err != nil {
		t.Fatalf("Dispute should succeed even with failing recorder: %v", err)
	}
	if result.Status != StatusDisputed {
		t.Errorf("Expected disputed, got %s", result.Status)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: escrow ID format
// ---------------------------------------------------------------------------

func TestEscrow_IDFormat(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// ID should start with "esc_"
	if len(esc.ID) < 5 || esc.ID[:4] != "esc_" {
		t.Errorf("Expected ID to start with 'esc_', got %s", esc.ID)
	}

	// ID should be reasonably long (esc_ + 24 hex chars from idgen.WithPrefix)
	if len(esc.ID) != 28 { // "esc_" (4) + 24 hex chars
		t.Errorf("Expected ID length 28, got %d (%s)", len(esc.ID), esc.ID)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: ListExpired with limit
// ---------------------------------------------------------------------------

func TestMemoryStore_ListExpiredLimit(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	past := time.Now().Add(-1 * time.Hour)

	// Create 10 expired escrows
	for i := 0; i < 10; i++ {
		store.Create(ctx, &Escrow{
			ID: "esc_" + string(rune('a'+i)), Status: StatusPending,
			BuyerAddr: "0xb", SellerAddr: "0xs",
			AutoReleaseAt: past, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	}

	expired, err := store.ListExpired(ctx, time.Now(), 3)
	if err != nil {
		t.Fatalf("ListExpired failed: %v", err)
	}
	if len(expired) != 3 {
		t.Errorf("Expected limit of 3, got %d", len(expired))
	}
}

// ---------------------------------------------------------------------------
// Timer: integration test with actual expired escrow
// ---------------------------------------------------------------------------

func TestTimer_ReleasesExpiredEscrow(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create an already-expired escrow directly in store
	now := time.Now()
	store.Create(context.Background(), &Escrow{
		ID:            "esc_expired_1",
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		Amount:        "2.00",
		Status:        StatusPending,
		AutoReleaseAt: now.Add(-1 * time.Minute), // already expired
		CreatedAt:     now,
		UpdatedAt:     now,
	})

	timer := NewTimer(svc, store, logger)

	// Manually trigger releaseExpired
	timer.releaseExpired(context.Background())

	// Verify it was auto-released
	esc, _ := store.Get(context.Background(), "esc_expired_1")
	if esc.Status != StatusExpired {
		t.Errorf("Expected expired status after timer tick, got %s", esc.Status)
	}
	if _, ok := ledger.released["esc_expired_1"]; !ok {
		t.Error("Expected ledger.ReleaseEscrow to be called")
	}
}

func TestTimer_SkipsTerminalEscrows(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	now := time.Now()
	past := now.Add(-1 * time.Minute)

	// Already released — should be skipped by ListExpired
	store.Create(context.Background(), &Escrow{
		ID: "esc_already_done", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "1.00", Status: StatusReleased,
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	// Pending + expired — should be auto-released
	store.Create(context.Background(), &Escrow{
		ID: "esc_pending", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "1.00", Status: StatusPending,
		AutoReleaseAt: past, CreatedAt: now, UpdatedAt: now,
	})

	timer := NewTimer(svc, store, logger)
	timer.releaseExpired(context.Background())

	// Only the pending one should have been released
	if len(ledger.released) != 1 {
		t.Errorf("Expected 1 release, got %d", len(ledger.released))
	}
	if _, ok := ledger.released["esc_pending"]; !ok {
		t.Error("Expected esc_pending to be released")
	}
}

// ---------------------------------------------------------------------------
// Concurrency test: concurrent disputes
// ---------------------------------------------------------------------------

func TestEscrow_ConcurrentDisputes(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Fire 10 concurrent dispute attempts — mainly testing for race conditions
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Dispute(ctx, esc.ID, "0xbuyer", "reason")
		}()
	}
	wg.Wait()

	// Final state should be disputed (the first one wins, rest get ErrInvalidStatus)
	got, _ := svc.Get(ctx, esc.ID)
	if got.Status != StatusDisputed {
		t.Errorf("Expected final status disputed, got %s", got.Status)
	}
}

// ---------------------------------------------------------------------------
// Concurrency test
// ---------------------------------------------------------------------------

func TestEscrow_ConcurrentOperations(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	// Create 50 escrows concurrently
	var wg sync.WaitGroup
	ids := make([]string, 50)
	errs := make([]error, 50)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			esc, err := svc.Create(ctx, CreateRequest{
				BuyerAddr:  "0xbuyer",
				SellerAddr: "0xseller",
				Amount:     "0.01",
			})
			if err != nil {
				errs[idx] = err
			} else {
				ids[idx] = esc.ID
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("Create %d failed: %v", i, err)
		}
	}

	// All IDs should be unique
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("Duplicate ID: %s", id)
		}
		seen[id] = true
	}

	// Confirm all concurrently
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.Confirm(ctx, ids[idx], "0xbuyer")
			if err != nil {
				errs[idx] = err
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Confirm %d failed: %v", i, err)
		}
	}

	// All should be released
	list, _ := svc.ListByAgent(ctx, "0xbuyer", 100)
	for _, e := range list {
		if e.Status != StatusReleased {
			t.Errorf("Escrow %s should be released, got %s", e.ID, e.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// #9: SubmitEvidence tests
// ---------------------------------------------------------------------------

// mockReputation captures RecordDispute calls for verification.
type mockReputation struct {
	mu      sync.Mutex
	records []reputationRecord
}

type reputationRecord struct {
	sellerAddr, outcome, amount string
}

func (m *mockReputation) RecordDispute(_ context.Context, sellerAddr, outcome, amount string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records = append(m.records, reputationRecord{sellerAddr, outcome, amount})
	return nil
}

func TestSubmitEvidence_BuyerAndSeller(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Dispute to enter disputed state
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad service")

	// Buyer submits evidence
	result, err := svc.SubmitEvidence(ctx, esc.ID, "0xbuyer", "screenshot of broken output")
	if err != nil {
		t.Fatalf("Buyer SubmitEvidence failed: %v", err)
	}
	// Initial dispute reason + buyer evidence = 2
	if len(result.DisputeEvidence) != 2 {
		t.Errorf("Expected 2 evidence entries, got %d", len(result.DisputeEvidence))
	}

	// Seller submits evidence
	result, err = svc.SubmitEvidence(ctx, esc.ID, "0xseller", "logs show delivery succeeded")
	if err != nil {
		t.Fatalf("Seller SubmitEvidence failed: %v", err)
	}
	if len(result.DisputeEvidence) != 3 {
		t.Errorf("Expected 3 evidence entries, got %d", len(result.DisputeEvidence))
	}
	if result.DisputeEvidence[2].SubmittedBy != "0xseller" {
		t.Errorf("Expected last evidence from seller, got %s", result.DisputeEvidence[2].SubmittedBy)
	}
}

func TestSubmitEvidence_Unauthorized(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")

	_, err := svc.SubmitEvidence(ctx, esc.ID, "0xstranger", "I have opinions")
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized for stranger, got %v", err)
	}
}

func TestSubmitEvidence_WrongStatus(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Pending escrow — can't submit evidence
	_, err := svc.SubmitEvidence(ctx, esc.ID, "0xbuyer", "evidence")
	if err != ErrInvalidStatus {
		t.Errorf("Expected ErrInvalidStatus for pending escrow, got %v", err)
	}
}

func TestSubmitEvidence_Nonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.SubmitEvidence(ctx, "esc_ghost", "0xbuyer", "evidence")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

func TestSubmitEvidence_ArbitratingStatus(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")

	// Both parties can still submit evidence during arbitration
	_, err := svc.SubmitEvidence(ctx, esc.ID, "0xseller", "additional proof")
	if err != nil {
		t.Fatalf("SubmitEvidence during arbitration should work: %v", err)
	}
}

// ---------------------------------------------------------------------------
// #9: AssignArbitrator tests
// ---------------------------------------------------------------------------

func TestAssignArbitrator_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")

	result, err := svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xARBITRATOR")
	if err != nil {
		t.Fatalf("AssignArbitrator failed: %v", err)
	}
	if result.Status != StatusArbitrating {
		t.Errorf("Expected status arbitrating, got %s", result.Status)
	}
	if result.ArbitratorAddr != "0xarbitrator" {
		t.Errorf("Expected lowercased arbitrator addr, got %s", result.ArbitratorAddr)
	}
	if result.ArbitrationDeadline == nil {
		t.Error("Expected ArbitrationDeadline to be set")
	}
}

func TestAssignArbitrator_WrongStatus(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Pending — not disputed yet (but caller is buyer, so auth passes)
	_, err := svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")
	if err != ErrInvalidStatus {
		t.Errorf("Expected ErrInvalidStatus for pending escrow, got %v", err)
	}
}

func TestAssignArbitrator_Nonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.AssignArbitrator(ctx, "esc_ghost", "0xanyone", "0xarbitrator")
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// #9: ResolveArbitration tests
// ---------------------------------------------------------------------------

func TestResolveArbitration_Release(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	rep := &mockReputation{}
	svc := NewService(store, ledger).WithReputationImpactor(rep)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "5.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")

	result, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "release",
		Reason:     "seller delivered correctly",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration release failed: %v", err)
	}
	if result.Status != StatusReleased {
		t.Errorf("Expected released, got %s", result.Status)
	}
	if _, ok := ledger.released[esc.ID]; !ok {
		t.Error("Expected ledger.ReleaseEscrow to be called")
	}
	if result.ResolvedAt == nil {
		t.Error("Expected ResolvedAt to be set")
	}
	// Reputation: dispute records "disputed", then release records "confirmed"
	if len(rep.records) != 2 {
		t.Fatalf("Expected 2 reputation records (disputed + confirmed), got %d: %v", len(rep.records), rep.records)
	}
	if rep.records[1].outcome != "confirmed" {
		t.Errorf("Expected second reputation record outcome 'confirmed', got %s", rep.records[1].outcome)
	}
}

func TestResolveArbitration_Refund(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	rep := &mockReputation{}
	svc := NewService(store, ledger).WithReputationImpactor(rep)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "3.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")

	result, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "refund",
		Reason:     "seller failed to deliver",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration refund failed: %v", err)
	}
	if result.Status != StatusRefunded {
		t.Errorf("Expected refunded, got %s", result.Status)
	}
	if _, ok := ledger.refunded[esc.ID]; !ok {
		t.Error("Expected ledger.RefundEscrow to be called")
	}
	// Reputation: dispute records "disputed", then refund records "refunded"
	if len(rep.records) != 2 {
		t.Fatalf("Expected 2 reputation records (disputed + refunded), got %d: %v", len(rep.records), rep.records)
	}
	if rep.records[1].outcome != "refunded" {
		t.Errorf("Expected second reputation record outcome 'refunded', got %s", rep.records[1].outcome)
	}
}

func TestResolveArbitration_Partial(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	rep := &mockReputation{}
	svc := NewService(store, ledger).WithReputationImpactor(rep)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "10.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "partial delivery")
	svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")

	result, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: "3.50",
		Reason:        "partial work done",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration partial failed: %v", err)
	}
	if result.Status != StatusReleased {
		t.Errorf("Expected released, got %s", result.Status)
	}
	if result.PartialReleaseAmount == "" {
		t.Error("Expected PartialReleaseAmount to be set")
	}
	if result.PartialRefundAmount == "" {
		t.Error("Expected PartialRefundAmount to be set")
	}
	// Atomic partial settlement should be called
	if len(ledger.partials) != 1 {
		t.Errorf("Expected 1 PartialEscrowSettle call, got %d", len(ledger.partials))
	}
	// Reputation: dispute records "disputed", then partial records "partial"
	if len(rep.records) != 2 {
		t.Fatalf("Expected 2 reputation records (disputed + partial), got %d: %v", len(rep.records), rep.records)
	}
	if rep.records[1].outcome != "partial" {
		t.Errorf("Expected second reputation record outcome 'partial', got %s", rep.records[1].outcome)
	}
}

func TestResolveArbitration_Unauthorized(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")

	// Wrong arbitrator
	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xstranger", ResolveRequest{
		Resolution: "release",
	})
	if err != ErrUnauthorized {
		t.Errorf("Expected ErrUnauthorized for wrong arbitrator, got %v", err)
	}
}

func TestResolveArbitration_WrongStatus(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Pending — not disputed
	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "release",
	})
	if err != ErrInvalidStatus {
		t.Errorf("Expected ErrInvalidStatus for pending escrow, got %v", err)
	}
}

func TestResolveArbitration_InvalidPartialAmount(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "10.00",
	})
	svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")
	svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")

	// Release amount >= total should fail
	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: "10.00",
	})
	if err == nil {
		t.Fatal("Expected error for releaseAmount >= total")
	}

	// Negative release amount should fail
	_, err = svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: "-1.00",
	})
	if err == nil {
		t.Fatal("Expected error for negative releaseAmount")
	}
}

func TestResolveArbitration_Nonexistent(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.ResolveArbitration(ctx, "esc_ghost", "0xarbitrator", ResolveRequest{
		Resolution: "release",
	})
	if err != ErrEscrowNotFound {
		t.Errorf("Expected ErrEscrowNotFound, got %v", err)
	}
}

// --- merged from coverage_extra2_test.go ---

// ============================================================================
// Escrow MemoryStore: deep copy, ListByAgent, ListExpired, ListByStatus
// ============================================================================

func TestMemoryStore_Get_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &Escrow{
		ID: "esc_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "1.000000", Status: StatusPending,
		DisputeEvidence: []EvidenceEntry{{SubmittedBy: "0xbuyer", Content: "proof"}},
	})

	e1, _ := store.Get(ctx, "esc_1")
	e1.DisputeEvidence = append(e1.DisputeEvidence, EvidenceEntry{SubmittedBy: "0xbuyer", Content: "new"})

	e2, _ := store.Get(ctx, "esc_1")
	if len(e2.DisputeEvidence) != 1 {
		t.Errorf("expected deep copy to prevent mutation, got %d evidence entries", len(e2.DisputeEvidence))
	}
}

func TestMemoryStore_Update_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &Escrow{
		ID: "esc_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "1.000000", Status: StatusPending,
		DisputeEvidence: []EvidenceEntry{{SubmittedBy: "0xbuyer", Content: "proof"}},
	})

	updated := &Escrow{
		ID: "esc_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "1.000000", Status: StatusDelivered,
		DisputeEvidence: []EvidenceEntry{{SubmittedBy: "0xseller", Content: "delivery"}},
	}
	store.Update(ctx, updated)

	// Mutate caller's copy
	updated.DisputeEvidence[0].Content = "mutated"

	stored, _ := store.Get(ctx, "esc_1")
	if stored.DisputeEvidence[0].Content != "delivery" {
		t.Errorf("expected deep copy on update, got %s", stored.DisputeEvidence[0].Content)
	}
}

func TestMemoryStore_Get_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.Get(context.Background(), "nonexistent")
	if !errors.Is(err, ErrEscrowNotFound) {
		t.Errorf("expected ErrEscrowNotFound, got %v", err)
	}
}

func TestMemoryStore_ListByAgent_BuyerAndSeller(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &Escrow{
		ID: "esc_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Status: StatusPending,
	})
	store.Create(ctx, &Escrow{
		ID: "esc_2", BuyerAddr: "0xother", SellerAddr: "0xbuyer",
		Status: StatusPending,
	})

	// Should find both as buyer and seller
	list, err := store.ListByAgent(ctx, "0xBuyer", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("expected 2 escrows for buyer, got %d", len(list))
	}
}

func TestValidateAmount_Coverage(t *testing.T) {
	tests := []struct {
		amount  string
		wantErr bool
	}{
		{"1.000000", false},
		{"0.000001", false},
		{"0", true},                           // not positive
		{"-1", true},                          // negative
		{"", true},                            // empty
		{"abc", true},                         // not a number
		{"  ", true},                          // whitespace only
		{"99999999999999999999.000000", true}, // exceeds max (parsed * 1e6 overflows)
	}
	for _, tt := range tests {
		err := validateAmount(tt.amount)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateAmount(%q) error = %v, wantErr %v", tt.amount, err, tt.wantErr)
		}
	}
}

// ============================================================================
// Escrow: MoneyError
// ============================================================================

func TestEscrow_MoneyError(t *testing.T) {
	inner := fmt.Errorf("ledger failure")
	me := &MoneyError{
		Err:         inner,
		FundsStatus: "locked_in_escrow",
		Recovery:    "Contact support",
		Amount:      "5.00",
		Reference:   "esc_123",
	}
	if me.Error() != "ledger failure" {
		t.Errorf("unexpected error message: %s", me.Error())
	}
	if me.Unwrap() != inner {
		t.Error("Unwrap should return inner error")
	}
	if !errors.Is(me, inner) {
		t.Error("expected errors.Is to find inner error via Unwrap")
	}
}

func TestEscrow_MoneyFields(t *testing.T) {
	fields := moneyFields(fmt.Errorf("plain error"))
	if fields != nil {
		t.Error("expected nil for non-MoneyError")
	}

	me := &MoneyError{
		Err:         fmt.Errorf("test"),
		FundsStatus: "no_change",
		Recovery:    "Retry",
		Amount:      "5.00",
		Reference:   "ref1",
	}
	fields = moneyFields(me)
	if fields == nil {
		t.Fatal("expected non-nil for MoneyError")
	}
	if fields["funds_status"] != "no_change" {
		t.Errorf("expected no_change, got %v", fields["funds_status"])
	}
	if fields["amount"] != "5.00" {
		t.Errorf("expected 5.00, got %v", fields["amount"])
	}
}

func TestEscrow_MoneyFields_EmptyOptional(t *testing.T) {
	me := &MoneyError{
		Err:         fmt.Errorf("test"),
		FundsStatus: "no_change",
		Recovery:    "Retry",
	}
	fields := moneyFields(me)
	if _, ok := fields["amount"]; ok {
		t.Error("empty amount should not be in fields")
	}
	if _, ok := fields["reference"]; ok {
		t.Error("empty reference should not be in fields")
	}
}

// ============================================================================
// Escrow: ListOption / WithCursor
// ============================================================================

func TestEscrow_ListOption_InvalidCursor(t *testing.T) {
	opt := WithCursor("invalid-base64")
	var o listOpts
	opt(&o)
	if o.cursor != nil {
		t.Error("expected nil cursor for invalid input")
	}
}

func TestEscrow_ApplyListOpts_Empty(t *testing.T) {
	o := applyListOpts(nil)
	if o.cursor != nil {
		t.Error("expected nil cursor for empty opts")
	}
}

// ============================================================================
// Escrow Service: With* methods
// ============================================================================

func TestEscrowService_WithMethods(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml)

	if got := svc.WithLogger(slog.Default()); got != svc {
		t.Error("WithLogger should return same service")
	}
	if got := svc.WithRecorder(&mockRecorder{}); got != svc {
		t.Error("WithRecorder should return same service")
	}
	if got := svc.WithRevenueAccumulator(nil); got != svc {
		t.Error("WithRevenueAccumulator should return same service")
	}
	if got := svc.WithReputationImpactor(nil); got != svc {
		t.Error("WithReputationImpactor should return same service")
	}
	if got := svc.WithReceiptIssuer(nil); got != svc {
		t.Error("WithReceiptIssuer should return same service")
	}
	if got := svc.WithWebhookEmitter(nil); got != svc {
		t.Error("WithWebhookEmitter should return same service")
	}
	if got := svc.WithTrustGate(nil); got != svc {
		t.Error("WithTrustGate should return same service")
	}
}

// ============================================================================
// Escrow: Create edge cases
// ============================================================================

func TestEscrow_Create_SameBuyerSeller_Extra(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml)

	_, err := svc.Create(context.Background(), CreateRequest{
		BuyerAddr:  "0xsame",
		SellerAddr: "0xSame", // same address, different case
		Amount:     "1.000000",
	})
	if err == nil {
		t.Fatal("expected error for same buyer and seller")
	}
}

func TestMultiStepMemoryStore_RecordStep_Duplicate(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &MultiStepEscrow{
		ID: "mse_1", BuyerAddr: "0xbuyer", TotalAmount: "2.000000",
		SpentAmount: "0", TotalSteps: 2, Status: MSOpen,
	})

	store.RecordStep(ctx, "mse_1", Step{StepIndex: 0, Amount: "1.000000", SellerAddr: "0xs"})
	err := store.RecordStep(ctx, "mse_1", Step{StepIndex: 0, Amount: "1.000000", SellerAddr: "0xs"})
	if !errors.Is(err, ErrDuplicateStep) {
		t.Errorf("expected ErrDuplicateStep, got %v", err)
	}
}

func TestMultiStepMemoryStore_DeleteStep(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &MultiStepEscrow{
		ID: "mse_1", BuyerAddr: "0xbuyer", TotalAmount: "2.000000",
		SpentAmount: "0", TotalSteps: 2, Status: MSOpen,
	})

	store.RecordStep(ctx, "mse_1", Step{StepIndex: 0, Amount: "1.000000", SellerAddr: "0xs"})

	// Verify step was recorded
	mse, _ := store.Get(ctx, "mse_1")
	if mse.ConfirmedSteps != 1 {
		t.Fatalf("expected 1 confirmed step, got %d", mse.ConfirmedSteps)
	}

	err := store.DeleteStep(ctx, "mse_1", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mse, _ = store.Get(ctx, "mse_1")
	if mse.ConfirmedSteps != 0 {
		t.Errorf("expected 0 after delete, got %d", mse.ConfirmedSteps)
	}
}

func TestMultiStepMemoryStore_DeleteStep_NotFound(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &MultiStepEscrow{
		ID: "mse_1", BuyerAddr: "0xbuyer", TotalAmount: "2.000000",
		SpentAmount: "0", TotalSteps: 2, Status: MSOpen,
	})

	err := store.DeleteStep(ctx, "mse_1", 99) // non-existent step
	if !errors.Is(err, ErrStepOutOfRange) {
		t.Errorf("expected ErrStepOutOfRange, got %v", err)
	}
}

func TestMultiStepMemoryStore_DeleteStep_EscrowNotFound(t *testing.T) {
	store := NewMultiStepMemoryStore()
	err := store.DeleteStep(context.Background(), "nonexistent", 0)
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Errorf("expected ErrMultiStepNotFound, got %v", err)
	}
}

func TestMultiStepMemoryStore_Abort(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &MultiStepEscrow{
		ID: "mse_1", BuyerAddr: "0xbuyer", TotalAmount: "2.000000",
		SpentAmount: "0", TotalSteps: 2, Status: MSOpen,
	})

	err := store.Abort(ctx, "mse_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mse, _ := store.Get(ctx, "mse_1")
	if mse.Status != MSAborted {
		t.Errorf("expected aborted, got %s", mse.Status)
	}
}

func TestMultiStepMemoryStore_Abort_NotFound(t *testing.T) {
	store := NewMultiStepMemoryStore()
	err := store.Abort(context.Background(), "nonexistent")
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Errorf("expected ErrMultiStepNotFound, got %v", err)
	}
}

func TestMultiStepMemoryStore_Complete_NotFound(t *testing.T) {
	store := NewMultiStepMemoryStore()
	err := store.Complete(context.Background(), "nonexistent")
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Errorf("expected ErrMultiStepNotFound, got %v", err)
	}
}

func TestMultiStepMemoryStore_Get_DeepCopy(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ctx := context.Background()

	store.Create(ctx, &MultiStepEscrow{
		ID: "mse_1", BuyerAddr: "0xbuyer", TotalAmount: "2.000000",
		SpentAmount: "0", TotalSteps: 2, Status: MSOpen,
		PlannedSteps: []PlannedStep{{SellerAddr: "0xs", Amount: "1.000000"}},
	})

	mse1, _ := store.Get(ctx, "mse_1")
	mse1.PlannedSteps[0].SellerAddr = "mutated"

	mse2, _ := store.Get(ctx, "mse_1")
	if mse2.PlannedSteps[0].SellerAddr != "0xs" {
		t.Error("expected deep copy to prevent mutation")
	}
}

// ============================================================================
// MultiStep Service: edge cases
// ============================================================================

func TestMultiStep_WithLogger(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	got := svc.WithLogger(slog.Default())
	if got != svc {
		t.Error("WithLogger should return same service")
	}
}

func TestMultiStep_Get_NotFound(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	_, err := svc.Get(context.Background(), "nonexistent")
	if !errors.Is(err, ErrMultiStepNotFound) {
		t.Errorf("expected ErrMultiStepNotFound, got %v", err)
	}
}

func TestMultiStep_LockSteps_InvalidSteps(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	// Zero steps
	_, err := svc.LockSteps(context.Background(), "0xbuyer", "1.000000", 0, nil)
	if err == nil {
		t.Error("expected error for zero steps")
	}

	// Too many steps
	_, err = svc.LockSteps(context.Background(), "0xbuyer", "1.000000", MaxTotalSteps+1, nil)
	if err == nil {
		t.Error("expected error for too many steps")
	}
}

func TestMultiStep_LockSteps_MismatchedPlannedSteps(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	_, err := svc.LockSteps(context.Background(), "0xbuyer", "1.000000", 2, []PlannedStep{
		{SellerAddr: "0xs", Amount: "1.000000"},
	})
	if err == nil {
		t.Error("expected error for mismatched planned steps count")
	}
}

func TestMultiStep_LockSteps_InvalidPlannedStepAmount(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	_, err := svc.LockSteps(context.Background(), "0xbuyer", "1.000000", 1, []PlannedStep{
		{SellerAddr: "0xs", Amount: "invalid"},
	})
	if err == nil {
		t.Error("expected error for invalid step amount")
	}
}

func TestMultiStep_LockSteps_EmptySeller(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	_, err := svc.LockSteps(context.Background(), "0xbuyer", "1.000000", 1, []PlannedStep{
		{SellerAddr: "", Amount: "1.000000"},
	})
	if err == nil {
		t.Error("expected error for empty seller")
	}
}

func TestMultiStep_RefundRemaining_WrongCaller(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	mse, _ := svc.LockSteps(context.Background(), "0xbuyer", "2.000000", 2, []PlannedStep{
		{SellerAddr: "0xs1", Amount: "1.000000"},
		{SellerAddr: "0xs2", Amount: "1.000000"},
	})

	_, err := svc.RefundRemaining(context.Background(), mse.ID, "0xstranger")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestMultiStep_ConfirmStep_StepMismatch(t *testing.T) {
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	mse, _ := svc.LockSteps(context.Background(), "0xbuyer", "2.000000", 2, []PlannedStep{
		{SellerAddr: "0xs1", Amount: "1.000000"},
		{SellerAddr: "0xs2", Amount: "1.000000"},
	})

	// Wrong seller for step 0
	_, err := svc.ConfirmStep(context.Background(), mse.ID, 0, "0xwrong", "1.000000")
	if err == nil {
		t.Fatal("expected error for wrong seller")
	}
	if !errors.Is(err, ErrStepMismatch) {
		t.Errorf("expected ErrStepMismatch, got %v", err)
	}

	// Wrong amount for step 0
	_, err = svc.ConfirmStep(context.Background(), mse.ID, 0, "0xs1", "0.500000")
	if err == nil {
		t.Fatal("expected error for wrong amount")
	}
}

// ============================================================================
// Coalition: CoalitionEscrow model methods
// ============================================================================

func TestCoalitionEscrow_IsTerminal_Coverage(t *testing.T) {
	tests := []struct {
		status   CoalitionStatus
		terminal bool
	}{
		{CSActive, false},
		{CSDelivered, false},
		{CSSettled, true},
		{CSAborted, true},
		{CSExpired, true},
	}
	for _, tt := range tests {
		ce := &CoalitionEscrow{Status: tt.status}
		if ce.IsTerminal() != tt.terminal {
			t.Errorf("IsTerminal(%s) = %v, want %v", tt.status, ce.IsTerminal(), tt.terminal)
		}
	}
}

func TestCoalitionEscrow_AllMembersCompleted_Coverage(t *testing.T) {
	now := time.Now()
	ce := &CoalitionEscrow{
		Members: []CoalitionMember{
			{AgentAddr: "0xa", CompletedAt: &now},
			{AgentAddr: "0xb", CompletedAt: nil},
		},
	}
	if ce.allMembersCompleted() {
		t.Error("expected false when not all completed")
	}

	ce.Members[1].CompletedAt = &now
	if !ce.allMembersCompleted() {
		t.Error("expected true when all completed")
	}
}

// ============================================================================
// Coalition MemoryStore: deep copy
// ============================================================================

func TestCoalitionMemoryStore_DeepCopy(t *testing.T) {
	store := NewCoalitionMemoryStore()
	ctx := context.Background()
	now := time.Now()
	score := 0.95

	store.Create(ctx, &CoalitionEscrow{
		ID: "coa_1", BuyerAddr: "0xbuyer", OracleAddr: "0xoracle",
		TotalAmount:   "1.000000",
		Members:       []CoalitionMember{{AgentAddr: "0xa", CompletedAt: &now}},
		QualityTiers:  []QualityTier{{Name: "good", MinScore: 0.5}},
		Contributions: map[string]float64{"0xa": 0.5},
		QualityScore:  &score,
		SettledAt:     &now,
		Status:        CSActive,
		AutoSettleAt:  now.Add(1 * time.Hour),
		CreatedAt:     now, UpdatedAt: now,
	})

	ce1, _ := store.Get(ctx, "coa_1")
	ce1.Members[0].AgentAddr = "mutated"
	ce1.QualityTiers[0].Name = "mutated"
	ce1.Contributions["0xa"] = 999

	ce2, _ := store.Get(ctx, "coa_1")
	if ce2.Members[0].AgentAddr == "mutated" {
		t.Error("members should be deep copied")
	}
	if ce2.QualityTiers[0].Name == "mutated" {
		t.Error("quality tiers should be deep copied")
	}
	if ce2.Contributions["0xa"] == 999 {
		t.Error("contributions should be deep copied")
	}
}

func TestCoalitionMemoryStore_Get_NotFound(t *testing.T) {
	store := NewCoalitionMemoryStore()
	_, err := store.Get(context.Background(), "nonexistent")
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Errorf("expected ErrCoalitionNotFound, got %v", err)
	}
}

// ============================================================================
// Escrow Handler: HTTP endpoint coverage
// ============================================================================

func testEscrowService() *Service {
	store := NewMemoryStore()
	ml := newMockLedger()
	return NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

func TestHandler_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := testEscrowService()
	handler := NewHandler(svc)

	r := gin.New()
	g := r.Group("/v1")
	handler.RegisterRoutes(g)
	handler.RegisterProtectedRoutes(g)

	routes := r.Routes()
	if len(routes) == 0 {
		t.Error("expected routes to be registered")
	}
}

func TestHandler_WithScopeChecker(t *testing.T) {
	svc := testEscrowService()
	handler := NewHandler(svc)
	got := handler.WithScopeChecker(nil)
	if got != handler {
		t.Error("WithScopeChecker should return same handler")
	}
}

func TestHandler_CreateEscrow_NotBuyer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := testEscrowService()
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/escrow", func(c *gin.Context) {
		c.Set("authAgentAddr", "0x1234567890abcdef1234567890abcdef12345678")
		c.Next()
	}, handler.CreateEscrow)

	body := `{"buyerAddr":"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","sellerAddr":"0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","amount":"1.000000"}`
	req := httptest.NewRequest("POST", "/escrow", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestHandler_SubmitEvidence_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := testEscrowService()
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/escrow/:id/evidence", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.SubmitEvidence)

	req := httptest.NewRequest("POST", "/escrow/esc_1/evidence", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_AssignArbitrator_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := testEscrowService()
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/escrow/:id/arbitrate", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.AssignArbitrator)

	req := httptest.NewRequest("POST", "/escrow/esc_1/arbitrate", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandler_ResolveArbitration_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := testEscrowService()
	handler := NewHandler(svc)

	r := gin.New()
	r.POST("/escrow/:id/resolve", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xarbitrator")
		c.Next()
	}, handler.ResolveArbitration)

	req := httptest.NewRequest("POST", "/escrow/esc_1/resolve", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// ============================================================================
// MultiStep Handler coverage
// ============================================================================

func TestMultiStepHandler_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	g := r.Group("/v1")
	handler.RegisterRoutes(g)
	handler.RegisterProtectedRoutes(g)

	routes := r.Routes()
	if len(routes) == 0 {
		t.Error("expected routes to be registered")
	}
}

func TestMultiStepHandler_CreateBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	r.POST("/escrow/multistep", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateMultiStepEscrow)

	req := httptest.NewRequest("POST", "/escrow/multistep", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestMultiStepHandler_ConfirmStep_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	r.POST("/escrow/multistep/:id/confirm-step", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.ConfirmStep)

	req := httptest.NewRequest("POST", "/escrow/multistep/mse_1/confirm-step", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestMultiStepHandler_GetNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	r.GET("/escrow/multistep/:id", handler.GetMultiStepEscrow)

	req := httptest.NewRequest("GET", "/escrow/multistep/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestMultiStepHandler_RefundNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	r.POST("/escrow/multistep/:id/refund", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.RefundRemaining)

	req := httptest.NewRequest("POST", "/escrow/multistep/nonexistent/refund", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ============================================================================
// Coalition Handler coverage
// ============================================================================

func TestCoalitionHandler_RegisterRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	g := r.Group("/v1")
	handler.RegisterRoutes(g)
	handler.RegisterProtectedRoutes(g)

	routes := r.Routes()
	if len(routes) == 0 {
		t.Error("expected routes to be registered")
	}
}

func TestCoalitionHandler_CreateBadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.POST("/coalition", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateCoalition)

	req := httptest.NewRequest("POST", "/coalition", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCoalitionHandler_CreateNotBuyer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.POST("/coalition", func(c *gin.Context) {
		c.Set("authAgentAddr", "0x1111111111111111111111111111111111111111")
		c.Next()
	}, handler.CreateCoalition)

	body, _ := json.Marshal(CreateCoalitionRequest{
		BuyerAddr:     "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		OracleAddr:    "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TotalAmount:   "1.000000",
		SplitStrategy: SplitEqual,
		Members:       []CoalitionMember{{AgentAddr: "0xcccccccccccccccccccccccccccccccccccccccc", Role: "a"}},
		QualityTiers:  []QualityTier{{Name: "good", MinScore: 0.5, PayoutPct: 100}},
	})
	req := httptest.NewRequest("POST", "/coalition", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestCoalitionHandler_GetNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.GET("/coalition/:id", handler.GetCoalition)

	req := httptest.NewRequest("GET", "/coalition/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCoalitionHandler_ListCoalitions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.GET("/agents/:address/coalitions", handler.ListCoalitions)

	req := httptest.NewRequest("GET", "/agents/0xbuyer/coalitions?limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestCoalitionHandler_ListCoalitions_CapAt200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.GET("/agents/:address/coalitions", handler.ListCoalitions)

	req := httptest.NewRequest("GET", "/agents/0xbuyer/coalitions?limit=500", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestCoalitionHandler_ReportCompletion_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.POST("/coalition/:id/complete", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xagent")
		c.Next()
	}, handler.ReportCompletion)

	req := httptest.NewRequest("POST", "/coalition/nonexistent/complete", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCoalitionHandler_OracleReport_BadBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.POST("/coalition/:id/oracle-report", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xoracle")
		c.Next()
	}, handler.OracleReport)

	req := httptest.NewRequest("POST", "/coalition/coa_1/oracle-report", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCoalitionHandler_AbortNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.POST("/coalition/:id/abort", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.AbortCoalition)

	req := httptest.NewRequest("POST", "/coalition/nonexistent/abort", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestCoalitionHandler_CreateWithInvalidContractID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	handler := NewCoalitionHandler(svc)

	r := gin.New()
	r.POST("/coalition", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateCoalition)

	body, _ := json.Marshal(CreateCoalitionRequest{
		BuyerAddr:     "0xbuyer",
		OracleAddr:    "0xoracle",
		TotalAmount:   "1.000000",
		SplitStrategy: SplitEqual,
		Members:       []CoalitionMember{{AgentAddr: "0xa", Role: "a"}},
		QualityTiers:  []QualityTier{{Name: "good", MinScore: 0.5, PayoutPct: 100}},
		ContractID:    "ab", // too short (< 4 characters)
	})
	req := httptest.NewRequest("POST", "/coalition", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for short contractId, got %d", w.Code)
	}
}

// ============================================================================
// Timer: lifecycle tests
// ============================================================================

func TestTimer_StartAndStop(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml)
	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if timer.Running() {
		t.Fatal("should not be running before Start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go timer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	if !timer.Running() {
		t.Fatal("should be running after Start")
	}

	timer.Stop()
	time.Sleep(100 * time.Millisecond)

	if timer.Running() {
		t.Fatal("should not be running after Stop")
	}
	cancel()
}

func TestTimer_ContextCancel(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml)
	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	go timer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond)

	if timer.Running() {
		t.Fatal("should not be running after context cancel")
	}
}

func TestCoalitionTimer_ContextCancel(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	timer := NewCoalitionTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	go timer.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	cancel()
	time.Sleep(100 * time.Millisecond)

	if timer.Running() {
		t.Fatal("should not be running after context cancel")
	}
}

// ============================================================================
// MultiStepHandler: ConfirmStep with non-buyer
// ============================================================================

func TestMultiStepHandler_ConfirmStep_NotBuyer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)

	sellerAddr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mse, _ := svc.LockSteps(context.Background(), "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "2.000000", 2, []PlannedStep{
		{SellerAddr: sellerAddr, Amount: "1.000000"},
		{SellerAddr: sellerAddr, Amount: "1.000000"},
	})

	handler := NewMultiStepHandler(svc)
	r := gin.New()
	r.POST("/escrow/multistep/:id/confirm-step", func(c *gin.Context) {
		c.Set("authAgentAddr", "0x1111111111111111111111111111111111111111")
		c.Next()
	}, handler.ConfirmStep)

	body := fmt.Sprintf(`{"stepIndex":0,"sellerAddr":"%s","amount":"1.000000"}`, sellerAddr)
	req := httptest.NewRequest("POST", "/escrow/multistep/"+mse.ID+"/confirm-step", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// ============================================================================
// MultiStepHandler: Create with totalSteps validation
// ============================================================================

func TestMultiStepHandler_Create_TooManySteps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	r.POST("/escrow/multistep", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateMultiStepEscrow)

	body, _ := json.Marshal(map[string]interface{}{
		"totalAmount":  "1.000000",
		"totalSteps":   MaxTotalSteps + 1,
		"plannedSteps": []PlannedStepRequest{},
	})
	req := httptest.NewRequest("POST", "/escrow/multistep", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestMultiStepHandler_Create_MismatchedSteps(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewMultiStepMemoryStore()
	ml := newMockLedger()
	svc := NewMultiStepService(store, ml)
	handler := NewMultiStepHandler(svc)

	r := gin.New()
	r.POST("/escrow/multistep", func(c *gin.Context) {
		c.Set("authAgentAddr", "0xbuyer")
		c.Next()
	}, handler.CreateMultiStepEscrow)

	body, _ := json.Marshal(map[string]interface{}{
		"totalAmount": "1.000000",
		"totalSteps":  2,
		"plannedSteps": []PlannedStepRequest{
			{SellerAddr: "0xs", Amount: "1.000000"},
		},
	})
	req := httptest.NewRequest("POST", "/escrow/multistep", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for mismatched steps, got %d", w.Code)
	}
}

// --- merged from escrow_extra_test.go ---

func testService() (*Service, *mockLedger) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return svc, ml
}

func TestEscrow_SubmitEvidence_EmptyContent(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")

	_, err := svc.SubmitEvidence(ctx, esc.ID, "0xbuyer", "")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestEscrow_AssignArbitrator_Unauthorized(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")

	_, err := svc.AssignArbitrator(ctx, esc.ID, "0xstranger", "0xarbitrator")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestEscrow_AssignArbitrator_SelfAssignment(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")

	// Buyer as arbitrator
	_, err := svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xbuyer")
	if err == nil {
		t.Fatal("expected error for self-assignment (buyer)")
	}

	// Seller as arbitrator
	_, err = svc.AssignArbitrator(ctx, esc.ID, "0xseller", "0xseller")
	if err == nil {
		t.Fatal("expected error for self-assignment (seller)")
	}
}

func setupArbitration(t *testing.T) (*Service, *Escrow) {
	t.Helper()
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "10.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad service")
	esc, _ = svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")
	return svc, esc
}

func TestEscrow_ResolveArbitration_InvalidResolution(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid resolution")
	}
}

func TestEscrow_ResolveArbitration_PartialExceedsTotal(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: "10.00", // equal to total, must be less
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestEscrow_ResolveArbitration_NoArbitrator(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "10.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad")

	// No arbitrator assigned yet
	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xsomeone", ResolveRequest{
		Resolution: "release",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized (no arbitrator), got %v", err)
	}
}

func TestEscrow_ResolveArbitration_AlreadyResolved(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	// First resolve succeeds
	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "release",
	})
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Second resolve should fail
	_, err = svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "refund",
	})
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("expected ErrAlreadyResolved, got %v", err)
	}
}

func TestEscrow_ForceCloseExpired(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml)
	ctx := context.Background()

	// Create 2 expired escrows and 1 disputed
	esc1, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr: "0xbuyer", SellerAddr: "0xseller", Amount: "1.00", AutoRelease: "1ms",
	})
	esc2, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr: "0xbuyer", SellerAddr: "0xseller2", Amount: "2.00", AutoRelease: "1ms",
	})
	esc3, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr: "0xbuyer", SellerAddr: "0xseller3", Amount: "3.00", AutoRelease: "1ms",
	})
	// Dispute esc3 — should NOT be force-closed
	_, _ = svc.Dispute(ctx, esc3.ID, "0xbuyer", "bad")

	time.Sleep(5 * time.Millisecond)

	closed, err := svc.ForceCloseExpired(ctx)
	if err != nil {
		t.Fatalf("ForceCloseExpired: %v", err)
	}
	if closed != 2 {
		t.Fatalf("expected 2 closed, got %d", closed)
	}

	g1, _ := svc.Get(ctx, esc1.ID)
	g2, _ := svc.Get(ctx, esc2.ID)
	g3, _ := svc.Get(ctx, esc3.ID)
	if g1.Status != StatusExpired {
		t.Errorf("esc1 should be expired, got %s", g1.Status)
	}
	if g2.Status != StatusExpired {
		t.Errorf("esc2 should be expired, got %s", g2.Status)
	}
	if g3.Status != StatusDisputed {
		t.Errorf("esc3 should remain disputed, got %s", g3.Status)
	}
}

func TestEscrow_Dispute_EmptyReason(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	_, err := svc.Dispute(ctx, esc.ID, "0xbuyer", "")
	if err == nil {
		t.Fatal("expected error for empty reason")
	}
}

func TestMemoryStore_ListByStatus(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	store.Create(ctx, &Escrow{
		ID: "esc1", Status: StatusPending,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: now, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &Escrow{
		ID: "esc2", Status: StatusDisputed,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: now, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &Escrow{
		ID: "esc3", Status: StatusPending,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		AutoReleaseAt: now, CreatedAt: now, UpdatedAt: now,
	})

	// List pending
	result, err := store.ListByStatus(ctx, StatusPending, 100)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(result))
	}

	// List disputed
	result, err = store.ListByStatus(ctx, StatusDisputed, 100)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 disputed, got %d", len(result))
	}

	// List with limit
	result, err = store.ListByStatus(ctx, StatusPending, 1)
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 (limited), got %d", len(result))
	}
}

func TestEscrow_AutoRelease_LedgerReleaseFailure(t *testing.T) {
	store := NewMemoryStore()
	fl := &failingLedger{releaseErr: errors.New("release failed")}
	svc := NewService(store, fl)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Amount: "1.00", AutoRelease: "1ms",
	})
	time.Sleep(5 * time.Millisecond)

	err := svc.AutoRelease(ctx, esc)
	if err == nil {
		t.Fatal("expected error when release fails")
	}

	var me *MoneyError
	if !errors.As(err, &me) {
		t.Fatalf("expected MoneyError, got %T", err)
	}
	if me.FundsStatus != "locked_in_escrow" {
		t.Errorf("expected locked_in_escrow, got %s", me.FundsStatus)
	}
}

// ---------------------------------------------------------------------------
// Escrow: Create with TrustGate
// ---------------------------------------------------------------------------

type mockTrustGate struct {
	err error
}

func (m *mockTrustGate) CheckCounterpartyTrust(_ context.Context, _ string) error {
	return m.err
}

func TestEscrow_Create_TrustGateReject(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml).
		WithTrustGate(&mockTrustGate{err: errors.New("untrusted agent")})
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	if err == nil {
		t.Fatal("expected error from trust gate")
	}
}

func TestEscrow_Create_TrustGateAllow(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml).
		WithTrustGate(&mockTrustGate{err: nil})
	ctx := context.Background()

	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if esc.Status != StatusPending {
		t.Fatalf("expected pending, got %s", esc.Status)
	}
}
