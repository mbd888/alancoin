// Package offers implements a standing offer / order book for agent services.
//
// Agents post standing offers (service type, price, capacity, conditions).
// Buyers claim offers atomically — funds lock in escrow, work begins.
// This transforms the registry from a directory into a two-sided marketplace.
//
// Inspired by Stellar claimable balances and Uniswap passive offers.
package offers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var (
	ErrOfferNotFound   = errors.New("offers: not found")
	ErrOfferExpired    = errors.New("offers: expired")
	ErrOfferExhausted  = errors.New("offers: no remaining capacity")
	ErrOfferCancelled  = errors.New("offers: cancelled")
	ErrInvalidPrice    = errors.New("offers: invalid price")
	ErrSelfClaim       = errors.New("offers: cannot claim own offer")
	ErrConditionNotMet = errors.New("offers: claim conditions not met")
	ErrClaimNotFound   = errors.New("offers: claim not found")
	ErrClaimNotPending = errors.New("offers: claim not in pending state")
	ErrUnauthorized    = errors.New("offers: not authorized")
)

// OfferStatus represents the lifecycle of a standing offer.
type OfferStatus string

const (
	OfferActive    OfferStatus = "active"
	OfferExhausted OfferStatus = "exhausted" // All capacity claimed
	OfferCancelled OfferStatus = "cancelled"
	OfferExpired   OfferStatus = "expired"
)

// ClaimStatus represents the lifecycle of a claim against an offer.
type ClaimStatus string

const (
	ClaimPending   ClaimStatus = "pending"   // Funds locked, awaiting seller delivery
	ClaimDelivered ClaimStatus = "delivered" // Seller marked delivered, awaiting buyer confirmation
	ClaimCompleted ClaimStatus = "completed" // Buyer confirmed, funds released to seller
	ClaimDisputed  ClaimStatus = "disputed"  // Buyer disputes, awaiting resolution
	ClaimRefunded  ClaimStatus = "refunded"  // Funds returned to buyer
)

// Condition defines a requirement for claiming an offer.
type Condition struct {
	Type  string `json:"type"`  // "min_reputation", "allowed_buyers", "min_balance"
	Value string `json:"value"` // Condition-specific value
}

// Offer represents a standing offer from a seller agent.
type Offer struct {
	ID           string      `json:"id"`
	SellerAddr   string      `json:"sellerAddr"`
	ServiceType  string      `json:"serviceType"`
	Description  string      `json:"description,omitempty"`
	Price        string      `json:"price"` // USDC per claim
	Capacity     int         `json:"capacity"`
	RemainingCap int         `json:"remainingCap"`
	Conditions   []Condition `json:"conditions,omitempty"`
	Status       OfferStatus `json:"status"`
	TotalClaims  int         `json:"totalClaims"`
	TotalRevenue string      `json:"totalRevenue"`
	Endpoint     string      `json:"endpoint,omitempty"` // Where to send work
	ExpiresAt    time.Time   `json:"expiresAt"`
	CreatedAt    time.Time   `json:"createdAt"`
	UpdatedAt    time.Time   `json:"updatedAt"`
}

// IsTerminal returns true if the offer can no longer be claimed.
func (o *Offer) IsTerminal() bool {
	switch o.Status {
	case OfferExhausted, OfferCancelled, OfferExpired:
		return true
	}
	return false
}

// Claim represents a buyer claiming a standing offer.
type Claim struct {
	ID         string      `json:"id"`
	OfferID    string      `json:"offerId"`
	BuyerAddr  string      `json:"buyerAddr"`
	SellerAddr string      `json:"sellerAddr"`
	Amount     string      `json:"amount"`
	Status     ClaimStatus `json:"status"`
	EscrowRef  string      `json:"escrowRef"` // Ledger reference
	CreatedAt  time.Time   `json:"createdAt"`
	ResolvedAt *time.Time  `json:"resolvedAt,omitempty"`
}

// CreateOfferRequest is the input for posting a standing offer.
type CreateOfferRequest struct {
	ServiceType string      `json:"serviceType" binding:"required"`
	Description string      `json:"description"`
	Price       string      `json:"price" binding:"required"`
	Capacity    int         `json:"capacity" binding:"required"`
	Conditions  []Condition `json:"conditions,omitempty"`
	Endpoint    string      `json:"endpoint"`
	ExpiresIn   string      `json:"expiresIn"` // Duration, e.g. "24h"
}

// DefaultOfferExpiry is the default time before an offer expires.
const DefaultOfferExpiry = 24 * time.Hour

// MaxCapacity is the maximum capacity for a single offer.
const MaxCapacity = 10000

// LedgerService abstracts ledger operations for offer claims.
type LedgerService interface {
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error
}

// TransactionRecorder records transactions for reputation.
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error
}

// Store persists offer and claim data.
type Store interface {
	// Offers
	CreateOffer(ctx context.Context, o *Offer) error
	GetOffer(ctx context.Context, id string) (*Offer, error)
	UpdateOffer(ctx context.Context, o *Offer) error
	ListOffers(ctx context.Context, serviceType string, limit int) ([]*Offer, error)
	ListOffersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Offer, error)
	ListExpiredOffers(ctx context.Context, before time.Time, limit int) ([]*Offer, error)

	// Claims
	CreateClaim(ctx context.Context, c *Claim) error
	GetClaim(ctx context.Context, id string) (*Claim, error)
	UpdateClaim(ctx context.Context, c *Claim) error
	ListClaimsByOffer(ctx context.Context, offerID string, limit int) ([]*Claim, error)
	ListClaimsByBuyer(ctx context.Context, buyerAddr string, limit int) ([]*Claim, error)
}

// RevenueAccumulator intercepts payments for revenue staking.
type RevenueAccumulator interface {
	AccumulateRevenue(ctx context.Context, agentAddr, amount, txRef string) error
}

// Service implements standing offer business logic.
type Service struct {
	store    Store
	ledger   LedgerService
	recorder TransactionRecorder
	revenue  RevenueAccumulator
	logger   *slog.Logger
	locks    sync.Map
}

// NewService creates a new offers service.
func NewService(store Store, ledger LedgerService) *Service {
	return &Service{
		store:  store,
		ledger: ledger,
		logger: slog.Default(),
	}
}

// WithLogger sets a structured logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

// WithRecorder adds a transaction recorder.
func (s *Service) WithRecorder(r TransactionRecorder) *Service {
	s.recorder = r
	return s
}

// WithRevenueAccumulator adds a revenue accumulator for staking.
func (s *Service) WithRevenueAccumulator(r RevenueAccumulator) *Service {
	s.revenue = r
	return s
}

func (s *Service) offerLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Service) cleanupLock(id string) {
	s.locks.Delete(id)
}

// PostOffer creates a standing offer visible in the marketplace.
func (s *Service) PostOffer(ctx context.Context, sellerAddr string, req CreateOfferRequest) (*Offer, error) {
	ctx, span := traces.StartSpan(ctx, "offers.PostOffer",
		attribute.String("seller", sellerAddr),
		attribute.String("service_type", req.ServiceType),
		attribute.String("price", req.Price),
	)
	defer span.End()

	if err := validatePrice(req.Price); err != nil {
		return nil, err
	}
	if req.Capacity <= 0 || req.Capacity > MaxCapacity {
		return nil, fmt.Errorf("capacity must be between 1 and %d", MaxCapacity)
	}
	if req.ServiceType == "" {
		return nil, errors.New("serviceType is required")
	}

	expiry := DefaultOfferExpiry
	if req.ExpiresIn != "" {
		d, err := time.ParseDuration(req.ExpiresIn)
		if err == nil && d > 0 {
			expiry = d
		}
	}

	now := time.Now()
	offer := &Offer{
		ID:           idgen.WithPrefix("ofr_"),
		SellerAddr:   strings.ToLower(sellerAddr),
		ServiceType:  req.ServiceType,
		Description:  req.Description,
		Price:        req.Price,
		Capacity:     req.Capacity,
		RemainingCap: req.Capacity,
		Conditions:   req.Conditions,
		Status:       OfferActive,
		TotalRevenue: "0.000000",
		Endpoint:     req.Endpoint,
		ExpiresAt:    now.Add(expiry),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.CreateOffer(ctx, offer); err != nil {
		span.RecordError(err)
		return nil, err
	}

	return offer, nil
}

// ClaimOffer atomically claims a standing offer: locks buyer funds, decrements
// capacity, creates a pending claim.
func (s *Service) ClaimOffer(ctx context.Context, offerID, buyerAddr string) (*Claim, error) {
	ctx, span := traces.StartSpan(ctx, "offers.ClaimOffer",
		attribute.String("offer_id", offerID),
		attribute.String("buyer", buyerAddr),
	)
	defer span.End()

	mu := s.offerLock(offerID)
	mu.Lock()
	defer mu.Unlock()

	offer, err := s.store.GetOffer(ctx, offerID)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	buyer := strings.ToLower(buyerAddr)

	// Validate offer state
	if offer.Status != OfferActive {
		switch offer.Status {
		case OfferExpired:
			return nil, ErrOfferExpired
		case OfferCancelled:
			return nil, ErrOfferCancelled
		case OfferExhausted:
			return nil, ErrOfferExhausted
		}
		return nil, fmt.Errorf("offer status: %s", offer.Status)
	}

	if time.Now().After(offer.ExpiresAt) {
		offer.Status = OfferExpired
		offer.UpdatedAt = time.Now()
		if err := s.store.UpdateOffer(ctx, offer); err != nil {
			s.logger.Warn("failed to mark offer as expired during claim",
				"offer_id", offer.ID, "error", err)
		}
		return nil, ErrOfferExpired
	}

	if offer.RemainingCap <= 0 {
		return nil, ErrOfferExhausted
	}

	if buyer == offer.SellerAddr {
		return nil, ErrSelfClaim
	}

	// Check conditions
	if err := s.checkConditions(offer.Conditions, buyer); err != nil {
		return nil, err
	}

	// Lock buyer funds
	claimID := idgen.WithPrefix("clm_")
	claim := &Claim{
		ID:         claimID,
		OfferID:    offer.ID,
		BuyerAddr:  buyer,
		SellerAddr: offer.SellerAddr,
		Amount:     offer.Price,
		Status:     ClaimPending,
		EscrowRef:  "offer:" + offer.ID + ":claim:" + claimID,
		CreatedAt:  time.Now(),
	}

	if err := s.ledger.EscrowLock(ctx, buyer, offer.Price, claim.EscrowRef); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to lock funds")
		return nil, fmt.Errorf("failed to lock funds for claim: %w", err)
	}

	// Decrement capacity
	offer.RemainingCap--
	offer.TotalClaims++
	offer.UpdatedAt = time.Now()
	if offer.RemainingCap == 0 {
		offer.Status = OfferExhausted
	}

	if err := s.store.UpdateOffer(ctx, offer); err != nil {
		// Refund on failure
		_ = s.ledger.RefundEscrow(ctx, buyer, offer.Price, claim.EscrowRef)
		span.RecordError(err)
		return nil, fmt.Errorf("failed to update offer: %w", err)
	}

	if err := s.store.CreateClaim(ctx, claim); err != nil {
		// Refund on failure, restore capacity
		_ = s.ledger.RefundEscrow(ctx, buyer, offer.Price, claim.EscrowRef)
		offer.RemainingCap++
		offer.TotalClaims--
		if offer.Status == OfferExhausted {
			offer.Status = OfferActive
		}
		if rbErr := s.store.UpdateOffer(ctx, offer); rbErr != nil {
			s.logger.Error("CRITICAL: claim rollback failed — offer capacity may be incorrect",
				"offer_id", offer.ID, "error", rbErr)
		}
		span.RecordError(err)
		return nil, fmt.Errorf("failed to create claim: %w", err)
	}

	if offer.RemainingCap == 0 {
		s.cleanupLock(offerID)
	}

	return claim, nil
}

// DeliverClaim marks a claim as delivered by the seller, signaling work is done.
// The buyer must then confirm (CompleteClaim) or dispute (RefundClaim).
func (s *Service) DeliverClaim(ctx context.Context, claimID, callerAddr string) (*Claim, error) {
	ctx, span := traces.StartSpan(ctx, "offers.DeliverClaim",
		attribute.String("claim_id", claimID),
	)
	defer span.End()

	claim, err := s.store.GetClaim(ctx, claimID)
	if err != nil {
		return nil, err
	}

	if claim.Status != ClaimPending {
		return nil, ErrClaimNotPending
	}

	caller := strings.ToLower(callerAddr)
	if caller != claim.SellerAddr {
		return nil, ErrUnauthorized
	}

	claim.Status = ClaimDelivered

	if err := s.store.UpdateClaim(ctx, claim); err != nil {
		return nil, err
	}

	return claim, nil
}

// CompleteClaim releases funds to the seller, marking the claim as completed.
// Can be called from pending (buyer skips delivery check) or delivered state.
func (s *Service) CompleteClaim(ctx context.Context, claimID, callerAddr string) (*Claim, error) {
	ctx, span := traces.StartSpan(ctx, "offers.CompleteClaim",
		attribute.String("claim_id", claimID),
	)
	defer span.End()

	claim, err := s.store.GetClaim(ctx, claimID)
	if err != nil {
		return nil, err
	}

	if claim.Status != ClaimPending && claim.Status != ClaimDelivered {
		return nil, ErrClaimNotPending
	}

	caller := strings.ToLower(callerAddr)
	if caller != claim.BuyerAddr {
		return nil, ErrUnauthorized
	}

	// Release funds to seller
	if err := s.ledger.ReleaseEscrow(ctx, claim.BuyerAddr, claim.SellerAddr, claim.Amount, claim.EscrowRef); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to release claim funds: %w", err)
	}

	now := time.Now()
	claim.Status = ClaimCompleted
	claim.ResolvedAt = &now

	if err := s.store.UpdateClaim(ctx, claim); err != nil {
		s.logger.Error("CRITICAL: claim funds released but status update failed",
			"claim_id", claim.ID, "amount", claim.Amount)
		return nil, err
	}

	// Update offer revenue (best-effort — funds are already released)
	offer, err := s.store.GetOffer(ctx, claim.OfferID)
	if err == nil {
		revBig, _ := usdc.Parse(offer.TotalRevenue)
		amtBig, _ := usdc.Parse(claim.Amount)
		offer.TotalRevenue = usdc.Format(new(big.Int).Add(revBig, amtBig))
		offer.UpdatedAt = time.Now()
		if revErr := s.store.UpdateOffer(ctx, offer); revErr != nil {
			s.logger.Warn("failed to update offer revenue after claim completion",
				"offer_id", offer.ID, "claim_id", claim.ID, "error", revErr)
		}
	}

	// Record for reputation
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, claim.ID, claim.BuyerAddr, claim.SellerAddr, claim.Amount, "", "confirmed")
	}

	// Revenue accumulation for flywheel
	if s.revenue != nil {
		_ = s.revenue.AccumulateRevenue(ctx, claim.SellerAddr, claim.Amount, "offer_claim:"+claim.ID)
	}

	return claim, nil
}

// RefundClaim refunds the buyer and marks the claim as refunded.
func (s *Service) RefundClaim(ctx context.Context, claimID, callerAddr string) (*Claim, error) {
	ctx, span := traces.StartSpan(ctx, "offers.RefundClaim",
		attribute.String("claim_id", claimID),
	)
	defer span.End()

	claim, err := s.store.GetClaim(ctx, claimID)
	if err != nil {
		return nil, err
	}

	if claim.Status != ClaimPending && claim.Status != ClaimDelivered && claim.Status != ClaimDisputed {
		return nil, ErrClaimNotPending
	}

	caller := strings.ToLower(callerAddr)
	if caller != claim.BuyerAddr && caller != claim.SellerAddr {
		return nil, ErrUnauthorized
	}

	if err := s.ledger.RefundEscrow(ctx, claim.BuyerAddr, claim.Amount, claim.EscrowRef); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to refund claim: %w", err)
	}

	now := time.Now()
	claim.Status = ClaimRefunded
	claim.ResolvedAt = &now

	if err := s.store.UpdateClaim(ctx, claim); err != nil {
		s.logger.Error("CRITICAL: claim refunded but status update failed",
			"claim_id", claim.ID, "amount", claim.Amount)
		return nil, err
	}

	return claim, nil
}

// CancelOffer cancels a standing offer. Only the seller can cancel.
func (s *Service) CancelOffer(ctx context.Context, offerID, callerAddr string) (*Offer, error) {
	mu := s.offerLock(offerID)
	mu.Lock()
	defer mu.Unlock()

	offer, err := s.store.GetOffer(ctx, offerID)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != offer.SellerAddr {
		return nil, ErrUnauthorized
	}

	if offer.IsTerminal() {
		return nil, fmt.Errorf("offer already %s", offer.Status)
	}

	offer.Status = OfferCancelled
	offer.UpdatedAt = time.Now()

	if err := s.store.UpdateOffer(ctx, offer); err != nil {
		return nil, err
	}

	s.cleanupLock(offerID)
	return offer, nil
}

// GetOffer returns an offer by ID.
func (s *Service) GetOffer(ctx context.Context, id string) (*Offer, error) {
	return s.store.GetOffer(ctx, id)
}

// GetClaim returns a claim by ID.
func (s *Service) GetClaim(ctx context.Context, id string) (*Claim, error) {
	return s.store.GetClaim(ctx, id)
}

// ListOffers returns active offers for a service type.
func (s *Service) ListOffers(ctx context.Context, serviceType string, limit int) ([]*Offer, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListOffers(ctx, serviceType, limit)
}

// ListOffersBySeller returns offers posted by a seller.
func (s *Service) ListOffersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Offer, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListOffersBySeller(ctx, strings.ToLower(sellerAddr), limit)
}

// ListClaimsByOffer returns claims for an offer.
func (s *Service) ListClaimsByOffer(ctx context.Context, offerID string, limit int) ([]*Claim, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListClaimsByOffer(ctx, offerID, limit)
}

// ForceExpireOffers expires all offers past their expiry time.
func (s *Service) ForceExpireOffers(ctx context.Context) (int, error) {
	expired, err := s.store.ListExpiredOffers(ctx, time.Now(), 100)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, o := range expired {
		if o.IsTerminal() {
			continue
		}
		o.Status = OfferExpired
		o.UpdatedAt = time.Now()
		if err := s.store.UpdateOffer(ctx, o); err != nil {
			s.logger.Warn("failed to expire offer", "offer_id", o.ID, "error", err)
			continue
		}
		count++
	}
	return count, nil
}

// --- Helpers ---

func (s *Service) checkConditions(conditions []Condition, buyerAddr string) error {
	for _, c := range conditions {
		switch c.Type {
		case "allowed_buyers":
			// c.Value is comma-separated list of allowed addresses
			allowed := strings.Split(strings.ToLower(c.Value), ",")
			found := false
			for _, a := range allowed {
				if strings.TrimSpace(a) == buyerAddr {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: buyer not in allowed list", ErrConditionNotMet)
			}
		case "min_reputation":
			// Reputation check would integrate with reputation service.
			// For now, this is a declaration recorded in the offer.
		case "min_balance":
			// Balance check would integrate with ledger service.
		}
	}
	return nil
}

func validatePrice(price string) error {
	price = strings.TrimSpace(price)
	if price == "" {
		return fmt.Errorf("%w: empty price", ErrInvalidPrice)
	}
	parsed, ok := usdc.Parse(price)
	if !ok {
		return fmt.Errorf("%w: %q is not a valid number", ErrInvalidPrice, price)
	}
	if parsed.Sign() <= 0 {
		return fmt.Errorf("%w: price must be positive", ErrInvalidPrice)
	}
	return nil
}
