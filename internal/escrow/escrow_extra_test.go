package escrow

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testService() (*Service, *mockLedger) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return svc, ml
}

// ---------------------------------------------------------------------------
// Escrow: SubmitEvidence
// ---------------------------------------------------------------------------

func TestEscrow_SubmitEvidence_Success(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Dispute first
	esc, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad quality")

	// Buyer submits evidence
	esc, err := svc.SubmitEvidence(ctx, esc.ID, "0xbuyer", "here is my proof")
	if err != nil {
		t.Fatalf("SubmitEvidence by buyer: %v", err)
	}
	// Should have 2 entries: initial dispute reason + new evidence
	if len(esc.DisputeEvidence) != 2 {
		t.Fatalf("expected 2 evidence entries, got %d", len(esc.DisputeEvidence))
	}

	// Seller submits evidence
	esc, err = svc.SubmitEvidence(ctx, esc.ID, "0xseller", "here is seller proof")
	if err != nil {
		t.Fatalf("SubmitEvidence by seller: %v", err)
	}
	if len(esc.DisputeEvidence) != 3 {
		t.Fatalf("expected 3 evidence entries, got %d", len(esc.DisputeEvidence))
	}
}

func TestEscrow_SubmitEvidence_Unauthorized(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad quality")

	_, err := svc.SubmitEvidence(ctx, esc.ID, "0xstranger", "evidence")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestEscrow_SubmitEvidence_WrongStatus(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Evidence on pending escrow should fail
	_, err := svc.SubmitEvidence(ctx, esc.ID, "0xbuyer", "evidence")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
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

func TestEscrow_SubmitEvidence_Nonexistent(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.SubmitEvidence(ctx, "esc_ghost", "0xbuyer", "evidence")
	if !errors.Is(err, ErrEscrowNotFound) {
		t.Fatalf("expected ErrEscrowNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Escrow: AssignArbitrator
// ---------------------------------------------------------------------------

func TestEscrow_AssignArbitrator_Success(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	_, _ = svc.Dispute(ctx, esc.ID, "0xbuyer", "bad service")

	esc, err := svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")
	if err != nil {
		t.Fatalf("AssignArbitrator: %v", err)
	}
	if esc.Status != StatusArbitrating {
		t.Fatalf("expected arbitrating, got %s", esc.Status)
	}
	if esc.ArbitratorAddr != "0xarbitrator" {
		t.Fatalf("expected arbitrator 0xarbitrator, got %s", esc.ArbitratorAddr)
	}
	if esc.ArbitrationDeadline == nil {
		t.Fatal("expected ArbitrationDeadline to be set")
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

func TestEscrow_AssignArbitrator_WrongStatus(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	esc, _ := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})

	// Assign from pending status should fail
	_, err := svc.AssignArbitrator(ctx, esc.ID, "0xbuyer", "0xarbitrator")
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("expected ErrInvalidStatus, got %v", err)
	}
}

func TestEscrow_AssignArbitrator_Nonexistent(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.AssignArbitrator(ctx, "esc_ghost", "0xbuyer", "0xarbitrator")
	if !errors.Is(err, ErrEscrowNotFound) {
		t.Fatalf("expected ErrEscrowNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Escrow: ResolveArbitration
// ---------------------------------------------------------------------------

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

func TestEscrow_ResolveArbitration_Release(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	result, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "release",
		Reason:     "seller delivered properly",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration release: %v", err)
	}
	if result.Status != StatusReleased {
		t.Fatalf("expected released, got %s", result.Status)
	}
	if result.ResolvedAt == nil {
		t.Fatal("expected ResolvedAt to be set")
	}
}

func TestEscrow_ResolveArbitration_Refund(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	result, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution: "refund",
		Reason:     "seller failed to deliver",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration refund: %v", err)
	}
	if result.Status != StatusRefunded {
		t.Fatalf("expected refunded, got %s", result.Status)
	}
}

func TestEscrow_ResolveArbitration_Partial(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	result, err := svc.ResolveArbitration(ctx, esc.ID, "0xarbitrator", ResolveRequest{
		Resolution:    "partial",
		ReleaseAmount: "7.00",
		Reason:        "partial delivery",
	})
	if err != nil {
		t.Fatalf("ResolveArbitration partial: %v", err)
	}
	if result.Status != StatusReleased {
		t.Fatalf("expected released, got %s", result.Status)
	}
	if result.PartialReleaseAmount != "7.000000" {
		t.Fatalf("expected release 7.000000, got %s", result.PartialReleaseAmount)
	}
	if result.PartialRefundAmount != "3.000000" {
		t.Fatalf("expected refund 3.000000, got %s", result.PartialRefundAmount)
	}
}

func TestEscrow_ResolveArbitration_Unauthorized(t *testing.T) {
	svc, esc := setupArbitration(t)
	ctx := context.Background()

	_, err := svc.ResolveArbitration(ctx, esc.ID, "0xstranger", ResolveRequest{
		Resolution: "release",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
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

func TestEscrow_ResolveArbitration_Nonexistent(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.ResolveArbitration(ctx, "esc_ghost", "0xarbitrator", ResolveRequest{
		Resolution: "release",
	})
	if !errors.Is(err, ErrEscrowNotFound) {
		t.Fatalf("expected ErrEscrowNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Escrow: ForceCloseExpired
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Escrow: ListByAgent
// ---------------------------------------------------------------------------

func TestEscrow_ListByAgentDefaultLimitZero(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	// Create some escrows
	for i := 0; i < 3; i++ {
		_, _ = svc.Create(ctx, CreateRequest{
			BuyerAddr: "0xbuyer", SellerAddr: "0xseller", Amount: "1.00",
		})
	}

	// limit 0 should default to 50
	list, err := svc.ListByAgent(ctx, "0xbuyer", 0)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Escrow: Dispute validation
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Escrow: Create validation
// ---------------------------------------------------------------------------

func TestEscrow_Create_SameBuyerSeller(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xAlice",
		SellerAddr: "0xAlice",
		Amount:     "1.00",
	})
	if err == nil {
		t.Fatal("expected error when buyer == seller")
	}
}

func TestEscrow_Create_InvalidAmount_Empty(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "",
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestEscrow_Create_InvalidAmount_Negative(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "-1.00",
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestEscrow_Create_InvalidAmount_NotNumber(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "abc",
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestEscrow_Create_ZeroAmount(t *testing.T) {
	svc, _ := testService()
	ctx := context.Background()

	_, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "0",
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Escrow: MoneyError wrapping
// ---------------------------------------------------------------------------

func TestMoneyError_UnwrapAndError(t *testing.T) {
	inner := errors.New("ledger failed")
	me := &MoneyError{
		Err:         inner,
		FundsStatus: "locked_in_escrow",
		Recovery:    "contact support",
		Amount:      "10.00",
		Reference:   "esc_123",
	}

	if me.Error() != "ledger failed" {
		t.Errorf("expected 'ledger failed', got %s", me.Error())
	}
	if !errors.Is(me, inner) {
		t.Error("expected Unwrap to return inner error")
	}
}

// ---------------------------------------------------------------------------
// Escrow: IsTerminal for Escrow and CoalitionEscrow
// ---------------------------------------------------------------------------

func TestCoalitionEscrow_IsTerminal(t *testing.T) {
	terminals := []CoalitionStatus{CSSettled, CSAborted, CSExpired}
	for _, s := range terminals {
		ce := &CoalitionEscrow{Status: s}
		if !ce.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}

	nonTerminals := []CoalitionStatus{CSActive, CSDelivered}
	for _, s := range nonTerminals {
		ce := &CoalitionEscrow{Status: s}
		if ce.IsTerminal() {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}

// ---------------------------------------------------------------------------
// Escrow: With... option chaining
// ---------------------------------------------------------------------------

func TestEscrow_ServiceOptions(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	rec := &mockRecorder{}

	svc := NewService(store, ml).
		WithLogger(slog.Default()).
		WithRecorder(rec).
		WithRevenueAccumulator(nil).
		WithReputationImpactor(nil).
		WithReceiptIssuer(nil).
		WithWebhookEmitter(nil).
		WithTrustGate(nil)

	if svc == nil {
		t.Fatal("expected non-nil service")
	}

	// Should work normally with nil optional dependencies
	ctx := context.Background()
	esc, err := svc.Create(ctx, CreateRequest{
		BuyerAddr:  "0xbuyer",
		SellerAddr: "0xseller",
		Amount:     "1.00",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = svc.Confirm(ctx, esc.ID, "0xbuyer")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Escrow: MemoryStore ListByStatus
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Escrow: validateAmount
// ---------------------------------------------------------------------------

func TestValidateAmount(t *testing.T) {
	tests := []struct {
		amount  string
		wantErr bool
	}{
		{"1.00", false},
		{"0.000001", false},
		{"999999.999999", false},
		{"", true},
		{"  ", true},
		{"-1.00", true},
		{"0", true},
		{"abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.amount, func(t *testing.T) {
			err := validateAmount(tt.amount)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateAmount(%q) = %v, wantErr %v", tt.amount, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Escrow: AutoRelease ledger failure
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Escrow: allMembersCompleted
// ---------------------------------------------------------------------------

func TestCoalitionEscrow_AllMembersCompleted(t *testing.T) {
	now := time.Now()
	ce := &CoalitionEscrow{
		Members: []CoalitionMember{
			{AgentAddr: "0xa", CompletedAt: &now},
			{AgentAddr: "0xb", CompletedAt: &now},
		},
	}
	if !ce.allMembersCompleted() {
		t.Fatal("expected all completed")
	}

	// One member not completed
	ce.Members = append(ce.Members, CoalitionMember{AgentAddr: "0xc"})
	if ce.allMembersCompleted() {
		t.Fatal("expected not all completed")
	}
}
