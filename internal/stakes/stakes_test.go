package stakes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// --- Mock Ledger ---

type mockLedger struct {
	mu           sync.Mutex
	escrows      map[string]string // reference → amount
	released     map[string]string // reference → amount (released escrow)
	refunded     map[string]string // reference → amount
	deposits     map[string]string // agentAddr → last amount
	spends       map[string]string // agentAddr → last amount
	holds        map[string]string // reference → amount
	confirmed    map[string]string // reference → amount
	holdReleased map[string]string // reference → amount
	escrowErr    error
	releaseErr   error
	spendErr     error
	depositErr   error
	holdErr      error
}

func newMockLedger() *mockLedger {
	return &mockLedger{
		escrows:      make(map[string]string),
		released:     make(map[string]string),
		refunded:     make(map[string]string),
		deposits:     make(map[string]string),
		spends:       make(map[string]string),
		holds:        make(map[string]string),
		confirmed:    make(map[string]string),
		holdReleased: make(map[string]string),
	}
}

func (m *mockLedger) EscrowLock(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.escrowErr != nil {
		return m.escrowErr
	}
	m.escrows[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseEscrow(_ context.Context, fromAddr, toAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.releaseErr != nil {
		return m.releaseErr
	}
	m.released[reference] = amount
	return nil
}

func (m *mockLedger) RefundEscrow(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refunded[reference] = amount
	return nil
}

func (m *mockLedger) Deposit(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.depositErr != nil {
		return m.depositErr
	}
	m.deposits[agentAddr] = amount
	return nil
}

func (m *mockLedger) Spend(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.spendErr != nil {
		return m.spendErr
	}
	m.spends[agentAddr] = amount
	return nil
}

func (m *mockLedger) Hold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.holdErr != nil {
		return m.holdErr
	}
	m.holds[reference] = amount
	return nil
}

func (m *mockLedger) ConfirmHold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.confirmed[reference] = amount
	return nil
}

func (m *mockLedger) ReleaseHold(_ context.Context, agentAddr, amount, reference string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.holdReleased[reference] = amount
	return nil
}

// --- Helpers ---

const (
	agentAddr    = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	investorAddr = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	buyerAddr    = "0xcccccccccccccccccccccccccccccccccccccccc"
)

func newTestService() (*Service, *mockLedger) {
	store := NewMemoryStore()
	ledger := newMockLedger()
	svc := NewService(store, ledger)
	return svc, ledger
}

func createTestStake(t *testing.T, svc *Service) *Stake {
	t.Helper()
	stake, err := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0.15,
		TotalShares:   1000,
		PricePerShare: "0.50",
		VestingPeriod: "90d",
		Distribution:  "weekly",
	})
	if err != nil {
		t.Fatalf("CreateStake: %v", err)
	}
	return stake
}

func investInStake(t *testing.T, svc *Service, stakeID string, shares int) *Holding {
	t.Helper()
	holding, err := svc.Invest(context.Background(), stakeID, InvestRequest{
		InvestorAddr: investorAddr,
		Shares:       shares,
	})
	if err != nil {
		t.Fatalf("Invest: %v", err)
	}
	return holding
}

// =========================================================================
// CreateStake tests
// =========================================================================

func TestCreateStake(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)

	if stake.ID == "" {
		t.Error("expected non-empty stake ID")
	}
	if stake.RevenueShareBPS != 1500 {
		t.Errorf("expected 1500 bps, got %d", stake.RevenueShareBPS)
	}
	if stake.TotalShares != 1000 {
		t.Errorf("expected 1000 shares, got %d", stake.TotalShares)
	}
	if stake.AvailableShares != 1000 {
		t.Errorf("expected 1000 available, got %d", stake.AvailableShares)
	}
	if stake.Status != string(StakeStatusOpen) {
		t.Errorf("expected open, got %s", stake.Status)
	}
	if stake.Undistributed != "0.000000" {
		t.Errorf("expected 0 undistributed, got %s", stake.Undistributed)
	}
}

func TestCreateStake_InvalidRevenueShare(t *testing.T) {
	svc, _ := newTestService()

	// Zero revenue share
	_, err := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0,
		TotalShares:   1000,
		PricePerShare: "0.50",
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("expected ErrInvalidAmount, got %v", err)
	}

	// Over 50%
	_, err = svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0.60,
		TotalShares:   1000,
		PricePerShare: "0.50",
	})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Errorf("expected ErrInvalidAmount for >50%%, got %v", err)
	}
}

func TestCreateStake_MaxRevenueShareCap(t *testing.T) {
	svc, _ := newTestService()

	// Create first stake at 30%
	_, err := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0.30,
		TotalShares:   100,
		PricePerShare: "1.00",
	})
	if err != nil {
		t.Fatalf("first stake: %v", err)
	}

	// Second stake at 25% would exceed 50%
	_, err = svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0.25,
		TotalShares:   100,
		PricePerShare: "1.00",
	})
	if !errors.Is(err, ErrMaxRevenueShare) {
		t.Errorf("expected ErrMaxRevenueShare, got %v", err)
	}

	// But 20% should work (30 + 20 = 50%)
	_, err = svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0.20,
		TotalShares:   100,
		PricePerShare: "1.00",
	})
	if err != nil {
		t.Errorf("50%% total should be allowed, got %v", err)
	}
}

func TestCreateStake_InvalidVestingPeriod(t *testing.T) {
	svc, _ := newTestService()

	_, err := svc.CreateStake(context.Background(), CreateStakeRequest{
		AgentAddr:     agentAddr,
		RevenueShare:  0.10,
		TotalShares:   100,
		PricePerShare: "1.00",
		VestingPeriod: "abc",
	})
	if err == nil {
		t.Error("expected error for invalid vesting period")
	}
}

// =========================================================================
// Invest tests
// =========================================================================

func TestInvest(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)

	holding := investInStake(t, svc, stake.ID, 100)

	if holding.Shares != 100 {
		t.Errorf("expected 100 shares, got %d", holding.Shares)
	}
	if holding.CostBasis != "50.000000" {
		t.Errorf("expected cost basis 50.000000, got %s", holding.CostBasis)
	}
	if holding.Status != string(HoldingStatusVesting) {
		t.Errorf("expected vesting, got %s", holding.Status)
	}

	// Verify ledger was called (two-phase hold)
	if len(ledger.holds) == 0 {
		t.Error("expected a hold to be placed on investor funds")
	}
	if len(ledger.confirmed) == 0 {
		t.Error("expected hold to be confirmed after deposit")
	}
	if ledger.deposits[agentAddr] != "50.000000" {
		t.Errorf("expected agent received 50.000000, got %s", ledger.deposits[agentAddr])
	}

	// Verify stake was updated
	updated, _ := svc.GetStake(context.Background(), stake.ID)
	if updated.AvailableShares != 900 {
		t.Errorf("expected 900 available, got %d", updated.AvailableShares)
	}
	if updated.TotalRaised != "50.000000" {
		t.Errorf("expected raised 50.000000, got %s", updated.TotalRaised)
	}
}

func TestInvest_SelfInvestment(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)

	_, err := svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: agentAddr, // same as the agent
		Shares:       10,
	})
	if !errors.Is(err, ErrSelfInvestment) {
		t.Errorf("expected ErrSelfInvestment, got %v", err)
	}
}

func TestInvest_InsufficientShares(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)

	_, err := svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: investorAddr,
		Shares:       1001, // only 1000 available
	})
	if !errors.Is(err, ErrInsufficientShare) {
		t.Errorf("expected ErrInsufficientShare, got %v", err)
	}
}

func TestInvest_ClosedStake(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)

	// Close the stake
	_, err := svc.CloseStake(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("CloseStake: %v", err)
	}

	_, err = svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: investorAddr,
		Shares:       10,
	})
	if !errors.Is(err, ErrStakeClosed) {
		t.Errorf("expected ErrStakeClosed, got %v", err)
	}
}

func TestInvest_InsufficientBalance(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)

	ledger.holdErr = errors.New("insufficient balance")

	_, err := svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: investorAddr,
		Shares:       10,
	})
	if err == nil {
		t.Error("expected error for insufficient balance")
	}
}

// =========================================================================
// AccumulateRevenue + Distribute tests
// =========================================================================

func TestAccumulateRevenue(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 100)

	// Agent earns $100
	err := svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")
	if err != nil {
		t.Fatalf("AccumulateRevenue: %v", err)
	}

	// 15% of $100 = $15 should be escrowed
	updated, _ := svc.GetStake(context.Background(), stake.ID)
	if updated.Undistributed != "15.000000" {
		t.Errorf("expected undistributed 15.000000, got %s", updated.Undistributed)
	}

	// Verify escrow was called
	found := false
	for ref, amt := range ledger.escrows {
		if amt == "15.000000" {
			found = true
			_ = ref
			break
		}
	}
	if !found {
		t.Error("expected escrow lock of 15.000000")
	}
}

func TestAccumulateRevenue_NoInvestors(t *testing.T) {
	svc, ledger := newTestService()
	createTestStake(t, svc) // no one invested

	err := svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")
	if err != nil {
		t.Fatalf("AccumulateRevenue: %v", err)
	}

	// No escrow should be called since there are no investors
	if len(ledger.escrows) != 0 {
		t.Errorf("expected no escrow calls, got %d", len(ledger.escrows))
	}
}

func TestAccumulateRevenue_UnrelatedAgent(t *testing.T) {
	svc, ledger := newTestService()
	createTestStake(t, svc)
	investInStake(t, svc, createTestStake(t, svc).ID, 10)

	// Different agent earns money — should not affect our stake
	err := svc.AccumulateRevenue(context.Background(), "0xdeaddeaddeaddeaddeaddeaddeaddeaddeaddead", "100.000000", "")
	if err != nil {
		t.Fatalf("AccumulateRevenue: %v", err)
	}

	// No escrow should be called for an unrelated agent
	if len(ledger.escrows) != 0 {
		t.Errorf("expected no escrow calls for unrelated agent, got %d", len(ledger.escrows))
	}
}

func TestDistribute(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 100)

	// Accumulate revenue
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")

	// Distribute
	updated, _ := svc.GetStake(context.Background(), stake.ID)
	err := svc.Distribute(context.Background(), updated)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	// Verify distribution happened
	afterDist, _ := svc.GetStake(context.Background(), stake.ID)
	if afterDist.Undistributed != "0.000000" {
		t.Errorf("expected undistributed 0 after distribution, got %s", afterDist.Undistributed)
	}
	if afterDist.TotalDistributed == "0.000000" {
		t.Error("expected non-zero total distributed")
	}
	if afterDist.LastDistributedAt == nil {
		t.Error("expected LastDistributedAt to be set")
	}

	// Verify escrow was released to investor
	if len(ledger.released) == 0 {
		t.Error("expected at least one escrow release")
	}

	// Verify distribution record was created
	dists, _ := svc.ListDistributions(context.Background(), stake.ID, 10)
	if len(dists) != 1 {
		t.Fatalf("expected 1 distribution, got %d", len(dists))
	}
	if dists[0].ShareCount != 100 {
		t.Errorf("expected 100 shares in dist, got %d", dists[0].ShareCount)
	}
	if dists[0].HoldingCount != 1 {
		t.Errorf("expected 1 holding in dist, got %d", dists[0].HoldingCount)
	}

	// Verify holding was updated with earnings
	holdings, _ := svc.ListHoldingsByStake(context.Background(), stake.ID)
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	earnedBig, _ := usdc.Parse(holdings[0].TotalEarned)
	if earnedBig.Sign() <= 0 {
		t.Errorf("expected positive earnings, got %s", holdings[0].TotalEarned)
	}
}

func TestDistribute_MultipleInvestors(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)

	// Investor A buys 300 shares
	investInStake(t, svc, stake.ID, 300)

	// Investor B buys 100 shares
	_, err := svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: buyerAddr,
		Shares:       100,
	})
	if err != nil {
		t.Fatalf("Invest B: %v", err)
	}

	// Agent earns $200 → 15% = $30 → distributed to 400 shares
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "200.000000", "")

	updated, _ := svc.GetStake(context.Background(), stake.ID)
	err = svc.Distribute(context.Background(), updated)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	// Verify proportional distribution
	dists, _ := svc.ListDistributions(context.Background(), stake.ID, 10)
	if len(dists) != 1 {
		t.Fatalf("expected 1 distribution, got %d", len(dists))
	}
	if dists[0].ShareCount != 400 {
		t.Errorf("expected 400 total shares, got %d", dists[0].ShareCount)
	}
	if dists[0].HoldingCount != 2 {
		t.Errorf("expected 2 holdings, got %d", dists[0].HoldingCount)
	}
}

// =========================================================================
// CloseStake tests
// =========================================================================

func TestCloseStake(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)

	closed, err := svc.CloseStake(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("CloseStake: %v", err)
	}
	if closed.Status != string(StakeStatusClosed) {
		t.Errorf("expected closed, got %s", closed.Status)
	}
}

func TestCloseStake_RefundsUndistributed(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 100)

	// Accumulate some revenue
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")

	// Close should refund the undistributed escrow
	_, err := svc.CloseStake(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("CloseStake: %v", err)
	}

	if len(ledger.refunded) == 0 {
		t.Error("expected escrow refund on close")
	}
}

func TestCloseStake_AlreadyClosed(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	_, _ = svc.CloseStake(context.Background(), stake.ID)

	_, err := svc.CloseStake(context.Background(), stake.ID)
	if !errors.Is(err, ErrStakeClosed) {
		t.Errorf("expected ErrStakeClosed, got %v", err)
	}
}

// =========================================================================
// Secondary market tests
// =========================================================================

func TestPlaceOrder_NotVested(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Holding is vesting (90 days out), should not be sellable
	_, err := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})
	if !errors.Is(err, ErrNotVested) {
		t.Errorf("expected ErrNotVested, got %v", err)
	}
}

func TestPlaceOrder_Success(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest the holding
	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	order, err := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}
	if order.Shares != 50 {
		t.Errorf("expected 50 shares, got %d", order.Shares)
	}
	if order.Status != string(OrderStatusOpen) {
		t.Errorf("expected open, got %s", order.Status)
	}
}

func TestPlaceOrder_WrongOwner(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	_, err := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    buyerAddr, // not the actual owner
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPlaceOrder_InsufficientShares(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	_, err := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        101, // only have 100
		PricePerShare: "0.75",
	})
	if !errors.Is(err, ErrInsufficientHeld) {
		t.Errorf("expected ErrInsufficientHeld, got %v", err)
	}
}

func TestFillOrder(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	// Place order
	order, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})

	// Fill order
	filledOrder, buyerHolding, err := svc.FillOrder(context.Background(), order.ID, FillOrderRequest{
		BuyerAddr: buyerAddr,
	})
	if err != nil {
		t.Fatalf("FillOrder: %v", err)
	}

	// Verify order is filled
	if filledOrder.Status != string(OrderStatusFilled) {
		t.Errorf("expected filled, got %s", filledOrder.Status)
	}
	if filledOrder.BuyerAddr != buyerAddr {
		t.Errorf("expected buyer %s, got %s", buyerAddr, filledOrder.BuyerAddr)
	}

	// Verify buyer holding
	if buyerHolding.Shares != 50 {
		t.Errorf("expected 50 buyer shares, got %d", buyerHolding.Shares)
	}
	if buyerHolding.Status != string(HoldingStatusActive) {
		t.Errorf("expected active (vested), got %s", buyerHolding.Status)
	}
	if buyerHolding.CostBasis != "37.500000" {
		t.Errorf("expected cost basis 37.500000 (50*0.75), got %s", buyerHolding.CostBasis)
	}

	// Verify seller holding reduced
	sellerHolding, _ := svc.store.GetHolding(context.Background(), holding.ID)
	if sellerHolding.Shares != 50 {
		t.Errorf("expected seller to have 50 remaining, got %d", sellerHolding.Shares)
	}

	// Verify ledger calls (two-phase hold)
	foundHold := false
	for _, amt := range ledger.holds {
		if amt == "37.500000" {
			foundHold = true
			break
		}
	}
	if !foundHold {
		t.Error("expected a hold of 37.500000 on buyer")
	}
	if ledger.deposits[investorAddr] != "37.500000" {
		t.Errorf("expected seller received 37.500000, got %s", ledger.deposits[investorAddr])
	}
}

func TestFillOrder_FullLiquidation(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	// Sell ALL shares
	order, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        100,
		PricePerShare: "0.75",
	})

	_, _, err := svc.FillOrder(context.Background(), order.ID, FillOrderRequest{
		BuyerAddr: buyerAddr,
	})
	if err != nil {
		t.Fatalf("FillOrder: %v", err)
	}

	// Verify seller holding is liquidated
	sellerHolding, _ := svc.store.GetHolding(context.Background(), holding.ID)
	if sellerHolding.Status != string(HoldingStatusLiquidated) {
		t.Errorf("expected liquidated, got %s", sellerHolding.Status)
	}
	if sellerHolding.Shares != 0 {
		t.Errorf("expected 0 shares, got %d", sellerHolding.Shares)
	}
}

func TestCancelOrder(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	order, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})

	cancelled, err := svc.CancelOrder(context.Background(), order.ID, investorAddr)
	if err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if cancelled.Status != string(OrderStatusCancelled) {
		t.Errorf("expected cancelled, got %s", cancelled.Status)
	}
}

func TestCancelOrder_WrongOwner(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	holding.VestedAt = time.Now().Add(-time.Hour)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	order, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})

	_, err := svc.CancelOrder(context.Background(), order.ID, buyerAddr)
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestFillOrder_AlreadyFilled(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	order, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})

	// Fill once
	_, _, _ = svc.FillOrder(context.Background(), order.ID, FillOrderRequest{BuyerAddr: buyerAddr})

	// Try to fill again
	_, _, err := svc.FillOrder(context.Background(), order.ID, FillOrderRequest{BuyerAddr: buyerAddr})
	if !errors.Is(err, ErrOrderNotOpen) {
		t.Errorf("expected ErrOrderNotOpen, got %v", err)
	}
}

// =========================================================================
// Portfolio tests
// =========================================================================

func TestGetPortfolio(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 200)

	portfolio, err := svc.GetPortfolio(context.Background(), investorAddr)
	if err != nil {
		t.Fatalf("GetPortfolio: %v", err)
	}

	if portfolio.TotalInvested != "100.000000" {
		t.Errorf("expected total invested 100.000000, got %s", portfolio.TotalInvested)
	}
	if len(portfolio.Holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(portfolio.Holdings))
	}
	if portfolio.Holdings[0].AgentAddr != agentAddr {
		t.Errorf("expected agent %s, got %s", agentAddr, portfolio.Holdings[0].AgentAddr)
	}
	// 200 out of 200 issued shares = 100%
	if portfolio.Holdings[0].SharePct != 100.0 {
		t.Errorf("expected 100%% share, got %.1f%%", portfolio.Holdings[0].SharePct)
	}
}

// =========================================================================
// End-to-end lifecycle test
// =========================================================================

func TestFullLifecycle(t *testing.T) {
	svc, _ := newTestService()
	ctx := context.Background()

	// 1. Create offering
	stake := createTestStake(t, svc)

	// 2. Invest
	holding := investInStake(t, svc, stake.ID, 500)

	// 3. Agent earns revenue
	_ = svc.AccumulateRevenue(ctx, agentAddr, "1000.000000", "")

	// 4. Verify undistributed = 15% of 1000 = 150
	updated, _ := svc.GetStake(ctx, stake.ID)
	if updated.Undistributed != "150.000000" {
		t.Fatalf("expected 150.000000 undistributed, got %s", updated.Undistributed)
	}

	// 5. Distribute
	err := svc.Distribute(ctx, updated)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	// 6. Verify earnings
	afterDist, _ := svc.GetStake(ctx, stake.ID)
	if afterDist.Undistributed != "0.000000" {
		t.Errorf("expected 0 undistributed, got %s", afterDist.Undistributed)
	}

	// Investor should have earned: 150 * (500/500) = 150
	holdings, _ := svc.ListHoldingsByStake(ctx, stake.ID)
	if len(holdings) != 1 {
		t.Fatalf("expected 1 holding, got %d", len(holdings))
	}
	if holdings[0].TotalEarned != "150.000000" {
		t.Errorf("expected earned 150.000000, got %s", holdings[0].TotalEarned)
	}

	// 7. Vest the holding and sell on market
	holdings[0].VestedAt = time.Now().Add(-time.Hour)
	holdings[0].Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(ctx, holdings[0])

	order, err := svc.PlaceOrder(ctx, PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        200,
		PricePerShare: "1.00", // appreciated from $0.50
	})
	if err != nil {
		t.Fatalf("PlaceOrder: %v", err)
	}

	// 8. Buyer fills
	_, buyerHolding, err := svc.FillOrder(ctx, order.ID, FillOrderRequest{
		BuyerAddr: buyerAddr,
	})
	if err != nil {
		t.Fatalf("FillOrder: %v", err)
	}
	if buyerHolding.Shares != 200 {
		t.Errorf("expected 200 buyer shares, got %d", buyerHolding.Shares)
	}

	// Seller should have 300 remaining
	sellerHolding, _ := svc.store.GetHolding(ctx, holding.ID)
	if sellerHolding.Shares != 300 {
		t.Errorf("expected 300 remaining seller shares, got %d", sellerHolding.Shares)
	}

	// 9. Close offering
	closed, err := svc.CloseStake(ctx, stake.ID)
	if err != nil {
		t.Fatalf("CloseStake: %v", err)
	}
	if closed.Status != string(StakeStatusClosed) {
		t.Errorf("expected closed, got %s", closed.Status)
	}
}

// =========================================================================
// Helper function tests
// =========================================================================

func TestParseVestingPeriod(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		hasErr   bool
	}{
		{"90d", 90 * 24 * time.Hour, false},
		{"30d", 30 * 24 * time.Hour, false},
		{"2w", 14 * 24 * time.Hour, false},
		{"abc", 0, true},
		{"d", 0, true},
		{"", 0, true},
	}

	for _, tt := range tests {
		d, err := parseVestingPeriod(tt.input)
		if tt.hasErr && err == nil {
			t.Errorf("parseVestingPeriod(%q): expected error", tt.input)
		}
		if !tt.hasErr && err != nil {
			t.Errorf("parseVestingPeriod(%q): unexpected error: %v", tt.input, err)
		}
		if !tt.hasErr && d != tt.expected {
			t.Errorf("parseVestingPeriod(%q): expected %v, got %v", tt.input, tt.expected, d)
		}
	}
}

func TestFreqToDuration(t *testing.T) {
	if freqToDuration("daily") != 24*time.Hour {
		t.Error("daily should be 24h")
	}
	if freqToDuration("weekly") != 7*24*time.Hour {
		t.Error("weekly should be 7d")
	}
	if freqToDuration("monthly") != 30*24*time.Hour {
		t.Error("monthly should be 30d")
	}
	if freqToDuration("unknown") != 7*24*time.Hour {
		t.Error("unknown should default to weekly")
	}
}

func TestHoldingIsVested(t *testing.T) {
	h := &Holding{VestedAt: time.Now().Add(-time.Hour)}
	if !h.IsVested(time.Now()) {
		t.Error("holding should be vested (past vesting date)")
	}

	h2 := &Holding{VestedAt: time.Now().Add(time.Hour)}
	if h2.IsVested(time.Now()) {
		t.Error("holding should NOT be vested (future vesting date)")
	}
}

// =========================================================================
// P0 Fix: Two-phase hold recovery tests
// =========================================================================

func TestInvest_DepositFailure_ReleasesHold(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)

	// Deposit will fail — hold should be released
	ledger.depositErr = errors.New("deposit failed")

	_, err := svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: investorAddr,
		Shares:       10,
	})
	if err == nil {
		t.Fatal("expected error when deposit fails")
	}

	// Verify hold was placed
	if len(ledger.holds) == 0 {
		t.Error("expected hold to be placed before deposit attempt")
	}

	// Verify hold was released (not confirmed)
	if len(ledger.holdReleased) == 0 {
		t.Error("expected hold to be released after deposit failure")
	}
	if len(ledger.confirmed) != 0 {
		t.Error("expected no confirmed holds after deposit failure")
	}

	// Verify stake was not modified
	updated, _ := svc.GetStake(context.Background(), stake.ID)
	if updated.AvailableShares != stake.TotalShares {
		t.Errorf("expected shares unchanged, got %d available", updated.AvailableShares)
	}
}

func TestFillOrder_DepositFailure_ReleasesHold(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Manually vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	order, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})

	// Now make deposit fail
	ledger.depositErr = errors.New("deposit failed")

	_, _, err := svc.FillOrder(context.Background(), order.ID, FillOrderRequest{
		BuyerAddr: buyerAddr,
	})
	if err == nil {
		t.Fatal("expected error when deposit fails")
	}

	// Verify hold was released
	if len(ledger.holdReleased) == 0 {
		t.Error("expected hold to be released after deposit failure")
	}

	// Order should still be open
	o, _ := svc.GetOrder(context.Background(), order.ID)
	if o.Status != string(OrderStatusOpen) {
		t.Errorf("expected order still open, got %s", o.Status)
	}
}

// =========================================================================
// P0 Fix: Distribution carry-forward tests
// =========================================================================

func TestDistribute_PartialFailure_CarriesForward(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)

	// Two investors
	investInStake(t, svc, stake.ID, 300)
	_, _ = svc.Invest(context.Background(), stake.ID, InvestRequest{
		InvestorAddr: buyerAddr,
		Shares:       100,
	})

	// Accumulate revenue: 15% of $200 = $30
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "200.000000", "")

	// Make escrow release fail — all distributions will fail
	ledger.releaseErr = errors.New("release failed")

	updated, _ := svc.GetStake(context.Background(), stake.ID)
	_ = svc.Distribute(context.Background(), updated)

	// Undistributed should remain at $30 (carried forward, not zeroed)
	afterDist, _ := svc.GetStake(context.Background(), stake.ID)
	if afterDist.Undistributed == "0.000000" {
		t.Error("expected undistributed to be carried forward, not zeroed")
	}
	if afterDist.Undistributed != "30.000000" {
		t.Errorf("expected undistributed 30.000000, got %s", afterDist.Undistributed)
	}
}

func TestDistribute_PartialSuccess_SubtractsDistributed(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)

	// One investor with 100 shares
	investInStake(t, svc, stake.ID, 100)

	// Accumulate: 15% of $100 = $15
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")

	// Allow releases (default: no error)
	updated, _ := svc.GetStake(context.Background(), stake.ID)
	err := svc.Distribute(context.Background(), updated)
	if err != nil {
		t.Fatalf("Distribute: %v", err)
	}

	afterDist, _ := svc.GetStake(context.Background(), stake.ID)

	// All succeeded, so undistributed should be 0
	if afterDist.Undistributed != "0.000000" {
		t.Errorf("expected undistributed 0.000000, got %s", afterDist.Undistributed)
	}

	// Verify distribution status is "completed" (not "partial")
	dists, _ := svc.ListDistributions(context.Background(), stake.ID, 10)
	if len(dists) != 1 {
		t.Fatalf("expected 1 distribution, got %d", len(dists))
	}
	if dists[0].Status != "completed" {
		t.Errorf("expected completed status, got %s", dists[0].Status)
	}
	_ = ledger
}

// =========================================================================
// P0 Fix: AccumulateRevenue idempotency tests
// =========================================================================

func TestAccumulateRevenue_Idempotent(t *testing.T) {
	svc, ledger := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 100)

	// First call with a txRef
	err := svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "tx_123")
	if err != nil {
		t.Fatalf("AccumulateRevenue: %v", err)
	}

	// 15% of $100 = $15 escrowed
	updated, _ := svc.GetStake(context.Background(), stake.ID)
	if updated.Undistributed != "15.000000" {
		t.Errorf("expected 15.000000, got %s", updated.Undistributed)
	}

	// Second call with SAME txRef — should be a no-op
	err = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "tx_123")
	if err != nil {
		t.Fatalf("AccumulateRevenue (duplicate): %v", err)
	}

	// Should still be $15, not $30
	updated2, _ := svc.GetStake(context.Background(), stake.ID)
	if updated2.Undistributed != "15.000000" {
		t.Errorf("expected 15.000000 after duplicate call, got %s", updated2.Undistributed)
	}

	// Only one escrow call
	if len(ledger.escrows) != 1 {
		t.Errorf("expected 1 escrow call, got %d", len(ledger.escrows))
	}
}

func TestAccumulateRevenue_DifferentRefs(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 100)

	// Two calls with different refs — both should process
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "tx_aaa")
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "tx_bbb")

	updated, _ := svc.GetStake(context.Background(), stake.ID)
	if updated.Undistributed != "30.000000" {
		t.Errorf("expected 30.000000 (two distinct txs), got %s", updated.Undistributed)
	}
}

func TestAccumulateRevenue_EmptyRef_NoDedup(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	investInStake(t, svc, stake.ID, 100)

	// Empty ref — no dedup, both should process
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")
	_ = svc.AccumulateRevenue(context.Background(), agentAddr, "100.000000", "")

	updated, _ := svc.GetStake(context.Background(), stake.ID)
	if updated.Undistributed != "30.000000" {
		t.Errorf("expected 30.000000 (empty ref = no dedup), got %s", updated.Undistributed)
	}
}

// =========================================================================
// P1 Fix: CloseStake cancels orphaned orders
// =========================================================================

func TestCloseStake_CancelsOpenOrders(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Vest and place two orders
	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	order1, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        30,
		PricePerShare: "0.75",
	})
	order2, _ := svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        20,
		PricePerShare: "1.00",
	})

	// Close the stake
	_, err := svc.CloseStake(context.Background(), stake.ID)
	if err != nil {
		t.Fatalf("CloseStake: %v", err)
	}

	// Both orders should be cancelled
	o1, _ := svc.GetOrder(context.Background(), order1.ID)
	if o1.Status != string(OrderStatusCancelled) {
		t.Errorf("expected order1 cancelled, got %s", o1.Status)
	}
	o2, _ := svc.GetOrder(context.Background(), order2.ID)
	if o2.Status != string(OrderStatusCancelled) {
		t.Errorf("expected order2 cancelled, got %s", o2.Status)
	}
}

// =========================================================================
// P1 Fix: ListOrdersBySeller
// =========================================================================

func TestListOrdersBySeller(t *testing.T) {
	svc, _ := newTestService()
	stake := createTestStake(t, svc)
	holding := investInStake(t, svc, stake.ID, 100)

	// Vest
	holding.VestedAt = time.Now().Add(-time.Hour)
	holding.Status = string(HoldingStatusActive)
	_ = svc.store.UpdateHolding(context.Background(), holding)

	// Place an order
	_, _ = svc.PlaceOrder(context.Background(), PlaceOrderRequest{
		SellerAddr:    investorAddr,
		HoldingID:     holding.ID,
		Shares:        50,
		PricePerShare: "0.75",
	})

	// Query by seller
	orders, err := svc.ListOrdersBySeller(context.Background(), investorAddr, 50)
	if err != nil {
		t.Fatalf("ListOrdersBySeller: %v", err)
	}
	if len(orders) != 1 {
		t.Errorf("expected 1 order, got %d", len(orders))
	}

	// Different seller should return 0
	orders2, _ := svc.ListOrdersBySeller(context.Background(), buyerAddr, 50)
	if len(orders2) != 0 {
		t.Errorf("expected 0 orders for different seller, got %d", len(orders2))
	}
}
