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
