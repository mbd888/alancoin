// Package arbitration implements programmatic dispute resolution for agent escrows.
//
// When an escrow dispute is filed, the arbitration engine determines the outcome:
//   - Auto-resolve: compare service output against behavioral contract spec
//   - Arbiter-assigned: route to human or agent arbiter for manual review
//   - Evidence-based: collect evidence from both parties, render decision
//
// Resolution is backed by the escrow system: the arbiter decision triggers
// fund release (to seller) or refund (to buyer) via existing escrow primitives.
//
// Fee: 2% of disputed amount (min $0.50, max $500), split between platform
// and arbiter (when applicable).
//
// Based on: AAA AI Arbitrator (March 2026), McKinsey dispute resolution research.
package arbitration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/usdc"
)

var (
	ErrCaseNotFound      = errors.New("arbitration: case not found")
	ErrCaseAlreadyClosed = errors.New("arbitration: case already resolved")
	ErrNotArbiter        = errors.New("arbitration: caller is not the assigned arbiter")
	ErrNoEvidence        = errors.New("arbitration: at least one piece of evidence required")
	ErrInvalidOutcome    = errors.New("arbitration: invalid outcome")
)

// CaseStatus represents the arbitration lifecycle.
type CaseStatus string

const (
	CSOpen         CaseStatus = "open"          // Filed, awaiting auto-resolution or assignment
	CSAutoResolved CaseStatus = "auto_resolved" // Resolved by behavioral contract comparison
	CSAssigned     CaseStatus = "assigned"      // Arbiter assigned, awaiting decision
	CSResolved     CaseStatus = "resolved"      // Arbiter rendered decision
	CSAppealed     CaseStatus = "appealed"      // Party appealed (escalated)
)

// Outcome is the resolution decision.
type Outcome string

const (
	OutcomeBuyerWins  Outcome = "buyer_wins"  // Funds refunded to buyer
	OutcomeSellerWins Outcome = "seller_wins" // Funds released to seller
	OutcomeSplit      Outcome = "split"       // Partial refund (percentage-based)
)

// Evidence is a piece of supporting material for a dispute.
type Evidence struct {
	ID          string    `json:"id"`
	CaseID      string    `json:"caseId"`
	SubmittedBy string    `json:"submittedBy"` // agent address
	Role        string    `json:"role"`        // "buyer", "seller", "arbiter"
	Type        string    `json:"type"`        // "text", "json", "url", "hash"
	Content     string    `json:"content"`
	SubmittedAt time.Time `json:"submittedAt"`
}

// Case represents a dispute arbitration case.
type Case struct {
	ID             string     `json:"id"`
	EscrowID       string     `json:"escrowId"`
	BuyerAddr      string     `json:"buyerAddr"`
	SellerAddr     string     `json:"sellerAddr"`
	DisputedAmount string     `json:"disputedAmount"` // USDC
	Reason         string     `json:"reason"`
	Status         CaseStatus `json:"status"`
	ArbiterAddr    string     `json:"arbiterAddr,omitempty"`
	Outcome        Outcome    `json:"outcome,omitempty"`
	SplitPct       int        `json:"splitPct,omitempty"`   // % to buyer (0-100) when outcome=split
	Decision       string     `json:"decision,omitempty"`   // Arbiter's written reasoning
	Fee            string     `json:"fee"`                  // Arbitration fee (USDC)
	ContractID     string     `json:"contractId,omitempty"` // Behavioral contract for auto-resolution
	Evidence       []Evidence `json:"evidence,omitempty"`
	AutoResolvable bool       `json:"autoResolvable"` // True if behavioral contract exists for comparison
	FiledAt        time.Time  `json:"filedAt"`
	ResolvedAt     *time.Time `json:"resolvedAt,omitempty"`
}

// Store persists arbitration cases.
type Store interface {
	Create(ctx context.Context, c *Case) error
	Get(ctx context.Context, id string) (*Case, error)
	Update(ctx context.Context, c *Case) error
	ListByEscrow(ctx context.Context, escrowID string) ([]*Case, error)
	ListOpen(ctx context.Context, limit int) ([]*Case, error)
}

// EscrowResolver handles the financial outcome of arbitration.
type EscrowResolver interface {
	RefundBuyer(ctx context.Context, escrowID string) error
	ReleaseSeller(ctx context.Context, escrowID string) error
	SplitFunds(ctx context.Context, escrowID string, buyerPct int) error
}

// ReputationUpdater applies reputation consequences.
type ReputationUpdater interface {
	RecordDisputeLoss(ctx context.Context, loserAddr string, amount string) error
}

// Service manages the arbitration lifecycle.
type Service struct {
	store      Store
	escrow     EscrowResolver
	reputation ReputationUpdater
	logger     *slog.Logger
	mu         sync.Mutex
}

// NewService creates a new arbitration service.
func NewService(store Store, escrow EscrowResolver, rep ReputationUpdater, logger *slog.Logger) *Service {
	return &Service{
		store:      store,
		escrow:     escrow,
		reputation: rep,
		logger:     logger,
	}
}

// FileCase opens a new arbitration case for an escrowed dispute.
func (s *Service) FileCase(ctx context.Context, escrowID, buyerAddr, sellerAddr, amount, reason string, contractID string) (*Case, error) {
	fee := computeFee(amount)

	c := &Case{
		ID:             idgen.WithPrefix("arb_"),
		EscrowID:       escrowID,
		BuyerAddr:      buyerAddr,
		SellerAddr:     sellerAddr,
		DisputedAmount: amount,
		Reason:         reason,
		Status:         CSOpen,
		Fee:            fee,
		ContractID:     contractID,
		AutoResolvable: contractID != "",
		FiledAt:        time.Now(),
	}

	if err := s.store.Create(ctx, c); err != nil {
		return nil, fmt.Errorf("arbitration: create case: %w", err)
	}

	metrics.ArbitrationCasesFiledTotal.Inc()

	s.logger.Info("arbitration: case filed",
		"case_id", c.ID, "escrow", escrowID, "amount", amount, "auto_resolvable", c.AutoResolvable)

	return c, nil
}

// AutoResolve attempts to resolve a case using behavioral contract comparison.
// Returns true if resolution was successful, false if manual arbiter needed.
func (s *Service) AutoResolve(ctx context.Context, caseID string, contractPassed bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.store.Get(ctx, caseID)
	if err != nil {
		return false, ErrCaseNotFound
	}
	if c.Status != CSOpen {
		return false, ErrCaseAlreadyClosed
	}
	if !c.AutoResolvable {
		return false, nil
	}

	now := time.Now()
	c.ResolvedAt = &now

	if contractPassed {
		// Contract says service was delivered correctly — seller wins
		c.Status = CSAutoResolved
		c.Outcome = OutcomeSellerWins
		c.Decision = "Auto-resolved: behavioral contract conditions were satisfied."
		if err := s.store.Update(ctx, c); err != nil {
			return false, err
		}
		if err := s.escrow.ReleaseSeller(ctx, c.EscrowID); err != nil {
			return false, err
		}
		if s.reputation != nil {
			_ = s.reputation.RecordDisputeLoss(ctx, c.BuyerAddr, c.Fee) // buyer pays fee for invalid dispute
		}
	} else {
		// Contract says service failed — buyer wins
		c.Status = CSAutoResolved
		c.Outcome = OutcomeBuyerWins
		c.Decision = "Auto-resolved: behavioral contract conditions were violated."
		if err := s.store.Update(ctx, c); err != nil {
			return false, err
		}
		if err := s.escrow.RefundBuyer(ctx, c.EscrowID); err != nil {
			return false, err
		}
		if s.reputation != nil {
			_ = s.reputation.RecordDisputeLoss(ctx, c.SellerAddr, c.DisputedAmount)
		}
	}

	metrics.ArbitrationCasesResolvedTotal.WithLabelValues(string(c.Outcome)).Inc()

	s.logger.Info("arbitration: auto-resolved",
		"case_id", caseID, "outcome", c.Outcome, "contract_passed", contractPassed)

	return true, nil
}

// AssignArbiter assigns a human or agent arbiter to a case.
func (s *Service) AssignArbiter(ctx context.Context, caseID, arbiterAddr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.store.Get(ctx, caseID)
	if err != nil {
		return ErrCaseNotFound
	}
	if c.Status != CSOpen {
		return ErrCaseAlreadyClosed
	}

	c.Status = CSAssigned
	c.ArbiterAddr = arbiterAddr

	if err := s.store.Update(ctx, c); err != nil {
		return err
	}

	s.logger.Info("arbitration: arbiter assigned", "case_id", caseID, "arbiter", arbiterAddr)
	return nil
}

// SubmitEvidence adds evidence to a case.
func (s *Service) SubmitEvidence(ctx context.Context, caseID, submittedBy, role, evidenceType, content string) (*Evidence, error) {
	c, err := s.store.Get(ctx, caseID)
	if err != nil {
		return nil, ErrCaseNotFound
	}
	if c.Status == CSResolved || c.Status == CSAutoResolved {
		return nil, ErrCaseAlreadyClosed
	}

	ev := Evidence{
		ID:          idgen.WithPrefix("ev_"),
		CaseID:      caseID,
		SubmittedBy: submittedBy,
		Role:        role,
		Type:        evidenceType,
		Content:     content,
		SubmittedAt: time.Now(),
	}

	c.Evidence = append(c.Evidence, ev)
	if err := s.store.Update(ctx, c); err != nil {
		return nil, err
	}

	return &ev, nil
}

// Resolve renders a final decision on an assigned case.
func (s *Service) Resolve(ctx context.Context, caseID, arbiterAddr string, outcome Outcome, splitPct int, decision string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.store.Get(ctx, caseID)
	if err != nil {
		return ErrCaseNotFound
	}
	if c.Status == CSResolved || c.Status == CSAutoResolved {
		return ErrCaseAlreadyClosed
	}
	if c.ArbiterAddr != arbiterAddr {
		return ErrNotArbiter
	}
	if outcome != OutcomeBuyerWins && outcome != OutcomeSellerWins && outcome != OutcomeSplit {
		return ErrInvalidOutcome
	}

	now := time.Now()
	c.Status = CSResolved
	c.Outcome = outcome
	c.SplitPct = splitPct
	c.Decision = decision
	c.ResolvedAt = &now

	if err := s.store.Update(ctx, c); err != nil {
		return err
	}

	// Execute financial outcome
	switch outcome {
	case OutcomeBuyerWins:
		if err := s.escrow.RefundBuyer(ctx, c.EscrowID); err != nil {
			return err
		}
		if s.reputation != nil {
			_ = s.reputation.RecordDisputeLoss(ctx, c.SellerAddr, c.DisputedAmount)
		}
	case OutcomeSellerWins:
		if err := s.escrow.ReleaseSeller(ctx, c.EscrowID); err != nil {
			return err
		}
	case OutcomeSplit:
		if err := s.escrow.SplitFunds(ctx, c.EscrowID, splitPct); err != nil {
			return err
		}
	}

	s.logger.Info("arbitration: resolved",
		"case_id", caseID, "outcome", outcome, "decision", decision)

	return nil
}

// computeFee calculates the arbitration fee: 2% of amount, min $0.50, max $500.
func computeFee(amount string) string {
	amountBig := parseUSDC(amount)
	// 2% = amount * 2 / 100
	fee := new(big.Int).Mul(amountBig, big.NewInt(2))
	fee.Div(fee, big.NewInt(100))

	minFee := parseUSDC("0.50")
	maxFee := parseUSDC("500.00")

	if fee.Cmp(minFee) < 0 {
		fee = minFee
	}
	if fee.Cmp(maxFee) > 0 {
		fee = maxFee
	}

	return usdc.Format(fee)
}

func parseUSDC(s string) *big.Int {
	v, _ := usdc.Parse(s)
	return v
}
