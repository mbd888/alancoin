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

	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
)

var (
	ErrMultiStepNotFound  = errors.New("escrow: multistep not found")
	ErrDuplicateStep      = errors.New("escrow: step index already confirmed")
	ErrStepOutOfRange     = errors.New("escrow: step index out of range")
	ErrAmountExceedsTotal = errors.New("escrow: step amount exceeds remaining balance")
	ErrStepMismatch       = errors.New("escrow: seller or amount does not match planned step")
)

// MaxTotalSteps is the maximum number of steps allowed in a multistep escrow.
const MaxTotalSteps = 100

// MultiStepStatus represents the state of a multistep escrow.
type MultiStepStatus string

const (
	MSOpen      MultiStepStatus = "open"
	MSCompleted MultiStepStatus = "completed"
	MSAborted   MultiStepStatus = "aborted"
)

// PlannedStep defines the expected seller and amount for a pipeline step.
// Set at creation time and validated during ConfirmStep to prevent fund misdirection.
type PlannedStep struct {
	SellerAddr string `json:"sellerAddr"`
	Amount     string `json:"amount"`
}

// MultiStepEscrow holds funds for an N-step pipeline with per-step release.
type MultiStepEscrow struct {
	ID             string          `json:"id"`
	BuyerAddr      string          `json:"buyerAddr"`
	TotalAmount    string          `json:"totalAmount"`
	SpentAmount    string          `json:"spentAmount"`
	TotalSteps     int             `json:"totalSteps"`
	ConfirmedSteps int             `json:"confirmedSteps"`
	PlannedSteps   []PlannedStep   `json:"plannedSteps"`
	Status         MultiStepStatus `json:"status"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// Step records a single confirmed step within a multistep escrow.
type Step struct {
	StepIndex  int       `json:"stepIndex"`
	SellerAddr string    `json:"sellerAddr"`
	Amount     string    `json:"amount"`
	CreatedAt  time.Time `json:"createdAt"`
}

// MultiStepStore persists multistep escrow data.
type MultiStepStore interface {
	Create(ctx context.Context, mse *MultiStepEscrow) error
	Get(ctx context.Context, id string) (*MultiStepEscrow, error)
	RecordStep(ctx context.Context, id string, step Step) error
	Abort(ctx context.Context, id string) error
	Complete(ctx context.Context, id string) error
}

// MultiStepService implements multistep escrow business logic.
type MultiStepService struct {
	store  MultiStepStore
	ledger LedgerService
	logger *slog.Logger
	locks  sync.Map
}

// NewMultiStepService creates a new multistep escrow service.
func NewMultiStepService(store MultiStepStore, ledger LedgerService) *MultiStepService {
	return &MultiStepService{
		store:  store,
		ledger: ledger,
		logger: slog.Default(),
	}
}

// WithLogger sets a structured logger.
func (s *MultiStepService) WithLogger(l *slog.Logger) *MultiStepService {
	s.logger = l
	return s
}

func (s *MultiStepService) escrowLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *MultiStepService) cleanupLock(id string) {
	s.locks.Delete(id)
}

// LockSteps creates a multistep escrow, locking totalAmount from the buyer.
// plannedSteps must have exactly totalSteps entries defining each step's seller and amount.
func (s *MultiStepService) LockSteps(ctx context.Context, buyerAddr, totalAmount string, totalSteps int, plannedSteps []PlannedStep) (*MultiStepEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.multistep.LockSteps",
		attribute.String("buyer", buyerAddr),
		attribute.Int("total_steps", totalSteps),
	)
	defer span.End()

	if totalSteps <= 0 || totalSteps > MaxTotalSteps {
		return nil, fmt.Errorf("totalSteps must be between 1 and %d", MaxTotalSteps)
	}
	if len(plannedSteps) != totalSteps {
		return nil, fmt.Errorf("plannedSteps length (%d) must match totalSteps (%d)", len(plannedSteps), totalSteps)
	}
	if err := validateAmount(totalAmount); err != nil {
		return nil, err
	}

	// Validate each planned step and normalize addresses
	normalized := make([]PlannedStep, totalSteps)
	stepSum := new(big.Int)
	for i, ps := range plannedSteps {
		if err := validateAmount(ps.Amount); err != nil {
			return nil, fmt.Errorf("plannedSteps[%d]: %w", i, err)
		}
		if ps.SellerAddr == "" {
			return nil, fmt.Errorf("plannedSteps[%d]: sellerAddr is required", i)
		}
		parsed, _ := usdc.Parse(ps.Amount)
		stepSum.Add(stepSum, parsed)
		normalized[i] = PlannedStep{
			SellerAddr: strings.ToLower(ps.SellerAddr),
			Amount:     ps.Amount,
		}
	}

	// Verify planned step amounts sum to totalAmount
	totalParsed, _ := usdc.Parse(totalAmount)
	if stepSum.Cmp(totalParsed) != 0 {
		return nil, fmt.Errorf("planned step amounts sum to %s but totalAmount is %s", usdc.Format(stepSum), totalAmount)
	}

	now := time.Now()
	mse := &MultiStepEscrow{
		ID:           generateMultiStepID(),
		BuyerAddr:    strings.ToLower(buyerAddr),
		TotalAmount:  totalAmount,
		SpentAmount:  "0",
		TotalSteps:   totalSteps,
		PlannedSteps: normalized,
		Status:       MSOpen,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	ref := "mse:" + mse.ID
	if err := s.ledger.EscrowLock(ctx, mse.BuyerAddr, mse.TotalAmount, ref); err != nil {
		return nil, fmt.Errorf("failed to lock escrow funds: %w", err)
	}

	if err := s.store.Create(ctx, mse); err != nil {
		// Best-effort refund if store fails
		_ = s.ledger.RefundEscrow(ctx, mse.BuyerAddr, mse.TotalAmount, ref)
		return nil, fmt.Errorf("failed to create multistep escrow: %w", err)
	}

	return mse, nil
}

// ConfirmStep releases funds for a single step to the seller.
func (s *MultiStepService) ConfirmStep(ctx context.Context, id string, stepIndex int, sellerAddr, amount string) (*MultiStepEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.multistep.ConfirmStep",
		attribute.String("escrow_id", id),
		attribute.Int("step_index", stepIndex),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	mse, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if mse.Status != MSOpen {
		return nil, fmt.Errorf("%w: escrow status is %s", ErrInvalidStatus, mse.Status)
	}

	if stepIndex < 0 || stepIndex >= mse.TotalSteps {
		return nil, ErrStepOutOfRange
	}

	if err := validateAmount(amount); err != nil {
		return nil, err
	}

	seller := strings.ToLower(sellerAddr)

	// Validate against planned step — prevents fund misdirection
	if stepIndex < len(mse.PlannedSteps) {
		planned := mse.PlannedSteps[stepIndex]
		if seller != planned.SellerAddr {
			return nil, fmt.Errorf("%w: step %d expected seller %s, got %s",
				ErrStepMismatch, stepIndex, planned.SellerAddr, seller)
		}
		if amount != planned.Amount {
			return nil, fmt.Errorf("%w: step %d expected amount %s, got %s",
				ErrStepMismatch, stepIndex, planned.Amount, amount)
		}
	}

	// Check amount doesn't exceed remaining
	spentBig, _ := usdc.Parse(mse.SpentAmount)
	totalBig, _ := usdc.Parse(mse.TotalAmount)
	amountBig, _ := usdc.Parse(amount)

	newSpent := new(big.Int).Add(spentBig, amountBig)
	if newSpent.Cmp(totalBig) > 0 {
		return nil, ErrAmountExceedsTotal
	}

	// Record step first to claim the index atomically (prevents duplicate releases).
	// If this fails (e.g. duplicate step), no funds are moved.
	step := Step{
		StepIndex:  stepIndex,
		SellerAddr: seller,
		Amount:     amount,
		CreatedAt:  time.Now(),
	}

	if err := s.store.RecordStep(ctx, id, step); err != nil {
		return nil, fmt.Errorf("failed to record step: %w", err)
	}

	// Release this step's amount from buyer's escrow to seller
	stepRef := fmt.Sprintf("mse:%s:step:%d", id, stepIndex)
	if err := s.ledger.ReleaseEscrow(ctx, mse.BuyerAddr, seller, amount, stepRef); err != nil {
		s.logger.Error("CRITICAL: step recorded but fund release failed",
			"escrow_id", id, "step", stepIndex, "seller", seller, "amount", amount, "error", err)
		return nil, fmt.Errorf("failed to release step funds: %w", err)
	}

	// Re-read to get updated counters
	mse, err = s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	// Auto-complete if all steps confirmed
	if mse.ConfirmedSteps >= mse.TotalSteps {
		if err := s.store.Complete(ctx, id); err != nil {
			s.logger.Error("failed to auto-complete multistep escrow", "id", id, "error", err)
		} else {
			mse.Status = MSCompleted
			mse.UpdatedAt = time.Now()
		}

		// Refund any remaining escrowed dust (if step amounts didn't exactly match total)
		mse2, _ := s.store.Get(ctx, id)
		if mse2 != nil {
			spentFinal, _ := usdc.Parse(mse2.SpentAmount)
			remaining := new(big.Int).Sub(totalBig, spentFinal)
			if remaining.Sign() > 0 {
				refRef := "mse:" + id + ":dust"
				if err := s.ledger.RefundEscrow(ctx, mse.BuyerAddr, usdc.Format(remaining), refRef); err != nil {
					s.logger.Error("CRITICAL: failed to refund dust during auto-complete",
						"escrow_id", id, "amount", usdc.Format(remaining), "error", err)
				}
			}
		}

		s.cleanupLock(id)
	}

	return mse, nil
}

// RefundRemaining aborts the escrow and returns unspent funds to the buyer.
func (s *MultiStepService) RefundRemaining(ctx context.Context, id, callerAddr string) (*MultiStepEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.multistep.RefundRemaining",
		attribute.String("escrow_id", id),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	mse, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	if strings.ToLower(callerAddr) != mse.BuyerAddr {
		return nil, ErrUnauthorized
	}

	if mse.Status != MSOpen {
		return nil, fmt.Errorf("%w: escrow status is %s", ErrInvalidStatus, mse.Status)
	}

	// Calculate remaining
	spentBig, _ := usdc.Parse(mse.SpentAmount)
	totalBig, _ := usdc.Parse(mse.TotalAmount)
	remaining := new(big.Int).Sub(totalBig, spentBig)

	if remaining.Sign() > 0 {
		refRef := "mse:" + id + ":refund"
		if err := s.ledger.RefundEscrow(ctx, mse.BuyerAddr, usdc.Format(remaining), refRef); err != nil {
			return nil, fmt.Errorf("failed to refund remaining: %w", err)
		}
	}

	if err := s.store.Abort(ctx, id); err != nil {
		if remaining.Sign() > 0 {
			s.logger.Error("CRITICAL: refund issued but abort failed",
				"escrow_id", id, "refunded", usdc.Format(remaining), "error", err)
		}
		return nil, fmt.Errorf("failed to abort escrow: %w", err)
	}

	mse.Status = MSAborted
	mse.UpdatedAt = time.Now()

	s.cleanupLock(id)
	return mse, nil
}

// Get returns a multistep escrow by ID.
func (s *MultiStepService) Get(ctx context.Context, id string) (*MultiStepEscrow, error) {
	return s.store.Get(ctx, id)
}

func generateMultiStepID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("mse_%x", b)
}
