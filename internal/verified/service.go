package verified

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// Service implements verified agent business logic.
type Service struct {
	store      Store
	scorer     *Scorer
	reputation ReputationProvider
	metrics    MetricsProvider
	ledger     LedgerService
}

// NewService creates a new verification service.
func NewService(store Store, scorer *Scorer, reputation ReputationProvider, metrics MetricsProvider, ledger LedgerService) *Service {
	return &Service{
		store:      store,
		scorer:     scorer,
		reputation: reputation,
		metrics:    metrics,
		ledger:     ledger,
	}
}

// Apply evaluates an agent for verification and creates a verified status if eligible.
// The agent must post a bond within the allowed range for their tier.
func (s *Service) Apply(ctx context.Context, agentAddr, bondAmount string) (*Verification, *EvaluationResult, error) {
	agentAddr = strings.ToLower(agentAddr)

	// Check for existing active verification
	existing, err := s.store.GetByAgent(ctx, agentAddr)
	if err == nil && existing != nil && !existing.IsTerminal() {
		return nil, nil, ErrAlreadyVerified
	}

	// Get reputation
	score, tier, err := s.reputation.GetScore(ctx, agentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get reputation: %w", err)
	}

	// Get transaction metrics
	totalTxns, successRate, daysOnNetwork, totalVolumeUSD, err := s.metrics.GetAgentMetrics(ctx, agentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	// Evaluate eligibility
	result := s.scorer.Evaluate(score, tier, totalTxns, successRate, daysOnNetwork, totalVolumeUSD)
	if !result.Eligible {
		return nil, result, ErrNotEligible
	}

	// Validate bond amount
	bond, ok := new(big.Float).SetString(bondAmount)
	if !ok || bond.Sign() <= 0 {
		return nil, result, ErrBondTooLow
	}
	bondFloat, _ := bond.Float64()
	if bondFloat < result.MinBondAmount {
		return nil, result, fmt.Errorf("%w: minimum bond is %.2f USDC", ErrBondTooLow, result.MinBondAmount)
	}
	if bondFloat > result.MaxBondAmount {
		bondAmount = fmt.Sprintf("%.6f", result.MaxBondAmount)
	}

	// Post performance bond via ledger hold
	bondRef := "vbond_" + idgen.Hex(12)
	if err := s.ledger.Hold(ctx, agentAddr, bondAmount, bondRef); err != nil {
		return nil, result, fmt.Errorf("failed to hold bond: %w", err)
	}

	// Create verification record
	now := time.Now()
	v := &Verification{
		ID:                    idgen.New(),
		AgentAddr:             agentAddr,
		Status:                StatusActive,
		BondAmount:            bondAmount,
		BondReference:         bondRef,
		GuaranteedSuccessRate: result.GuaranteedSuccessRate,
		SLAWindowSize:         result.SLAWindowSize,
		GuaranteePremiumRate:  result.GuaranteePremiumRate,
		ReputationScore:       score,
		ReputationTier:        tier,
		LastReviewAt:          now,
		VerifiedAt:            now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}

	if err := s.store.Create(ctx, v); err != nil {
		// Compensate: release the bond hold
		_ = s.ledger.ReleaseHold(ctx, agentAddr, bondAmount, bondRef)
		return nil, result, fmt.Errorf("failed to create verification: %w", err)
	}

	return v, result, nil
}

// Get returns a verification by ID.
func (s *Service) Get(ctx context.Context, id string) (*Verification, error) {
	return s.store.Get(ctx, id)
}

// GetByAgent returns a verification by agent address.
func (s *Service) GetByAgent(ctx context.Context, agentAddr string) (*Verification, error) {
	return s.store.GetByAgent(ctx, strings.ToLower(agentAddr))
}

// IsVerified checks if an agent currently has active verified status.
func (s *Service) IsVerified(ctx context.Context, agentAddr string) (bool, error) {
	return s.store.IsVerified(ctx, strings.ToLower(agentAddr))
}

// GetGuarantee returns the guaranteed success rate and premium rate for a verified agent.
// Returns zeros if the agent is not verified.
func (s *Service) GetGuarantee(ctx context.Context, agentAddr string) (guaranteedSuccessRate float64, premiumRate float64, err error) {
	v, err := s.store.GetByAgent(ctx, strings.ToLower(agentAddr))
	if err != nil {
		return 0, 0, err
	}
	if !v.IsActive() {
		return 0, 0, nil
	}
	return v.GuaranteedSuccessRate, v.GuaranteePremiumRate, nil
}

// Revoke voluntarily revokes verified status and releases the bond.
func (s *Service) Revoke(ctx context.Context, agentAddr string) (*Verification, error) {
	agentAddr = strings.ToLower(agentAddr)

	v, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return nil, ErrNotVerified
	}

	if v.IsTerminal() {
		return nil, ErrInvalidStatus
	}

	now := time.Now()
	v.Status = StatusRevoked
	v.RevokedAt = &now
	v.UpdatedAt = now

	// Release the performance bond back to the agent
	if err := s.ledger.ReleaseHold(ctx, agentAddr, v.BondAmount, v.BondReference); err != nil {
		return nil, fmt.Errorf("failed to release bond: %w", err)
	}

	if err := s.store.Update(ctx, v); err != nil {
		return nil, fmt.Errorf("failed to update verification: %w", err)
	}

	return v, nil
}

// RecordViolation handles an SLA breach. Forfeits a portion of the bond
// proportional to the severity, and credits it to the platform guarantee fund.
// The guarantee fund address receives forfeited bonds for buyer compensation.
func (s *Service) RecordViolation(ctx context.Context, agentAddr string, windowSuccessRate float64, guaranteeFundAddr string) (*Verification, error) {
	agentAddr = strings.ToLower(agentAddr)

	v, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return nil, ErrNotVerified
	}

	if !v.IsActive() {
		return nil, ErrInvalidStatus
	}

	// Calculate forfeiture: proportional to how far below guarantee
	// e.g. guaranteed 95%, actual 80% → shortfall = 15/95 ≈ 15.8% of bond
	shortfall := (v.GuaranteedSuccessRate - windowSuccessRate) / v.GuaranteedSuccessRate
	if shortfall < 0.01 {
		shortfall = 0.01 // Minimum 1% forfeiture
	}
	if shortfall > 1.0 {
		shortfall = 1.0
	}

	bondBig, _ := new(big.Float).SetString(v.BondAmount)
	if bondBig == nil {
		bondBig = new(big.Float)
	}
	forfeitAmount := new(big.Float).Mul(bondBig, new(big.Float).SetFloat64(shortfall))
	forfeitStr := forfeitAmount.Text('f', 6)

	// Confirm the forfeited portion (removes from pending)
	if err := s.ledger.ConfirmHold(ctx, agentAddr, forfeitStr, v.BondReference); err != nil {
		return nil, fmt.Errorf("failed to confirm bond forfeiture: %w", err)
	}

	// Deposit forfeited amount into guarantee fund
	if guaranteeFundAddr != "" {
		if err := s.ledger.Deposit(ctx, guaranteeFundAddr, forfeitStr, "vforfeit_"+v.ID); err != nil {
			// Non-fatal: the bond was already confirmed, log but continue
			_ = err
		}
	}

	// Update remaining bond
	remainingBig := new(big.Float).Sub(bondBig, forfeitAmount)
	if remainingBig.Sign() <= 0 {
		// Entire bond forfeited
		v.Status = StatusForfeited
		v.BondAmount = "0.000000"
	} else {
		// Partial forfeiture — suspend pending review
		v.Status = StatusSuspended
		v.BondAmount = remainingBig.Text('f', 6)
	}

	now := time.Now()
	v.ViolationCount++
	v.LastViolationAt = now
	v.UpdatedAt = now

	if err := s.store.Update(ctx, v); err != nil {
		return nil, fmt.Errorf("failed to update verification: %w", err)
	}

	return v, nil
}

// Reinstate reactivates a suspended verification after review.
func (s *Service) Reinstate(ctx context.Context, agentAddr string) (*Verification, error) {
	agentAddr = strings.ToLower(agentAddr)

	v, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return nil, ErrNotVerified
	}

	if v.Status != StatusSuspended {
		return nil, ErrInvalidStatus
	}

	// Re-check eligibility
	score, tier, err := s.reputation.GetScore(ctx, agentAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get reputation: %w", err)
	}

	totalTxns, successRate, daysOnNetwork, totalVolumeUSD, err := s.metrics.GetAgentMetrics(ctx, agentAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	result := s.scorer.Evaluate(score, tier, totalTxns, successRate, daysOnNetwork, totalVolumeUSD)
	if !result.Eligible {
		return nil, fmt.Errorf("%w: %s", ErrNotEligible, result.Reason)
	}

	now := time.Now()
	v.Status = StatusActive
	v.ReputationScore = score
	v.ReputationTier = tier
	v.LastReviewAt = now
	v.UpdatedAt = now

	if err := s.store.Update(ctx, v); err != nil {
		return nil, fmt.Errorf("failed to update verification: %w", err)
	}

	return v, nil
}

// Review re-evaluates a verified agent's standing and may suspend if reputation dropped.
func (s *Service) Review(ctx context.Context, agentAddr string) (*Verification, *EvaluationResult, error) {
	agentAddr = strings.ToLower(agentAddr)

	v, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return nil, nil, ErrNotVerified
	}

	if v.IsTerminal() {
		return nil, nil, ErrInvalidStatus
	}

	score, tier, err := s.reputation.GetScore(ctx, agentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get reputation: %w", err)
	}

	totalTxns, successRate, daysOnNetwork, totalVolumeUSD, err := s.metrics.GetAgentMetrics(ctx, agentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	result := s.scorer.Evaluate(score, tier, totalTxns, successRate, daysOnNetwork, totalVolumeUSD)

	now := time.Now()
	v.ReputationScore = score
	v.ReputationTier = tier
	v.LastReviewAt = now
	v.UpdatedAt = now

	if !result.Eligible && v.Status == StatusActive {
		v.Status = StatusSuspended
	} else if result.Eligible && v.Status == StatusSuspended {
		v.Status = StatusActive
	}

	if err := s.store.Update(ctx, v); err != nil {
		return nil, result, fmt.Errorf("failed to update verification: %w", err)
	}

	return v, result, nil
}

// ReviewAllActive re-evaluates all active verifications.
func (s *Service) ReviewAllActive(ctx context.Context) error {
	verifications, err := s.store.ListActive(ctx, 1000)
	if err != nil {
		return fmt.Errorf("failed to list active verifications: %w", err)
	}

	var errs []error
	for _, v := range verifications {
		if _, _, err := s.Review(ctx, v.AgentAddr); err != nil {
			errs = append(errs, fmt.Errorf("review %s: %w", v.AgentAddr, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to review %d/%d verifications: %v", len(errs), len(verifications), errs[0])
	}
	return nil
}

// ListActive returns all active verifications.
func (s *Service) ListActive(ctx context.Context, limit int) ([]*Verification, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListActive(ctx, limit)
}

// ListAll returns all verifications.
func (s *Service) ListAll(ctx context.Context, limit int) ([]*Verification, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListAll(ctx, limit)
}
