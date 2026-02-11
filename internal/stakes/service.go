package stakes

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Service implements the revenue staking business logic.
type Service struct {
	store    Store
	ledger   LedgerService
	locks    sync.Map // per-stake ID locks
	seenRefs sync.Map // txRef → struct{} for AccumulateRevenue idempotency
}

// NewService creates a new staking service.
func NewService(store Store, ledger LedgerService) *Service {
	return &Service{
		store:  store,
		ledger: ledger,
	}
}

func (s *Service) stakeLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// CreateStake creates a new revenue-sharing offering.
func (s *Service) CreateStake(ctx context.Context, req CreateStakeRequest) (*Stake, error) {
	bps := int(req.RevenueShare * 10000)
	if bps <= 0 || bps > MaxRevenueShareBPS {
		return nil, fmt.Errorf("%w: revenue share must be between 0 and 50%%", ErrInvalidAmount)
	}

	if req.TotalShares <= 0 {
		return nil, fmt.Errorf("%w: totalShares must be positive", ErrInvalidAmount)
	}

	priceBig, ok := usdc.Parse(req.PricePerShare)
	if !ok || priceBig.Sign() <= 0 {
		return nil, fmt.Errorf("%w: pricePerShare must be positive", ErrInvalidAmount)
	}

	agentAddr := strings.ToLower(req.AgentAddr)

	// Check total revenue share cap
	existingBPS, err := s.store.GetAgentTotalShareBPS(ctx, agentAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing stakes: %w", err)
	}
	if existingBPS+bps > MaxRevenueShareBPS {
		return nil, ErrMaxRevenueShare
	}

	vestingPeriod := req.VestingPeriod
	if vestingPeriod == "" {
		vestingPeriod = "90d"
	}
	if _, err := parseVestingPeriod(vestingPeriod); err != nil {
		return nil, err
	}

	distFreq := req.Distribution
	if distFreq == "" {
		distFreq = "weekly"
	}
	switch distFreq {
	case "daily", "weekly", "monthly":
	default:
		return nil, fmt.Errorf("invalid distribution frequency: %s", distFreq)
	}

	now := time.Now()
	stake := &Stake{
		ID:               generateStakeID(),
		AgentAddr:        agentAddr,
		RevenueShareBPS:  bps,
		TotalShares:      req.TotalShares,
		AvailableShares:  req.TotalShares,
		PricePerShare:    usdc.Format(priceBig),
		VestingPeriod:    vestingPeriod,
		DistributionFreq: distFreq,
		Status:           string(StakeStatusOpen),
		TotalRaised:      "0.000000",
		TotalDistributed: "0.000000",
		Undistributed:    "0.000000",
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.store.CreateStake(ctx, stake); err != nil {
		return nil, fmt.Errorf("failed to create stake: %w", err)
	}

	return stake, nil
}

// Invest buys shares in a stake offering.
func (s *Service) Invest(ctx context.Context, stakeID string, req InvestRequest) (*Holding, error) {
	mu := s.stakeLock(stakeID)
	mu.Lock()
	defer mu.Unlock()

	stake, err := s.store.GetStake(ctx, stakeID)
	if err != nil {
		return nil, err
	}

	if stake.Status != string(StakeStatusOpen) {
		return nil, ErrStakeClosed
	}

	investorAddr := strings.ToLower(req.InvestorAddr)
	if investorAddr == stake.AgentAddr {
		return nil, ErrSelfInvestment
	}

	if req.Shares <= 0 {
		return nil, fmt.Errorf("%w: shares must be positive", ErrInvalidAmount)
	}
	if req.Shares > stake.AvailableShares {
		return nil, ErrInsufficientShare
	}

	// Calculate total cost
	priceBig, _ := usdc.Parse(stake.PricePerShare)
	totalCost := new(big.Int).Mul(priceBig, big.NewInt(int64(req.Shares)))
	totalCostStr := usdc.Format(totalCost)

	// Two-phase hold: Hold investor funds (available → pending) before any
	// state mutations. If Deposit or any subsequent step fails, we release
	// the hold instead of leaving the ledger inconsistent.
	holdRef := fmt.Sprintf("stake_invest:%s:%s", stakeID, investorAddr)
	if err := s.ledger.Hold(ctx, investorAddr, totalCostStr, holdRef); err != nil {
		return nil, fmt.Errorf("failed to hold investor funds: %w", err)
	}

	// Credit agent — if this fails, release the hold
	if err := s.ledger.Deposit(ctx, stake.AgentAddr, totalCostStr, holdRef); err != nil {
		if relErr := s.ledger.ReleaseHold(ctx, investorAddr, totalCostStr, holdRef); relErr != nil {
			log.Printf("CRITICAL: stake %s deposit failed AND hold release failed: %v", stakeID, relErr)
		}
		return nil, fmt.Errorf("failed to credit agent: %w", err)
	}

	// Confirm hold (pending → total_out) — deposit succeeded
	if err := s.ledger.ConfirmHold(ctx, investorAddr, totalCostStr, holdRef); err != nil {
		log.Printf("CRITICAL: stake %s agent credited but confirm hold failed: %v", stakeID, err)
		return nil, fmt.Errorf("failed to confirm investor hold: %w", err)
	}

	// Calculate vesting date
	vestDur, _ := parseVestingPeriod(stake.VestingPeriod)
	now := time.Now()

	holding := &Holding{
		ID:           generateHoldingID(),
		StakeID:      stakeID,
		InvestorAddr: investorAddr,
		Shares:       req.Shares,
		CostBasis:    totalCostStr,
		VestedAt:     now.Add(vestDur),
		Status:       string(HoldingStatusVesting),
		TotalEarned:  "0.000000",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.CreateHolding(ctx, holding); err != nil {
		return nil, fmt.Errorf("failed to create holding: %w", err)
	}

	// Update stake
	raisedBig, _ := usdc.Parse(stake.TotalRaised)
	newRaised := new(big.Int).Add(raisedBig, totalCost)
	stake.AvailableShares -= req.Shares
	stake.TotalRaised = usdc.Format(newRaised)
	stake.UpdatedAt = now

	if err := s.store.UpdateStake(ctx, stake); err != nil {
		log.Printf("CRITICAL: stake %s holding created but stake update failed: %v", stakeID, err)
		return nil, fmt.Errorf("failed to update stake: %w", err)
	}

	return holding, nil
}

// AccumulateRevenue escrows the revenue share portion when an agent earns money.
// Called by the revenue interceptor after session key payments, escrow releases,
// and stream settlements. The txRef parameter is an idempotency key — if the
// same txRef is seen twice, the second call is silently skipped to prevent
// double-escrowing the same revenue.
func (s *Service) AccumulateRevenue(ctx context.Context, agentAddr, amount, txRef string) error {
	agentAddr = strings.ToLower(agentAddr)

	ctx, span := traces.StartSpan(ctx, "stakes.AccumulateRevenue",
		traces.AgentAddr(agentAddr), traces.Amount(amount), attribute.String("tx.ref", txRef))
	defer span.End()

	// Idempotency check: skip if this transaction ref was already processed
	if txRef != "" {
		if _, loaded := s.seenRefs.LoadOrStore(txRef, struct{}{}); loaded {
			return nil // already processed
		}
	}

	stakes, err := s.store.ListByAgent(ctx, agentAddr)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to list agent stakes")
		return fmt.Errorf("failed to list agent stakes: %w", err)
	}

	amountBig, ok := usdc.Parse(amount)
	if !ok || amountBig.Sign() <= 0 {
		return nil // nothing to accumulate
	}

	for _, stake := range stakes {
		if stake.Status != string(StakeStatusOpen) {
			continue
		}
		// issued shares = total - available; skip if nobody invested
		if stake.TotalShares-stake.AvailableShares == 0 {
			continue
		}

		mu := s.stakeLock(stake.ID)
		mu.Lock()

		// Re-read under lock
		fresh, err := s.store.GetStake(ctx, stake.ID)
		if err != nil {
			mu.Unlock()
			continue
		}

		// Calculate revenue share: amount * bps / 10000
		share := new(big.Int).Mul(amountBig, big.NewInt(int64(fresh.RevenueShareBPS)))
		share.Div(share, big.NewInt(10000))

		if share.Sign() <= 0 {
			mu.Unlock()
			continue
		}

		shareStr := usdc.Format(share)
		ref := fmt.Sprintf("stake_revenue:%s", fresh.ID)

		// Escrow the revenue share from the agent's balance
		if err := s.ledger.EscrowLock(ctx, agentAddr, shareStr, ref); err != nil {
			log.Printf("WARNING: failed to escrow revenue share for stake %s: %v", fresh.ID, err)
			mu.Unlock()
			continue
		}

		// Update undistributed
		undistBig, _ := usdc.Parse(fresh.Undistributed)
		newUndist := new(big.Int).Add(undistBig, share)
		fresh.Undistributed = usdc.Format(newUndist)
		fresh.UpdatedAt = time.Now()

		if err := s.store.UpdateStake(ctx, fresh); err != nil {
			log.Printf("CRITICAL: stake %s revenue escrowed but undistributed update failed: %v", fresh.ID, err)
		}

		mu.Unlock()
	}

	metrics.StakesRevenueAccumulatedTotal.Inc()
	return nil
}

// Distribute pays out accumulated revenue to shareholders of a stake.
func (s *Service) Distribute(ctx context.Context, stake *Stake) error {
	ctx, span := traces.StartSpan(ctx, "stakes.Distribute",
		traces.StakeID(stake.ID))
	defer span.End()

	mu := s.stakeLock(stake.ID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read under lock
	fresh, err := s.store.GetStake(ctx, stake.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get stake")
		metrics.StakesDistributionFailuresTotal.Inc()
		return err
	}

	undistBig, _ := usdc.Parse(fresh.Undistributed)
	if undistBig.Sign() <= 0 {
		return nil // nothing to distribute
	}

	holdings, err := s.store.ListHoldingsByStake(ctx, fresh.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to list holdings")
		metrics.StakesDistributionFailuresTotal.Inc()
		return fmt.Errorf("failed to list holdings: %w", err)
	}

	if len(holdings) == 0 {
		return nil
	}

	// Calculate total issued shares from active holdings
	totalIssuedShares := 0
	for _, h := range holdings {
		totalIssuedShares += h.Shares
	}
	if totalIssuedShares == 0 {
		return nil
	}

	// Per-share amount
	perShare := new(big.Int).Div(undistBig, big.NewInt(int64(totalIssuedShares)))
	if perShare.Sign() <= 0 {
		return nil // amount too small to distribute
	}

	totalDistributed := big.NewInt(0)
	holdingCount := 0
	failedCount := 0

	for _, h := range holdings {
		payout := new(big.Int).Mul(perShare, big.NewInt(int64(h.Shares)))
		if payout.Sign() <= 0 {
			continue
		}

		payoutStr := usdc.Format(payout)
		ref := fmt.Sprintf("stake_dist:%s:%s", fresh.ID, h.ID)

		// Release escrow: agent → investor
		if err := s.ledger.ReleaseEscrow(ctx, fresh.AgentAddr, h.InvestorAddr, payoutStr, ref); err != nil {
			log.Printf("WARNING: failed to distribute to %s for stake %s: %v", h.InvestorAddr, fresh.ID, err)
			failedCount++
			continue
		}

		// Update holding earnings
		earnedBig, _ := usdc.Parse(h.TotalEarned)
		newEarned := new(big.Int).Add(earnedBig, payout)
		h.TotalEarned = usdc.Format(newEarned)
		h.UpdatedAt = time.Now()

		// Update vesting status if applicable
		if h.Status == string(HoldingStatusVesting) && h.IsVested(time.Now()) {
			h.Status = string(HoldingStatusActive)
		}

		if err := s.store.UpdateHolding(ctx, h); err != nil {
			log.Printf("WARNING: holding %s distribution succeeded but update failed: %v", h.ID, err)
		}

		totalDistributed.Add(totalDistributed, payout)
		holdingCount++
	}

	// Only record a distribution if at least one holder was paid
	if holdingCount == 0 {
		log.Printf("WARNING: stake %s distribution attempted but all %d payouts failed", fresh.ID, failedCount)
		span.SetStatus(codes.Error, "all payouts failed")
		metrics.StakesDistributionFailuresTotal.Inc()
		return nil
	}

	// Record distribution event
	now := time.Now()
	distStatus := "completed"
	if failedCount > 0 {
		distStatus = "partial"
	}
	dist := &Distribution{
		ID:             generateDistributionID(),
		StakeID:        fresh.ID,
		AgentAddr:      fresh.AgentAddr,
		RevenueAmount:  fresh.Undistributed,
		ShareAmount:    usdc.Format(totalDistributed),
		PerShareAmount: usdc.Format(perShare),
		ShareCount:     totalIssuedShares,
		HoldingCount:   holdingCount,
		Status:         distStatus,
		CreatedAt:      now,
	}

	if err := s.store.CreateDistribution(ctx, dist); err != nil {
		log.Printf("WARNING: distribution record for stake %s failed: %v", fresh.ID, err)
	}

	// Update stake totals — only subtract what was actually distributed,
	// carrying forward any failed payouts for the next distribution cycle.
	remaining := new(big.Int).Sub(undistBig, totalDistributed)
	distTotalBig, _ := usdc.Parse(fresh.TotalDistributed)
	newDistTotal := new(big.Int).Add(distTotalBig, totalDistributed)
	fresh.TotalDistributed = usdc.Format(newDistTotal)
	fresh.Undistributed = usdc.Format(remaining)
	fresh.LastDistributedAt = &now
	fresh.UpdatedAt = now

	if err := s.store.UpdateStake(ctx, fresh); err != nil {
		log.Printf("CRITICAL: stake %s distribution completed but state update failed: %v", fresh.ID, err)
	}

	metrics.StakesDistributionsTotal.Inc()
	return nil
}

// CloseStake closes an offering. Undistributed revenue is refunded to the agent.
func (s *Service) CloseStake(ctx context.Context, stakeID string) (*Stake, error) {
	mu := s.stakeLock(stakeID)
	mu.Lock()
	defer mu.Unlock()

	stake, err := s.store.GetStake(ctx, stakeID)
	if err != nil {
		return nil, err
	}

	if stake.Status == string(StakeStatusClosed) {
		return nil, ErrStakeClosed
	}

	// Refund any undistributed escrow to the agent
	undistBig, _ := usdc.Parse(stake.Undistributed)
	if undistBig.Sign() > 0 {
		ref := fmt.Sprintf("stake_close_refund:%s", stake.ID)
		if err := s.ledger.RefundEscrow(ctx, stake.AgentAddr, stake.Undistributed, ref); err != nil {
			return nil, fmt.Errorf("failed to refund undistributed escrow: %w", err)
		}
	}

	// Cancel all open orders — prevents orphaned fillable orders on a closed stake
	openOrders, err := s.store.ListOrdersByStake(ctx, stakeID, string(OrderStatusOpen), 1000)
	if err != nil {
		log.Printf("WARNING: failed to list orders for cleanup on close stake %s: %v", stakeID, err)
	}
	now := time.Now()
	for _, order := range openOrders {
		order.Status = string(OrderStatusCancelled)
		order.UpdatedAt = now
		if err := s.store.UpdateOrder(ctx, order); err != nil {
			log.Printf("WARNING: failed to cancel order %s during stake close: %v", order.ID, err)
		}
	}

	stake.Status = string(StakeStatusClosed)
	stake.Undistributed = "0.000000"
	stake.UpdatedAt = now

	if err := s.store.UpdateStake(ctx, stake); err != nil {
		return nil, fmt.Errorf("failed to close stake: %w", err)
	}

	return stake, nil
}

// PlaceOrder lists shares for sale on the secondary market.
func (s *Service) PlaceOrder(ctx context.Context, req PlaceOrderRequest) (*Order, error) {
	holding, err := s.store.GetHolding(ctx, req.HoldingID)
	if err != nil {
		return nil, err
	}

	sellerAddr := strings.ToLower(req.SellerAddr)
	if strings.ToLower(holding.InvestorAddr) != sellerAddr {
		return nil, ErrUnauthorized
	}

	if !holding.IsVested(time.Now()) {
		return nil, ErrNotVested
	}

	if req.Shares <= 0 || req.Shares > holding.Shares {
		return nil, ErrInsufficientHeld
	}

	priceBig, ok := usdc.Parse(req.PricePerShare)
	if !ok || priceBig.Sign() <= 0 {
		return nil, fmt.Errorf("%w: pricePerShare must be positive", ErrInvalidAmount)
	}

	now := time.Now()
	order := &Order{
		ID:            generateOrderID(),
		StakeID:       holding.StakeID,
		HoldingID:     holding.ID,
		SellerAddr:    sellerAddr,
		Shares:        req.Shares,
		PricePerShare: usdc.Format(priceBig),
		Status:        string(OrderStatusOpen),
		FilledShares:  0,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.store.CreateOrder(ctx, order); err != nil {
		return nil, fmt.Errorf("failed to create order: %w", err)
	}

	return order, nil
}

// FillOrder buys shares from a sell order on the secondary market.
func (s *Service) FillOrder(ctx context.Context, orderID string, req FillOrderRequest) (*Order, *Holding, error) {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}

	if order.Status != string(OrderStatusOpen) {
		return nil, nil, ErrOrderNotOpen
	}

	buyerAddr := strings.ToLower(req.BuyerAddr)
	if buyerAddr == order.SellerAddr {
		return nil, nil, fmt.Errorf("cannot buy your own order")
	}

	// Calculate total cost
	priceBig, _ := usdc.Parse(order.PricePerShare)
	totalCost := new(big.Int).Mul(priceBig, big.NewInt(int64(order.Shares)))
	totalCostStr := usdc.Format(totalCost)

	// Two-phase hold: Hold buyer funds first, then credit seller, then confirm.
	holdRef := fmt.Sprintf("stake_market:%s:%s", order.ID, buyerAddr)
	if err := s.ledger.Hold(ctx, buyerAddr, totalCostStr, holdRef); err != nil {
		return nil, nil, fmt.Errorf("failed to hold buyer funds: %w", err)
	}

	// Credit seller — if this fails, release the hold
	if err := s.ledger.Deposit(ctx, order.SellerAddr, totalCostStr, holdRef); err != nil {
		if relErr := s.ledger.ReleaseHold(ctx, buyerAddr, totalCostStr, holdRef); relErr != nil {
			log.Printf("CRITICAL: order %s deposit failed AND hold release failed: %v", orderID, relErr)
		}
		return nil, nil, fmt.Errorf("failed to credit seller: %w", err)
	}

	// Confirm hold (pending → total_out) — seller credited
	if err := s.ledger.ConfirmHold(ctx, buyerAddr, totalCostStr, holdRef); err != nil {
		log.Printf("CRITICAL: order %s seller credited but confirm hold failed: %v", orderID, err)
		return nil, nil, fmt.Errorf("failed to confirm buyer hold: %w", err)
	}

	// Reduce seller's holding
	sellerHolding, err := s.store.GetHolding(ctx, order.HoldingID)
	if err != nil {
		log.Printf("CRITICAL: order %s payment done but seller holding lookup failed: %v", orderID, err)
		return nil, nil, fmt.Errorf("failed to get seller holding: %w", err)
	}

	now := time.Now()
	sellerHolding.Shares -= order.Shares
	sellerHolding.UpdatedAt = now
	if sellerHolding.Shares == 0 {
		sellerHolding.Status = string(HoldingStatusLiquidated)
	}

	if err := s.store.UpdateHolding(ctx, sellerHolding); err != nil {
		log.Printf("CRITICAL: order %s payment done but seller holding update failed: %v", orderID, err)
	}

	// Create or augment buyer's holding
	buyerHolding, err := s.store.GetHoldingByInvestorAndStake(ctx, buyerAddr, order.StakeID)
	if err != nil {
		// Create new holding for buyer (already vested since purchased on market)
		buyerHolding = &Holding{
			ID:           generateHoldingID(),
			StakeID:      order.StakeID,
			InvestorAddr: buyerAddr,
			Shares:       order.Shares,
			CostBasis:    totalCostStr,
			VestedAt:     now, // immediately vested for secondary market purchases
			Status:       string(HoldingStatusActive),
			TotalEarned:  "0.000000",
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := s.store.CreateHolding(ctx, buyerHolding); err != nil {
			log.Printf("CRITICAL: order %s payment done but buyer holding creation failed: %v", orderID, err)
			return nil, nil, fmt.Errorf("failed to create buyer holding: %w", err)
		}
	} else {
		// Augment existing holding
		costBig, _ := usdc.Parse(buyerHolding.CostBasis)
		newCost := new(big.Int).Add(costBig, totalCost)
		buyerHolding.Shares += order.Shares
		buyerHolding.CostBasis = usdc.Format(newCost)
		buyerHolding.UpdatedAt = now
		if err := s.store.UpdateHolding(ctx, buyerHolding); err != nil {
			log.Printf("CRITICAL: order %s buyer holding augmentation failed: %v", orderID, err)
		}
	}

	// Mark order filled
	order.Status = string(OrderStatusFilled)
	order.FilledShares = order.Shares
	order.BuyerAddr = buyerAddr
	order.UpdatedAt = now

	if err := s.store.UpdateOrder(ctx, order); err != nil {
		log.Printf("WARNING: order %s filled but status update failed: %v", orderID, err)
	}

	return order, buyerHolding, nil
}

// CancelOrder cancels an open sell order.
func (s *Service) CancelOrder(ctx context.Context, orderID, callerAddr string) (*Order, error) {
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}

	if order.Status != string(OrderStatusOpen) {
		return nil, ErrOrderNotOpen
	}

	if strings.ToLower(callerAddr) != order.SellerAddr {
		return nil, ErrUnauthorized
	}

	now := time.Now()
	order.Status = string(OrderStatusCancelled)
	order.UpdatedAt = now

	if err := s.store.UpdateOrder(ctx, order); err != nil {
		return nil, fmt.Errorf("failed to cancel order: %w", err)
	}

	return order, nil
}

// --- Read-only methods ---

// GetStake returns a stake by ID.
func (s *Service) GetStake(ctx context.Context, id string) (*Stake, error) {
	return s.store.GetStake(ctx, id)
}

// ListOpen returns open stake offerings.
func (s *Service) ListOpen(ctx context.Context, limit int) ([]*Stake, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListOpen(ctx, limit)
}

// ListByAgent returns stakes for an agent.
func (s *Service) ListByAgent(ctx context.Context, agentAddr string) ([]*Stake, error) {
	return s.store.ListByAgent(ctx, strings.ToLower(agentAddr))
}

// GetPortfolio returns an investor's portfolio summary.
func (s *Service) GetPortfolio(ctx context.Context, investorAddr string) (*PortfolioResponse, error) {
	holdings, err := s.store.ListHoldingsByInvestor(ctx, strings.ToLower(investorAddr))
	if err != nil {
		return nil, err
	}

	totalInvested := big.NewInt(0)
	totalEarned := big.NewInt(0)
	var entries []PortfolioEntry

	for _, h := range holdings {
		// Update vesting status if needed
		if h.Status == string(HoldingStatusVesting) && h.IsVested(time.Now()) {
			h.Status = string(HoldingStatusActive)
			h.UpdatedAt = time.Now()
			if err := s.store.UpdateHolding(ctx, h); err != nil {
				log.Printf("WARNING: holding %s vesting auto-upgrade failed: %v", h.ID, err)
			}
		}

		costBig, _ := usdc.Parse(h.CostBasis)
		earnedBig, _ := usdc.Parse(h.TotalEarned)
		totalInvested.Add(totalInvested, costBig)
		totalEarned.Add(totalEarned, earnedBig)

		// Get stake to find agent addr and calculate share percentage
		stake, err := s.store.GetStake(ctx, h.StakeID)
		if err != nil {
			continue
		}

		issuedShares := stake.TotalShares - stake.AvailableShares
		sharePct := 0.0
		if issuedShares > 0 {
			sharePct = float64(h.Shares) / float64(issuedShares) * 100.0
		}

		entries = append(entries, PortfolioEntry{
			AgentAddr: stake.AgentAddr,
			Holding:   h,
			SharePct:  sharePct,
		})
	}

	return &PortfolioResponse{
		TotalInvested: usdc.Format(totalInvested),
		TotalEarned:   usdc.Format(totalEarned),
		Holdings:      entries,
	}, nil
}

// ListHoldingsByStake returns holdings for a specific stake.
func (s *Service) ListHoldingsByStake(ctx context.Context, stakeID string) ([]*Holding, error) {
	return s.store.ListHoldingsByStake(ctx, stakeID)
}

// ListHoldingsByInvestor returns holdings for an investor.
func (s *Service) ListHoldingsByInvestor(ctx context.Context, investorAddr string) ([]*Holding, error) {
	return s.store.ListHoldingsByInvestor(ctx, strings.ToLower(investorAddr))
}

// ListDistributions returns distributions for a stake.
func (s *Service) ListDistributions(ctx context.Context, stakeID string, limit int) ([]*Distribution, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListDistributions(ctx, stakeID, limit)
}

// GetOrder returns an order by ID.
func (s *Service) GetOrder(ctx context.Context, id string) (*Order, error) {
	return s.store.GetOrder(ctx, id)
}

// ListOrdersByStake returns orders for a stake.
func (s *Service) ListOrdersByStake(ctx context.Context, stakeID, status string, limit int) ([]*Order, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListOrdersByStake(ctx, stakeID, status, limit)
}

// ListOrdersBySeller returns orders placed by a seller.
func (s *Service) ListOrdersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Order, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListOrdersBySeller(ctx, strings.ToLower(sellerAddr), limit)
}

// ListDueForDistribution returns stakes that need distribution.
func (s *Service) ListDueForDistribution(ctx context.Context, limit int) ([]*Stake, error) {
	return s.store.ListDueForDistribution(ctx, time.Now(), limit)
}
