package credit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

// Service provides credit line business logic.
type Service struct {
	store      Store
	scorer     *Scorer
	reputation ReputationProvider
	metrics    MetricsProvider
	ledger     LedgerService
}

// NewService creates a new credit service.
func NewService(store Store, scorer *Scorer, reputation ReputationProvider, metrics MetricsProvider, ledger LedgerService) *Service {
	return &Service{
		store:      store,
		scorer:     scorer,
		reputation: reputation,
		metrics:    metrics,
		ledger:     ledger,
	}
}

// Apply evaluates an agent for credit and creates a credit line if eligible.
func (s *Service) Apply(ctx context.Context, agentAddr string) (*CreditLine, *EvaluationResult, error) {
	agentAddr = strings.ToLower(agentAddr)

	// Check for existing active credit line
	existing, err := s.store.GetByAgent(ctx, agentAddr)
	if err == nil && existing != nil {
		switch existing.Status {
		case StatusActive, StatusSuspended:
			return nil, nil, ErrCreditLineExists
		case StatusRevoked:
			return nil, nil, ErrCreditLineRevoked
		case StatusDefaulted:
			return nil, nil, ErrCreditLineDefaulted
		}
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

	// Evaluate
	result := s.scorer.Evaluate(score, tier, totalTxns, successRate, daysOnNetwork, totalVolumeUSD)
	if !result.Eligible {
		return nil, result, ErrNotEligible
	}

	// Create credit line
	now := time.Now()
	line := &CreditLine{
		ID:              idgen.New(),
		AgentAddr:       agentAddr,
		CreditLimit:     fmt.Sprintf("%.6f", result.CreditLimit),
		CreditUsed:      "0.000000",
		InterestRate:    result.InterestRate,
		Status:          StatusActive,
		ReputationTier:  tier,
		ReputationScore: score,
		ApprovedAt:      now,
		LastReviewAt:    now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.store.Create(ctx, line); err != nil {
		return nil, result, fmt.Errorf("failed to create credit line: %w", err)
	}

	// Set credit limit in ledger
	if err := s.ledger.SetCreditLimit(ctx, agentAddr, line.CreditLimit); err != nil {
		return nil, result, fmt.Errorf("failed to set credit limit in ledger: %w", err)
	}

	return line, result, nil
}

// Get returns a credit line by ID.
func (s *Service) Get(ctx context.Context, id string) (*CreditLine, error) {
	return s.store.Get(ctx, id)
}

// GetByAgent returns a credit line by agent address.
func (s *Service) GetByAgent(ctx context.Context, agentAddr string) (*CreditLine, error) {
	return s.store.GetByAgent(ctx, strings.ToLower(agentAddr))
}

// Repay processes a manual credit repayment.
func (s *Service) Repay(ctx context.Context, agentAddr, amount string) error {
	agentAddr = strings.ToLower(agentAddr)

	line, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return err
	}

	if line.Status != StatusActive && line.Status != StatusSuspended {
		return fmt.Errorf("credit line status %q does not allow repayment", line.Status)
	}

	return s.ledger.RepayCredit(ctx, agentAddr, amount)
}

// Review re-evaluates an agent's credit line and adjusts limits.
func (s *Service) Review(ctx context.Context, agentAddr string) (*CreditLine, *EvaluationResult, error) {
	agentAddr = strings.ToLower(agentAddr)

	line, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return nil, nil, err
	}

	if line.Status != StatusActive && line.Status != StatusSuspended {
		return nil, nil, fmt.Errorf("credit line status %q cannot be reviewed", line.Status)
	}

	// Re-evaluate
	score, tier, err := s.reputation.GetScore(ctx, agentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get reputation: %w", err)
	}

	totalTxns, successRate, daysOnNetwork, totalVolumeUSD, err := s.metrics.GetAgentMetrics(ctx, agentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	result := s.scorer.Evaluate(score, tier, totalTxns, successRate, daysOnNetwork, totalVolumeUSD)

	// Update credit line
	line.ReputationScore = score
	line.ReputationTier = tier
	line.LastReviewAt = time.Now()

	if result.Eligible {
		line.CreditLimit = fmt.Sprintf("%.6f", result.CreditLimit)
		line.InterestRate = result.InterestRate
		if line.Status == StatusSuspended {
			line.Status = StatusActive
		}
	} else {
		line.Status = StatusSuspended
	}

	if err := s.store.Update(ctx, line); err != nil {
		return nil, result, fmt.Errorf("failed to update credit line: %w", err)
	}

	// Update ledger credit limit
	if err := s.ledger.SetCreditLimit(ctx, agentAddr, line.CreditLimit); err != nil {
		return nil, result, fmt.Errorf("failed to update ledger credit limit: %w", err)
	}

	return line, result, nil
}

// Revoke permanently revokes an agent's credit line.
func (s *Service) Revoke(ctx context.Context, agentAddr, reason string) (*CreditLine, error) {
	agentAddr = strings.ToLower(agentAddr)

	line, err := s.store.GetByAgent(ctx, agentAddr)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	line.Status = StatusRevoked
	line.RevokedAt = now

	if err := s.store.Update(ctx, line); err != nil {
		return nil, fmt.Errorf("failed to revoke credit line: %w", err)
	}

	// Zero out credit limit in ledger
	if err := s.ledger.SetCreditLimit(ctx, agentAddr, "0"); err != nil {
		return nil, fmt.Errorf("failed to zero credit limit in ledger: %w", err)
	}

	return line, nil
}

// CheckDefaults scans for overdue credit lines (90+ days) and marks them as defaulted.
// It also re-evaluates all active credit lines, adjusting limits downward if
// an agent's reputation has dropped since the last review.
func (s *Service) CheckDefaults(ctx context.Context) (int, error) {
	overdue, err := s.store.ListOverdue(ctx, 90, 100)
	if err != nil {
		return 0, fmt.Errorf("failed to list overdue: %w", err)
	}

	defaulted := 0
	for _, line := range overdue {
		now := time.Now()
		line.Status = StatusDefaulted
		line.DefaultedAt = now

		if err := s.store.Update(ctx, line); err != nil {
			continue
		}

		// Zero out credit limit in ledger
		_ = s.ledger.SetCreditLimit(ctx, line.AgentAddr, "0")
		defaulted++
	}

	// Re-evaluate all active credit lines to catch reputation downgrades
	_ = s.ReviewAllActive(ctx)

	return defaulted, nil
}

// ReviewAllActive re-evaluates all active credit lines and adjusts limits
// based on current reputation. This catches reputation downgrades that
// should reduce or suspend a credit line.
func (s *Service) ReviewAllActive(ctx context.Context) error {
	lines, err := s.store.ListActive(ctx, 1000)
	if err != nil {
		return fmt.Errorf("failed to list active credit lines: %w", err)
	}

	for _, line := range lines {
		_, _, _ = s.Review(ctx, line.AgentAddr)
	}

	return nil
}

// ListActive returns all active credit lines.
func (s *Service) ListActive(ctx context.Context, limit int) ([]*CreditLine, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListActive(ctx, limit)
}
