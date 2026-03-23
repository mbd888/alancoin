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

func newCoalitionTestService() (*CoalitionService, *mockLedger) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	svc := NewCoalitionService(store, ml).WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	return svc, ml
}

func defaultCreateReq() CreateCoalitionRequest {
	return CreateCoalitionRequest{
		BuyerAddr:     "0xBuyer",
		OracleAddr:    "0xOracle",
		TotalAmount:   "1.000000",
		SplitStrategy: SplitEqual,
		Members: []CoalitionMember{
			{AgentAddr: "0xAgent1", Role: "translator"},
			{AgentAddr: "0xAgent2", Role: "summarizer"},
			{AgentAddr: "0xAgent3", Role: "formatter"},
		},
		QualityTiers: []QualityTier{
			{Name: "excellent", MinScore: 0.9, PayoutPct: 100},
			{Name: "good", MinScore: 0.7, PayoutPct: 75},
			{Name: "acceptable", MinScore: 0.5, PayoutPct: 50},
			{Name: "poor", MinScore: 0.0, PayoutPct: 0},
		},
	}
}

func TestCoalition_CreateAndGet(t *testing.T) {
	svc, ml := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if ce.Status != CSActive {
		t.Fatalf("expected active, got %s", ce.Status)
	}
	if len(ce.Members) != 3 {
		t.Fatalf("expected 3 members, got %d", len(ce.Members))
	}
	if ce.BuyerAddr != "0xbuyer" {
		t.Fatalf("expected normalized buyer, got %s", ce.BuyerAddr)
	}
	if ce.OracleAddr != "0xoracle" {
		t.Fatalf("expected normalized oracle, got %s", ce.OracleAddr)
	}

	// Verify funds locked
	ref := "coa:" + ce.ID
	if ml.locked[ref] != "1.000000" {
		t.Fatalf("expected locked 1.000000, got %s", ml.locked[ref])
	}

	// Get should return same
	got, err := svc.Get(ctx, ce.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != ce.ID {
		t.Fatalf("Get returned wrong ID")
	}
}

func TestCoalition_FullLifecycle_EqualSplit_Excellent(t *testing.T) {
	svc, ml := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// All members report completion
	for _, addr := range []string{"0xAgent1", "0xAgent2", "0xAgent3"} {
		ce, err = svc.ReportCompletion(ctx, ce.ID, addr)
		if err != nil {
			t.Fatalf("ReportCompletion(%s): %v", addr, err)
		}
	}
	if ce.Status != CSDelivered {
		t.Fatalf("expected delivered after all complete, got %s", ce.Status)
	}

	// Oracle reports excellent quality (1.0)
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.Status != CSSettled {
		t.Fatalf("expected settled, got %s", ce.Status)
	}
	if ce.MatchedTier != "excellent" {
		t.Fatalf("expected excellent tier, got %s", ce.MatchedTier)
	}
	if ce.PayoutPct != 100 {
		t.Fatalf("expected 100%% payout, got %f", ce.PayoutPct)
	}
	if ce.TotalPayout != "1.000000" {
		t.Fatalf("expected payout 1.000000, got %s", ce.TotalPayout)
	}
	if ce.RefundAmount != "0.000000" {
		t.Fatalf("expected refund 0.000000, got %s", ce.RefundAmount)
	}

	// Verify 3 releases (equal split: 0.333333, 0.333333, 0.333334)
	ref := "coa:" + ce.ID
	releaseCount := 0
	for key := range ml.released {
		if len(key) > len(ref) && key[:len(ref)] == ref {
			releaseCount++
		}
	}
	if releaseCount != 3 {
		t.Fatalf("expected 3 releases, got %d", releaseCount)
	}

	// Verify each member has a payout share set
	for _, m := range ce.Members {
		if m.PayoutShare == "" || m.PayoutShare == "0.000000" {
			t.Fatalf("member %s has no payout share", m.AgentAddr)
		}
	}
}

func TestCoalition_PartialPayout_GoodQuality(t *testing.T) {
	svc, ml := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Oracle reports good quality (score=0.75 → 75% payout)
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.75,
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.MatchedTier != "good" {
		t.Fatalf("expected good tier, got %s", ce.MatchedTier)
	}
	if ce.TotalPayout != "0.750000" {
		t.Fatalf("expected payout 0.750000, got %s", ce.TotalPayout)
	}
	if ce.RefundAmount != "0.250000" {
		t.Fatalf("expected refund 0.250000, got %s", ce.RefundAmount)
	}

	// Verify refund happened
	ref := "coa:" + ce.ID + ":refund"
	if ml.refunded[ref] != "0.250000" {
		t.Fatalf("expected refund 0.250000, got %s", ml.refunded[ref])
	}
}

func TestCoalition_ZeroPayout_PoorQuality(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Oracle reports poor quality (score=0.3 → below 0.5 → poor tier → 0% payout)
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.3,
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.MatchedTier != "poor" {
		t.Fatalf("expected poor tier for score 0.3, got %s", ce.MatchedTier)
	}
	if ce.TotalPayout != "0.000000" {
		t.Fatalf("expected payout 0.000000, got %s", ce.TotalPayout)
	}
	if ce.RefundAmount != "1.000000" {
		t.Fatalf("expected full refund, got %s", ce.RefundAmount)
	}
}

func TestCoalition_ProportionalSplit(t *testing.T) {
	svc, ml := newCoalitionTestService()
	ctx := context.Background()
	_ = ml

	req := defaultCreateReq()
	req.SplitStrategy = SplitProportional
	req.Members = []CoalitionMember{
		{AgentAddr: "0xAgent1", Role: "lead", Weight: 0.5},
		{AgentAddr: "0xAgent2", Role: "support", Weight: 0.3},
		{AgentAddr: "0xAgent3", Role: "junior", Weight: 0.2},
	}

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Oracle reports excellent
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Agent1 should get ~50% = 0.500000
	if ce.Members[0].PayoutShare != "0.500000" {
		t.Fatalf("expected agent1 share 0.500000, got %s", ce.Members[0].PayoutShare)
	}
	// Agent2 should get ~30% = 0.300000
	if ce.Members[1].PayoutShare != "0.300000" {
		t.Fatalf("expected agent2 share 0.300000, got %s", ce.Members[1].PayoutShare)
	}
	// Agent3 gets remainder = 0.200000
	if ce.Members[2].PayoutShare != "0.200000" {
		t.Fatalf("expected agent3 share 0.200000, got %s", ce.Members[2].PayoutShare)
	}
}

func TestCoalition_ShapleySplit(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.SplitStrategy = SplitShapley

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Oracle provides contribution scores
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
		Contributions: map[string]float64{
			"0xagent1": 0.6, // 60% contribution
			"0xagent2": 0.3, // 30% contribution
			"0xagent3": 0.1, // 10% contribution
		},
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.Status != CSSettled {
		t.Fatalf("expected settled, got %s", ce.Status)
	}

	// Agent1 should get ~60% of 1.000000 = ~0.600000
	if ce.Members[0].PayoutShare != "0.600000" {
		t.Fatalf("expected agent1 share ~0.600000, got %s", ce.Members[0].PayoutShare)
	}
	// Agent2 should get ~30%
	if ce.Members[1].PayoutShare != "0.300000" {
		t.Fatalf("expected agent2 share ~0.300000, got %s", ce.Members[1].PayoutShare)
	}
}

func TestCoalition_Abort(t *testing.T) {
	svc, ml := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Buyer aborts
	ce, err = svc.Abort(ctx, ce.ID, "0xBuyer")
	if err != nil {
		t.Fatalf("Abort: %v", err)
	}
	if ce.Status != CSAborted {
		t.Fatalf("expected aborted, got %s", ce.Status)
	}

	// Verify full refund
	ref := "coa:" + ce.ID + ":abort"
	if ml.refunded[ref] != "1.000000" {
		t.Fatalf("expected refund 1.000000, got %s", ml.refunded[ref])
	}
}

func TestCoalition_AbortUnauthorized(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Abort(ctx, ce.ID, "0xStranger")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
}

func TestCoalition_OracleReportUnauthorized(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.OracleReport(ctx, ce.ID, "0xStranger", OracleReportRequest{QualityScore: 0.5})
	if !errors.Is(err, ErrOracleUnauthorized) {
		t.Fatalf("expected ErrOracleUnauthorized, got %v", err)
	}
}

func TestCoalition_InvalidQualityScore(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Score > 1.0
	_, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 1.5})
	if !errors.Is(err, ErrInvalidQualityScore) {
		t.Fatalf("expected ErrInvalidQualityScore, got %v", err)
	}

	// Score < 0
	_, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: -0.1})
	if !errors.Is(err, ErrInvalidQualityScore) {
		t.Fatalf("expected ErrInvalidQualityScore, got %v", err)
	}
}

func TestCoalition_DoubleSettle(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First oracle report
	_, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Second oracle report should fail
	_, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.5})
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("expected ErrAlreadyResolved, got %v", err)
	}
}

func TestCoalition_ReportCompletion_NotMember(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.ReportCompletion(ctx, ce.ID, "0xStranger")
	if !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}
}

func TestCoalition_ReportCompletion_Duplicate(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.ReportCompletion(ctx, ce.ID, "0xAgent1")
	if err != nil {
		t.Fatalf("ReportCompletion: %v", err)
	}

	_, err = svc.ReportCompletion(ctx, ce.ID, "0xAgent1")
	if !errors.Is(err, ErrMemberAlreadyReported) {
		t.Fatalf("expected ErrMemberAlreadyReported, got %v", err)
	}
}

func TestCoalition_OracleReportBeforeAllComplete(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Only 1 of 3 members complete, but oracle can still report
	_, err = svc.ReportCompletion(ctx, ce.ID, "0xAgent1")
	if err != nil {
		t.Fatalf("ReportCompletion: %v", err)
	}

	// Oracle can report even if not all members completed (active status)
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.5})
	if err != nil {
		t.Fatalf("OracleReport should succeed from active state: %v", err)
	}
	if ce.Status != CSSettled {
		t.Fatalf("expected settled, got %s", ce.Status)
	}
}

func TestCoalition_AutoSettle(t *testing.T) {
	svc, ml := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Force auto-settle
	err = svc.AutoSettle(ctx, ce)
	if err != nil {
		t.Fatalf("AutoSettle: %v", err)
	}

	got, _ := svc.Get(ctx, ce.ID)
	if got.Status != CSExpired {
		t.Fatalf("expected expired, got %s", got.Status)
	}

	// Verify full refund
	ref := "coa:" + ce.ID + ":expired"
	if ml.refunded[ref] != "1.000000" {
		t.Fatalf("expected refund 1.000000, got %s", ml.refunded[ref])
	}
}

func TestCoalition_AutoSettle_AlreadyTerminal(t *testing.T) {
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

	// AutoSettle should be a no-op on terminal
	err = svc.AutoSettle(ctx, ce)
	if err != nil {
		t.Fatalf("AutoSettle on terminal should not error: %v", err)
	}
}

func TestCoalition_NotFound(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	_, err := svc.Get(ctx, "nonexistent")
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Fatalf("expected ErrCoalitionNotFound, got %v", err)
	}

	_, err = svc.ReportCompletion(ctx, "nonexistent", "0xAgent1")
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Fatalf("expected ErrCoalitionNotFound, got %v", err)
	}

	_, err = svc.OracleReport(ctx, "nonexistent", "0xOracle", OracleReportRequest{QualityScore: 0.5})
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Fatalf("expected ErrCoalitionNotFound, got %v", err)
	}

	_, err = svc.Abort(ctx, "nonexistent", "0xBuyer")
	if !errors.Is(err, ErrCoalitionNotFound) {
		t.Fatalf("expected ErrCoalitionNotFound, got %v", err)
	}
}

func TestCoalition_ValidationErrors(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	tests := []struct {
		name    string
		modify  func(*CreateCoalitionRequest)
		wantErr error
	}{
		{
			name:    "invalid amount",
			modify:  func(r *CreateCoalitionRequest) { r.TotalAmount = "-1" },
			wantErr: ErrInvalidAmount,
		},
		{
			name:    "no members",
			modify:  func(r *CreateCoalitionRequest) { r.Members = nil },
			wantErr: ErrNoMembers,
		},
		{
			name:    "no quality tiers",
			modify:  func(r *CreateCoalitionRequest) { r.QualityTiers = nil },
			wantErr: ErrNoQualityTiers,
		},
		{
			name:    "invalid split strategy",
			modify:  func(r *CreateCoalitionRequest) { r.SplitStrategy = "invalid" },
			wantErr: ErrInvalidSplitStrategy,
		},
		{
			name: "duplicate member",
			modify: func(r *CreateCoalitionRequest) {
				r.Members = []CoalitionMember{
					{AgentAddr: "0xAgent1", Role: "a"},
					{AgentAddr: "0xAgent1", Role: "b"},
				}
			},
			wantErr: ErrDuplicateMember,
		},
		{
			name: "proportional weights don't sum to 1",
			modify: func(r *CreateCoalitionRequest) {
				r.SplitStrategy = SplitProportional
				r.Members = []CoalitionMember{
					{AgentAddr: "0xAgent1", Role: "a", Weight: 0.3},
					{AgentAddr: "0xAgent2", Role: "b", Weight: 0.3},
				}
			},
			wantErr: ErrWeightsSumInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := defaultCreateReq()
			tt.modify(&req)
			_, err := svc.Create(ctx, req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}

	// Buyer cannot be a member
	reqBuyerMember := defaultCreateReq()
	reqBuyerMember.Members[0].AgentAddr = "0xBuyer"
	_, err := svc.Create(ctx, reqBuyerMember)
	if err == nil {
		t.Fatal("expected error when buyer is a member")
	}

	// Buyer cannot be the oracle
	reqBuyerOracle := defaultCreateReq()
	reqBuyerOracle.OracleAddr = "0xBuyer"
	_, err = svc.Create(ctx, reqBuyerOracle)
	if err == nil {
		t.Fatal("expected error when buyer is the oracle")
	}
}

func TestCoalition_ListByAgent(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	// Create 2 coalitions
	_, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create 1: %v", err)
	}
	_, err = svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create 2: %v", err)
	}

	// List as buyer
	list, err := svc.ListByAgent(ctx, "0xBuyer", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}

	// List as member
	list, err = svc.ListByAgent(ctx, "0xAgent1", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}

	// List as oracle
	list, err = svc.ListByAgent(ctx, "0xOracle", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}

	// List as stranger
	list, err = svc.ListByAgent(ctx, "0xStranger", 10)
	if err != nil {
		t.Fatalf("ListByAgent: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0, got %d", len(list))
	}
}

// --- Unit tests for split computation ---

func TestComputeEqualShares(t *testing.T) {
	total := big.NewInt(1000000) // 1.000000 in USDC micro-units

	shares := computeEqualShares(
		[]CoalitionMember{
			{AgentAddr: "a"},
			{AgentAddr: "b"},
			{AgentAddr: "c"},
		},
		total,
	)

	// a: 333333, b: 333333, c: 333334 (remainder to last)
	if shares["a"].Int64() != 333333 {
		t.Fatalf("a: expected 333333, got %d", shares["a"].Int64())
	}
	if shares["b"].Int64() != 333333 {
		t.Fatalf("b: expected 333333, got %d", shares["b"].Int64())
	}
	if shares["c"].Int64() != 333334 {
		t.Fatalf("c: expected 333334, got %d", shares["c"].Int64())
	}

	// Sum must equal total
	sum := new(big.Int).Add(shares["a"], shares["b"])
	sum.Add(sum, shares["c"])
	if sum.Cmp(total) != 0 {
		t.Fatalf("shares sum %d != total %d", sum.Int64(), total.Int64())
	}
}

func TestComputeProportionalShares(t *testing.T) {
	total := big.NewInt(1000000)

	shares := computeProportionalShares(
		[]CoalitionMember{
			{AgentAddr: "a", Weight: 0.5},
			{AgentAddr: "b", Weight: 0.3},
			{AgentAddr: "c", Weight: 0.2},
		},
		total,
	)

	if shares["a"].Int64() != 500000 {
		t.Fatalf("a: expected 500000, got %d", shares["a"].Int64())
	}
	if shares["b"].Int64() != 300000 {
		t.Fatalf("b: expected 300000, got %d", shares["b"].Int64())
	}
	// c gets remainder
	if shares["c"].Int64() != 200000 {
		t.Fatalf("c: expected 200000, got %d", shares["c"].Int64())
	}
}

func TestComputeShapleyShares(t *testing.T) {
	total := big.NewInt(1000000)

	shares := computeShapleyShares(
		[]CoalitionMember{
			{AgentAddr: "a"},
			{AgentAddr: "b"},
		},
		total,
		map[string]float64{
			"a": 0.8,
			"b": 0.2,
		},
	)

	// a should get ~80%
	if shares["a"].Int64() != 800000 {
		t.Fatalf("a: expected 800000, got %d", shares["a"].Int64())
	}
	// b gets remainder
	if shares["b"].Int64() != 200000 {
		t.Fatalf("b: expected 200000, got %d", shares["b"].Int64())
	}
}

func TestComputeShapleyShares_ZeroContributions(t *testing.T) {
	total := big.NewInt(1000000)

	shares := computeShapleyShares(
		[]CoalitionMember{
			{AgentAddr: "a"},
			{AgentAddr: "b"},
		},
		total,
		map[string]float64{
			"a": 0,
			"b": 0,
		},
	)

	// Falls back to equal: 500000 each
	if shares["a"].Int64() != 500000 {
		t.Fatalf("a: expected 500000, got %d", shares["a"].Int64())
	}
}

func TestMatchTier(t *testing.T) {
	tiers := []QualityTier{
		{Name: "excellent", MinScore: 0.9, PayoutPct: 100},
		{Name: "good", MinScore: 0.7, PayoutPct: 75},
		{Name: "acceptable", MinScore: 0.5, PayoutPct: 50},
		{Name: "poor", MinScore: 0.0, PayoutPct: 0},
	}

	tests := []struct {
		score    float64
		wantTier string
		wantPct  float64
	}{
		{1.0, "excellent", 100},
		{0.95, "excellent", 100},
		{0.9, "excellent", 100},
		{0.89, "good", 75},
		{0.7, "good", 75},
		{0.69, "acceptable", 50},
		{0.5, "acceptable", 50},
		{0.49, "poor", 0},
		{0.0, "poor", 0},
	}

	for _, tt := range tests {
		tier, pct := matchTier(tiers, tt.score)
		if tier != tt.wantTier || pct != tt.wantPct {
			t.Errorf("score %.2f: got (%s, %.0f), want (%s, %.0f)",
				tt.score, tier, pct, tt.wantTier, tt.wantPct)
		}
	}
}

func TestComputePayout(t *testing.T) {
	total := big.NewInt(1000000) // 1.000000

	tests := []struct {
		pct  float64
		want int64
	}{
		{100, 1000000},
		{75, 750000},
		{50, 500000},
		{0, 0},
		{33.33, 333300},
	}

	for _, tt := range tests {
		got := computePayout(total, tt.pct)
		if got.Int64() != tt.want {
			t.Errorf("pct %.2f: got %d, want %d", tt.pct, got.Int64(), tt.want)
		}
	}
}

// --- Production robustness tests ---

func TestCoalition_OracleCannotBeMember(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.Members[0].AgentAddr = "0xOracle" // Same as oracle
	_, err := svc.Create(ctx, req)
	if err == nil {
		t.Fatal("expected error when oracle is a member (conflict of interest)")
	}
}

func TestCoalition_ShapleyMissingContributions(t *testing.T) {
	// Verify that members missing from contributions map still get a share
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.SplitStrategy = SplitShapley

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Only provide contributions for 2 of 3 members — agent3 is missing
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{
		QualityScore: 0.95,
		Contributions: map[string]float64{
			"0xagent1": 0.8,
			"0xagent2": 0.2,
			// "0xagent3" intentionally omitted
		},
	})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Agent3 must NOT have zero payout — they should get a floor share
	for _, m := range ce.Members {
		if m.PayoutShare == "0.000000" {
			t.Fatalf("member %s got zero payout despite being in coalition", m.AgentAddr)
		}
	}
}

func TestCoalition_PartialSettlement_PayoutStatus(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	ce, err := svc.Create(ctx, defaultCreateReq())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Normal oracle report
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Verify all members have payoutStatus set
	for _, m := range ce.Members {
		if m.PayoutStatus == "" {
			t.Fatalf("member %s has empty payoutStatus", m.AgentAddr)
		}
		if m.PayoutStatus != "paid" {
			t.Fatalf("member %s expected 'paid', got %s", m.AgentAddr, m.PayoutStatus)
		}
	}
}

func TestComputeShapleyShares_MissingMember(t *testing.T) {
	total := big.NewInt(1000000)

	// Only agent "a" has contribution, "b" is missing
	shares := computeShapleyShares(
		[]CoalitionMember{
			{AgentAddr: "a"},
			{AgentAddr: "b"},
		},
		total,
		map[string]float64{
			"a": 0.9,
			// "b" intentionally omitted
		},
	)

	// Both must have non-zero shares
	if shares["a"].Sign() <= 0 {
		t.Fatalf("a should have positive share, got %d", shares["a"].Int64())
	}
	if shares["b"].Sign() <= 0 {
		t.Fatalf("b should have positive share (floor), got %d", shares["b"].Int64())
	}

	// Sum must equal total
	sum := new(big.Int).Add(shares["a"], shares["b"])
	if sum.Cmp(total) != 0 {
		t.Fatalf("shares sum %d != total %d", sum.Int64(), total.Int64())
	}
}

func TestCoalition_DeepCopy_MutationSafety(t *testing.T) {
	store := NewCoalitionMemoryStore()
	ctx := context.Background()

	ce := &CoalitionEscrow{
		ID:        "coa_test",
		BuyerAddr: "buyer",
		Members: []CoalitionMember{
			{AgentAddr: "agent1"},
		},
		QualityTiers: []QualityTier{
			{Name: "good", MinScore: 0.5, PayoutPct: 50},
		},
		Status: CSActive,
	}
	store.Create(ctx, ce)

	// Mutate the original — should not affect stored copy
	ce.Members = append(ce.Members, CoalitionMember{AgentAddr: "agent2"})

	got, _ := store.Get(ctx, "coa_test")
	if len(got.Members) != 1 {
		t.Fatalf("deep copy broken: stored members mutated, got %d", len(got.Members))
	}
}

func TestCoalition_TwoMembersEqualSplit_ExactDivision(t *testing.T) {
	total := big.NewInt(1000000) // Exactly divisible by 2

	shares := computeEqualShares(
		[]CoalitionMember{
			{AgentAddr: "a"},
			{AgentAddr: "b"},
		},
		total,
	)

	if shares["a"].Int64() != 500000 {
		t.Fatalf("a: expected 500000, got %d", shares["a"].Int64())
	}
	if shares["b"].Int64() != 500000 {
		t.Fatalf("b: expected 500000, got %d", shares["b"].Int64())
	}
}

func TestCoalition_SingleMember(t *testing.T) {
	svc, _ := newCoalitionTestService()
	ctx := context.Background()

	req := defaultCreateReq()
	req.Members = []CoalitionMember{
		{AgentAddr: "0xSolo", Role: "everything"},
	}

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Single member gets full payout
	if ce.Members[0].PayoutShare != "1.000000" {
		t.Fatalf("expected 1.000000, got %s", ce.Members[0].PayoutShare)
	}
}

// --- Contract integration tests ---

// mockContractChecker simulates the contracts service for testing.
type mockContractChecker struct {
	contracts map[string]*BoundContract // escrowID -> contract
	bound     map[string]string         // contractID -> escrowID
}

func newMockContractChecker() *mockContractChecker {
	return &mockContractChecker{
		contracts: make(map[string]*BoundContract),
		bound:     make(map[string]string),
	}
}

func (m *mockContractChecker) GetContractByEscrow(ctx context.Context, escrowID string) (*BoundContract, error) {
	bc, ok := m.contracts[escrowID]
	if !ok {
		return nil, ErrCoalitionNotFound
	}
	return bc, nil
}

func (m *mockContractChecker) BindContract(ctx context.Context, contractID, escrowID string) error {
	m.bound[contractID] = escrowID
	return nil
}

func (m *mockContractChecker) MarkContractPassed(ctx context.Context, contractID string) error {
	return nil
}

func newCoalitionTestServiceWithContracts() (*CoalitionService, *mockLedger, *mockContractChecker) {
	ml := newMockLedger()
	store := NewCoalitionMemoryStore()
	cc := newMockContractChecker()
	svc := NewCoalitionService(store, ml).
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))).
		WithContractChecker(cc)
	return svc, ml, cc
}

func TestCoalition_ContractBinding_OnCreate(t *testing.T) {
	svc, _, cc := newCoalitionTestServiceWithContracts()
	ctx := context.Background()

	req := defaultCreateReq()
	req.ContractID = "abc_test123"

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Contract should be bound
	if cc.bound["abc_test123"] != ce.ID {
		t.Fatalf("expected contract bound to %s, got %s", ce.ID, cc.bound["abc_test123"])
	}
	if ce.ContractID != "abc_test123" {
		t.Fatalf("expected contractId abc_test123, got %s", ce.ContractID)
	}
}

func TestCoalition_ContractPenalty_ReducesPayout(t *testing.T) {
	svc, _, cc := newCoalitionTestServiceWithContracts()
	ctx := context.Background()

	req := defaultCreateReq()
	req.ContractID = "abc_penalty"

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate contract with 0.2 quality penalty from soft violations
	cc.contracts[ce.ID] = &BoundContract{
		ID:             "abc_penalty",
		Status:         "active",
		QualityPenalty: 0.2,
		HardViolations: 0,
	}

	// Oracle reports 0.95 quality — effective becomes 0.95 - 0.2 = 0.75
	// 0.75 matches "good" tier (minScore 0.7, payoutPct 75)
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.MatchedTier != "good" {
		t.Fatalf("expected good tier (penalty-adjusted), got %s", ce.MatchedTier)
	}
	if ce.TotalPayout != "0.750000" {
		t.Fatalf("expected payout 0.750000 (75%% after penalty), got %s", ce.TotalPayout)
	}
}

func TestCoalition_ContractHardViolation_ZeroPayout(t *testing.T) {
	svc, ml, cc := newCoalitionTestServiceWithContracts()
	ctx := context.Background()

	req := defaultCreateReq()
	req.ContractID = "abc_violated"

	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate contract with hard violation
	cc.contracts[ce.ID] = &BoundContract{
		ID:             "abc_violated",
		Status:         "violated",
		QualityPenalty: 0,
		HardViolations: 1,
	}

	// Oracle reports excellent quality — but contract violated → 0 payout
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.99})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	if ce.TotalPayout != "0.000000" {
		t.Fatalf("expected 0.000000 payout (hard violation), got %s", ce.TotalPayout)
	}
	if ce.RefundAmount != "1.000000" {
		t.Fatalf("expected full refund, got %s", ce.RefundAmount)
	}

	// Verify full refund happened
	ref := "coa:" + ce.ID + ":refund"
	if ml.refunded[ref] != "1.000000" {
		t.Fatalf("expected refund 1.000000, got %s", ml.refunded[ref])
	}
}

func TestCoalition_NoContract_NoPenalty(t *testing.T) {
	svc, _, _ := newCoalitionTestServiceWithContracts()
	ctx := context.Background()

	// No contract ID — should work normally with no penalty
	req := defaultCreateReq()
	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// No penalty → excellent tier, full payout
	if ce.MatchedTier != "excellent" {
		t.Fatalf("expected excellent, got %s", ce.MatchedTier)
	}
	if ce.TotalPayout != "1.000000" {
		t.Fatalf("expected 1.000000, got %s", ce.TotalPayout)
	}
}

func TestCoalition_EffectiveScoreStoredCorrectly(t *testing.T) {
	svc, _, cc := newCoalitionTestServiceWithContracts()
	ctx := context.Background()

	req := defaultCreateReq()
	req.ContractID = "abc_score_check"
	ce, err := svc.Create(ctx, req)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Contract with 0.1 penalty
	cc.contracts[ce.ID] = &BoundContract{
		ID:             "abc_score_check",
		Status:         "active",
		QualityPenalty: 0.1,
	}

	// Oracle reports 0.95, effective should be 0.85
	ce, err = svc.OracleReport(ctx, ce.ID, "0xOracle", OracleReportRequest{QualityScore: 0.95})
	if err != nil {
		t.Fatalf("OracleReport: %v", err)
	}

	// Stored score should be the effective (penalty-adjusted) score, not the raw one
	if ce.QualityScore == nil {
		t.Fatal("QualityScore should not be nil")
	}
	if *ce.QualityScore < 0.84 || *ce.QualityScore > 0.86 {
		t.Fatalf("expected effective score ~0.85, got %f", *ce.QualityScore)
	}
}

// --- merged from coalition_extra_test.go ---

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
