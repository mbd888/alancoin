// Package contracts implements Agent Behavioral Contracts (ABC) for
// runtime enforcement of agent behavior during coalition escrow execution.
//
// A behavioral contract defines:
//   - Preconditions: must be true before execution starts
//   - Invariants: must remain true during execution (checked per-step)
//   - Recovery: what happens on violation (circuit-break, degrade, continue)
//
// Contracts gate coalition escrow settlement: payment releases only when
// the behavioral SLA is satisfied.
//
// Based on: arxiv:2602.22302 (Agent Behavioral Contracts, Feb 2026)
package contracts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
)

var (
	ErrContractNotFound     = errors.New("contracts: not found")
	ErrContractViolation    = errors.New("contracts: hard violation")
	ErrContractAlreadyBound = errors.New("contracts: already bound to an escrow")
	ErrPreconditionFailed   = errors.New("contracts: precondition check failed")
	ErrNoConditions         = errors.New("contracts: at least one condition is required")
)

// ConditionType identifies the kind of behavioral check.
type ConditionType string

const (
	CondMaxLatency       ConditionType = "max_latency"       // Max response time per step
	CondMaxTotalCost     ConditionType = "max_total_cost"    // Budget ceiling
	CondMaxStepCost      ConditionType = "max_step_cost"     // Per-step cost ceiling
	CondRateLimit        ConditionType = "rate_limit"        // Max requests per window
	CondNoPII            ConditionType = "no_pii"            // Output must not contain PII patterns
	CondOutputSchema     ConditionType = "output_schema"     // Output must match JSON schema
	CondAllowedEndpoints ConditionType = "allowed_endpoints" // Restrict callable URLs
	CondCustom           ConditionType = "custom"            // User-defined check expression
)

// Severity determines the response to a violation.
type Severity string

const (
	SeverityHard Severity = "hard" // Immediate circuit break, abort escrow
	SeveritySoft Severity = "soft" // Log violation, reduce quality score, continue
)

// Condition defines a single behavioral constraint.
type Condition struct {
	Type     ConditionType          `json:"type"`
	Severity Severity               `json:"severity"`
	Params   map[string]interface{} `json:"params"`
}

// RecoveryAction defines what happens when a contract is violated.
type RecoveryAction string

const (
	RecoveryAbort   RecoveryAction = "abort"   // Abort coalition, full refund
	RecoveryDegrade RecoveryAction = "degrade" // Reduce quality score, continue
	RecoveryAlert   RecoveryAction = "alert"   // Log and alert, continue
)

// Violation records a single contract violation event.
type Violation struct {
	ConditionType ConditionType `json:"conditionType"`
	Severity      Severity      `json:"severity"`
	MemberAddr    string        `json:"memberAddr"`
	Message       string        `json:"message"`
	OccurredAt    time.Time     `json:"occurredAt"`
	StepIndex     int           `json:"stepIndex"`
}

// ContractStatus represents the lifecycle state.
type ContractStatus string

const (
	StatusDraft    ContractStatus = "draft"    // Created, not yet bound
	StatusActive   ContractStatus = "active"   // Bound to escrow, monitoring
	StatusPassed   ContractStatus = "passed"   // All conditions satisfied
	StatusViolated ContractStatus = "violated" // Hard violation triggered
	StatusExpired  ContractStatus = "expired"  // Escrow completed, contract archived
)

// Contract defines a behavioral SLA for a coalition escrow.
type Contract struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description,omitempty"`
	Preconditions  []Condition    `json:"preconditions"`
	Invariants     []Condition    `json:"invariants"`
	Recovery       RecoveryAction `json:"recovery"`
	Status         ContractStatus `json:"status"`
	BoundEscrowID  string         `json:"boundEscrowId,omitempty"`
	Violations     []Violation    `json:"violations,omitempty"`
	SoftViolations int            `json:"softViolations"`
	HardViolations int            `json:"hardViolations"`
	QualityPenalty float64        `json:"qualityPenalty"` // Cumulative score reduction (0.0–1.0)
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

// IsTerminal returns true if the contract is in a final state.
func (c *Contract) IsTerminal() bool {
	switch c.Status {
	case StatusPassed, StatusViolated, StatusExpired:
		return true
	}
	return false
}

// EffectiveQualityScore applies the penalty to a raw quality score.
func (c *Contract) EffectiveQualityScore(rawScore float64) float64 {
	adjusted := rawScore - c.QualityPenalty
	if adjusted < 0 {
		return 0
	}
	return adjusted
}

// CreateContractRequest is the input for creating a behavioral contract.
type CreateContractRequest struct {
	Name          string         `json:"name" binding:"required"`
	Description   string         `json:"description"`
	Preconditions []Condition    `json:"preconditions"`
	Invariants    []Condition    `json:"invariants" binding:"required"`
	Recovery      RecoveryAction `json:"recovery" binding:"required"`
}

// CheckInvariantRequest is the input for checking an invariant during execution.
type CheckInvariantRequest struct {
	MemberAddr    string                 `json:"memberAddr" binding:"required"`
	StepIndex     int                    `json:"stepIndex"`
	LatencyMs     float64                `json:"latencyMs,omitempty"`
	StepCost      string                 `json:"stepCost,omitempty"`
	TotalCost     string                 `json:"totalCost,omitempty"`
	OutputPayload string                 `json:"outputPayload,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// Store persists contract data.
type Store interface {
	Create(ctx context.Context, c *Contract) error
	Get(ctx context.Context, id string) (*Contract, error)
	Update(ctx context.Context, c *Contract) error
	GetByEscrow(ctx context.Context, escrowID string) (*Contract, error)
}

// Service implements behavioral contract business logic.
type Service struct {
	store  Store
	logger *slog.Logger
	locks  sync.Map
}

// NewService creates a new contract service.
func NewService(store Store) *Service {
	return &Service{
		store:  store,
		logger: slog.Default(),
	}
}

// WithLogger sets a structured logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

func (s *Service) contractLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Service) cleanupLock(id string) {
	s.locks.Delete(id)
}

// Create creates a new behavioral contract in draft state.
func (s *Service) Create(ctx context.Context, req CreateContractRequest) (*Contract, error) {
	if len(req.Invariants) == 0 && len(req.Preconditions) == 0 {
		return nil, ErrNoConditions
	}

	switch req.Recovery {
	case RecoveryAbort, RecoveryDegrade, RecoveryAlert:
		// ok
	default:
		return nil, fmt.Errorf("invalid recovery action: %q", req.Recovery)
	}

	// Validate condition types
	for _, c := range req.Preconditions {
		if err := validateCondition(c); err != nil {
			return nil, fmt.Errorf("precondition: %w", err)
		}
	}
	for _, c := range req.Invariants {
		if err := validateCondition(c); err != nil {
			return nil, fmt.Errorf("invariant: %w", err)
		}
	}

	now := time.Now()
	contract := &Contract{
		ID:            idgen.WithPrefix("abc_"),
		Name:          req.Name,
		Description:   req.Description,
		Preconditions: req.Preconditions,
		Invariants:    req.Invariants,
		Recovery:      req.Recovery,
		Status:        StatusDraft,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.store.Create(ctx, contract); err != nil {
		return nil, err
	}
	return contract, nil
}

// BindToEscrow activates the contract and binds it to a coalition escrow.
func (s *Service) BindToEscrow(ctx context.Context, contractID, escrowID string) (*Contract, error) {
	mu := s.contractLock(contractID)
	mu.Lock()
	defer mu.Unlock()

	c, err := s.store.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}

	if c.Status != StatusDraft {
		return nil, fmt.Errorf("contract must be in draft state to bind, current: %s", c.Status)
	}
	if c.BoundEscrowID != "" {
		return nil, ErrContractAlreadyBound
	}

	c.Status = StatusActive
	c.BoundEscrowID = escrowID
	c.UpdatedAt = time.Now()

	if err := s.store.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// CheckInvariant evaluates all invariants against a step execution report.
// Returns nil if all checks pass. Returns ErrContractViolation on hard violation.
// Soft violations are recorded but execution continues.
func (s *Service) CheckInvariant(ctx context.Context, contractID string, req CheckInvariantRequest) (*Contract, error) {
	mu := s.contractLock(contractID)
	mu.Lock()
	defer mu.Unlock()

	c, err := s.store.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}

	if c.Status != StatusActive {
		return nil, fmt.Errorf("contract not active: %s", c.Status)
	}

	memberAddr := strings.ToLower(req.MemberAddr)
	var hardViolation bool

	for _, inv := range c.Invariants {
		violated, msg := evaluateCondition(inv, req)
		if !violated {
			continue
		}

		v := Violation{
			ConditionType: inv.Type,
			Severity:      inv.Severity,
			MemberAddr:    memberAddr,
			Message:       msg,
			OccurredAt:    time.Now(),
			StepIndex:     req.StepIndex,
		}
		// Cap violations list to prevent unbounded growth from repeated checks.
		if len(c.Violations) < 1000 {
			c.Violations = append(c.Violations, v)
		}

		if inv.Severity == SeverityHard {
			c.HardViolations++
			hardViolation = true
			s.logger.Warn("contract hard violation",
				"contract_id", c.ID, "type", inv.Type, "member", memberAddr, "message", msg)
		} else {
			c.SoftViolations++
			// Each soft violation applies a 0.05 quality penalty (capped at 1.0)
			c.QualityPenalty += 0.05
			if c.QualityPenalty > 1.0 {
				c.QualityPenalty = 1.0
			}
			s.logger.Info("contract soft violation",
				"contract_id", c.ID, "type", inv.Type, "member", memberAddr,
				"penalty", c.QualityPenalty, "message", msg)
		}
	}

	if hardViolation {
		c.Status = StatusViolated
		c.UpdatedAt = time.Now()
		if err := s.store.Update(ctx, c); err != nil {
			return nil, err
		}
		s.cleanupLock(contractID)
		return c, ErrContractViolation
	}

	c.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// MarkPassed transitions contract to passed state.
func (s *Service) MarkPassed(ctx context.Context, contractID string) (*Contract, error) {
	mu := s.contractLock(contractID)
	mu.Lock()
	defer mu.Unlock()

	c, err := s.store.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}

	if c.Status != StatusActive {
		return nil, fmt.Errorf("contract not active: %s", c.Status)
	}

	c.Status = StatusPassed
	c.UpdatedAt = time.Now()
	if err := s.store.Update(ctx, c); err != nil {
		return nil, err
	}
	s.cleanupLock(contractID)
	return c, nil
}

// Get returns a contract by ID.
func (s *Service) Get(ctx context.Context, id string) (*Contract, error) {
	return s.store.Get(ctx, id)
}

// GetByEscrow returns the contract bound to a coalition escrow.
func (s *Service) GetByEscrow(ctx context.Context, escrowID string) (*Contract, error) {
	return s.store.GetByEscrow(ctx, escrowID)
}

// GetAuditTrail returns a structured audit trail for compliance reporting.
func (s *Service) GetAuditTrail(ctx context.Context, contractID string) (*AuditTrail, error) {
	c, err := s.store.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}

	return &AuditTrail{
		ContractID:     c.ID,
		ContractName:   c.Name,
		EscrowID:       c.BoundEscrowID,
		Status:         c.Status,
		TotalChecks:    c.SoftViolations + c.HardViolations + len(c.Invariants),
		SoftViolations: c.SoftViolations,
		HardViolations: c.HardViolations,
		QualityPenalty: c.QualityPenalty,
		Violations:     c.Violations,
		Preconditions:  c.Preconditions,
		Invariants:     c.Invariants,
		Recovery:       c.Recovery,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}, nil
}

// AuditTrail is a structured compliance report for a behavioral contract.
type AuditTrail struct {
	ContractID     string         `json:"contractId"`
	ContractName   string         `json:"contractName"`
	EscrowID       string         `json:"escrowId"`
	Status         ContractStatus `json:"status"`
	TotalChecks    int            `json:"totalChecks"`
	SoftViolations int            `json:"softViolations"`
	HardViolations int            `json:"hardViolations"`
	QualityPenalty float64        `json:"qualityPenalty"`
	Violations     []Violation    `json:"violations"`
	Preconditions  []Condition    `json:"preconditions"`
	Invariants     []Condition    `json:"invariants"`
	Recovery       RecoveryAction `json:"recovery"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

// --- Condition evaluation ---

func validateCondition(c Condition) error {
	switch c.Type {
	case CondMaxLatency, CondMaxTotalCost, CondMaxStepCost, CondRateLimit,
		CondNoPII, CondOutputSchema, CondAllowedEndpoints, CondCustom:
		// ok
	default:
		return fmt.Errorf("unknown condition type: %q", c.Type)
	}
	switch c.Severity {
	case SeverityHard, SeveritySoft:
		// ok
	default:
		return fmt.Errorf("unknown severity: %q", c.Severity)
	}
	return nil
}

// evaluateCondition checks a single condition against execution data.
// Returns (violated bool, message string).
func evaluateCondition(cond Condition, req CheckInvariantRequest) (bool, string) {
	switch cond.Type {
	case CondMaxLatency:
		maxMs, ok := condParamFloat(cond, "maxMs")
		if !ok {
			return false, ""
		}
		if req.LatencyMs > maxMs {
			return true, fmt.Sprintf("latency %.0fms exceeds max %.0fms", req.LatencyMs, maxMs)
		}

	case CondMaxStepCost:
		maxCost, ok := condParamString(cond, "maxCost")
		if !ok || req.StepCost == "" {
			return false, ""
		}
		if compareCost(req.StepCost, maxCost) > 0 {
			return true, fmt.Sprintf("step cost %s exceeds max %s", req.StepCost, maxCost)
		}

	case CondMaxTotalCost:
		maxCost, ok := condParamString(cond, "maxCost")
		if !ok || req.TotalCost == "" {
			return false, ""
		}
		if compareCost(req.TotalCost, maxCost) > 0 {
			return true, fmt.Sprintf("total cost %s exceeds max %s", req.TotalCost, maxCost)
		}

	case CondNoPII:
		if req.OutputPayload == "" {
			return false, ""
		}
		lower := strings.ToLower(req.OutputPayload)
		patterns := []string{"ssn", "social security", "credit card", "passport"}
		for _, p := range patterns {
			if strings.Contains(lower, p) {
				return true, fmt.Sprintf("output contains PII pattern: %q", p)
			}
		}

	case CondOutputSchema:
		// Schema validation would use a JSON schema library.
		// For now, check that output is non-empty if required.
		required, _ := condParamFloat(cond, "required")
		if required > 0 && req.OutputPayload == "" {
			return true, "output payload is required but empty"
		}

	case CondRateLimit:
		// Rate limiting is enforced externally by the rate limiter middleware.
		// This condition acts as a declaration for the audit trail.
		return false, ""

	case CondAllowedEndpoints:
		// Endpoint restriction is enforced at the gateway proxy level.
		return false, ""

	case CondCustom:
		// Custom conditions are evaluated externally.
		return false, ""
	}

	return false, ""
}

// compareCost compares two USDC amount strings numerically.
// Returns -1, 0, or 1 like big.Int.Cmp.
func compareCost(a, b string) int {
	af, aErr := strconv.ParseFloat(a, 64)
	bf, bErr := strconv.ParseFloat(b, 64)
	if aErr != nil || bErr != nil {
		// Fallback to string comparison if parsing fails (shouldn't happen
		// with validated USDC amounts, but safe).
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
	if af < bf {
		return -1
	}
	if af > bf {
		return 1
	}
	return 0
}

func condParamFloat(c Condition, key string) (float64, bool) {
	v, ok := c.Params[key]
	if !ok {
		return 0, false
	}
	switch f := v.(type) {
	case float64:
		return f, true
	case int:
		return float64(f), true
	case int64:
		return float64(f), true
	}
	return 0, false
}

func condParamString(c Condition, key string) (string, bool) {
	v, ok := c.Params[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
