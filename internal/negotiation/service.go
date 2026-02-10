package negotiation

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Service implements negotiation business logic.
type Service struct {
	store      Store
	reputation ReputationProvider
	contracts  ContractFormer
	locks      sync.Map // per-RFP ID locks
}

// rfpLock returns a mutex for the given RFP ID.
func (s *Service) rfpLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// NewService creates a new negotiation service.
func NewService(store Store, reputation ReputationProvider) *Service {
	return &Service{
		store:      store,
		reputation: reputation,
	}
}

// WithContractFormer adds the ability to auto-form contracts from winning bids.
func (s *Service) WithContractFormer(cf ContractFormer) *Service {
	s.contracts = cf
	return s
}

// PublishRFP creates a new RFP.
func (s *Service) PublishRFP(ctx context.Context, req PublishRFPRequest) (*RFP, error) {
	// Parse deadline
	deadline, err := parseDeadline(req.BidDeadline)
	if err != nil {
		return nil, fmt.Errorf("invalid bid deadline: %w", err)
	}

	// Validate budget range
	minBudget := parseFloat(req.MinBudget)
	maxBudget := parseFloat(req.MaxBudget)
	if minBudget <= 0 || maxBudget < minBudget {
		return nil, errors.New("invalid budget range: min must be > 0 and max >= min")
	}

	// Defaults
	if req.MaxLatencyMs <= 0 {
		req.MaxLatencyMs = 10000
	}
	if req.MinSuccessRate <= 0 {
		req.MinSuccessRate = 95.00
	}
	if req.MinVolume <= 0 {
		req.MinVolume = 1
	}
	if req.MaxCounterRounds <= 0 {
		req.MaxCounterRounds = 3
	}

	weights := DefaultScoringWeights()
	if req.ScoringWeights != nil {
		weights = *req.ScoringWeights
	}

	now := time.Now()
	rfp := &RFP{
		ID:               generateRFPID(),
		BuyerAddr:        strings.ToLower(req.BuyerAddr),
		ServiceType:      req.ServiceType,
		Description:      req.Description,
		MinBudget:        req.MinBudget,
		MaxBudget:        req.MaxBudget,
		MaxLatencyMs:     req.MaxLatencyMs,
		MinSuccessRate:   req.MinSuccessRate,
		Duration:         req.Duration,
		MinVolume:        req.MinVolume,
		BidDeadline:      deadline,
		AutoSelect:       req.AutoSelect,
		MinReputation:    req.MinReputation,
		MaxCounterRounds: req.MaxCounterRounds,
		ScoringWeights:   weights,
		Status:           RFPStatusOpen,
		BidCount:         0,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	if err := s.store.CreateRFP(ctx, rfp); err != nil {
		return nil, fmt.Errorf("failed to create RFP: %w", err)
	}

	return rfp, nil
}

// PlaceBid places a bid on an open RFP.
func (s *Service) PlaceBid(ctx context.Context, rfpID string, req PlaceBidRequest) (*Bid, error) {
	mu := s.rfpLock(rfpID)
	mu.Lock()
	defer mu.Unlock()

	rfp, err := s.store.GetRFP(ctx, rfpID)
	if err != nil {
		return nil, err
	}

	// Validate RFP state
	if rfp.Status != RFPStatusOpen {
		return nil, ErrInvalidStatus
	}

	if time.Now().After(rfp.BidDeadline) {
		return nil, ErrBidDeadlinePast
	}

	sellerAddr := strings.ToLower(req.SellerAddr)

	// Self-bid check
	if sellerAddr == rfp.BuyerAddr {
		return nil, ErrSelfBid
	}

	// Duplicate bid check
	if _, err := s.store.GetBidBySellerAndRFP(ctx, sellerAddr, rfpID); err == nil {
		return nil, ErrDuplicateBid
	}

	// Budget range check
	bidBudget := parseFloat(req.TotalBudget)
	minBudget := parseFloat(rfp.MinBudget)
	maxBudget := parseFloat(rfp.MaxBudget)
	if bidBudget < minBudget || bidBudget > maxBudget {
		return nil, ErrBudgetOutOfRange
	}

	// Reputation check
	repScore := 0.0
	if s.reputation != nil && rfp.MinReputation > 0 {
		score, _, err := s.reputation.GetScore(ctx, sellerAddr)
		if err == nil {
			repScore = score
		}
		if repScore < rfp.MinReputation {
			return nil, ErrLowReputation
		}
	} else if s.reputation != nil {
		score, _, err := s.reputation.GetScore(ctx, sellerAddr)
		if err == nil {
			repScore = score
		}
	}

	// Defaults
	if req.MaxLatencyMs <= 0 {
		req.MaxLatencyMs = 10000
	}
	if req.SuccessRate <= 0 {
		req.SuccessRate = 95.00
	}
	if req.SellerPenalty == "" {
		req.SellerPenalty = "0"
	}

	now := time.Now()
	bid := &Bid{
		ID:            generateBidID(),
		RFPID:         rfpID,
		SellerAddr:    sellerAddr,
		PricePerCall:  req.PricePerCall,
		TotalBudget:   req.TotalBudget,
		MaxLatencyMs:  req.MaxLatencyMs,
		SuccessRate:   req.SuccessRate,
		Duration:      req.Duration,
		SellerPenalty: req.SellerPenalty,
		Status:        BidStatusPending,
		CounterRound:  0,
		Message:       req.Message,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Compute score
	bid.Score = ScoreBid(bid, rfp, repScore)

	if err := s.store.CreateBid(ctx, bid); err != nil {
		return nil, fmt.Errorf("failed to create bid: %w", err)
	}

	// Increment bid count
	rfp.BidCount++
	rfp.UpdatedAt = now
	if err := s.store.UpdateRFP(ctx, rfp); err != nil {
		log.Printf("WARNING: failed to update RFP bid count: %v", err)
	}

	return bid, nil
}

// Counter creates a counter-offer on an existing bid.
func (s *Service) Counter(ctx context.Context, rfpID, bidID, callerAddr string, req CounterRequest) (*Bid, error) {
	mu := s.rfpLock(rfpID)
	mu.Lock()
	defer mu.Unlock()

	rfp, err := s.store.GetRFP(ctx, rfpID)
	if err != nil {
		return nil, err
	}

	if rfp.Status != RFPStatusOpen && rfp.Status != RFPStatusSelecting {
		return nil, ErrInvalidStatus
	}

	oldBid, err := s.store.GetBid(ctx, bidID)
	if err != nil {
		return nil, err
	}

	if oldBid.RFPID != rfpID {
		return nil, ErrBidNotFound
	}

	if oldBid.Status != BidStatusPending {
		return nil, ErrInvalidStatus
	}

	caller := strings.ToLower(callerAddr)
	isBuyer := caller == rfp.BuyerAddr
	isSeller := caller == oldBid.SellerAddr
	if !isBuyer && !isSeller {
		return nil, ErrUnauthorized
	}

	// Check counter round limit
	if oldBid.CounterRound >= rfp.MaxCounterRounds {
		return nil, ErrMaxCounterRounds
	}

	// Mark old bid as countered
	now := time.Now()
	newBid := &Bid{
		ID:            generateBidID(),
		RFPID:         rfpID,
		SellerAddr:    oldBid.SellerAddr,
		PricePerCall:  mergeString(req.PricePerCall, oldBid.PricePerCall),
		TotalBudget:   mergeString(req.TotalBudget, oldBid.TotalBudget),
		MaxLatencyMs:  mergeInt(req.MaxLatencyMs, oldBid.MaxLatencyMs),
		SuccessRate:   mergeFloat(req.SuccessRate, oldBid.SuccessRate),
		Duration:      mergeString(req.Duration, oldBid.Duration),
		SellerPenalty: mergeString(req.SellerPenalty, oldBid.SellerPenalty),
		Status:        BidStatusPending,
		CounterRound:  oldBid.CounterRound + 1,
		ParentBidID:   oldBid.ID,
		Message:       req.Message,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Get reputation for scoring
	repScore := 0.0
	if s.reputation != nil {
		score, _, err := s.reputation.GetScore(ctx, oldBid.SellerAddr)
		if err == nil {
			repScore = score
		}
	}
	newBid.Score = ScoreBid(newBid, rfp, repScore)

	oldBid.Status = BidStatusCountered
	oldBid.CounteredByID = newBid.ID
	oldBid.UpdatedAt = now

	if err := s.store.UpdateBid(ctx, oldBid); err != nil {
		return nil, fmt.Errorf("failed to update old bid: %w", err)
	}

	if err := s.store.CreateBid(ctx, newBid); err != nil {
		return nil, fmt.Errorf("failed to create counter bid: %w", err)
	}

	return newBid, nil
}

// SelectWinner selects a winning bid for an RFP and forms a binding contract.
func (s *Service) SelectWinner(ctx context.Context, rfpID, bidID, callerAddr string) (*RFP, *Bid, error) {
	mu := s.rfpLock(rfpID)
	mu.Lock()
	defer mu.Unlock()

	rfp, err := s.store.GetRFP(ctx, rfpID)
	if err != nil {
		return nil, nil, err
	}

	if rfp.IsTerminal() {
		return nil, nil, ErrAlreadyAwarded
	}

	if rfp.Status != RFPStatusOpen && rfp.Status != RFPStatusSelecting {
		return nil, nil, ErrInvalidStatus
	}

	caller := strings.ToLower(callerAddr)
	if caller != rfp.BuyerAddr {
		return nil, nil, ErrUnauthorized
	}

	winningBid, err := s.store.GetBid(ctx, bidID)
	if err != nil {
		return nil, nil, err
	}

	if winningBid.RFPID != rfpID || winningBid.Status != BidStatusPending {
		return nil, nil, ErrInvalidStatus
	}

	return s.awardBid(ctx, rfp, winningBid)
}

// AutoSelect automatically selects the highest-scored bid for an RFP.
func (s *Service) AutoSelect(ctx context.Context, rfpID string) (*RFP, *Bid, error) {
	mu := s.rfpLock(rfpID)
	mu.Lock()
	defer mu.Unlock()

	rfp, err := s.store.GetRFP(ctx, rfpID)
	if err != nil {
		return nil, nil, err
	}

	if rfp.IsTerminal() {
		return nil, nil, ErrAlreadyAwarded
	}

	// Get all pending bids
	bids, err := s.store.ListActiveBidsByRFP(ctx, rfpID)
	if err != nil {
		return nil, nil, err
	}
	if len(bids) == 0 {
		return nil, nil, ErrNoBids
	}

	// Recompute scores with fresh reputation data
	var bestBid *Bid
	bestScore := -1.0
	for _, bid := range bids {
		repScore := 0.0
		if s.reputation != nil {
			score, _, err := s.reputation.GetScore(ctx, bid.SellerAddr)
			if err == nil {
				repScore = score
			}
		}
		bid.Score = ScoreBid(bid, rfp, repScore)
		if bid.Score > bestScore {
			bestScore = bid.Score
			bestBid = bid
		}
	}

	return s.awardBid(ctx, rfp, bestBid)
}

// awardBid awards the RFP to a bid. Must be called under rfpLock.
func (s *Service) awardBid(ctx context.Context, rfp *RFP, bid *Bid) (*RFP, *Bid, error) {
	now := time.Now()

	// Accept the winning bid
	bid.Status = BidStatusAccepted
	bid.UpdatedAt = now
	if err := s.store.UpdateBid(ctx, bid); err != nil {
		return nil, nil, fmt.Errorf("failed to accept winning bid: %w", err)
	}

	// Reject all other pending bids
	allBids, err := s.store.ListActiveBidsByRFP(ctx, rfp.ID)
	if err == nil {
		for _, b := range allBids {
			if b.ID != bid.ID {
				b.Status = BidStatusRejected
				b.UpdatedAt = now
				_ = s.store.UpdateBid(ctx, b)
			}
		}
	}

	// Form binding contract if ContractFormer is configured
	var contractID string
	if s.contracts != nil {
		cid, err := s.contracts.FormContract(ctx, rfp, bid)
		if err != nil {
			log.Printf("WARNING: failed to form contract for RFP %s: %v", rfp.ID, err)
		} else {
			contractID = cid
		}
	}

	// Update RFP
	rfp.Status = RFPStatusAwarded
	rfp.WinningBidID = bid.ID
	rfp.ContractID = contractID
	rfp.AwardedAt = &now
	rfp.UpdatedAt = now

	if err := s.store.UpdateRFP(ctx, rfp); err != nil {
		return nil, nil, fmt.Errorf("failed to update RFP: %w", err)
	}

	return rfp, bid, nil
}

// CancelRFP cancels an open RFP and rejects all pending bids.
func (s *Service) CancelRFP(ctx context.Context, rfpID, callerAddr, reason string) (*RFP, error) {
	mu := s.rfpLock(rfpID)
	mu.Lock()
	defer mu.Unlock()

	rfp, err := s.store.GetRFP(ctx, rfpID)
	if err != nil {
		return nil, err
	}

	if rfp.IsTerminal() {
		return nil, ErrAlreadyAwarded
	}

	caller := strings.ToLower(callerAddr)
	if caller != rfp.BuyerAddr {
		return nil, ErrUnauthorized
	}

	now := time.Now()

	// Reject all pending bids
	bids, err := s.store.ListActiveBidsByRFP(ctx, rfpID)
	if err == nil {
		for _, b := range bids {
			b.Status = BidStatusRejected
			b.UpdatedAt = now
			_ = s.store.UpdateBid(ctx, b)
		}
	}

	rfp.Status = RFPStatusCancelled
	rfp.CancelReason = reason
	rfp.UpdatedAt = now

	if err := s.store.UpdateRFP(ctx, rfp); err != nil {
		return nil, fmt.Errorf("failed to cancel RFP: %w", err)
	}

	return rfp, nil
}

// CheckExpired handles expired RFPs:
// 1. Auto-select ready → AutoSelect
// 2. Non-auto past deadline, has bids → "selecting" (24h grace)
// 3. Non-auto past deadline, no bids → "expired"
// 4. Stale "selecting" (24h past deadline) → "expired"
func (s *Service) CheckExpired(ctx context.Context) {
	now := time.Now()

	// 1. Auto-select ready
	autoReady, err := s.store.ListAutoSelectReady(ctx, now, 100)
	if err == nil {
		for _, rfp := range autoReady {
			if _, _, err := s.AutoSelect(ctx, rfp.ID); err != nil {
				if errors.Is(err, ErrNoBids) {
					// No bids → expire
					s.expireRFP(ctx, rfp)
				} else {
					log.Printf("WARNING: auto-select failed for RFP %s: %v", rfp.ID, err)
				}
			}
		}
	}

	// 2. Non-auto expired
	expired, err := s.store.ListExpiredRFPs(ctx, now, 100)
	if err == nil {
		for _, rfp := range expired {
			mu := s.rfpLock(rfp.ID)
			mu.Lock()

			fresh, err := s.store.GetRFP(ctx, rfp.ID)
			if err != nil || fresh.IsTerminal() {
				mu.Unlock()
				continue
			}

			if fresh.BidCount > 0 {
				// Has bids → selecting (give buyer 24h to pick)
				fresh.Status = RFPStatusSelecting
				fresh.UpdatedAt = now
				_ = s.store.UpdateRFP(ctx, fresh)
			} else {
				// No bids → expired
				s.expireRFPLocked(ctx, fresh)
			}

			mu.Unlock()
		}
	}

	// 3. Stale "selecting" (24h past deadline) → expired
	stale, err := s.store.ListStaleSelecting(ctx, now.Add(-24*time.Hour), 100)
	if err == nil {
		for _, rfp := range stale {
			s.expireRFP(ctx, rfp)
		}
	}
}

// expireRFP expires an RFP (acquires lock).
func (s *Service) expireRFP(ctx context.Context, rfp *RFP) {
	mu := s.rfpLock(rfp.ID)
	mu.Lock()
	defer mu.Unlock()

	fresh, err := s.store.GetRFP(ctx, rfp.ID)
	if err != nil || fresh.IsTerminal() {
		return
	}
	s.expireRFPLocked(ctx, fresh)
}

// expireRFPLocked expires an RFP. Must be called under rfpLock.
func (s *Service) expireRFPLocked(ctx context.Context, rfp *RFP) {
	now := time.Now()

	// Reject pending bids
	bids, err := s.store.ListActiveBidsByRFP(ctx, rfp.ID)
	if err == nil {
		for _, b := range bids {
			b.Status = BidStatusRejected
			b.UpdatedAt = now
			_ = s.store.UpdateBid(ctx, b)
		}
	}

	rfp.Status = RFPStatusExpired
	rfp.UpdatedAt = now
	_ = s.store.UpdateRFP(ctx, rfp)
}

// Get returns an RFP by ID.
func (s *Service) Get(ctx context.Context, id string) (*RFP, error) {
	return s.store.GetRFP(ctx, id)
}

// GetBid returns a bid by ID.
func (s *Service) GetBid(ctx context.Context, id string) (*Bid, error) {
	return s.store.GetBid(ctx, id)
}

// ListOpenRFPs returns open RFPs, optionally filtered by service type.
func (s *Service) ListOpenRFPs(ctx context.Context, serviceType string, limit int) ([]*RFP, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListOpenRFPs(ctx, serviceType, limit)
}

// ListByBuyer returns RFPs created by a buyer.
func (s *Service) ListByBuyer(ctx context.Context, buyerAddr string, limit int) ([]*RFP, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByBuyer(ctx, strings.ToLower(buyerAddr), limit)
}

// ListBidsByRFP returns all bids for an RFP.
func (s *Service) ListBidsByRFP(ctx context.Context, rfpID string, limit int) ([]*Bid, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListBidsByRFP(ctx, rfpID, limit)
}

// ListBidsBySeller returns all bids by a seller.
func (s *Service) ListBidsBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Bid, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListBidsBySeller(ctx, strings.ToLower(sellerAddr), limit)
}

// --- helpers ---

// parseDeadline parses a deadline string: either a duration ("24h", "7d") or RFC3339.
func parseDeadline(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty deadline")
	}

	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		if t.Before(time.Now()) {
			return time.Time{}, errors.New("deadline must be in the future")
		}
		return t, nil
	}

	// Try duration ("24h", "7d", "30m")
	d, err := parseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid deadline: %w", err)
	}
	return time.Now().Add(d), nil
}

// parseDuration parses "7d", "24h", "30m" etc.
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return 0, errors.New("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		numStr := s[:len(s)-1]
		var days int
		if _, err := fmt.Sscanf(numStr, "%d", &days); err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		if days <= 0 {
			return 0, fmt.Errorf("duration must be positive: %s", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive: %s", s)
	}
	return d, nil
}

func mergeString(new, old string) string {
	if new != "" {
		return new
	}
	return old
}

func mergeInt(new, old int) int {
	if new != 0 {
		return new
	}
	return old
}

func mergeFloat(new, old float64) float64 {
	if new != 0 {
		return new
	}
	return old
}
