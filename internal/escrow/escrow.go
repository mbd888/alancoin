// Package escrow provides buyer-protection for service payments.
//
// Flow:
//  1. Buyer calls service → funds moved: available → escrowed
//  2. Service delivers result → seller marks delivered
//  3. Buyer confirms → funds moved: buyer's escrowed → seller's available
//  4. Buyer disputes → funds moved: buyer's escrowed → buyer's available
//  5. Timeout → auto-released to seller
package escrow

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

var (
	ErrEscrowNotFound  = errors.New("escrow not found")
	ErrInvalidStatus   = errors.New("invalid escrow status for this operation")
	ErrUnauthorized    = errors.New("not authorized for this escrow operation")
	ErrInvalidAmount   = errors.New("invalid amount")
	ErrAlreadyResolved = errors.New("escrow already resolved")
)

// Status represents the state of an escrow.
type Status string

const (
	StatusPending   Status = "pending"   // Created, funds locked
	StatusDelivered Status = "delivered" // Seller marked service as delivered
	StatusReleased  Status = "released"  // Buyer confirmed, funds sent to seller
	StatusDisputed  Status = "disputed"  // Buyer disputed, funds returned
	StatusRefunded  Status = "refunded"  // Dispute resolved with refund
	StatusExpired   Status = "expired"   // Auto-released after timeout
)

// DefaultAutoRelease is the default time before auto-releasing to seller.
const DefaultAutoRelease = 5 * time.Minute

// Escrow represents a buyer-protection escrow record.
type Escrow struct {
	ID            string     `json:"id"`
	BuyerAddr     string     `json:"buyerAddr"`
	SellerAddr    string     `json:"sellerAddr"`
	Amount        string     `json:"amount"`
	ServiceID     string     `json:"serviceId,omitempty"`
	SessionKeyID  string     `json:"sessionKeyId,omitempty"`
	Status        Status     `json:"status"`
	AutoReleaseAt time.Time  `json:"autoReleaseAt"`
	DeliveredAt   *time.Time `json:"deliveredAt,omitempty"`
	ResolvedAt    *time.Time `json:"resolvedAt,omitempty"`
	DisputeReason string     `json:"disputeReason,omitempty"`
	Resolution    string     `json:"resolution,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// IsTerminal returns true if the escrow is in a final state.
func (e *Escrow) IsTerminal() bool {
	switch e.Status {
	case StatusReleased, StatusRefunded, StatusExpired:
		return true
	}
	return false
}

// Store persists escrow data.
type Store interface {
	Create(ctx context.Context, escrow *Escrow) error
	Get(ctx context.Context, id string) (*Escrow, error)
	Update(ctx context.Context, escrow *Escrow) error
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Escrow, error)
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*Escrow, error)
}

// LedgerService abstracts ledger operations so escrow doesn't import ledger.
type LedgerService interface {
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error
}

// TransactionRecorder records transactions for reputation tracking.
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error
}

// RevenueAccumulator intercepts payments for revenue staking.
type RevenueAccumulator interface {
	AccumulateRevenue(ctx context.Context, agentAddr, amount string) error
}

// CreateRequest contains the parameters for creating an escrow.
type CreateRequest struct {
	BuyerAddr    string `json:"buyerAddr" binding:"required"`
	SellerAddr   string `json:"sellerAddr" binding:"required"`
	Amount       string `json:"amount" binding:"required"`
	ServiceID    string `json:"serviceId"`
	SessionKeyID string `json:"sessionKeyId"`
	AutoRelease  string `json:"autoRelease"` // Duration string, e.g. "5m", "1h"
}

// DisputeRequest contains the parameters for disputing an escrow.
type DisputeRequest struct {
	Reason string `json:"reason" binding:"required"`
}

// Service implements escrow business logic.
type Service struct {
	store    Store
	ledger   LedgerService
	recorder TransactionRecorder
	revenue  RevenueAccumulator
	locks    sync.Map // per-escrow ID locks to prevent race conditions
}

// escrowLock returns a mutex for the given escrow ID.
// This prevents concurrent state transitions (e.g. confirm + auto-release racing).
func (s *Service) escrowLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// NewService creates a new escrow service.
func NewService(store Store, ledger LedgerService) *Service {
	return &Service{
		store:  store,
		ledger: ledger,
	}
}

// WithRecorder adds a transaction recorder for reputation integration.
func (s *Service) WithRecorder(r TransactionRecorder) *Service {
	s.recorder = r
	return s
}

// WithRevenueAccumulator adds a revenue accumulator for stakes interception.
func (s *Service) WithRevenueAccumulator(r RevenueAccumulator) *Service {
	s.revenue = r
	return s
}

// Create creates a new escrow and locks buyer funds.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Escrow, error) {
	if strings.EqualFold(req.BuyerAddr, req.SellerAddr) {
		return nil, errors.New("buyer and seller cannot be the same address")
	}

	autoRelease := DefaultAutoRelease
	if req.AutoRelease != "" {
		d, err := time.ParseDuration(req.AutoRelease)
		if err == nil && d > 0 {
			autoRelease = d
		}
	}

	now := time.Now()
	escrow := &Escrow{
		ID:            generateEscrowID(),
		BuyerAddr:     strings.ToLower(req.BuyerAddr),
		SellerAddr:    strings.ToLower(req.SellerAddr),
		Amount:        req.Amount,
		ServiceID:     req.ServiceID,
		SessionKeyID:  req.SessionKeyID,
		Status:        StatusPending,
		AutoReleaseAt: now.Add(autoRelease),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Lock buyer funds in escrow
	if err := s.ledger.EscrowLock(ctx, escrow.BuyerAddr, escrow.Amount, escrow.ID); err != nil {
		return nil, fmt.Errorf("failed to lock escrow funds: %w", err)
	}

	if err := s.store.Create(ctx, escrow); err != nil {
		// Best-effort refund if store fails
		_ = s.ledger.RefundEscrow(ctx, escrow.BuyerAddr, escrow.Amount, escrow.ID)
		return nil, fmt.Errorf("failed to create escrow record: %w", err)
	}

	return escrow, nil
}

// MarkDelivered marks the escrow as delivered by the seller.
func (s *Service) MarkDelivered(ctx context.Context, id, callerAddr string) (*Escrow, error) {
	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != escrow.SellerAddr {
		return nil, ErrUnauthorized
	}

	if escrow.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if escrow.Status != StatusPending {
		return nil, ErrInvalidStatus
	}

	now := time.Now()
	escrow.Status = StatusDelivered
	escrow.DeliveredAt = &now
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		return nil, err
	}

	return escrow, nil
}

// Confirm releases escrowed funds to the seller.
func (s *Service) Confirm(ctx context.Context, id, callerAddr string) (*Escrow, error) {
	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != escrow.BuyerAddr {
		return nil, ErrUnauthorized
	}

	if escrow.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if escrow.Status != StatusPending && escrow.Status != StatusDelivered {
		return nil, ErrInvalidStatus
	}

	// Release funds to seller
	if err := s.ledger.ReleaseEscrow(ctx, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ID); err != nil {
		return nil, fmt.Errorf("failed to release escrow funds: %w", err)
	}

	now := time.Now()
	escrow.Status = StatusReleased
	escrow.ResolvedAt = &now
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		// Retry once — funds already moved, we must persist the state change
		if retryErr := s.store.Update(ctx, escrow); retryErr != nil {
			// CRITICAL: Funds were released to seller but escrow record is stale.
			// Cannot safely reverse ReleaseEscrow (no inverse operation).
			// Log for manual resolution rather than applying wrong compensation.
			log.Printf("CRITICAL: escrow %s funds released to %s but status update failed: %v",
				escrow.ID, escrow.SellerAddr, retryErr)
			return nil, fmt.Errorf("failed to update escrow after fund release (requires manual resolution): %w", err)
		}
	}

	// Record confirmed transaction for reputation
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, escrow.ID, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "confirmed")
	}

	// Intercept revenue for stakes (seller earned money)
	if s.revenue != nil {
		_ = s.revenue.AccumulateRevenue(ctx, escrow.SellerAddr, escrow.Amount)
	}

	return escrow, nil
}

// Dispute refunds escrowed funds to the buyer.
func (s *Service) Dispute(ctx context.Context, id, callerAddr, reason string) (*Escrow, error) {
	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != escrow.BuyerAddr {
		return nil, ErrUnauthorized
	}

	if escrow.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if escrow.Status != StatusPending && escrow.Status != StatusDelivered {
		return nil, ErrInvalidStatus
	}

	// Refund buyer
	if err := s.ledger.RefundEscrow(ctx, escrow.BuyerAddr, escrow.Amount, escrow.ID); err != nil {
		return nil, fmt.Errorf("failed to refund escrow: %w", err)
	}

	now := time.Now()
	escrow.Status = StatusRefunded
	escrow.DisputeReason = reason
	escrow.Resolution = "auto_refund"
	escrow.ResolvedAt = &now
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		// Compensate: re-lock the refunded funds
		_ = s.ledger.EscrowLock(ctx, escrow.BuyerAddr, escrow.Amount, escrow.ID)
		return nil, fmt.Errorf("failed to update escrow after refund: %w", err)
	}

	// Record failed transaction for reputation
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, escrow.ID, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "failed")
	}

	return escrow, nil
}

// AutoRelease releases expired escrows to the seller.
func (s *Service) AutoRelease(ctx context.Context, escrow *Escrow) error {
	mu := s.escrowLock(escrow.ID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read from store under lock to prevent stale-state races
	fresh, err := s.store.Get(ctx, escrow.ID)
	if err != nil {
		return err
	}
	escrow = fresh

	if escrow.IsTerminal() {
		return ErrAlreadyResolved
	}

	// Release funds to seller
	if err := s.ledger.ReleaseEscrow(ctx, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ID); err != nil {
		return fmt.Errorf("failed to auto-release escrow: %w", err)
	}

	now := time.Now()
	escrow.Status = StatusExpired
	escrow.Resolution = "auto_released"
	escrow.ResolvedAt = &now
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		// Retry once — funds already moved, we must persist the state change
		if retryErr := s.store.Update(ctx, escrow); retryErr != nil {
			// CRITICAL: Funds were auto-released to seller but escrow record is stale.
			// Cannot safely reverse ReleaseEscrow (no inverse operation).
			// Log for manual resolution rather than applying wrong compensation.
			log.Printf("CRITICAL: escrow %s auto-released to %s but status update failed: %v",
				escrow.ID, escrow.SellerAddr, retryErr)
			return fmt.Errorf("failed to update escrow after auto-release (requires manual resolution): %w", err)
		}
	}

	// Record confirmed transaction for reputation (auto-release counts as success)
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, escrow.ID, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "confirmed")
	}

	// Intercept revenue for stakes (seller earned money)
	if s.revenue != nil {
		_ = s.revenue.AccumulateRevenue(ctx, escrow.SellerAddr, escrow.Amount)
	}

	return nil
}

// Get returns an escrow by ID.
func (s *Service) Get(ctx context.Context, id string) (*Escrow, error) {
	return s.store.Get(ctx, id)
}

// ListByAgent returns escrows involving an agent (as buyer or seller).
func (s *Service) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Escrow, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByAgent(ctx, strings.ToLower(agentAddr), limit)
}

func generateEscrowID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("esc_%x", b)
}
