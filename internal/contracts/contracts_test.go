package contracts

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
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		locked:   make(map[string]string),
		released: make(map[string]string),
		refunded: make(map[string]string),
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

// failingLedger returns errors on specific operations.
type failingLedger struct {
	mu         sync.Mutex
	lockErr    error
	releaseErr error
	refundErr  error
	calls      []string
	lockCount  int // count how many locks succeed before failing
	lockLimit  int // fail after this many locks (-1 = always fail)
}

func (f *failingLedger) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "lock:"+reference)
	if f.lockErr != nil {
		if f.lockLimit < 0 {
			return f.lockErr
		}
		f.lockCount++
		if f.lockCount > f.lockLimit {
			return f.lockErr
		}
	}
	return nil
}

func (f *failingLedger) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "release:"+reference)
	return f.releaseErr
}

func (f *failingLedger) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "refund:"+reference)
	return f.refundErr
}

// ---------------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------------

func TestContract_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"

	// Propose
	contract, err := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    buyer,
		SellerAddr:   seller,
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "0.025",
		Duration:     "7d",
		MinVolume:    3,
	})
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}
	if contract.Status != StatusProposed {
		t.Errorf("Expected status proposed, got %s", contract.Status)
	}
	if len(ledger.locked) != 0 {
		t.Error("No funds should be locked on propose")
	}

	// Accept
	contract, err = svc.Accept(ctx, contract.ID, seller)
	if err != nil {
		t.Fatalf("Accept failed: %v", err)
	}
	if contract.Status != StatusActive {
		t.Errorf("Expected status active, got %s", contract.Status)
	}
	if _, ok := ledger.locked[contract.ID]; !ok {
		t.Error("Expected buyer budget to be locked")
	}
	if contract.StartsAt == nil {
		t.Error("Expected StartsAt to be set")
	}
	if contract.ExpiresAt == nil {
		t.Error("Expected ExpiresAt to be set")
	}

	// Record 5 successful calls (budget is 0.025 at 0.005 each = 5 calls)
	for i := 0; i < 4; i++ {
		contract, err = svc.RecordCall(ctx, contract.ID, RecordCallRequest{
			Status:    "success",
			LatencyMs: 100,
		}, buyer)
		if err != nil {
			t.Fatalf("RecordCall %d failed: %v", i, err)
		}
		if contract.TotalCalls != i+1 {
			t.Errorf("Expected %d total calls, got %d", i+1, contract.TotalCalls)
		}
	}

	// Last call should trigger completion (budget exhausted + min volume met)
	contract, err = svc.RecordCall(ctx, contract.ID, RecordCallRequest{
		Status:    "success",
		LatencyMs: 100,
	}, buyer)
	if err != nil {
		t.Fatalf("RecordCall final failed: %v", err)
	}
	if contract.Status != StatusCompleted {
		t.Errorf("Expected status completed, got %s", contract.Status)
	}
	if contract.TotalCalls != 5 {
		t.Errorf("Expected 5 total calls, got %d", contract.TotalCalls)
	}
	if contract.SuccessfulCalls != 5 {
		t.Errorf("Expected 5 successful calls, got %d", contract.SuccessfulCalls)
	}
	if contract.ResolvedAt == nil {
		t.Error("Expected ResolvedAt to be set")
	}

	// Verify micro-releases: 5 releases (one per successful call)
	if len(ledger.released) != 5 {
		t.Errorf("Expected 5 releases, got %d", len(ledger.released))
	}
}

// ---------------------------------------------------------------------------
// Rejection
// ---------------------------------------------------------------------------

func TestContract_Rejection(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})

	contract, err := svc.Reject(ctx, contract.ID, "0xseller")
	if err != nil {
		t.Fatalf("Reject failed: %v", err)
	}
	if contract.Status != StatusRejected {
		t.Errorf("Expected status rejected, got %s", contract.Status)
	}
	if len(ledger.locked) != 0 {
		t.Error("No funds should be locked for rejected contract")
	}
	if contract.ResolvedAt == nil {
		t.Error("Expected ResolvedAt to be set")
	}
}

// ---------------------------------------------------------------------------
// SLA violation
// ---------------------------------------------------------------------------

func TestContract_SLAViolation(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:      "0xbuyer",
		SellerAddr:     "0xseller",
		ServiceType:    "translation",
		PricePerCall:   "0.001",
		BuyerBudget:    "1.00",
		Duration:       "7d",
		SellerPenalty:  "0.10",
		MinSuccessRate: 80.0,
		SLAWindowSize:  5,
	})

	svc.Accept(ctx, contract.ID, "0xseller")

	// Record 2 successes and 3 failures in the window (40% success < 80% threshold)
	for i := 0; i < 2; i++ {
		svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")
	}
	for i := 0; i < 2; i++ {
		svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "failed", LatencyMs: 100}, "0xbuyer")
	}

	// This 5th call (failed) completes the window and triggers violation
	contract, err := svc.RecordCall(ctx, contract.ID, RecordCallRequest{
		Status:    "failed",
		LatencyMs: 100,
	}, "0xbuyer")
	if err != nil {
		t.Fatalf("RecordCall failed: %v", err)
	}
	if contract.Status != StatusViolated {
		t.Errorf("Expected status violated, got %s", contract.Status)
	}
	if contract.ViolationDetails == "" {
		t.Error("Expected violation details to be set")
	}

	// Seller penalty should be transferred to buyer
	penaltyRef := contract.ID + "_pen"
	if _, ok := ledger.released[penaltyRef]; !ok {
		t.Error("Expected seller penalty to be released to buyer")
	}
}

// ---------------------------------------------------------------------------
// Buyer termination
// ---------------------------------------------------------------------------

func TestContract_BuyerTermination(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		ServiceType:   "translation",
		PricePerCall:  "0.005",
		BuyerBudget:   "1.00",
		Duration:      "7d",
		SellerPenalty: "0.10",
	})

	svc.Accept(ctx, contract.ID, "0xseller")

	// Record a few calls
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")

	// Buyer terminates
	contract, err := svc.Terminate(ctx, contract.ID, "0xbuyer", "no longer needed")
	if err != nil {
		t.Fatalf("Terminate failed: %v", err)
	}
	if contract.Status != StatusTerminated {
		t.Errorf("Expected status terminated, got %s", contract.Status)
	}
	if contract.TerminatedBy != "0xbuyer" {
		t.Errorf("Expected terminated by buyer, got %s", contract.TerminatedBy)
	}

	// Remaining budget released to seller (compensation)
	termRef := contract.ID + "_term"
	if _, ok := ledger.released[termRef]; !ok {
		t.Error("Expected remaining budget to be released to seller")
	}

	// Seller penalty returned
	penaltyRef := contract.ID + "_pen"
	if _, ok := ledger.refunded[penaltyRef]; !ok {
		t.Error("Expected seller penalty to be refunded")
	}
}

// ---------------------------------------------------------------------------
// Seller termination
// ---------------------------------------------------------------------------

func TestContract_SellerTermination(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		ServiceType:   "translation",
		PricePerCall:  "0.005",
		BuyerBudget:   "1.00",
		Duration:      "7d",
		SellerPenalty: "0.10",
	})

	svc.Accept(ctx, contract.ID, "0xseller")

	// Seller terminates
	contract, err := svc.Terminate(ctx, contract.ID, "0xseller", "cannot fulfill")
	if err != nil {
		t.Fatalf("Terminate failed: %v", err)
	}
	if contract.Status != StatusTerminated {
		t.Errorf("Expected status terminated, got %s", contract.Status)
	}
	if contract.TerminatedBy != "0xseller" {
		t.Errorf("Expected terminated by seller, got %s", contract.TerminatedBy)
	}

	// Seller penalty forfeited to buyer
	penaltyRef := contract.ID + "_pen"
	if _, ok := ledger.released[penaltyRef]; !ok {
		t.Error("Expected seller penalty to be released to buyer")
	}

	// Remaining buyer budget refunded
	refundRef := contract.ID + "_refund"
	if _, ok := ledger.refunded[refundRef]; !ok {
		t.Error("Expected remaining buyer budget to be refunded")
	}
}

// ---------------------------------------------------------------------------
// Budget exhaustion
// ---------------------------------------------------------------------------

func TestContract_BudgetExhaustion(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.50",
		BuyerBudget:  "1.00",
		Duration:     "7d",
		MinVolume:    1,
	})

	svc.Accept(ctx, contract.ID, "0xseller")

	// First call
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")

	// Second call exhausts budget and completes (minVolume=1 already met)
	contract, err := svc.RecordCall(ctx, contract.ID, RecordCallRequest{
		Status:    "success",
		LatencyMs: 100,
	}, "0xbuyer")
	if err != nil {
		t.Fatalf("RecordCall failed: %v", err)
	}
	if contract.Status != StatusCompleted {
		t.Errorf("Expected status completed, got %s", contract.Status)
	}

	// Next call should fail with budget exhausted
	_, err = svc.RecordCall(ctx, contract.ID, RecordCallRequest{
		Status:    "success",
		LatencyMs: 100,
	}, "0xbuyer")
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("Expected ErrAlreadyResolved after completion, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Authorization
// ---------------------------------------------------------------------------

func TestContract_Authorization(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	buyer := "0xbuyer"
	seller := "0xseller"
	stranger := "0xstranger"

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    buyer,
		SellerAddr:   seller,
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})

	// Only seller can accept
	_, err := svc.Accept(ctx, contract.ID, buyer)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Expected ErrUnauthorized when buyer tries to accept, got %v", err)
	}
	_, err = svc.Accept(ctx, contract.ID, stranger)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Expected ErrUnauthorized when stranger tries to accept, got %v", err)
	}

	// Only seller can reject
	_, err = svc.Reject(ctx, contract.ID, buyer)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Expected ErrUnauthorized when buyer tries to reject, got %v", err)
	}

	// Accept to test RecordCall and Terminate auth
	svc.Accept(ctx, contract.ID, seller)

	// Stranger cannot record calls
	_, err = svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, stranger)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Expected ErrUnauthorized when stranger tries to record call, got %v", err)
	}

	// Stranger cannot terminate
	_, err = svc.Terminate(ctx, contract.ID, stranger, "reason")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Expected ErrUnauthorized when stranger tries to terminate, got %v", err)
	}

	// Buyer or seller can record calls
	_, err = svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, buyer)
	if err != nil {
		t.Errorf("Buyer should be able to record calls: %v", err)
	}
	_, err = svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, seller)
	if err != nil {
		t.Errorf("Seller should be able to record calls: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Double accept
// ---------------------------------------------------------------------------

func TestContract_DoubleAccept(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})

	_, err := svc.Accept(ctx, contract.ID, "0xseller")
	if err != nil {
		t.Fatalf("First accept failed: %v", err)
	}

	// Second accept should fail
	_, err = svc.Accept(ctx, contract.ID, "0xseller")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("Expected ErrInvalidStatus for double accept, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Same buyer and seller
// ---------------------------------------------------------------------------

func TestContract_SameBuyerAndSeller(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xsame",
		SellerAddr:   "0xsame",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})
	if err == nil {
		t.Fatal("Propose with same buyer/seller should be rejected")
	}
}

// ---------------------------------------------------------------------------
// Ledger failure on accept
// ---------------------------------------------------------------------------

func TestContract_AcceptLedgerFailure(t *testing.T) {
	store := NewMemoryStore()
	fl := &failingLedger{
		lockErr:   errors.New("insufficient balance"),
		lockLimit: 1, // first lock succeeds, second fails
	}
	svc := NewService(store, fl)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:     "0xbuyer",
		SellerAddr:    "0xseller",
		ServiceType:   "translation",
		PricePerCall:  "0.005",
		BuyerBudget:   "1.00",
		Duration:      "7d",
		SellerPenalty: "0.10",
	})

	_, err := svc.Accept(ctx, contract.ID, "0xseller")
	if err == nil {
		t.Fatal("Expected error when seller penalty lock fails")
	}

	// Buyer budget should have been refunded (compensation)
	hasRefund := false
	for _, call := range fl.calls {
		if call == "refund:"+contract.ID {
			hasRefund = true
		}
	}
	if !hasRefund {
		t.Error("Expected buyer budget refund after seller lock failure")
	}
}

// ---------------------------------------------------------------------------
// Concurrent call recording
// ---------------------------------------------------------------------------

func TestContract_ConcurrentCallRecording(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.001",
		BuyerBudget:  "100.00",
		Duration:     "7d",
	})
	svc.Accept(ctx, contract.ID, "0xseller")

	var wg sync.WaitGroup
	errs := make([]error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := svc.RecordCall(ctx, contract.ID, RecordCallRequest{
				Status:    "success",
				LatencyMs: 100,
			}, "0xbuyer")
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	// No errors should occur (budget is large enough)
	for i, err := range errs {
		if err != nil {
			t.Errorf("RecordCall %d failed: %v", i, err)
		}
	}

	// All 50 calls should be recorded
	got, _ := svc.Get(ctx, contract.ID)
	if got.TotalCalls != 50 {
		t.Errorf("Expected 50 total calls, got %d", got.TotalCalls)
	}
	if got.SuccessfulCalls != 50 {
		t.Errorf("Expected 50 successful calls, got %d", got.SuccessfulCalls)
	}
}

// ---------------------------------------------------------------------------
// Micro-release accounting
// ---------------------------------------------------------------------------

func TestContract_MicroReleaseAccounting(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
		MinVolume:    1,
	})
	svc.Accept(ctx, contract.ID, "0xseller")

	// Record 3 successful calls
	for i := 0; i < 3; i++ {
		svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")
	}

	// Each call should have released exactly pricePerCall
	releaseCount := 0
	for _, amount := range ledger.released {
		if amount == "0.005" {
			releaseCount++
		}
	}
	if releaseCount != 3 {
		t.Errorf("Expected 3 releases of 0.005, got %d", releaseCount)
	}

	// Budget spent should be 0.015
	got, _ := svc.Get(ctx, contract.ID)
	if got.BudgetSpent != "0.015000" {
		t.Errorf("Expected budget spent 0.015000, got %s", got.BudgetSpent)
	}

	// Failed calls should not release funds
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "failed", LatencyMs: 100}, "0xbuyer")
	got, _ = svc.Get(ctx, contract.ID)
	if got.BudgetSpent != "0.015000" {
		t.Errorf("Budget spent should not change on failed call, got %s", got.BudgetSpent)
	}
}

// ---------------------------------------------------------------------------
// Timer expiration
// ---------------------------------------------------------------------------

func TestContract_TimerExpiration(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "1ms",
		MinVolume:    1,
	})
	svc.Accept(ctx, contract.ID, "0xseller")

	// Record enough calls to meet minVolume
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")

	// Wait for expiry
	time.Sleep(5 * time.Millisecond)

	// CheckExpired should complete the contract (min volume met)
	svc.CheckExpired(ctx)

	got, _ := svc.Get(ctx, contract.ID)
	if got.Status != StatusCompleted {
		t.Errorf("Expected status completed after expiration with min volume met, got %s", got.Status)
	}
}

func TestContract_TimerExpirationWithoutMinVolume(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "1ms",
		MinVolume:    10,
	})
	svc.Accept(ctx, contract.ID, "0xseller")

	// Only record 2 calls (less than minVolume of 10)
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")
	svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")

	time.Sleep(5 * time.Millisecond)

	svc.CheckExpired(ctx)

	got, _ := svc.Get(ctx, contract.ID)
	if got.Status != StatusTerminated {
		t.Errorf("Expected status terminated after expiration without min volume, got %s", got.Status)
	}
	if got.TerminatedReason != "expired" {
		t.Errorf("Expected terminated reason 'expired', got %s", got.TerminatedReason)
	}
}

// ---------------------------------------------------------------------------
// Rolling window SLA
// ---------------------------------------------------------------------------

func TestContract_RollingWindowSLA(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:      "0xbuyer",
		SellerAddr:     "0xseller",
		ServiceType:    "translation",
		PricePerCall:   "0.001",
		BuyerBudget:    "10.00",
		Duration:       "7d",
		MinSuccessRate: 50.0,
		SLAWindowSize:  4,
		SellerPenalty:  "0.50",
	})
	svc.Accept(ctx, contract.ID, "0xseller")

	// Record 10 successes first (lifetime rate high)
	for i := 0; i < 10; i++ {
		svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")
	}

	// Now failures: after 3 failures, the window is [success, failed, failed, failed] = 25% < 50%
	// The 3rd failure is the one that fills the window and triggers violation
	for i := 0; i < 2; i++ {
		contract, err := svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "failed", LatencyMs: 100}, "0xbuyer")
		if err != nil {
			t.Fatalf("RecordCall %d failed: %v", i, err)
		}
		if contract.Status == StatusViolated {
			t.Fatalf("Should not violate yet at failure %d", i)
		}
	}

	// 3rd failure fills the 4-call window: [success, failed, failed, failed] = 25% < 50%
	contract, err := svc.RecordCall(ctx, contract.ID, RecordCallRequest{
		Status:    "failed",
		LatencyMs: 100,
	}, "0xbuyer")
	if err != nil {
		t.Fatalf("RecordCall failed: %v", err)
	}
	if contract.Status != StatusViolated {
		t.Errorf("Expected violated from rolling window, got %s", contract.Status)
	}

	// Lifetime rate is 10/13 = 77%, well above 50%, proving window is used not lifetime
}

// ---------------------------------------------------------------------------
// Nonexistent contract
// ---------------------------------------------------------------------------

func TestContract_NonexistentContract(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	_, err := svc.Get(ctx, "ct_does_not_exist")
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("Expected ErrContractNotFound, got %v", err)
	}

	_, err = svc.Accept(ctx, "ct_ghost", "0xseller")
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("Expected ErrContractNotFound for Accept, got %v", err)
	}

	_, err = svc.Reject(ctx, "ct_ghost", "0xseller")
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("Expected ErrContractNotFound for Reject, got %v", err)
	}

	_, err = svc.RecordCall(ctx, "ct_ghost", RecordCallRequest{Status: "success"}, "0xbuyer")
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("Expected ErrContractNotFound for RecordCall, got %v", err)
	}

	_, err = svc.Terminate(ctx, "ct_ghost", "0xbuyer", "reason")
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("Expected ErrContractNotFound for Terminate, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Case insensitive addresses
// ---------------------------------------------------------------------------

func TestContract_CaseInsensitiveAddresses(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, err := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xBUYER",
		SellerAddr:   "0xSELLER",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})
	if err != nil {
		t.Fatalf("Propose failed: %v", err)
	}

	if contract.BuyerAddr != "0xbuyer" {
		t.Errorf("Expected buyer addr lowercased, got %s", contract.BuyerAddr)
	}
	if contract.SellerAddr != "0xseller" {
		t.Errorf("Expected seller addr lowercased, got %s", contract.SellerAddr)
	}

	// Accept with uppercase seller
	_, err = svc.Accept(ctx, contract.ID, "0xSELLER")
	if err != nil {
		t.Fatalf("Accept with uppercase seller should work: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ID format
// ---------------------------------------------------------------------------

func TestContract_IDFormat(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})

	if len(contract.ID) < 4 || contract.ID[:3] != "ct_" {
		t.Errorf("Expected ID to start with 'ct_', got %s", contract.ID)
	}
	if len(contract.ID) != 35 { // "ct_" (3) + 32 hex chars
		t.Errorf("Expected ID length 35, got %d (%s)", len(contract.ID), contract.ID)
	}
}

// ---------------------------------------------------------------------------
// IsTerminal coverage
// ---------------------------------------------------------------------------

func TestContract_IsTerminal(t *testing.T) {
	tests := []struct {
		status   Status
		terminal bool
	}{
		{StatusProposed, false},
		{StatusAccepted, false},
		{StatusActive, false},
		{StatusCompleted, true},
		{StatusTerminated, true},
		{StatusViolated, true},
		{StatusRejected, true},
	}

	for _, tt := range tests {
		c := &Contract{Status: tt.status}
		if c.IsTerminal() != tt.terminal {
			t.Errorf("Status %s: expected IsTerminal=%v, got %v", tt.status, tt.terminal, c.IsTerminal())
		}
	}
}

// ---------------------------------------------------------------------------
// Helper method tests
// ---------------------------------------------------------------------------

func TestContract_HelperMethods(t *testing.T) {
	c := &Contract{
		TotalCalls:      10,
		SuccessfulCalls: 8,
		FailedCalls:     2,
		TotalLatencyMs:  5000,
	}

	rate := c.CurrentSuccessRate()
	if rate != 80.0 {
		t.Errorf("Expected success rate 80.0, got %f", rate)
	}

	avg := c.AverageLatencyMs()
	if avg != 500.0 {
		t.Errorf("Expected average latency 500.0, got %f", avg)
	}

	// Zero calls
	empty := &Contract{}
	if empty.CurrentSuccessRate() != 100.0 {
		t.Errorf("Expected 100%% success rate for zero calls, got %f", empty.CurrentSuccessRate())
	}
	if empty.AverageLatencyMs() != 0 {
		t.Errorf("Expected 0 average latency for zero calls, got %f", empty.AverageLatencyMs())
	}
}

// ---------------------------------------------------------------------------
// Duration parsing
// ---------------------------------------------------------------------------

func TestContract_DurationParsing(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	// Valid day duration
	contract, err := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})
	if err != nil {
		t.Fatalf("7d duration should work: %v", err)
	}

	svc.Accept(ctx, contract.ID, "0xseller")
	got, _ := svc.Get(ctx, contract.ID)
	expected := got.StartsAt.Add(7 * 24 * time.Hour)
	diff := got.ExpiresAt.Sub(expected)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("Expected expires_at ~7d from starts_at, got diff %v", diff)
	}

	// Valid hour duration
	_, err = svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller2",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "24h",
	})
	if err != nil {
		t.Fatalf("24h duration should work: %v", err)
	}

	// Invalid duration
	_, err = svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller3",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "garbage",
	})
	if err == nil {
		t.Fatal("Invalid duration should be rejected")
	}
}

// ---------------------------------------------------------------------------
// MemoryStore tests
// ---------------------------------------------------------------------------

func TestMemoryStore_ListByAgentStatus(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()

	store.Create(ctx, &Contract{
		ID: "ct_1", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Status: StatusActive, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &Contract{
		ID: "ct_2", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Status: StatusCompleted, CreatedAt: now, UpdatedAt: now,
	})
	store.Create(ctx, &Contract{
		ID: "ct_3", BuyerAddr: "0xbuyer", SellerAddr: "0xseller",
		Status: StatusActive, CreatedAt: now, UpdatedAt: now,
	})

	// Filter by status
	active, err := store.ListByAgent(ctx, "0xbuyer", "active", 50)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(active) != 2 {
		t.Errorf("Expected 2 active contracts, got %d", len(active))
	}

	// No filter
	all, err := store.ListByAgent(ctx, "0xbuyer", "", 50)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("Expected 3 total contracts, got %d", len(all))
	}
}

func TestMemoryStore_UpdateNonexistent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	err := store.Update(ctx, &Contract{ID: "ct_ghost"})
	if !errors.Is(err, ErrContractNotFound) {
		t.Errorf("Expected ErrContractNotFound, got %v", err)
	}
}

func TestMemoryStore_ListExpiring(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-1 * time.Hour)
	future := now.Add(1 * time.Hour)

	// Active + expired
	store.Create(ctx, &Contract{
		ID: "ct_expired", Status: StatusActive,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		ExpiresAt: &past, CreatedAt: now, UpdatedAt: now,
	})

	// Active + not expired
	store.Create(ctx, &Contract{
		ID: "ct_future", Status: StatusActive,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		ExpiresAt: &future, CreatedAt: now, UpdatedAt: now,
	})

	// Completed (terminal) + expired
	store.Create(ctx, &Contract{
		ID: "ct_done", Status: StatusCompleted,
		BuyerAddr: "0xb", SellerAddr: "0xs",
		ExpiresAt: &past, CreatedAt: now, UpdatedAt: now,
	})

	expired, err := store.ListExpiring(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListExpiring failed: %v", err)
	}
	if len(expired) != 1 {
		t.Errorf("Expected 1 expiring contract, got %d", len(expired))
	}
	if len(expired) > 0 && expired[0].ID != "ct_expired" {
		t.Errorf("Expected ct_expired, got %s", expired[0].ID)
	}
}

func TestMemoryStore_GetRecentCalls(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Record 10 calls with increasing timestamps
	for i := 0; i < 10; i++ {
		store.RecordCall(ctx, &ContractCall{
			ID:         generateCallID(),
			ContractID: "ct_1",
			Status:     "success",
			Amount:     "0.005",
			CreatedAt:  time.Now().Add(time.Duration(i) * time.Millisecond),
		})
	}

	// Get recent 3
	recent, err := store.GetRecentCalls(ctx, "ct_1", 3)
	if err != nil {
		t.Fatalf("GetRecentCalls failed: %v", err)
	}
	if len(recent) != 3 {
		t.Errorf("Expected 3 recent calls, got %d", len(recent))
	}

	// Should be in reverse chronological order (most recent first)
	if len(recent) >= 2 && recent[0].CreatedAt.Before(recent[1].CreatedAt) {
		t.Error("Expected most recent calls first")
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
// Reject after accept should fail
// ---------------------------------------------------------------------------

func TestContract_RejectAfterAccept(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})

	svc.Accept(ctx, contract.ID, "0xseller")

	_, err := svc.Reject(ctx, contract.ID, "0xseller")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("Expected ErrInvalidStatus for reject after accept, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Record call on proposed contract should fail
// ---------------------------------------------------------------------------

func TestContract_RecordCallOnProposed(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
	})

	_, err := svc.RecordCall(ctx, contract.ID, RecordCallRequest{Status: "success", LatencyMs: 100}, "0xbuyer")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Errorf("Expected ErrInvalidStatus for record call on proposed contract, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// No penalty contract
// ---------------------------------------------------------------------------

func TestContract_NoPenalty(t *testing.T) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	ctx := context.Background()

	contract, _ := svc.Propose(ctx, ProposeRequest{
		BuyerAddr:    "0xbuyer",
		SellerAddr:   "0xseller",
		ServiceType:  "translation",
		PricePerCall: "0.005",
		BuyerBudget:  "1.00",
		Duration:     "7d",
		// No seller penalty
	})

	_, err := svc.Accept(ctx, contract.ID, "0xseller")
	if err != nil {
		t.Fatalf("Accept without penalty should work: %v", err)
	}

	// Only buyer budget should be locked (no penalty lock)
	if len(ledger.locked) != 1 {
		t.Errorf("Expected 1 lock (buyer budget only), got %d", len(ledger.locked))
	}
}
