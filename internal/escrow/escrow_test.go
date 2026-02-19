package escrow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
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

// ---------------------------------------------------------------------------
// Edge cases: IsTerminal coverage
// ---------------------------------------------------------------------------

func TestEscrow_IsTerminal(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
	}{
		{StatusPending, false},
		{StatusDelivered, false},
		{StatusReleased, true},
		{StatusRefunded, true},
		{StatusExpired, true},
		{StatusDisputed, false},
		{StatusArbitrating, false},
	}

	for _, tt := range tests {
		e := &Escrow{Status: tt.status}
		if e.IsTerminal() != tt.terminal {
			t.Errorf("Status %s: expected IsTerminal=%v, got %v", tt.status, tt.terminal, e.IsTerminal())
		}
	}
}

// ---------------------------------------------------------------------------
// Timer tests
// ---------------------------------------------------------------------------

func TestTimer_StartStop(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	timer := NewTimer(svc, store, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		timer.Start(ctx)
		close(done)
	}()

	// Let it tick at least once (timer is 30s, so let's stop via context)
	time.Sleep(50 * time.Millisecond)
	timer.Stop()

	select {
	case <-done:
		// Timer stopped cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("Timer did not stop within 2 seconds")
	}
}

func TestTimer_ContextCancellation(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	timer := NewTimer(svc, store, logger)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		timer.Start(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Timer stopped cleanly via context
	case <-time.After(2 * time.Second):
		t.Fatal("Timer did not stop on context cancel within 2 seconds")
	}
}

// ---------------------------------------------------------------------------
// Edge cases: store failure triggers ledger rollback on Create
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Edge cases: auto-release ledger failure
// ---------------------------------------------------------------------------

func TestEscrow_AutoReleaseFailsOnLedgerError(t *testing.T) {
	store := NewMemoryStore()
	fl := &failingLedger{releaseErr: errors.New("ledger down")}
	svc := NewService(store, fl)
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:   "0xbuyer",
		SellerAddr:  "0xseller",
		Amount:      "1.00",
		AutoRelease: "1ms",
	})

	time.Sleep(5 * time.Millisecond)

	err := svc.AutoRelease(ctx, esc)
	if err == nil {
		t.Fatal("Expected error when ledger fails during auto-release")
	}

	// Should remain pending (not expired)
	got, _ := store.Get(ctx, esc.ID)
	if got.Status != StatusPending {
		t.Errorf("Should remain pending after failed auto-release, got %s", got.Status)
	}
}

// ---------------------------------------------------------------------------
// Edge cases: same buyer and seller
// ---------------------------------------------------------------------------

func TestEscrow_SameBuyerAndSeller(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	// Self-payment escrow must be rejected to prevent reputation gaming
	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xsame",
		SellerAddr: "0xsame",
		Amount:     "1.00",
	})
	if err == nil {
		t.Fatal("Create with same buyer/seller should be rejected")
	}
}

// ---------------------------------------------------------------------------
// Edge cases: dispute with empty-ish caller
// ---------------------------------------------------------------------------

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

	// ID should be reasonably long (esc_ + 32 hex chars)
	if len(esc.ID) != 36 { // "esc_" (4) + 32 hex chars
		t.Errorf("Expected ID length 36, got %d (%s)", len(esc.ID), esc.ID)
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
