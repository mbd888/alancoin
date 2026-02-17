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
	"log/slog"
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
	StatusPending     Status = "pending"     // Created, funds locked
	StatusDelivered   Status = "delivered"   // Seller marked service as delivered
	StatusReleased    Status = "released"    // Buyer confirmed, funds sent to seller
	StatusRefunded    Status = "refunded"    // Dispute resolved with refund
	StatusExpired     Status = "expired"     // Auto-released after timeout
	StatusDisputed    Status = "disputed"    // Buyer raised dispute, awaiting arbitration
	StatusArbitrating Status = "arbitrating" // Arbitrator assigned, evidence being reviewed
)

// MoneyError wraps an error with funds-state context so callers know
// whether their money is safe and what to do next.
type MoneyError struct {
	Err         error
	FundsStatus string // no_change, locked_in_escrow, released_to_seller
	Recovery    string // human-readable next step
	Amount      string // amount involved
	Reference   string // escrow ID for support
}

func (e *MoneyError) Error() string { return e.Err.Error() }
func (e *MoneyError) Unwrap() error { return e.Err }

// DefaultAutoRelease is the default time before auto-releasing to seller.
const DefaultAutoRelease = 5 * time.Minute

// DefaultDisputeWindow is the time after delivery during which buyer can dispute.
const DefaultDisputeWindow = 24 * time.Hour

// DefaultArbitrationDeadline is the time given for arbitration after assignment.
const DefaultArbitrationDeadline = 72 * time.Hour

// EvidenceEntry represents a piece of evidence submitted during a dispute.
type EvidenceEntry struct {
	SubmittedBy string    `json:"submittedBy"`
	Content     string    `json:"content"`
	SubmittedAt time.Time `json:"submittedAt"`
}

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

	// Arbitration fields
	DisputeEvidence      []EvidenceEntry `json:"disputeEvidence,omitempty"`
	ArbitratorAddr       string          `json:"arbitratorAddr,omitempty"`
	ArbitrationDeadline  *time.Time      `json:"arbitrationDeadline,omitempty"`
	PartialReleaseAmount string          `json:"partialReleaseAmount,omitempty"`
	PartialRefundAmount  string          `json:"partialRefundAmount,omitempty"`
	DisputeWindowUntil   *time.Time      `json:"disputeWindowUntil,omitempty"`
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
	ListByStatus(ctx context.Context, status Status, limit int) ([]*Escrow, error)
}

// LedgerService abstracts ledger operations so escrow doesn't import ledger.
type LedgerService interface {
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error
	PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error
}

// TransactionRecorder records transactions for reputation tracking.
type TransactionRecorder interface {
	RecordTransaction(ctx context.Context, txHash, from, to, amount, serviceID, status string) error
}

// RevenueAccumulator intercepts payments for revenue staking.
type RevenueAccumulator interface {
	AccumulateRevenue(ctx context.Context, agentAddr, amount, txRef string) error
}

// ReputationImpactor records dispute/confirm outcomes for reputation scoring.
type ReputationImpactor interface {
	RecordDispute(ctx context.Context, sellerAddr string, outcome string, amount string) error
}

// ReceiptIssuer issues cryptographic receipts for payments.
type ReceiptIssuer interface {
	IssueReceipt(ctx context.Context, path, reference, from, to, amount, serviceID, status, metadata string) error
}

// WebhookEmitter emits lifecycle events to webhook subscribers.
type WebhookEmitter interface {
	EmitEscrowCreated(buyerAddr, escrowID, sellerAddr, amount string)
	EmitEscrowDelivered(buyerAddr, escrowID, sellerAddr string)
	EmitEscrowReleased(sellerAddr, escrowID, buyerAddr, amount string)
	EmitEscrowRefunded(buyerAddr, escrowID, amount string)
	EmitEscrowDisputed(sellerAddr, escrowID, buyerAddr, reason string)
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

// EvidenceRequest contains the parameters for submitting evidence.
type EvidenceRequest struct {
	Content string `json:"content" binding:"required"`
}

// ArbitrateRequest assigns an arbitrator to a disputed escrow.
type ArbitrateRequest struct {
	ArbitratorAddr string `json:"arbitratorAddr" binding:"required"`
}

// ResolveRequest contains the arbitrator's resolution.
type ResolveRequest struct {
	// Resolution type: "release" (to seller), "refund" (to buyer), or "partial"
	Resolution string `json:"resolution" binding:"required"`
	// For partial resolutions: how much goes to seller (rest refunded to buyer)
	ReleaseAmount string `json:"releaseAmount,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

// Service implements escrow business logic.
type Service struct {
	store          Store
	ledger         LedgerService
	recorder       TransactionRecorder
	revenue        RevenueAccumulator
	reputation     ReputationImpactor
	receiptIssuer  ReceiptIssuer
	webhookEmitter WebhookEmitter
	logger         *slog.Logger
	locks          sync.Map // per-escrow ID locks to prevent race conditions
}

// escrowLock returns a mutex for the given escrow ID.
// This prevents concurrent state transitions (e.g. confirm + auto-release racing).
func (s *Service) escrowLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// cleanupLock removes the per-escrow mutex after a terminal state is reached.
func (s *Service) cleanupLock(id string) {
	s.locks.Delete(id)
}

// NewService creates a new escrow service.
func NewService(store Store, ledger LedgerService) *Service {
	return &Service{
		store:  store,
		ledger: ledger,
		logger: slog.Default(),
	}
}

// WithLogger sets a structured logger for the service.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
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

// WithReputationImpactor adds a reputation impactor for dispute/confirm outcomes.
func (s *Service) WithReputationImpactor(r ReputationImpactor) *Service {
	s.reputation = r
	return s
}

// WithReceiptIssuer adds a receipt issuer for cryptographic payment proofs.
func (s *Service) WithReceiptIssuer(r ReceiptIssuer) *Service {
	s.receiptIssuer = r
	return s
}

// WithWebhookEmitter adds a webhook emitter for lifecycle event notifications.
func (s *Service) WithWebhookEmitter(e WebhookEmitter) *Service {
	s.webhookEmitter = e
	return s
}

// validateAmount checks that the amount string is a positive number within NUMERIC(20,6) range.
func validateAmount(amount string) error {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return fmt.Errorf("%w: empty amount", ErrInvalidAmount)
	}
	parsed, ok := usdc.Parse(amount)
	if !ok {
		return fmt.Errorf("%w: %q is not a valid number", ErrInvalidAmount, amount)
	}
	if parsed.Sign() <= 0 {
		return fmt.Errorf("%w: amount must be positive", ErrInvalidAmount)
	}
	maxAmount, _ := new(big.Int).SetString("99999999999999999999", 10)
	if parsed.Cmp(maxAmount) > 0 {
		return fmt.Errorf("%w: amount exceeds maximum", ErrInvalidAmount)
	}
	return nil
}

// Create creates a new escrow and locks buyer funds.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*Escrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.Create",
		attribute.String("buyer", req.BuyerAddr),
		attribute.String("seller", req.SellerAddr),
		attribute.String("amount", req.Amount),
	)
	defer span.End()

	if strings.EqualFold(req.BuyerAddr, req.SellerAddr) {
		err := errors.New("buyer and seller cannot be the same address")
		span.RecordError(err)
		span.SetStatus(codes.Error, "buyer and seller cannot be the same address")
		return nil, err
	}

	if err := validateAmount(req.Amount); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid amount")
		return nil, err
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
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to lock escrow funds")
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to lock escrow funds: %w", err),
			FundsStatus: "no_change",
			Recovery:    "No funds were moved. Check your available balance and try again.",
			Amount:      escrow.Amount,
			Reference:   escrow.ID,
		}
	}

	if err := s.store.Create(ctx, escrow); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create escrow record")
		if refundErr := s.ledger.RefundEscrow(ctx, escrow.BuyerAddr, escrow.Amount, escrow.ID); refundErr != nil {
			s.logger.Error("RefundEscrow failed after store error: funds stuck in escrow",
				"escrow_id", escrow.ID, "amount", escrow.Amount, "error", refundErr)
			return nil, &MoneyError{
				Err:         fmt.Errorf("failed to create escrow record: %w", err),
				FundsStatus: "locked_in_escrow",
				Recovery:    "Escrow creation failed and refund also failed. Contact support with the reference to release your funds.",
				Amount:      escrow.Amount,
				Reference:   escrow.ID,
			}
		}
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to create escrow record: %w", err),
			FundsStatus: "no_change",
			Recovery:    "Escrow creation failed but your funds were returned. Safe to retry.",
			Amount:      escrow.Amount,
			Reference:   escrow.ID,
		}
	}

	metrics.EscrowCreatedTotal.Inc()

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowCreated(escrow.BuyerAddr, escrow.ID, escrow.SellerAddr, escrow.Amount)
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
	disputeWindow := now.Add(DefaultDisputeWindow)
	escrow.Status = StatusDelivered
	escrow.DeliveredAt = &now
	escrow.DisputeWindowUntil = &disputeWindow
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		return nil, err
	}

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowDelivered(escrow.BuyerAddr, escrow.ID, escrow.SellerAddr)
	}

	return escrow, nil
}

// Confirm releases escrowed funds to the seller.
func (s *Service) Confirm(ctx context.Context, id, callerAddr string) (*Escrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.Confirm",
		attribute.String("escrow.id", id),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get escrow")
		return nil, err
	}

	if strings.ToLower(callerAddr) != escrow.BuyerAddr {
		span.RecordError(ErrUnauthorized)
		span.SetStatus(codes.Error, "unauthorized")
		return nil, ErrUnauthorized
	}

	if escrow.IsTerminal() {
		span.RecordError(ErrAlreadyResolved)
		span.SetStatus(codes.Error, "already resolved")
		return nil, ErrAlreadyResolved
	}

	if escrow.Status != StatusPending && escrow.Status != StatusDelivered {
		span.RecordError(ErrInvalidStatus)
		span.SetStatus(codes.Error, "invalid status")
		return nil, ErrInvalidStatus
	}

	// Release funds to seller
	if err := s.ledger.ReleaseEscrow(ctx, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to release escrow funds")
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to release escrow funds: %w", err),
			FundsStatus: "locked_in_escrow",
			Recovery:    "Funds are still locked in escrow. The release to the seller failed. Contact support with the reference.",
			Amount:      escrow.Amount,
			Reference:   escrow.ID,
		}
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
			s.logger.Error("CRITICAL: escrow funds released but status update failed",
				"escrow_id", escrow.ID, "seller", escrow.SellerAddr, "amount", escrow.Amount, "error", retryErr)
			span.RecordError(retryErr)
			span.SetStatus(codes.Error, "failed to update escrow after fund release")
			return nil, &MoneyError{
				Err:         fmt.Errorf("failed to update escrow after fund release (requires manual resolution): %w", retryErr),
				FundsStatus: "released_to_seller",
				Recovery:    "Funds were successfully released to the seller but the escrow record could not be updated. No action needed regarding your funds.",
				Amount:      escrow.Amount,
				Reference:   escrow.ID,
			}
		}
	}

	// Record confirmed transaction for reputation
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, escrow.ID, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "confirmed")
	}

	// Intercept revenue for stakes (seller earned money)
	if s.revenue != nil {
		_ = s.revenue.AccumulateRevenue(ctx, escrow.SellerAddr, escrow.Amount, "escrow_confirm:"+escrow.ID)
	}

	// Issue receipt for confirmed escrow release
	if s.receiptIssuer != nil {
		_ = s.receiptIssuer.IssueReceipt(ctx, "escrow", escrow.ID, escrow.BuyerAddr,
			escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "confirmed", "buyer_confirmed")
	}

	metrics.EscrowConfirmedTotal.Inc()
	metrics.EscrowDuration.Observe(time.Since(escrow.CreatedAt).Seconds())

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowReleased(escrow.SellerAddr, escrow.ID, escrow.BuyerAddr, escrow.Amount)
	}

	s.cleanupLock(id)
	return escrow, nil
}

// Dispute marks an escrow as disputed, initiating the arbitration process.
// Funds remain locked until arbitration resolves the dispute.
func (s *Service) Dispute(ctx context.Context, id, callerAddr, reason string) (*Escrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.Dispute",
		attribute.String("escrow.id", id),
		attribute.String("reason", reason),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get escrow")
		return nil, err
	}

	if strings.ToLower(callerAddr) != escrow.BuyerAddr {
		span.RecordError(ErrUnauthorized)
		span.SetStatus(codes.Error, "unauthorized")
		return nil, ErrUnauthorized
	}

	if escrow.IsTerminal() {
		span.RecordError(ErrAlreadyResolved)
		span.SetStatus(codes.Error, "already resolved")
		return nil, ErrAlreadyResolved
	}

	if escrow.Status != StatusPending && escrow.Status != StatusDelivered {
		span.RecordError(ErrInvalidStatus)
		span.SetStatus(codes.Error, "invalid status")
		return nil, ErrInvalidStatus
	}

	now := time.Now()
	escrow.Status = StatusDisputed
	escrow.DisputeReason = reason
	escrow.DisputeEvidence = []EvidenceEntry{{
		SubmittedBy: escrow.BuyerAddr,
		Content:     reason,
		SubmittedAt: now,
	}}
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to update escrow")
		return nil, fmt.Errorf("failed to update escrow: %w", err)
	}

	// Record failed transaction for reputation
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, escrow.ID, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "failed")
	}

	// Record dispute outcome for seller reputation
	if s.reputation != nil {
		_ = s.reputation.RecordDispute(ctx, escrow.SellerAddr, "disputed", escrow.Amount)
	}

	metrics.EscrowDisputedTotal.Inc()

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowDisputed(escrow.SellerAddr, escrow.ID, escrow.BuyerAddr, reason)
	}

	return escrow, nil
}

// AutoRelease releases expired escrows to the seller.
func (s *Service) AutoRelease(ctx context.Context, escrow *Escrow) error {
	ctx, span := traces.StartSpan(ctx, "escrow.AutoRelease",
		attribute.String("escrow.id", escrow.ID),
	)
	defer span.End()

	mu := s.escrowLock(escrow.ID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read from store under lock to prevent stale-state races
	fresh, err := s.store.Get(ctx, escrow.ID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get escrow")
		return err
	}
	escrow = fresh

	if escrow.IsTerminal() {
		span.RecordError(ErrAlreadyResolved)
		span.SetStatus(codes.Error, "already resolved")
		return ErrAlreadyResolved
	}

	// Don't auto-release escrows in dispute/arbitration
	if escrow.Status == StatusDisputed || escrow.Status == StatusArbitrating {
		span.RecordError(ErrInvalidStatus)
		span.SetStatus(codes.Error, "escrow is in dispute")
		return ErrInvalidStatus
	}

	// Release funds to seller
	if err := s.ledger.ReleaseEscrow(ctx, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ID); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to auto-release escrow")
		return &MoneyError{
			Err:         fmt.Errorf("failed to auto-release escrow: %w", err),
			FundsStatus: "locked_in_escrow",
			Recovery:    "Funds are still locked in escrow. The auto-release to the seller failed. Contact support with the reference.",
			Amount:      escrow.Amount,
			Reference:   escrow.ID,
		}
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
			s.logger.Error("CRITICAL: escrow auto-released but status update failed",
				"escrow_id", escrow.ID, "seller", escrow.SellerAddr, "amount", escrow.Amount, "error", retryErr)
			span.RecordError(retryErr)
			span.SetStatus(codes.Error, "failed to update escrow after auto-release")
			// BUG FIX: was wrapping `err` instead of `retryErr`
			return &MoneyError{
				Err:         fmt.Errorf("failed to update escrow after auto-release (requires manual resolution): %w", retryErr),
				FundsStatus: "released_to_seller",
				Recovery:    "Funds were successfully auto-released to the seller but the escrow record could not be updated. No action needed regarding funds.",
				Amount:      escrow.Amount,
				Reference:   escrow.ID,
			}
		}
	}

	// Record confirmed transaction for reputation (auto-release counts as success)
	if s.recorder != nil {
		_ = s.recorder.RecordTransaction(ctx, escrow.ID, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "confirmed")
	}

	// Intercept revenue for stakes (seller earned money)
	if s.revenue != nil {
		_ = s.revenue.AccumulateRevenue(ctx, escrow.SellerAddr, escrow.Amount, "escrow_release:"+escrow.ID)
	}

	// Issue receipt for auto-released escrow
	if s.receiptIssuer != nil {
		_ = s.receiptIssuer.IssueReceipt(ctx, "escrow", escrow.ID, escrow.BuyerAddr,
			escrow.SellerAddr, escrow.Amount, escrow.ServiceID, "confirmed", "auto_released")
	}

	metrics.EscrowAutoReleasedTotal.Inc()
	metrics.EscrowDuration.Observe(time.Since(escrow.CreatedAt).Seconds())

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowReleased(escrow.SellerAddr, escrow.ID, escrow.BuyerAddr, escrow.Amount)
	}

	s.cleanupLock(escrow.ID)
	return nil
}

// SubmitEvidence adds evidence to a disputed/arbitrating escrow.
// Both buyer and seller can submit evidence.
func (s *Service) SubmitEvidence(ctx context.Context, id, callerAddr, content string) (*Escrow, error) {
	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	caller := strings.ToLower(callerAddr)
	if caller != escrow.BuyerAddr && caller != escrow.SellerAddr {
		return nil, ErrUnauthorized
	}

	if escrow.Status != StatusDisputed && escrow.Status != StatusArbitrating {
		return nil, ErrInvalidStatus
	}

	escrow.DisputeEvidence = append(escrow.DisputeEvidence, EvidenceEntry{
		SubmittedBy: caller,
		Content:     content,
		SubmittedAt: time.Now(),
	})
	escrow.UpdatedAt = time.Now()

	if err := s.store.Update(ctx, escrow); err != nil {
		return nil, err
	}
	return escrow, nil
}

// AssignArbitrator assigns an arbitrator to a disputed escrow.
func (s *Service) AssignArbitrator(ctx context.Context, id, arbitratorAddr string) (*Escrow, error) {
	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if escrow.Status != StatusDisputed {
		return nil, ErrInvalidStatus
	}

	now := time.Now()
	deadline := now.Add(DefaultArbitrationDeadline)
	escrow.Status = StatusArbitrating
	escrow.ArbitratorAddr = strings.ToLower(arbitratorAddr)
	escrow.ArbitrationDeadline = &deadline
	escrow.UpdatedAt = now

	if err := s.store.Update(ctx, escrow); err != nil {
		return nil, err
	}
	return escrow, nil
}

// ResolveArbitration resolves a disputed escrow. Only the assigned arbitrator can call this.
// Supports full release to seller, full refund to buyer, or partial split.
func (s *Service) ResolveArbitration(ctx context.Context, id, callerAddr string, req ResolveRequest) (*Escrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.ResolveArbitration",
		traces.EscrowID(id),
		attribute.String("resolution", req.Resolution),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	escrow, err := s.store.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if escrow.Status != StatusArbitrating && escrow.Status != StatusDisputed {
		return nil, ErrInvalidStatus
	}

	// Only assigned arbitrator (or anyone if no arbitrator yet)
	if escrow.ArbitratorAddr != "" && strings.ToLower(callerAddr) != escrow.ArbitratorAddr {
		return nil, ErrUnauthorized
	}

	now := time.Now()

	switch req.Resolution {
	case "release":
		// Full release to seller
		if err := s.ledger.ReleaseEscrow(ctx, escrow.BuyerAddr, escrow.SellerAddr, escrow.Amount, escrow.ID); err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to release escrow: %w", err)
		}
		escrow.Status = StatusReleased
		escrow.Resolution = "arbitration_release"
		if s.revenue != nil {
			_ = s.revenue.AccumulateRevenue(ctx, escrow.SellerAddr, escrow.Amount, "escrow_arb_release:"+escrow.ID)
		}
		if s.reputation != nil {
			_ = s.reputation.RecordDispute(ctx, escrow.SellerAddr, "confirmed", escrow.Amount)
		}

	case "refund":
		// Full refund to buyer
		if err := s.ledger.RefundEscrow(ctx, escrow.BuyerAddr, escrow.Amount, escrow.ID); err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to refund escrow: %w", err)
		}
		escrow.Status = StatusRefunded
		escrow.Resolution = "arbitration_refund"
		if s.reputation != nil {
			_ = s.reputation.RecordDispute(ctx, escrow.SellerAddr, "refunded", escrow.Amount)
		}

	case "partial":
		// Split: releaseAmount to seller, remainder to buyer
		if err := validateAmount(req.ReleaseAmount); err != nil {
			return nil, fmt.Errorf("invalid releaseAmount: %w", err)
		}

		releaseAmtBig, ok := usdc.Parse(req.ReleaseAmount)
		if !ok {
			return nil, fmt.Errorf("%w: cannot parse releaseAmount", ErrInvalidAmount)
		}

		totalBig, _ := usdc.Parse(escrow.Amount)
		if releaseAmtBig.Cmp(totalBig) >= 0 {
			return nil, fmt.Errorf("%w: releaseAmount must be less than total", ErrInvalidAmount)
		}

		refundBig := new(big.Int).Sub(totalBig, releaseAmtBig)
		releaseStr := usdc.Format(releaseAmtBig)
		refundStr := usdc.Format(refundBig)

		// Atomically settle: release to seller + refund to buyer in one transaction
		if err := s.ledger.PartialEscrowSettle(ctx, escrow.BuyerAddr, escrow.SellerAddr, releaseStr, refundStr, escrow.ID+":partial"); err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("failed to settle partial escrow: %w", err)
		}

		escrow.Status = StatusReleased
		escrow.Resolution = "arbitration_partial"
		escrow.PartialReleaseAmount = releaseStr
		escrow.PartialRefundAmount = refundStr

		if s.revenue != nil {
			_ = s.revenue.AccumulateRevenue(ctx, escrow.SellerAddr, releaseStr, "escrow_arb_partial:"+escrow.ID)
		}
		if s.reputation != nil {
			_ = s.reputation.RecordDispute(ctx, escrow.SellerAddr, "partial", releaseStr)
		}

	default:
		return nil, fmt.Errorf("invalid resolution: %s (must be release, refund, or partial)", req.Resolution)
	}

	escrow.ResolvedAt = &now
	escrow.UpdatedAt = now
	if req.Reason != "" && escrow.Resolution != "" {
		escrow.Resolution += ": " + req.Reason
	}

	if err := s.store.Update(ctx, escrow); err != nil {
		s.logger.Error("CRITICAL: arbitration resolved but state update failed",
			"escrow_id", escrow.ID, "resolution", req.Resolution)
		span.RecordError(err)
		return nil, err
	}

	// Issue receipt for arbitration resolution (release or partial → funds moved to seller)
	if s.receiptIssuer != nil && (req.Resolution == "release" || req.Resolution == "partial") {
		receiptAmount := escrow.Amount
		if req.Resolution == "partial" && escrow.PartialReleaseAmount != "" {
			receiptAmount = escrow.PartialReleaseAmount
		}
		_ = s.receiptIssuer.IssueReceipt(ctx, "escrow", escrow.ID, escrow.BuyerAddr,
			escrow.SellerAddr, receiptAmount, escrow.ServiceID, "confirmed", "arbitration_"+req.Resolution)
	}

	metrics.EscrowDuration.Observe(time.Since(escrow.CreatedAt).Seconds())
	s.cleanupLock(id)
	return escrow, nil
}

// Get returns an escrow by ID.
func (s *Service) Get(ctx context.Context, id string) (*Escrow, error) {
	return s.store.Get(ctx, id)
}

// ForceCloseExpired auto-releases all expired escrows. Returns the number closed.
func (s *Service) ForceCloseExpired(ctx context.Context) (int, error) {
	expired, err := s.store.ListExpired(ctx, time.Now(), 100)
	if err != nil {
		return 0, err
	}

	closed := 0
	for _, esc := range expired {
		if esc.IsTerminal() {
			continue
		}
		if esc.Status == StatusDisputed || esc.Status == StatusArbitrating {
			continue
		}
		if err := s.AutoRelease(ctx, esc); err != nil {
			s.logger.Warn("force-close: failed to auto-release escrow", "escrowId", esc.ID, "error", err)
			continue
		}
		closed++
	}
	return closed, nil
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
