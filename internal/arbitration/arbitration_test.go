package arbitration

import (
	"context"
	"log/slog"
	"testing"
)

// --- Mock escrow resolver ---

type mockEscrow struct {
	lastAction string
	lastID     string
	splitPct   int
}

func (m *mockEscrow) RefundBuyer(_ context.Context, escrowID string) error {
	m.lastAction = "refund"
	m.lastID = escrowID
	return nil
}
func (m *mockEscrow) ReleaseSeller(_ context.Context, escrowID string) error {
	m.lastAction = "release"
	m.lastID = escrowID
	return nil
}
func (m *mockEscrow) SplitFunds(_ context.Context, escrowID string, buyerPct int) error {
	m.lastAction = "split"
	m.lastID = escrowID
	m.splitPct = buyerPct
	return nil
}

// --- Mock reputation ---

type mockReputation struct {
	losses []string
}

func (m *mockReputation) RecordDisputeLoss(_ context.Context, addr string, _ string) error {
	m.losses = append(m.losses, addr)
	return nil
}

func newTestService() (*Service, *mockEscrow, *mockReputation) {
	e := &mockEscrow{}
	r := &mockReputation{}
	return NewService(NewMemoryStore(), e, r, slog.Default()), e, r
}

func TestFileCase(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, err := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Bad output", "contract_1")
	if err != nil {
		t.Fatalf("FileCase: %v", err)
	}
	if c.Status != CSOpen {
		t.Errorf("status = %q, want open", c.Status)
	}
	if !c.AutoResolvable {
		t.Error("should be auto-resolvable when contractID provided")
	}
	if c.Fee != "2.000000" {
		t.Errorf("fee = %q, want 2.000000", c.Fee)
	}
}

func TestFileCaseNoContract(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_2", "0xBuyer", "0xSeller", "50.00", "Slow response", "")
	if c.AutoResolvable {
		t.Error("should not be auto-resolvable without contract")
	}
}

func TestAutoResolveContractPassed(t *testing.T) {
	svc, escrow, rep := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "200.00", "Dispute", "contract_1")

	resolved, err := svc.AutoResolve(ctx, c.ID, true)
	if err != nil {
		t.Fatalf("AutoResolve: %v", err)
	}
	if !resolved {
		t.Error("expected resolved=true")
	}

	// Seller should win — funds released
	if escrow.lastAction != "release" {
		t.Errorf("escrow action = %q, want release", escrow.lastAction)
	}
	// Buyer gets reputation hit for invalid dispute
	if len(rep.losses) != 1 || rep.losses[0] != "0xBuyer" {
		t.Errorf("reputation losses = %v, want [0xBuyer]", rep.losses)
	}

	// Case should be auto_resolved
	got, _ := svc.store.Get(ctx, c.ID)
	if got.Status != CSAutoResolved {
		t.Errorf("status = %q, want auto_resolved", got.Status)
	}
	if got.Outcome != OutcomeSellerWins {
		t.Errorf("outcome = %q, want seller_wins", got.Outcome)
	}
}

func TestAutoResolveContractFailed(t *testing.T) {
	svc, escrow, rep := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "200.00", "Dispute", "contract_1")

	resolved, _ := svc.AutoResolve(ctx, c.ID, false)
	if !resolved {
		t.Error("expected resolved=true")
	}

	// Buyer should win — funds refunded
	if escrow.lastAction != "refund" {
		t.Errorf("escrow action = %q, want refund", escrow.lastAction)
	}
	// Seller gets reputation hit
	if len(rep.losses) != 1 || rep.losses[0] != "0xSeller" {
		t.Errorf("reputation losses = %v, want [0xSeller]", rep.losses)
	}
}

func TestAutoResolveNotResolvable(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "No contract", "")

	resolved, err := svc.AutoResolve(ctx, c.ID, true)
	if err != nil {
		t.Fatalf("AutoResolve: %v", err)
	}
	if resolved {
		t.Error("should not resolve without contract")
	}
}

func TestArbiterFlow(t *testing.T) {
	svc, escrow, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "500.00", "Quality issue", "")

	// Assign arbiter
	err := svc.AssignArbiter(ctx, c.ID, "0xArbiter")
	if err != nil {
		t.Fatalf("AssignArbiter: %v", err)
	}

	// Submit evidence
	ev, err := svc.SubmitEvidence(ctx, c.ID, "0xBuyer", "buyer", "text", "Output was wrong")
	if err != nil {
		t.Fatalf("SubmitEvidence: %v", err)
	}
	if ev.ID == "" {
		t.Error("evidence ID empty")
	}

	// Resolve — split 60% to buyer
	err = svc.Resolve(ctx, c.ID, "0xArbiter", OutcomeSplit, 60, "Partial delivery — quality below SLA")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if escrow.lastAction != "split" {
		t.Errorf("escrow action = %q, want split", escrow.lastAction)
	}
	if escrow.splitPct != 60 {
		t.Errorf("split pct = %d, want 60", escrow.splitPct)
	}
}

func TestResolveWrongArbiter(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")

	err := svc.Resolve(ctx, c.ID, "0xWrongPerson", OutcomeBuyerWins, 0, "I decide")
	if err != ErrNotArbiter {
		t.Errorf("err = %v, want ErrNotArbiter", err)
	}
}

func TestFeeCalculation(t *testing.T) {
	tests := []struct {
		amount string
		want   string
	}{
		{"10.00", "0.500000"},      // 2% = 0.20, min = 0.50
		{"25.00", "0.500000"},      // 2% = 0.50, exactly min
		{"100.00", "2.000000"},     // 2% = 2.00
		{"1000.00", "20.000000"},   // 2% = 20.00
		{"30000.00", "500.000000"}, // 2% = 600, capped at 500
	}
	for _, tt := range tests {
		got := computeFee(tt.amount)
		if got != tt.want {
			t.Errorf("computeFee(%q) = %q, want %q", tt.amount, got, tt.want)
		}
	}
}

func TestSubmitEvidence_CaseNotFound(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	_, err := svc.SubmitEvidence(ctx, "nonexistent", "0xBuyer", "buyer", "text", "Evidence")
	if err != ErrCaseNotFound {
		t.Errorf("err = %v, want ErrCaseNotFound", err)
	}
}

func TestSubmitEvidence_CaseResolved(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "200.00", "Dispute", "contract_1")
	// Auto-resolve the case
	svc.AutoResolve(ctx, c.ID, true)

	_, err := svc.SubmitEvidence(ctx, c.ID, "0xBuyer", "buyer", "text", "Late evidence")
	if err != ErrCaseAlreadyClosed {
		t.Errorf("err = %v, want ErrCaseAlreadyClosed", err)
	}
}

func TestSubmitEvidence_CaseAutoResolved(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "200.00", "Dispute", "contract_1")
	svc.AutoResolve(ctx, c.ID, false)

	_, err := svc.SubmitEvidence(ctx, c.ID, "0xSeller", "seller", "text", "Post-resolution evidence")
	if err != ErrCaseAlreadyClosed {
		t.Errorf("err = %v, want ErrCaseAlreadyClosed", err)
	}
}

func TestSubmitEvidence_AssignedCase(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Quality", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")

	// Evidence should be allowed on assigned cases
	ev, err := svc.SubmitEvidence(ctx, c.ID, "0xSeller", "seller", "url", "https://proof.example.com")
	if err != nil {
		t.Fatalf("SubmitEvidence on assigned case: %v", err)
	}
	if ev.Role != "seller" {
		t.Errorf("role = %q, want seller", ev.Role)
	}
	if ev.Type != "url" {
		t.Errorf("type = %q, want url", ev.Type)
	}
}

func TestSubmitEvidence_MultipleEvidences(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")

	// Submit multiple pieces of evidence
	_, err := svc.SubmitEvidence(ctx, c.ID, "0xBuyer", "buyer", "text", "Evidence 1")
	if err != nil {
		t.Fatalf("SubmitEvidence 1: %v", err)
	}
	_, err = svc.SubmitEvidence(ctx, c.ID, "0xSeller", "seller", "json", `{"key":"value"}`)
	if err != nil {
		t.Fatalf("SubmitEvidence 2: %v", err)
	}

	got, _ := svc.store.Get(ctx, c.ID)
	if len(got.Evidence) != 2 {
		t.Errorf("evidence count = %d, want 2", len(got.Evidence))
	}
}

func TestMemoryStore_ListByEscrow(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create cases for different escrows
	store.Create(ctx, &Case{ID: "arb_1", EscrowID: "esc_A", Status: CSOpen})
	store.Create(ctx, &Case{ID: "arb_2", EscrowID: "esc_A", Status: CSAssigned})
	store.Create(ctx, &Case{ID: "arb_3", EscrowID: "esc_B", Status: CSOpen})

	cases, err := store.ListByEscrow(ctx, "esc_A")
	if err != nil {
		t.Fatalf("ListByEscrow: %v", err)
	}
	if len(cases) != 2 {
		t.Errorf("cases for esc_A = %d, want 2", len(cases))
	}

	cases, err = store.ListByEscrow(ctx, "esc_B")
	if err != nil {
		t.Fatalf("ListByEscrow: %v", err)
	}
	if len(cases) != 1 {
		t.Errorf("cases for esc_B = %d, want 1", len(cases))
	}
}

func TestMemoryStore_ListByEscrow_Empty(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	cases, err := store.ListByEscrow(ctx, "esc_nonexistent")
	if err != nil {
		t.Fatalf("ListByEscrow: %v", err)
	}
	if len(cases) != 0 {
		t.Errorf("cases = %d, want 0", len(cases))
	}
}

func TestResolve_InvalidOutcome(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")

	err := svc.Resolve(ctx, c.ID, "0xArbiter", Outcome("invalid"), 0, "Bad outcome")
	if err != ErrInvalidOutcome {
		t.Errorf("err = %v, want ErrInvalidOutcome", err)
	}
}

func TestResolve_AlreadyResolved(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")
	svc.Resolve(ctx, c.ID, "0xArbiter", OutcomeBuyerWins, 0, "Buyer wins")

	err := svc.Resolve(ctx, c.ID, "0xArbiter", OutcomeSellerWins, 0, "Double resolve")
	if err != ErrCaseAlreadyClosed {
		t.Errorf("err = %v, want ErrCaseAlreadyClosed", err)
	}
}

func TestResolve_SellerWins(t *testing.T) {
	svc, escrow, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")

	err := svc.Resolve(ctx, c.ID, "0xArbiter", OutcomeSellerWins, 0, "Seller delivered")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if escrow.lastAction != "release" {
		t.Errorf("escrow action = %q, want release", escrow.lastAction)
	}
}

func TestResolve_BuyerWins(t *testing.T) {
	svc, escrow, rep := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")

	err := svc.Resolve(ctx, c.ID, "0xArbiter", OutcomeBuyerWins, 0, "Seller failed")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if escrow.lastAction != "refund" {
		t.Errorf("escrow action = %q, want refund", escrow.lastAction)
	}
	if len(rep.losses) != 1 || rep.losses[0] != "0xSeller" {
		t.Errorf("reputation losses = %v, want [0xSeller]", rep.losses)
	}
}

func TestAssignArbiter_CaseNotFound(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	err := svc.AssignArbiter(ctx, "nonexistent", "0xArbiter")
	if err != ErrCaseNotFound {
		t.Errorf("err = %v, want ErrCaseNotFound", err)
	}
}

func TestAssignArbiter_AlreadyAssigned(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	c, _ := svc.FileCase(ctx, "esc_1", "0xBuyer", "0xSeller", "100.00", "Test", "")
	svc.AssignArbiter(ctx, c.ID, "0xArbiter")

	// Try to assign again — case is no longer open
	err := svc.AssignArbiter(ctx, c.ID, "0xArbiter2")
	if err != ErrCaseAlreadyClosed {
		t.Errorf("err = %v, want ErrCaseAlreadyClosed", err)
	}
}

func TestListOpen(t *testing.T) {
	svc, _, _ := newTestService()
	ctx := context.Background()

	svc.FileCase(ctx, "esc_1", "0xB", "0xS", "100.00", "r1", "")
	svc.FileCase(ctx, "esc_2", "0xB", "0xS", "200.00", "r2", "c1")
	c3, _ := svc.FileCase(ctx, "esc_3", "0xB", "0xS", "300.00", "r3", "c2")

	// Auto-resolve one
	svc.AutoResolve(ctx, c3.ID, true)

	open, _ := svc.store.ListOpen(ctx, 100)
	if len(open) != 2 {
		t.Errorf("open cases = %d, want 2", len(open))
	}
}
