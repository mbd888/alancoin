// Package workflows provides budgeted multi-agent workflow management.
//
// A workflow is a single API concept that ties together:
//   - A hard budget cap (ledger hold)
//   - Per-step cost tracking with attribution
//   - Behavioral circuit breakers (max cost per step, max velocity)
//   - A compliance-ready audit trail
//
// This is the integration point for LangGraph, CrewAI, and AutoGen pipelines.
// One workflow = one budgeted agent pipeline with guaranteed cost control.
//
// Flow:
//  1. Create workflow with budget cap and step definitions
//  2. Start each step (validates budget remaining)
//  3. Complete each step with actual cost (records attribution)
//  4. Query cost breakdown and audit trail at any time
//  5. Workflow auto-closes when all steps complete or budget exhausted
package workflows

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/metrics"
	"github.com/mbd888/alancoin/internal/retry"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

var (
	ErrWorkflowNotFound   = errors.New("workflows: not found")
	ErrWorkflowCompleted  = errors.New("workflows: already completed")
	ErrWorkflowAborted    = errors.New("workflows: aborted")
	ErrBudgetExceeded     = errors.New("workflows: budget exceeded")
	ErrStepBudgetExceeded = errors.New("workflows: step budget exceeded")
	ErrStepNotFound       = errors.New("workflows: step not found")
	ErrStepAlreadyStarted = errors.New("workflows: step already started")
	ErrStepNotStarted     = errors.New("workflows: step not started")
	ErrStepAlreadyDone    = errors.New("workflows: step already completed")
	ErrVelocityBreaker    = errors.New("workflows: spend velocity circuit breaker triggered")
	ErrUnauthorized       = errors.New("workflows: not authorized")
	ErrInvalidAmount      = errors.New("workflows: invalid amount")
)

// WorkflowStatus represents the lifecycle state.
type WorkflowStatus string

const (
	WSActive    WorkflowStatus = "active"
	WSCompleted WorkflowStatus = "completed"
	WSAborted   WorkflowStatus = "aborted"
	WSBreaker   WorkflowStatus = "circuit_broken"
)

// StepStatus represents a step's lifecycle.
type StepStatus string

const (
	SSPending StepStatus = "pending"
	SSRunning StepStatus = "running"
	SSDone    StepStatus = "done"
	SSFailed  StepStatus = "failed"
	SSSkipped StepStatus = "skipped"
)

// StepDefinition defines a step in the workflow at creation time.
type StepDefinition struct {
	Name        string `json:"name"`
	AgentAddr   string `json:"agentAddr"`
	MaxCost     string `json:"maxCost"` // Per-step budget cap
	ServiceType string `json:"serviceType,omitempty"`
}

// WorkflowStep tracks a step's execution and cost.
type WorkflowStep struct {
	Name        string     `json:"name"`
	AgentAddr   string     `json:"agentAddr"`
	MaxCost     string     `json:"maxCost"`
	ActualCost  string     `json:"actualCost"`
	Status      StepStatus `json:"status"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	ServiceType string     `json:"serviceType,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// AuditEntry is a tamper-evident log entry for compliance.
type AuditEntry struct {
	Seq       int       `json:"seq"`
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"` // "workflow_created", "step_started", "step_completed", etc.
	Actor     string    `json:"actor"`
	StepName  string    `json:"stepName,omitempty"`
	Amount    string    `json:"amount,omitempty"`
	Details   string    `json:"details,omitempty"`
	Hash      string    `json:"hash"` // SHA-256 chain: hash(prevHash + entry)
}

// Workflow is a budgeted multi-agent workflow with cost attribution.
type Workflow struct {
	ID           string         `json:"id"`
	OwnerAddr    string         `json:"ownerAddr"`
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	BudgetTotal  string         `json:"budgetTotal"`
	BudgetSpent  string         `json:"budgetSpent"`
	BudgetRemain string         `json:"budgetRemaining"`
	Steps        []WorkflowStep `json:"steps"`
	Status       WorkflowStatus `json:"status"`
	EscrowRef    string         `json:"escrowRef"`
	AuditTrail   []AuditEntry   `json:"auditTrail"`
	StepsTotal   int            `json:"stepsTotal"`
	StepsDone    int            `json:"stepsDone"`

	// Circuit breaker config
	MaxCostPerStep string  `json:"maxCostPerStep,omitempty"` // Global per-step cap
	MaxVelocity    float64 `json:"maxVelocity,omitempty"`    // Max USDC/minute spend rate

	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	ClosedAt  *time.Time `json:"closedAt,omitempty"`
}

// IsTerminal returns true if the workflow is in a final state.
func (w *Workflow) IsTerminal() bool {
	return w.Status == WSCompleted || w.Status == WSAborted || w.Status == WSBreaker
}

// CostReport returns a structured cost attribution report.
func (w *Workflow) CostReport() *CostReport {
	report := &CostReport{
		WorkflowID:   w.ID,
		WorkflowName: w.Name,
		BudgetTotal:  w.BudgetTotal,
		BudgetSpent:  w.BudgetSpent,
		BudgetRemain: w.BudgetRemain,
		Status:       w.Status,
		StepsTotal:   w.StepsTotal,
		StepsDone:    w.StepsDone,
		StepCosts:    make([]StepCostEntry, 0, len(w.Steps)),
		GeneratedAt:  time.Now(),
	}
	for _, s := range w.Steps {
		report.StepCosts = append(report.StepCosts, StepCostEntry{
			Name:       s.Name,
			AgentAddr:  s.AgentAddr,
			MaxCost:    s.MaxCost,
			ActualCost: s.ActualCost,
			Status:     s.Status,
		})
	}
	return report
}

// CostReport is a structured cost attribution report for CFO/CDAO dashboards.
type CostReport struct {
	WorkflowID   string          `json:"workflowId"`
	WorkflowName string          `json:"workflowName"`
	BudgetTotal  string          `json:"budgetTotal"`
	BudgetSpent  string          `json:"budgetSpent"`
	BudgetRemain string          `json:"budgetRemaining"`
	Status       WorkflowStatus  `json:"status"`
	StepsTotal   int             `json:"stepsTotal"`
	StepsDone    int             `json:"stepsDone"`
	StepCosts    []StepCostEntry `json:"stepCosts"`
	GeneratedAt  time.Time       `json:"generatedAt"`
}

// StepCostEntry is a single step's cost in the attribution report.
type StepCostEntry struct {
	Name       string     `json:"name"`
	AgentAddr  string     `json:"agentAddr"`
	MaxCost    string     `json:"maxCost"`
	ActualCost string     `json:"actualCost"`
	Status     StepStatus `json:"status"`
}

// CreateWorkflowRequest is the input for creating a budgeted workflow.
type CreateWorkflowRequest struct {
	Name           string           `json:"name" binding:"required"`
	Description    string           `json:"description"`
	BudgetTotal    string           `json:"budgetTotal" binding:"required"`
	Steps          []StepDefinition `json:"steps" binding:"required"`
	MaxCostPerStep string           `json:"maxCostPerStep,omitempty"`
	MaxVelocity    float64          `json:"maxVelocity,omitempty"` // Max USDC/minute
}

// LedgerService abstracts ledger operations.
type LedgerService interface {
	EscrowLock(ctx context.Context, agentAddr, amount, reference string) error
	ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error
	RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error
}

// Store persists workflow data.
type Store interface {
	Create(ctx context.Context, w *Workflow) error
	Get(ctx context.Context, id string) (*Workflow, error)
	Update(ctx context.Context, w *Workflow) error
	ListByOwner(ctx context.Context, ownerAddr string, limit int) ([]*Workflow, error)
}

// Service implements workflow business logic.
type Service struct {
	store   Store
	ledger  LedgerService
	logger  *slog.Logger
	hmacKey []byte // HMAC key for tamper-evident audit trail
	locks   sync.Map
}

// NewService creates a new workflow service.
func NewService(store Store, ledger LedgerService) *Service {
	// Generate a random HMAC key if none provided. In production, inject
	// via WithHMACKey from config.
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return &Service{
		store:   store,
		ledger:  ledger,
		hmacKey: key,
		logger:  slog.Default(),
	}
}

// WithHMACKey sets the HMAC key for tamper-evident audit trail sealing.
func (s *Service) WithHMACKey(key []byte) *Service {
	s.hmacKey = key
	return s
}

// WithLogger sets a structured logger.
func (s *Service) WithLogger(l *slog.Logger) *Service {
	s.logger = l
	return s
}

func (s *Service) wfLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *Service) cleanupLock(id string) {
	s.locks.Delete(id)
}

// Create creates a new budgeted workflow and locks the budget.
func (s *Service) Create(ctx context.Context, ownerAddr string, req CreateWorkflowRequest) (*Workflow, error) {
	ctx, span := traces.StartSpan(ctx, "workflows.Create",
		attribute.String("owner", ownerAddr),
		attribute.String("budget", req.BudgetTotal),
		attribute.Int("steps", len(req.Steps)),
	)
	defer span.End()

	if err := validateAmount(req.BudgetTotal); err != nil {
		return nil, err
	}
	if len(req.Steps) == 0 {
		return nil, errors.New("at least one step is required")
	}
	if len(req.Steps) > 100 {
		return nil, errors.New("maximum 100 steps per workflow")
	}

	// Validate per-step budget caps sum doesn't exceed total
	totalBig, _ := usdc.Parse(req.BudgetTotal)
	stepSum := new(big.Int)
	steps := make([]WorkflowStep, len(req.Steps))
	for i, sd := range req.Steps {
		if sd.Name == "" {
			return nil, fmt.Errorf("step %d: name is required", i)
		}
		if sd.AgentAddr == "" {
			return nil, fmt.Errorf("step %d: agentAddr is required", i)
		}
		maxCost := sd.MaxCost
		if maxCost == "" {
			maxCost = req.BudgetTotal // Default: step can use full budget
		}
		if err := validateAmount(maxCost); err != nil {
			return nil, fmt.Errorf("step %d: %w", i, err)
		}
		mc, _ := usdc.Parse(maxCost)
		stepSum.Add(stepSum, mc)

		steps[i] = WorkflowStep{
			Name:        sd.Name,
			AgentAddr:   strings.ToLower(sd.AgentAddr),
			MaxCost:     maxCost,
			ActualCost:  "0.000000",
			Status:      SSPending,
			ServiceType: sd.ServiceType,
		}
	}

	// Step sum can exceed total (steps share the budget), but warn
	if stepSum.Cmp(totalBig) > 0 {
		s.logger.Info("workflow step budgets exceed total (shared budget model)",
			"total", req.BudgetTotal, "step_sum", usdc.Format(stepSum))
	}

	now := time.Now()
	wf := &Workflow{
		ID:             idgen.WithPrefix("wfl_"),
		OwnerAddr:      strings.ToLower(ownerAddr),
		Name:           req.Name,
		Description:    req.Description,
		BudgetTotal:    req.BudgetTotal,
		BudgetSpent:    "0.000000",
		BudgetRemain:   req.BudgetTotal,
		Steps:          steps,
		Status:         WSActive,
		StepsTotal:     len(steps),
		MaxCostPerStep: req.MaxCostPerStep,
		MaxVelocity:    req.MaxVelocity,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	wf.EscrowRef = "wf:" + wf.ID

	// Lock budget in escrow
	if err := s.ledger.EscrowLock(ctx, wf.OwnerAddr, wf.BudgetTotal, wf.EscrowRef); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to lock workflow budget")
		return nil, fmt.Errorf("failed to lock workflow budget: %w", err)
	}

	// Initialize audit trail with genesis entry
	wf.AuditTrail = []AuditEntry{
		s.auditEntry(wf, "workflow_created", wf.OwnerAddr, "", req.BudgetTotal,
			fmt.Sprintf("budget=%s steps=%d", req.BudgetTotal, len(steps))),
	}

	if err := s.store.Create(ctx, wf); err != nil {
		if refundErr := s.ledger.RefundEscrow(ctx, wf.OwnerAddr, wf.BudgetTotal, wf.EscrowRef); refundErr != nil {
			s.logger.Error("CRITICAL: workflow store create failed and refund also failed — funds stuck in escrow",
				"workflow_id", wf.ID, "owner", wf.OwnerAddr, "amount", wf.BudgetTotal,
				"escrow_ref", wf.EscrowRef, "store_error", err, "refund_error", refundErr,
				"action", "manual_review_needed")
		}
		return nil, err
	}

	metrics.WorkflowCreatedTotal.Inc()
	return wf, nil
}

// StartStep marks a step as running. Validates the workflow has budget remaining.
func (s *Service) StartStep(ctx context.Context, wfID, stepName, callerAddr string) (*Workflow, error) {
	ctx, span := traces.StartSpan(ctx, "workflows.StartStep",
		attribute.String("workflow_id", wfID),
		attribute.String("step", stepName),
	)
	defer span.End()

	mu := s.wfLock(wfID)
	mu.Lock()
	defer mu.Unlock()

	wf, err := s.store.Get(ctx, wfID)
	if err != nil {
		return nil, err
	}
	if wf.IsTerminal() {
		return nil, ErrWorkflowCompleted
	}
	if strings.ToLower(callerAddr) != wf.OwnerAddr {
		return nil, ErrUnauthorized
	}

	stepIdx := s.findStep(wf, stepName)
	if stepIdx < 0 {
		return nil, ErrStepNotFound
	}
	step := &wf.Steps[stepIdx]
	if step.Status != SSPending {
		return nil, ErrStepAlreadyStarted
	}

	// Check budget remaining
	remaining, _ := usdc.Parse(wf.BudgetRemain)
	if remaining.Sign() <= 0 {
		return nil, ErrBudgetExceeded
	}

	now := time.Now()
	step.Status = SSRunning
	step.StartedAt = &now
	wf.UpdatedAt = now

	wf.AuditTrail = append(wf.AuditTrail, s.auditEntry(wf, "step_started", callerAddr, stepName, "", ""))

	if err := s.store.Update(ctx, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// CompleteStep records the actual cost of a step, pays the agent, and updates the budget.
func (s *Service) CompleteStep(ctx context.Context, wfID, stepName, callerAddr, actualCost string) (*Workflow, error) {
	ctx, span := traces.StartSpan(ctx, "workflows.CompleteStep",
		attribute.String("workflow_id", wfID),
		attribute.String("step", stepName),
		attribute.String("cost", actualCost),
	)
	defer span.End()

	if err := validateAmount(actualCost); err != nil {
		return nil, err
	}

	mu := s.wfLock(wfID)
	mu.Lock()
	defer mu.Unlock()

	wf, err := s.store.Get(ctx, wfID)
	if err != nil {
		return nil, err
	}
	if wf.IsTerminal() {
		return nil, ErrWorkflowCompleted
	}
	if strings.ToLower(callerAddr) != wf.OwnerAddr {
		return nil, ErrUnauthorized
	}

	stepIdx := s.findStep(wf, stepName)
	if stepIdx < 0 {
		return nil, ErrStepNotFound
	}
	step := &wf.Steps[stepIdx]
	if step.Status == SSDone || step.Status == SSFailed {
		return nil, ErrStepAlreadyDone
	}
	if step.Status == SSPending {
		return nil, ErrStepNotStarted
	}

	costBig, _ := usdc.Parse(actualCost)

	// Check per-step budget
	maxStepBig, _ := usdc.Parse(step.MaxCost)
	if costBig.Cmp(maxStepBig) > 0 {
		return nil, fmt.Errorf("%w: step %q cost %s exceeds max %s", ErrStepBudgetExceeded, stepName, actualCost, step.MaxCost)
	}

	// Check global per-step cap
	if wf.MaxCostPerStep != "" {
		globalMax, _ := usdc.Parse(wf.MaxCostPerStep)
		if costBig.Cmp(globalMax) > 0 {
			return nil, fmt.Errorf("%w: step %q cost %s exceeds global max %s", ErrStepBudgetExceeded, stepName, actualCost, wf.MaxCostPerStep)
		}
	}

	// Check total budget
	remainBig, _ := usdc.Parse(wf.BudgetRemain)
	if costBig.Cmp(remainBig) > 0 {
		return nil, fmt.Errorf("%w: step %q cost %s exceeds remaining budget %s", ErrBudgetExceeded, stepName, actualCost, wf.BudgetRemain)
	}

	// Check velocity circuit breaker
	if wf.MaxVelocity > 0 {
		if err := s.checkVelocity(wf, costBig); err != nil {
			wf.Status = WSBreaker
			now := time.Now()
			step.Status = SSFailed
			step.Error = err.Error()
			wf.ClosedAt = &now
			wf.UpdatedAt = now
			wf.AuditTrail = append(wf.AuditTrail, s.auditEntry(wf, "circuit_broken", callerAddr, stepName, actualCost, err.Error()))
			if updateErr := s.store.Update(ctx, wf); updateErr != nil {
				s.logger.Error("workflow circuit breaker state update failed",
					"workflow_id", wfID, "step", stepName, "error", updateErr)
			}
			metrics.WorkflowBreakerTotal.Inc()
			s.cleanupLock(wfID)
			return wf, err
		}
	}

	// Release step cost from escrow to the step's agent (with retry for transient failures)
	stepRef := wf.EscrowRef + ":step:" + stepName
	if err := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
		return s.ledger.ReleaseEscrow(ctx, wf.OwnerAddr, step.AgentAddr, actualCost, stepRef)
	}); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to release step payment: %w", err)
	}

	// Update budgets
	spentBig, _ := usdc.Parse(wf.BudgetSpent)
	newSpent := new(big.Int).Add(spentBig, costBig)
	newRemain := new(big.Int).Sub(remainBig, costBig)

	now := time.Now()
	step.ActualCost = actualCost
	step.Status = SSDone
	step.CompletedAt = &now
	wf.BudgetSpent = usdc.Format(newSpent)
	wf.BudgetRemain = usdc.Format(newRemain)
	wf.StepsDone++
	wf.UpdatedAt = now

	wf.AuditTrail = append(wf.AuditTrail, s.auditEntry(wf, "step_completed", callerAddr, stepName, actualCost,
		fmt.Sprintf("agent=%s spent=%s remaining=%s", step.AgentAddr, wf.BudgetSpent, wf.BudgetRemain)))

	// Auto-complete if all steps done
	if wf.StepsDone >= wf.StepsTotal {
		wf.Status = WSCompleted
		wf.ClosedAt = &now

		// Refund remaining budget
		if newRemain.Sign() > 0 {
			refRef := wf.EscrowRef + ":refund"
			if err := s.ledger.RefundEscrow(ctx, wf.OwnerAddr, usdc.Format(newRemain), refRef); err != nil {
				s.logger.Error("failed to refund workflow remainder",
					"workflow_id", wf.ID, "amount", usdc.Format(newRemain), "error", err)
			}
		}

		wf.AuditTrail = append(wf.AuditTrail, s.auditEntry(wf, "workflow_completed", callerAddr, "", wf.BudgetSpent,
			fmt.Sprintf("total_spent=%s refunded=%s", wf.BudgetSpent, wf.BudgetRemain)))

		metrics.WorkflowCompletedTotal.Inc()
		s.cleanupLock(wfID)
	}

	if err := s.store.Update(ctx, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// FailStep marks a step as failed without releasing funds.
func (s *Service) FailStep(ctx context.Context, wfID, stepName, callerAddr, reason string) (*Workflow, error) {
	mu := s.wfLock(wfID)
	mu.Lock()
	defer mu.Unlock()

	wf, err := s.store.Get(ctx, wfID)
	if err != nil {
		return nil, err
	}
	if wf.IsTerminal() {
		return nil, ErrWorkflowCompleted
	}
	if strings.ToLower(callerAddr) != wf.OwnerAddr {
		return nil, ErrUnauthorized
	}

	stepIdx := s.findStep(wf, stepName)
	if stepIdx < 0 {
		return nil, ErrStepNotFound
	}
	step := &wf.Steps[stepIdx]
	if step.Status == SSDone || step.Status == SSFailed {
		return nil, ErrStepAlreadyDone
	}

	now := time.Now()
	step.Status = SSFailed
	step.Error = reason
	step.CompletedAt = &now
	wf.StepsDone++
	wf.UpdatedAt = now

	wf.AuditTrail = append(wf.AuditTrail, s.auditEntry(wf, "step_failed", callerAddr, stepName, "", reason))

	// Auto-complete if all steps done (even if some failed)
	if wf.StepsDone >= wf.StepsTotal {
		wf.Status = WSCompleted
		wf.ClosedAt = &now
		remainBig, _ := usdc.Parse(wf.BudgetRemain)
		if remainBig.Sign() > 0 {
			refRef := wf.EscrowRef + ":refund"
			if refundErr := s.ledger.RefundEscrow(ctx, wf.OwnerAddr, wf.BudgetRemain, refRef); refundErr != nil {
				s.logger.Error("CRITICAL: workflow completion refund failed — funds stuck in escrow",
					"workflow_id", wf.ID, "owner", wf.OwnerAddr, "amount", wf.BudgetRemain,
					"escrow_ref", refRef, "error", refundErr,
					"action", "manual_review_needed")
			}
		}
		s.cleanupLock(wfID)
	}

	if err := s.store.Update(ctx, wf); err != nil {
		return nil, err
	}
	return wf, nil
}

// Abort cancels the workflow and refunds remaining budget.
func (s *Service) Abort(ctx context.Context, wfID, callerAddr string) (*Workflow, error) {
	mu := s.wfLock(wfID)
	mu.Lock()
	defer mu.Unlock()

	wf, err := s.store.Get(ctx, wfID)
	if err != nil {
		return nil, err
	}
	if wf.IsTerminal() {
		return nil, ErrWorkflowCompleted
	}
	if strings.ToLower(callerAddr) != wf.OwnerAddr {
		return nil, ErrUnauthorized
	}

	remainBig, _ := usdc.Parse(wf.BudgetRemain)
	if remainBig.Sign() > 0 {
		refRef := wf.EscrowRef + ":abort"
		if err := s.ledger.RefundEscrow(ctx, wf.OwnerAddr, wf.BudgetRemain, refRef); err != nil {
			return nil, fmt.Errorf("failed to refund: %w", err)
		}
	}

	now := time.Now()
	wf.Status = WSAborted
	wf.ClosedAt = &now
	wf.UpdatedAt = now

	wf.AuditTrail = append(wf.AuditTrail, s.auditEntry(wf, "workflow_aborted", callerAddr, "", wf.BudgetRemain, "manual abort"))

	if err := s.store.Update(ctx, wf); err != nil {
		return nil, err
	}
	metrics.WorkflowAbortedTotal.Inc()
	s.cleanupLock(wfID)
	return wf, nil
}

// Get returns a workflow by ID.
func (s *Service) Get(ctx context.Context, id string) (*Workflow, error) {
	return s.store.Get(ctx, id)
}

// GetCostReport returns the cost attribution report.
func (s *Service) GetCostReport(ctx context.Context, id string) (*CostReport, error) {
	wf, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return wf.CostReport(), nil
}

// GetAuditTrail returns the compliance audit trail.
func (s *Service) GetAuditTrail(ctx context.Context, id string) ([]AuditEntry, error) {
	wf, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return wf.AuditTrail, nil
}

// ListByOwner returns workflows for an owner.
func (s *Service) ListByOwner(ctx context.Context, ownerAddr string, limit int) ([]*Workflow, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByOwner(ctx, strings.ToLower(ownerAddr), limit)
}

// --- Helpers ---

func (s *Service) findStep(wf *Workflow, name string) int {
	for i, step := range wf.Steps {
		if step.Name == name {
			return i
		}
	}
	return -1
}

// checkVelocity computes spend rate from the persisted audit trail (survives
// restarts) rather than an in-memory window. Sums amounts from all
// "step_completed" entries in the last 60 seconds plus the new cost.
func (s *Service) checkVelocity(wf *Workflow, newCost *big.Int) error {
	cutoff := time.Now().Add(-1 * time.Minute)
	var windowTotal big.Int
	for _, entry := range wf.AuditTrail {
		if entry.Action == "step_completed" && entry.Timestamp.After(cutoff) && entry.Amount != "" {
			amt, ok := usdc.Parse(entry.Amount)
			if ok {
				windowTotal.Add(&windowTotal, amt)
			}
		}
	}
	windowTotal.Add(&windowTotal, newCost)

	// Convert to float for comparison (velocity is USDC/minute)
	velocityF := float64(windowTotal.Int64()) / 1_000_000.0 // micro-USDC to USDC
	if velocityF > wf.MaxVelocity {
		return fmt.Errorf("%w: %.2f USDC/min exceeds max %.2f", ErrVelocityBreaker, velocityF, wf.MaxVelocity)
	}
	return nil
}

// auditEntry creates a hash-chained audit entry for tamper evidence.
func (s *Service) auditEntry(wf *Workflow, action, actor, stepName, amount, details string) AuditEntry {
	seq := len(wf.AuditTrail)
	prevHash := ""
	if seq > 0 {
		prevHash = wf.AuditTrail[seq-1].Hash
	}

	entry := AuditEntry{
		Seq:       seq,
		Timestamp: time.Now(),
		Action:    action,
		Actor:     actor,
		StepName:  stepName,
		Amount:    amount,
		Details:   details,
	}

	// HMAC-SHA256 sealed hash chain. Each entry is authenticated by a server
	// secret, making the chain tamper-evident even if the store is compromised.
	// An attacker cannot recompute hashes without the HMAC key.
	hashInput := fmt.Sprintf("%s|%d|%s|%s|%s|%s", prevHash, seq, action, actor, amount, details)
	mac := hmac.New(sha256.New, s.hmacKey)
	mac.Write([]byte(hashInput))
	entry.Hash = hex.EncodeToString(mac.Sum(nil))

	return entry
}

func validateAmount(amount string) error {
	amount = strings.TrimSpace(amount)
	if amount == "" {
		return fmt.Errorf("%w: empty amount", ErrInvalidAmount)
	}
	parsed, ok := usdc.Parse(amount)
	if !ok {
		return fmt.Errorf("%w: %q is not valid", ErrInvalidAmount, amount)
	}
	if parsed.Sign() <= 0 {
		return fmt.Errorf("%w: must be positive", ErrInvalidAmount)
	}
	return nil
}
