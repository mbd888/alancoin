package escrow

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"os"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Coalition: ForceCloseExpired
// ---------------------------------------------------------------------------

func TestCoalition_ForceCloseExpired(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	// Create a coalition with very short auto-settle
	req := defaultCreateReq()
	req.AutoSettle = "1ms"
	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	time.Sleep(5 * time.Millisecond)

	closed, err := svc.ForceCloseExpired(ctx)
	if err != nil {
		t.Fatalf("ForceCloseExpired: %v", err)
	}
	if closed != 1 {
		t.Fatalf("expected 1 closed, got %d", closed)
	}

	got, _ := svc.Get(ctx, ce.ID)
	if got.Status != CSExpired {
		t.Fatalf("expected expired, got %s", got.Status)
	}
}

func TestCoalition_ForceCloseExpired_SkipsTerminal(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	req := defaultCreateReq()
	req.AutoSettle = "1ms"
	ce, _ := svc.Create(ctx, req)

	// Settle first via oracle report
	_, _ = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	time.Sleep(5 * time.Millisecond)

	closed, err := svc.ForceCloseExpired(ctx)
	if err != nil {
		t.Fatalf("ForceCloseExpired: %v", err)
	}
	if closed != 0 {
		t.Fatalf("expected 0 closed (already terminal), got %d", closed)
	}
}

// ---------------------------------------------------------------------------
// Coalition: Abort after partial settlement
// ---------------------------------------------------------------------------

func TestCoalition_AbortAlreadySettled(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Settle first
	_, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Abort should fail (already terminal)
	_, err = svc.Abort(ctx, ce.ID, "0xBuyer")
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("expected ErrAlreadyResolved, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Coalition: Report completion from settled escrow
// ---------------------------------------------------------------------------

func TestCoalition_ReportCompletion_AfterSettled(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, _ := svc.Create(ctx, defaultCreateReq())

	// Settle
	_, _ = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})

	// Report completion after settled should fail
	_, err := svc.ReportCompletion(ctx, ce.ID, "0xAgent1")
	if err == nil {
		t.Fatal("expected error for completion after settlement")
	}
}

// ---------------------------------------------------------------------------
// Coalition: Service options
// ---------------------------------------------------------------------------

func TestCoalition_ServiceOptions(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml).
		WithLogger(slog.Default()).
		WithRecorder(nil).
		WithRevenueAccumulator(nil).
		WithReputationImpactor(nil).
		WithReceiptIssuer(nil).
		WithWebhookEmitter(nil).
		WithRealtimeBroadcaster(nil).
		WithContractChecker(nil)

	if svc == nil {
		t.Fatal("expected non-nil service")
	}
}

// ---------------------------------------------------------------------------
// Coalition: ListByAgent with default limit
// ---------------------------------------------------------------------------

func TestCoalition_ListByAgent_DefaultLimit(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	_, _ = svc.Create(ctx, defaultCreateReq())

	// limit 0 → default 50
	list, err := svc.ListByAgent(ctx, "0xBuyer", 0)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}
}

// ---------------------------------------------------------------------------
// Coalition: LedgerService failures
// ---------------------------------------------------------------------------

func TestCoalition_Create_LedgerLockFails(t *testing.T) {
	fl := &failingLedger{lockErr: errors.New("insufficient balance")}
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, fl).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	_, err := svc.Create(ctx, defaultCreateReq())
	if err == nil {
		t.Fatal("expected error when lock fails")
	}
	var me *MoneyError
	if !errors.As(err, &me) {
		t.Fatalf("expected MoneyError, got %T", err)
	}
	if me.FundsStatus != "no_change" {
		t.Errorf("expected no_change, got %s", me.FundsStatus)
	}
}

func TestCoalition_AutoSettle_RefundFails(t *testing.T) {
	fl := &failingLedger{refundErr: errors.New("refund failed")}
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, fl).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx := context.Background()

	// Create with a working ledger first, then switch to failing
	ml := newMockLedger()
	svc2 := NewCoalitionService(store, ml)
	ce, _ := svc2.Create(ctx, defaultCreateReq())

	// Auto-settle with failing ledger
	err := svc.AutoSettle(ctx, ce)
	if err == nil {
		t.Fatal("expected error when refund fails")
	}
}

// ---------------------------------------------------------------------------
// Coalition: matchTier edge case
// ---------------------------------------------------------------------------

func TestMatchTier_BelowAllTiers(t *testing.T) {
	tiers := []QualityTier{
		{Name: "good", MinScore: 0.5, PayoutPct: 75},
	}

	tier, pct := matchTier(tiers, 0.3)
	if tier != "none" {
		t.Fatalf("expected 'none', got %s", tier)
	}
	if pct != 0 {
		t.Fatalf("expected 0, got %.0f", pct)
	}
}

// ---------------------------------------------------------------------------
// Coalition: computePayout edge cases
// ---------------------------------------------------------------------------

func TestComputePayout_NegativeAndOver100(t *testing.T) {
	total := big.NewInt(1_000000) // $1

	// Negative pct treated as zero
	result := computePayout(total, -5)
	if result.Int64() != 0 {
		t.Errorf("negative pct: expected 0, got %d", result.Int64())
	}

	// Over 100 treated as 100
	result = computePayout(total, 150)
	if result.Int64() != 1_000000 {
		t.Errorf("150 pct: expected 1000000, got %d", result.Int64())
	}
}

// ---------------------------------------------------------------------------
// Coalition: computeEqualShares edge cases
// ---------------------------------------------------------------------------

func TestComputeEqualShares_Empty(t *testing.T) {
	shares := computeEqualShares(nil, big.NewInt(1_000000))
	if shares != nil {
		t.Fatal("expected nil for empty members")
	}
}

func TestComputeEqualShares_SingleMember(t *testing.T) {
	members := []CoalitionMember{{AgentAddr: "0xa"}}
	total := big.NewInt(1_000000)
	shares := computeEqualShares(members, total)

	if shares["0xa"].Int64() != 1_000000 {
		t.Fatalf("single member should get full total, got %d", shares["0xa"].Int64())
	}
}

// ---------------------------------------------------------------------------
// Coalition: computeProportionalShares edge cases
// ---------------------------------------------------------------------------

func TestComputeProportionalShares_Empty(t *testing.T) {
	shares := computeProportionalShares(nil, big.NewInt(1_000000))
	if shares != nil {
		t.Fatal("expected nil for empty members")
	}
}

// ---------------------------------------------------------------------------
// Coalition: computeShapleyShares edge cases
// ---------------------------------------------------------------------------

func TestComputeShapleyShares_EmptyMembers(t *testing.T) {
	shares := computeShapleyShares(nil, big.NewInt(1_000000), map[string]float64{})
	if shares != nil {
		t.Fatal("expected nil for empty members")
	}
}

// ---------------------------------------------------------------------------
// Coalition: sortTiersDesc
// ---------------------------------------------------------------------------

func TestSortTiersDesc(t *testing.T) {
	tiers := []QualityTier{
		{Name: "poor", MinScore: 0.0},
		{Name: "excellent", MinScore: 0.9},
		{Name: "good", MinScore: 0.7},
		{Name: "acceptable", MinScore: 0.5},
	}

	sortTiersDesc(tiers)

	if tiers[0].Name != "excellent" {
		t.Errorf("expected excellent first, got %s", tiers[0].Name)
	}
	if tiers[1].Name != "good" {
		t.Errorf("expected good second, got %s", tiers[1].Name)
	}
	if tiers[2].Name != "acceptable" {
		t.Errorf("expected acceptable third, got %s", tiers[2].Name)
	}
	if tiers[3].Name != "poor" {
		t.Errorf("expected poor last, got %s", tiers[3].Name)
	}
}

// ---------------------------------------------------------------------------
// Coalition: validateCreateRequest edge cases
// ---------------------------------------------------------------------------

func TestCoalition_ValidationOracleCannotBeMember(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.Members[0].AgentAddr = "0xOracle" // same as oracle
	_, err := svc.Create(ctx, req)
	if err == nil {
		t.Fatal("expected error when oracle is a member")
	}
}

func TestCoalition_ValidationTooManyMembers(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.Members = make([]CoalitionMember, MaxCoalitionMembers+1)
	for i := range req.Members {
		req.Members[i] = CoalitionMember{AgentAddr: "0x" + string(rune('A'+i)), Role: "worker"}
	}
	_, err := svc.Create(ctx, req)
	if err == nil {
		t.Fatal("expected error for too many members")
	}
}

func TestCoalition_ValidationInvalidTierScore(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.QualityTiers = []QualityTier{
		{Name: "bad", MinScore: -0.5, PayoutPct: 50},
	}
	_, err := svc.Create(ctx, req)
	if err == nil {
		t.Fatal("expected error for negative tier score")
	}
}

func TestCoalition_ValidationInvalidTierPayout(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.QualityTiers = []QualityTier{
		{Name: "too much", MinScore: 0.5, PayoutPct: 150},
	}
	_, err := svc.Create(ctx, req)
	if err == nil {
		t.Fatal("expected error for payout > 100")
	}
}

func TestCoalition_ValidationProportionalZeroWeight(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.SplitStrategy = SplitProportional
	req.Members = []CoalitionMember{
		{AgentAddr: "0xAgent1", Role: "a", Weight: 0.0}, // zero weight
		{AgentAddr: "0xAgent2", Role: "b", Weight: 1.0},
	}
	_, err := svc.Create(ctx, req)
	if err == nil {
		t.Fatal("expected error for zero weight in proportional strategy")
	}
}

// ---------------------------------------------------------------------------
// Coalition: OracleReport with non-member contributions
// ---------------------------------------------------------------------------

func TestCoalition_OracleReport_NonMemberContribution(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.SplitStrategy = SplitShapley
	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Include a non-member address in contributions
	_, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
		Contributions: map[string]float64{
			"0xagent1": 0.5,
			"0xagent2": 0.3,
			"0xagent3": 0.1,
			"0xhacker": 0.1, // not a member!
		},
	})
	if err == nil {
		t.Fatal("expected error for non-member in contributions")
	}
}

// ---------------------------------------------------------------------------
// Coalition: Timer lifecycle
// ---------------------------------------------------------------------------

func TestCoalitionTimer_StartStop(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml)
	timer := NewCoalitionTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))

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

// ---------------------------------------------------------------------------
// Escrow: Timer Running check
// ---------------------------------------------------------------------------

func TestTimer_Running(t *testing.T) {
	store := NewMemoryStore()
	ml := newMockLedger()
	svc := NewService(store, ml)
	timer := NewTimer(svc, store, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if timer.Running() {
		t.Fatal("should not be running before Start")
	}
}

// ---------------------------------------------------------------------------
// Coalition: MemoryStore edge cases
// ---------------------------------------------------------------------------

func TestCoalitionMemoryStore_UpdateNonexistent(t *testing.T) {
	store := NewCoalitionMemoryStore()
	ctx := context.Background()

	err := store.Update(ctx, &CoalitionEscrow{ID: "nonexistent"})
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Fatalf("expected ErrCoalitionNotFound, got %v", err)
	}
}

func TestCoalitionMemoryStore_ListByAgent(t *testing.T) {
	store := NewCoalitionMemoryStore()
	ctx := context.Background()

	now := time.Now()
	store.Create(ctx, &CoalitionEscrow{
		ID: "coa_1", BuyerAddr: "0xbuyer", OracleAddr: "0xoracle",
		Members:      []CoalitionMember{{AgentAddr: "0xagent1"}},
		Status:       CSActive,
		AutoSettleAt: now.Add(1 * time.Hour),
		CreatedAt:    now, UpdatedAt: now,
	})

	// Find by buyer
	list, err := store.ListByAgent(ctx, "0xbuyer", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1, got %d", len(list))
	}

	// Find by member
	list, _ = store.ListByAgent(ctx, "0xagent1", 10)
	if len(list) != 1 {
		t.Fatalf("expected 1 for member, got %d", len(list))
	}

	// Find by oracle
	list, _ = store.ListByAgent(ctx, "0xoracle", 10)
	if len(list) != 1 {
		t.Fatalf("expected 1 for oracle, got %d", len(list))
	}

	// Not found
	list, _ = store.ListByAgent(ctx, "0xstranger", 10)
	if len(list) != 0 {
		t.Fatalf("expected 0 for stranger, got %d", len(list))
	}
}

func TestCoalitionMemoryStore_ListExpired(t *testing.T) {
	store := NewCoalitionMemoryStore()
	ctx := context.Background()
	now := time.Now()

	// Active, expired
	store.Create(ctx, &CoalitionEscrow{
		ID: "coa_expired", BuyerAddr: "0xb", OracleAddr: "0xo",
		Status:       CSActive,
		AutoSettleAt: now.Add(-1 * time.Hour),
		CreatedAt:    now, UpdatedAt: now,
	})

	// Active, not expired
	store.Create(ctx, &CoalitionEscrow{
		ID: "coa_future", BuyerAddr: "0xb", OracleAddr: "0xo",
		Status:       CSActive,
		AutoSettleAt: now.Add(1 * time.Hour),
		CreatedAt:    now, UpdatedAt: now,
	})

	// Terminal, expired
	store.Create(ctx, &CoalitionEscrow{
		ID: "coa_settled", BuyerAddr: "0xb", OracleAddr: "0xo",
		Status:       CSSettled,
		AutoSettleAt: now.Add(-1 * time.Hour),
		CreatedAt:    now, UpdatedAt: now,
	})

	list, err := store.ListExpired(ctx, now, 10)
	if err != nil {
		t.Fatalf("ListExpired: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 expired (active only), got %d", len(list))
	}
	if list[0].ID != "coa_expired" {
		t.Fatalf("expected coa_expired, got %s", list[0].ID)
	}
}

// ---------------------------------------------------------------------------
// Coalition: Abort nonexistent
// ---------------------------------------------------------------------------

func TestCoalition_AbortNonexistent(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	_, err := svc.Abort(ctx, "nonexistent", "0xBuyer")
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Fatalf("expected ErrCoalitionNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Coalition: OracleReport with contract integration
// ---------------------------------------------------------------------------

// mockContractCheckerExtra is a simpler mock for contract integration tests.
type mockContractCheckerExtra struct {
	contract  *BoundContract
	getErr    error
	bindCalls int
	markCalls int
}

func (m *mockContractCheckerExtra) GetContractByEscrow(_ context.Context, _ string) (*BoundContract, error) {
	return m.contract, m.getErr
}
func (m *mockContractCheckerExtra) BindContract(_ context.Context, _, _ string) error {
	m.bindCalls++
	return nil
}
func (m *mockContractCheckerExtra) MarkContractPassed(_ context.Context, _ string) error {
	m.markCalls++
	return nil
}

func TestCoalition_OracleReport_WithContractPenaltyExtra(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	cc := &mockContractCheckerExtra{
		contract: &BoundContract{
			ID:             "contract_1",
			Status:         "active",
			QualityPenalty: 0.1, // 10% penalty
			HardViolations: 0,
		},
	}
	svc := NewCoalitionService(store, ml).
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithContractChecker(cc)
	ctx := context.Background()

	req := defaultCreateReq()
	req.ContractID = "contract_1"
	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Oracle reports 0.95, but penalty is 0.1, so effective is 0.85 → "good" tier (0.7–0.9)
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.MatchedTier != "good" {
		t.Fatalf("expected 'good' tier after penalty, got %s", ce.MatchedTier)
	}
}

func TestCoalition_OracleReport_WithHardViolationExtra(t *testing.T) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	cc := &mockContractCheckerExtra{
		contract: &BoundContract{
			ID:             "contract_1",
			Status:         "violated",
			QualityPenalty: 0.0,
			HardViolations: 1,
		},
	}
	svc := NewCoalitionService(store, ml).
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithContractChecker(cc)
	ctx := context.Background()

	req := defaultCreateReq()
	req.ContractID = "contract_1"
	ce, _ := svc.Create(ctx, req)

	// Oracle reports 0.95, but hard violation → effective score 0 → poor → 0% payout
	ce, err := svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.TotalPayout != "0.000000" {
		t.Fatalf("expected 0 payout for hard violation, got %s", ce.TotalPayout)
	}
}
