// Coalition escrow provides outcome-triggered settlement for multi-agent
// pipelines.  A buyer deposits funds into a single escrow that
// automatically splits payment across a coalition of worker agents,
// proportional to each agent's verified contribution, released only when
// the output meets quality criteria judged by an oracle agent.
//
// Flow:
//  1. Buyer creates coalition escrow with budget, members, quality tiers
//  2. Each member agent executes its step
//  3. Oracle agent reports quality score
//  4. Settlement engine matches quality tier → computes payout percentage
//  5. Split strategy (equal, proportional, shapley) divides payout among members
//  6. Excess funds refunded to buyer
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
	"github.com/mbd888/alancoin/internal/retry"
	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// Coalition-specific errors.
var (
	ErrCoalitionNotFound     = errors.New("escrow: coalition not found")
	ErrOracleUnauthorized    = errors.New("escrow: caller is not the oracle")
	ErrNotMember             = errors.New("escrow: caller is not a coalition member")
	ErrMemberAlreadyReported = errors.New("escrow: member already reported completion")
	ErrInvalidQualityScore   = errors.New("escrow: quality score must be between 0 and 1")
	ErrInvalidSplitStrategy  = errors.New("escrow: invalid split strategy")
	ErrWeightsSumInvalid     = errors.New("escrow: member weights must sum to 1.0")
	ErrNoMembers             = errors.New("escrow: at least one member is required")
	ErrNoQualityTiers        = errors.New("escrow: at least one quality tier is required")
	ErrDuplicateMember       = errors.New("escrow: duplicate member address")
)

// MaxCoalitionMembers is the maximum number of members in a coalition.
const MaxCoalitionMembers = 20

// CoalitionStatus represents the state of a coalition escrow.
type CoalitionStatus string

const (
	CSActive    CoalitionStatus = "active"    // Created, funds locked, awaiting work
	CSDelivered CoalitionStatus = "delivered" // All members reported, awaiting oracle
	CSSettled   CoalitionStatus = "settled"   // Oracle reported, funds distributed
	CSAborted   CoalitionStatus = "aborted"   // Buyer aborted, remaining funds refunded
	CSExpired   CoalitionStatus = "expired"   // Timed out, auto-settled or refunded
)

// SplitStrategy determines how payout is divided among members.
type SplitStrategy string

const (
	SplitEqual        SplitStrategy = "equal"        // Each member gets equal share
	SplitProportional SplitStrategy = "proportional" // Based on manual weights
	SplitShapley      SplitStrategy = "shapley"      // Based on contribution scores
)

// QualityTier defines a payout bracket based on oracle-reported quality.
type QualityTier struct {
	Name      string  `json:"name"`      // e.g. "excellent", "good", "poor"
	MinScore  float64 `json:"minScore"`  // Minimum quality score (0.0–1.0) inclusive
	PayoutPct float64 `json:"payoutPct"` // Percentage of total to pay out (0–100)
}

// CoalitionMember represents a worker agent in the coalition.
type CoalitionMember struct {
	AgentAddr    string     `json:"agentAddr"`
	Role         string     `json:"role"`
	Weight       float64    `json:"weight,omitempty"` // For proportional split (0–1)
	PayoutShare  string     `json:"payoutShare,omitempty"`
	PayoutStatus string     `json:"payoutStatus,omitempty"` // "pending", "paid", "failed"
	CompletedAt  *time.Time `json:"completedAt,omitempty"`
}

// CoalitionEscrow is an escrow holding funds for a coalition of agents,
// settled by an oracle's quality judgment.
type CoalitionEscrow struct {
	ID            string            `json:"id"`
	BuyerAddr     string            `json:"buyerAddr"`
	OracleAddr    string            `json:"oracleAddr"`
	TotalAmount   string            `json:"totalAmount"`
	SplitStrategy SplitStrategy     `json:"splitStrategy"`
	Members       []CoalitionMember `json:"members"`
	QualityTiers  []QualityTier     `json:"qualityTiers"`
	Status        CoalitionStatus   `json:"status"`

	// Oracle result
	QualityScore *float64 `json:"qualityScore,omitempty"`
	MatchedTier  string   `json:"matchedTier,omitempty"`
	PayoutPct    float64  `json:"payoutPct,omitempty"`
	TotalPayout  string   `json:"totalPayout,omitempty"`
	RefundAmount string   `json:"refundAmount,omitempty"`

	// Shapley contributions (set by oracle for shapley strategy)
	Contributions map[string]float64 `json:"contributions,omitempty"`

	ContractID   string     `json:"contractId,omitempty"`
	AutoSettleAt time.Time  `json:"autoSettleAt"`
	SettledAt    *time.Time `json:"settledAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// IsTerminal returns true if the coalition escrow is in a final state.
func (c *CoalitionEscrow) IsTerminal() bool {
	switch c.Status {
	case CSSettled, CSAborted, CSExpired:
		return true
	}
	return false
}

// allMembersCompleted returns true if every member has reported completion.
func (c *CoalitionEscrow) allMembersCompleted() bool {
	for i := range c.Members {
		if c.Members[i].CompletedAt == nil {
			return false
		}
	}
	return true
}

// CoalitionStore persists coalition escrow data.
type CoalitionStore interface {
	Create(ctx context.Context, ce *CoalitionEscrow) error
	Get(ctx context.Context, id string) (*CoalitionEscrow, error)
	Update(ctx context.Context, ce *CoalitionEscrow) error
	ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*CoalitionEscrow, error)
	ListExpired(ctx context.Context, before time.Time, limit int) ([]*CoalitionEscrow, error)
}

// CreateCoalitionRequest is the input for creating a coalition escrow.
type CreateCoalitionRequest struct {
	BuyerAddr     string            `json:"buyerAddr" binding:"required"`
	OracleAddr    string            `json:"oracleAddr" binding:"required"`
	TotalAmount   string            `json:"totalAmount" binding:"required"`
	SplitStrategy SplitStrategy     `json:"splitStrategy" binding:"required"`
	Members       []CoalitionMember `json:"members" binding:"required"`
	QualityTiers  []QualityTier     `json:"qualityTiers" binding:"required"`
	AutoSettle    string            `json:"autoSettle"`           // Duration string, e.g. "30m", "1h"
	ContractID    string            `json:"contractId,omitempty"` // Optional behavioral contract
}

// OracleReportRequest is the input from the quality oracle.
type OracleReportRequest struct {
	QualityScore  float64            `json:"qualityScore" binding:"required"`
	Contributions map[string]float64 `json:"contributions,omitempty"` // For shapley strategy
}

// DefaultCoalitionAutoSettle is the default auto-settle timeout.
const DefaultCoalitionAutoSettle = 30 * time.Minute

// RealtimeBroadcaster broadcasts coalition lifecycle events to WebSocket subscribers.
type RealtimeBroadcaster interface {
	BroadcastCoalitionEvent(eventType string, coalitionID, buyerAddr, status string)
}

// ContractChecker retrieves and applies behavioral contract penalties.
// This interface decouples the coalition package from the contracts package.
type ContractChecker interface {
	// GetByEscrow returns the contract bound to a coalition escrow, or nil if none.
	GetContractByEscrow(ctx context.Context, escrowID string) (*BoundContract, error)
	// BindToEscrow activates a contract and binds it to a coalition escrow.
	BindContract(ctx context.Context, contractID, escrowID string) error
	// MarkPassed transitions the contract to passed state.
	MarkContractPassed(ctx context.Context, contractID string) error
}

// BoundContract is a minimal view of a contract for penalty application.
type BoundContract struct {
	ID             string
	Status         string // "active", "violated", "passed"
	QualityPenalty float64
	HardViolations int
}

// CoalitionService implements coalition escrow business logic.
type CoalitionService struct {
	store          CoalitionStore
	ledger         LedgerService
	recorder       TransactionRecorder
	revenue        RevenueAccumulator
	reputation     ReputationImpactor
	receiptIssuer  ReceiptIssuer
	webhookEmitter WebhookEmitter
	realtime       RealtimeBroadcaster
	contracts      ContractChecker
	logger         *slog.Logger
	locks          sync.Map
}

// NewCoalitionService creates a new coalition escrow service.
func NewCoalitionService(store CoalitionStore, ledger LedgerService) *CoalitionService {
	return &CoalitionService{
		store:  store,
		ledger: ledger,
		logger: slog.Default(),
	}
}

// WithLogger sets a structured logger.
func (s *CoalitionService) WithLogger(l *slog.Logger) *CoalitionService {
	s.logger = l
	return s
}

// WithRecorder adds a transaction recorder for reputation integration.
func (s *CoalitionService) WithRecorder(r TransactionRecorder) *CoalitionService {
	s.recorder = r
	return s
}

// WithRevenueAccumulator adds a revenue accumulator for staking.
func (s *CoalitionService) WithRevenueAccumulator(r RevenueAccumulator) *CoalitionService {
	s.revenue = r
	return s
}

// WithReputationImpactor adds a reputation impactor for outcomes.
func (s *CoalitionService) WithReputationImpactor(r ReputationImpactor) *CoalitionService {
	s.reputation = r
	return s
}

// WithReceiptIssuer adds a receipt issuer for cryptographic payment proofs.
func (s *CoalitionService) WithReceiptIssuer(r ReceiptIssuer) *CoalitionService {
	s.receiptIssuer = r
	return s
}

// WithWebhookEmitter adds a webhook emitter for lifecycle event notifications.
func (s *CoalitionService) WithWebhookEmitter(e WebhookEmitter) *CoalitionService {
	s.webhookEmitter = e
	return s
}

// WithRealtimeBroadcaster adds a real-time event broadcaster for dashboard.
func (s *CoalitionService) WithRealtimeBroadcaster(r RealtimeBroadcaster) *CoalitionService {
	s.realtime = r
	return s
}

// WithContractChecker adds contract integration for penalty application.
func (s *CoalitionService) WithContractChecker(c ContractChecker) *CoalitionService {
	s.contracts = c
	return s
}

func (s *CoalitionService) escrowLock(id string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *CoalitionService) cleanupLock(id string) {
	s.locks.Delete(id)
}

// Create creates a new coalition escrow and locks buyer funds.
func (s *CoalitionService) Create(ctx context.Context, req CreateCoalitionRequest) (*CoalitionEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.coalition.Create",
		attribute.String("buyer", req.BuyerAddr),
		attribute.String("oracle", req.OracleAddr),
		attribute.String("amount", req.TotalAmount),
		attribute.Int("members", len(req.Members)),
	)
	defer span.End()

	// Validate inputs
	if err := s.validateCreateRequest(req); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	autoSettle := DefaultCoalitionAutoSettle
	if req.AutoSettle != "" {
		d, err := time.ParseDuration(req.AutoSettle)
		if err == nil && d > 0 {
			autoSettle = d
		}
	}

	// Normalize addresses
	buyer := strings.ToLower(req.BuyerAddr)
	oracle := strings.ToLower(req.OracleAddr)
	members := make([]CoalitionMember, len(req.Members))
	for i, m := range req.Members {
		members[i] = CoalitionMember{
			AgentAddr: strings.ToLower(m.AgentAddr),
			Role:      m.Role,
			Weight:    m.Weight,
		}
	}

	// Sort quality tiers by MinScore descending (highest first)
	tiers := make([]QualityTier, len(req.QualityTiers))
	copy(tiers, req.QualityTiers)
	sortTiersDesc(tiers)

	now := time.Now()
	ce := &CoalitionEscrow{
		ID:            generateCoalitionID(),
		BuyerAddr:     buyer,
		OracleAddr:    oracle,
		TotalAmount:   req.TotalAmount,
		SplitStrategy: req.SplitStrategy,
		Members:       members,
		QualityTiers:  tiers,
		Status:        CSActive,
		ContractID:    req.ContractID,
		AutoSettleAt:  now.Add(autoSettle),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	// Lock buyer funds
	ref := "coa:" + ce.ID
	if err := s.ledger.EscrowLock(ctx, ce.BuyerAddr, ce.TotalAmount, ref); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to lock coalition funds")
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to lock coalition escrow funds: %w", err),
			FundsStatus: "no_change",
			Recovery:    "No funds were moved. Check your available balance and try again.",
			Amount:      ce.TotalAmount,
			Reference:   ce.ID,
		}
	}

	if err := s.store.Create(ctx, ce); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to store coalition escrow")
		// Best-effort refund
		refundErr := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
			return s.ledger.RefundEscrow(ctx, ce.BuyerAddr, ce.TotalAmount, ref)
		})
		if refundErr != nil {
			s.logger.Error("RefundEscrow failed after store error: funds stuck",
				"coalition_id", ce.ID, "amount", ce.TotalAmount, "error", refundErr)
			return nil, &MoneyError{
				Err:         fmt.Errorf("failed to create coalition escrow: %w", err),
				FundsStatus: "locked_in_escrow",
				Recovery:    "Creation failed and refund also failed. Contact support with the reference.",
				Amount:      ce.TotalAmount,
				Reference:   ce.ID,
			}
		}
		return nil, &MoneyError{
			Err:         fmt.Errorf("failed to create coalition escrow: %w", err),
			FundsStatus: "no_change",
			Recovery:    "Creation failed but your funds were returned. Safe to retry.",
			Amount:      ce.TotalAmount,
			Reference:   ce.ID,
		}
	}

	// Auto-bind contract if provided
	if ce.ContractID != "" && s.contracts != nil {
		if err := s.contracts.BindContract(ctx, ce.ContractID, ce.ID); err != nil {
			s.logger.Warn("failed to bind contract to coalition",
				"coalition_id", ce.ID, "contract_id", ce.ContractID, "error", err)
			// Non-fatal: coalition is created, contract binding is best-effort.
			// The buyer can manually bind via POST /v1/contracts/:id/bind.
		}
	}

	metrics.CoalitionCreatedTotal.Inc()

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowCreated(ce.BuyerAddr, ce.ID, ce.OracleAddr, ce.TotalAmount)
	}
	if s.realtime != nil {
		go s.realtime.BroadcastCoalitionEvent("coalition_created", ce.ID, ce.BuyerAddr, string(ce.Status))
	}

	return ce, nil
}

// ReportCompletion marks a member's work as completed.
func (s *CoalitionService) ReportCompletion(ctx context.Context, id, callerAddr string) (*CoalitionEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.coalition.ReportCompletion",
		attribute.String("coalition_id", id),
		attribute.String("caller", callerAddr),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	ce, err := s.store.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if ce.Status != CSActive && ce.Status != CSDelivered {
		return nil, fmt.Errorf("%w: coalition status is %s", ErrInvalidStatus, ce.Status)
	}

	caller := strings.ToLower(callerAddr)
	memberIdx := -1
	for i, m := range ce.Members {
		if m.AgentAddr == caller {
			memberIdx = i
			break
		}
	}
	if memberIdx < 0 {
		return nil, ErrNotMember
	}
	if ce.Members[memberIdx].CompletedAt != nil {
		return nil, ErrMemberAlreadyReported
	}

	now := time.Now()
	ce.Members[memberIdx].CompletedAt = &now
	ce.UpdatedAt = now

	// If all members completed, transition to delivered
	if ce.allMembersCompleted() {
		ce.Status = CSDelivered
	}

	if err := s.store.Update(ctx, ce); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("failed to update coalition after completion report: %w", err)
	}

	return ce, nil
}

// OracleReport processes the oracle's quality judgment and settles the escrow.
func (s *CoalitionService) OracleReport(ctx context.Context, id, callerAddr string, req OracleReportRequest) (*CoalitionEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.coalition.OracleReport",
		attribute.String("coalition_id", id),
		attribute.Float64("quality_score", req.QualityScore),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	ce, err := s.store.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if ce.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	if ce.Status != CSActive && ce.Status != CSDelivered {
		return nil, fmt.Errorf("%w: coalition status is %s", ErrInvalidStatus, ce.Status)
	}

	if strings.ToLower(callerAddr) != ce.OracleAddr {
		return nil, ErrOracleUnauthorized
	}

	if req.QualityScore < 0.0 || req.QualityScore > 1.0 {
		return nil, ErrInvalidQualityScore
	}

	// Apply behavioral contract penalty if a contract is bound.
	// Soft violations reduce the effective quality score, hard violations
	// set it to zero (full refund).
	effectiveScore := req.QualityScore
	if ce.ContractID != "" && s.contracts != nil {
		bc, err := s.contracts.GetContractByEscrow(ctx, ce.ID)
		if err == nil && bc != nil {
			if bc.Status == "violated" || bc.HardViolations > 0 {
				// Hard violation → zero payout, full refund
				effectiveScore = 0
				s.logger.Warn("contract hard violation: zeroing coalition payout",
					"coalition_id", ce.ID, "contract_id", bc.ID)
			} else {
				effectiveScore -= bc.QualityPenalty
				if effectiveScore < 0 {
					effectiveScore = 0
				}
			}
			// Mark contract as passed (or it stays violated)
			if bc.HardViolations == 0 {
				_ = s.contracts.MarkContractPassed(ctx, bc.ID)
			}
		}
	}

	// Match quality tier using the effective (penalty-adjusted) score
	tier, payoutPct := matchTier(ce.QualityTiers, effectiveScore)

	// Calculate total payout and refund
	totalBig, _ := usdc.Parse(ce.TotalAmount)
	payoutBig := computePayout(totalBig, payoutPct)
	refundBig := new(big.Int).Sub(totalBig, payoutBig)

	// Validate contributions map — only accept addresses that are coalition members.
	// Reject any address not in the coalition to prevent fund misdirection.
	if req.Contributions != nil {
		memberSet := make(map[string]bool, len(ce.Members))
		for _, m := range ce.Members {
			memberSet[m.AgentAddr] = true
		}
		for addr := range req.Contributions {
			if !memberSet[strings.ToLower(addr)] {
				return nil, fmt.Errorf("contributions contains non-member address: %s", addr)
			}
		}
	}

	// Compute per-member shares
	shares := s.computeShares(ce, payoutBig, req.Contributions)

	// Set oracle results on the escrow
	finalScore := effectiveScore // heap-allocate for struct storage
	ce.QualityScore = &finalScore
	ce.MatchedTier = tier
	ce.PayoutPct = payoutPct
	ce.TotalPayout = usdc.Format(payoutBig)
	ce.RefundAmount = usdc.Format(refundBig)
	if req.Contributions != nil {
		ce.Contributions = req.Contributions
	}

	// Apply per-member shares
	for i := range ce.Members {
		if share, ok := shares[ce.Members[i].AgentAddr]; ok {
			ce.Members[i].PayoutShare = usdc.Format(share)
		}
	}

	// Settle: release each member's share from buyer's escrow.
	// Track per-member settlement status for auditability.
	ref := "coa:" + ce.ID
	var settlementFailures int
	for i := range ce.Members {
		m := &ce.Members[i]
		if m.PayoutShare == "" || m.PayoutShare == "0.000000" {
			m.PayoutStatus = "paid" // Nothing to pay
			continue
		}
		releaseRef := ref + ":member:" + m.AgentAddr
		if err := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
			return s.ledger.ReleaseEscrow(ctx, ce.BuyerAddr, m.AgentAddr, m.PayoutShare, releaseRef)
		}); err != nil {
			span.RecordError(err)
			s.logger.Error("CRITICAL: failed to release member share",
				"coalition_id", ce.ID, "member", m.AgentAddr, "share", m.PayoutShare, "error", err)
			m.PayoutStatus = "failed"
			settlementFailures++
			continue // Continue settling other members — partial is better than none
		}
		m.PayoutStatus = "paid"

		// Record transaction for reputation
		if s.recorder != nil {
			_ = s.recorder.RecordTransaction(ctx, ce.ID, ce.BuyerAddr, m.AgentAddr, m.PayoutShare, "", "confirmed")
		}
		// Revenue accumulation
		if s.revenue != nil {
			_ = s.revenue.AccumulateRevenue(ctx, m.AgentAddr, m.PayoutShare, "coalition_settle:"+ce.ID)
		}
		// Reputation impact
		if s.reputation != nil {
			_ = s.reputation.RecordDispute(ctx, m.AgentAddr, "confirmed", m.PayoutShare)
		}
	}

	if settlementFailures > 0 {
		s.logger.Error("coalition settlement partially failed",
			"coalition_id", ce.ID, "failures", settlementFailures, "total", len(ce.Members))
	}

	// Refund excess to buyer
	if refundBig.Sign() > 0 {
		refundRef := ref + ":refund"
		if err := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
			return s.ledger.RefundEscrow(ctx, ce.BuyerAddr, usdc.Format(refundBig), refundRef)
		}); err != nil {
			span.RecordError(err)
			s.logger.Error("CRITICAL: failed to refund excess to buyer",
				"coalition_id", ce.ID, "refund", usdc.Format(refundBig), "error", err)
		}
	}

	now := time.Now()
	ce.Status = CSSettled
	ce.SettledAt = &now
	ce.UpdatedAt = now

	if err := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
		return s.store.Update(ctx, ce)
	}); err != nil {
		s.logger.Error("CRITICAL: coalition settled but status update failed",
			"coalition_id", ce.ID)
		span.RecordError(err)
		return nil, err
	}

	metrics.CoalitionSettledTotal.Inc()
	metrics.CoalitionDuration.Observe(time.Since(ce.CreatedAt).Seconds())

	if s.realtime != nil {
		go s.realtime.BroadcastCoalitionEvent("coalition_settled", ce.ID, ce.BuyerAddr, string(ce.Status))
	}

	// Issue receipts for each member payout (synchronous — must complete before
	// lock cleanup to prevent state races).
	if s.receiptIssuer != nil {
		for _, m := range ce.Members {
			if m.PayoutShare != "" && m.PayoutShare != "0.000000" {
				_ = s.receiptIssuer.IssueReceipt(ctx, "coalition", ce.ID, ce.BuyerAddr,
					m.AgentAddr, m.PayoutShare, "", "confirmed",
					fmt.Sprintf("coalition_settle:tier=%s:score=%.4f", ce.MatchedTier, *ce.QualityScore))
			}
		}
	}

	// Clean up lock BEFORE firing async webhooks — the escrow is now in a
	// terminal state so no further mutations are possible.
	s.cleanupLock(id)

	// Webhooks are fire-and-forget, safe to run after lock cleanup since
	// the coalition is terminal and the optimistic lock prevents re-mutation.
	if s.webhookEmitter != nil {
		for _, m := range ce.Members {
			if m.PayoutShare != "" && m.PayoutShare != "0.000000" {
				addr, share := m.AgentAddr, m.PayoutShare
				go s.webhookEmitter.EmitEscrowReleased(addr, ce.ID, ce.BuyerAddr, share)
			}
		}
	}

	return ce, nil
}

// Abort cancels the coalition escrow and refunds all locked funds to the buyer.
func (s *CoalitionService) Abort(ctx context.Context, id, callerAddr string) (*CoalitionEscrow, error) {
	ctx, span := traces.StartSpan(ctx, "escrow.coalition.Abort",
		attribute.String("coalition_id", id),
	)
	defer span.End()

	mu := s.escrowLock(id)
	mu.Lock()
	defer mu.Unlock()

	ce, err := s.store.Get(ctx, id)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if strings.ToLower(callerAddr) != ce.BuyerAddr {
		return nil, ErrUnauthorized
	}

	if ce.IsTerminal() {
		return nil, ErrAlreadyResolved
	}

	// Calculate refund amount: total minus any already-paid member shares.
	// In normal flow this equals TotalAmount (no payouts before abort).
	// Defense-in-depth for edge cases where partial settlement occurred.
	totalBig, _ := usdc.Parse(ce.TotalAmount)
	alreadyPaid := new(big.Int)
	for _, m := range ce.Members {
		if m.PayoutStatus == "paid" && m.PayoutShare != "" {
			paid, _ := usdc.Parse(m.PayoutShare)
			alreadyPaid.Add(alreadyPaid, paid)
		}
	}
	refundBig := new(big.Int).Sub(totalBig, alreadyPaid)
	refundStr := usdc.Format(refundBig)

	ref := "coa:" + ce.ID + ":abort"
	if refundBig.Sign() > 0 {
		if err := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
			return s.ledger.RefundEscrow(ctx, ce.BuyerAddr, refundStr, ref)
		}); err != nil {
			span.RecordError(err)
			return nil, &MoneyError{
				Err:         fmt.Errorf("failed to refund coalition: %w", err),
				FundsStatus: "locked_in_escrow",
				Recovery:    "Funds are still locked. Contact support with the reference.",
				Amount:      refundStr,
				Reference:   ce.ID,
			}
		}
	}

	now := time.Now()
	ce.Status = CSAborted
	ce.RefundAmount = refundStr
	ce.SettledAt = &now
	ce.UpdatedAt = now

	if err := s.store.Update(ctx, ce); err != nil {
		s.logger.Error("CRITICAL: coalition refunded but status update failed",
			"coalition_id", ce.ID, "amount", ce.TotalAmount, "error", err)
		return nil, err
	}

	if s.webhookEmitter != nil {
		go s.webhookEmitter.EmitEscrowRefunded(ce.BuyerAddr, ce.ID, ce.TotalAmount)
	}
	if s.realtime != nil {
		go s.realtime.BroadcastCoalitionEvent("coalition_aborted", ce.ID, ce.BuyerAddr, string(ce.Status))
	}

	s.cleanupLock(id)
	return ce, nil
}

// AutoSettle expires a coalition escrow, refunding the buyer.
func (s *CoalitionService) AutoSettle(ctx context.Context, ce *CoalitionEscrow) error {
	ctx, span := traces.StartSpan(ctx, "escrow.coalition.AutoSettle",
		attribute.String("coalition_id", ce.ID),
	)
	defer span.End()

	mu := s.escrowLock(ce.ID)
	mu.Lock()
	defer mu.Unlock()

	// Re-read under lock
	fresh, err := s.store.Get(ctx, ce.ID)
	if err != nil {
		return err
	}
	ce = fresh

	if ce.IsTerminal() {
		return nil
	}

	// Refund full amount to buyer
	ref := "coa:" + ce.ID + ":expired"
	if err := retry.Do(ctx, 3, 50*time.Millisecond, func() error {
		return s.ledger.RefundEscrow(ctx, ce.BuyerAddr, ce.TotalAmount, ref)
	}); err != nil {
		return fmt.Errorf("failed to auto-refund coalition: %w", err)
	}

	now := time.Now()
	ce.Status = CSExpired
	ce.RefundAmount = ce.TotalAmount
	ce.SettledAt = &now
	ce.UpdatedAt = now

	if err := s.store.Update(ctx, ce); err != nil {
		s.logger.Error("CRITICAL: coalition auto-settled but status update failed",
			"coalition_id", ce.ID, "error", err)
		return err
	}

	metrics.CoalitionExpiredTotal.Inc()
	s.cleanupLock(ce.ID)
	return nil
}

// ForceCloseExpired auto-settles all expired coalition escrows.
func (s *CoalitionService) ForceCloseExpired(ctx context.Context) (int, error) {
	expired, err := s.store.ListExpired(ctx, time.Now(), 100)
	if err != nil {
		return 0, err
	}

	closed := 0
	for _, ce := range expired {
		if ce.IsTerminal() {
			continue
		}
		if err := s.AutoSettle(ctx, ce); err != nil {
			s.logger.Warn("force-close: failed to auto-settle coalition",
				"coalition_id", ce.ID, "error", err)
			continue
		}
		closed++
	}
	return closed, nil
}

// Get returns a coalition escrow by ID.
func (s *CoalitionService) Get(ctx context.Context, id string) (*CoalitionEscrow, error) {
	return s.store.Get(ctx, id)
}

// ListByAgent returns coalition escrows involving an agent.
func (s *CoalitionService) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*CoalitionEscrow, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListByAgent(ctx, strings.ToLower(agentAddr), limit)
}

// --- Validation ---

func (s *CoalitionService) validateCreateRequest(req CreateCoalitionRequest) error {
	if err := validateAmount(req.TotalAmount); err != nil {
		return err
	}

	if strings.EqualFold(req.BuyerAddr, req.OracleAddr) {
		return errors.New("buyer and oracle cannot be the same address")
	}

	if len(req.Members) == 0 {
		return ErrNoMembers
	}
	if len(req.Members) > MaxCoalitionMembers {
		return fmt.Errorf("too many members: max %d", MaxCoalitionMembers)
	}

	if len(req.QualityTiers) == 0 {
		return ErrNoQualityTiers
	}

	switch req.SplitStrategy {
	case SplitEqual, SplitProportional, SplitShapley:
		// ok
	default:
		return fmt.Errorf("%w: %q", ErrInvalidSplitStrategy, req.SplitStrategy)
	}

	// Check for duplicate member addresses
	seen := make(map[string]bool, len(req.Members))
	for _, m := range req.Members {
		addr := strings.ToLower(m.AgentAddr)
		if seen[addr] {
			return fmt.Errorf("%w: %s", ErrDuplicateMember, addr)
		}
		seen[addr] = true

		// Member cannot be the buyer or oracle (conflict of interest)
		if strings.EqualFold(m.AgentAddr, req.BuyerAddr) {
			return errors.New("buyer cannot be a coalition member")
		}
		if strings.EqualFold(m.AgentAddr, req.OracleAddr) {
			return errors.New("oracle cannot be a coalition member")
		}
	}

	// Validate quality tiers
	for _, t := range req.QualityTiers {
		if t.MinScore < 0 || t.MinScore > 1 {
			return fmt.Errorf("quality tier %q: minScore must be between 0 and 1", t.Name)
		}
		if t.PayoutPct < 0 || t.PayoutPct > 100 {
			return fmt.Errorf("quality tier %q: payoutPct must be between 0 and 100", t.Name)
		}
	}

	// For proportional strategy, validate weights sum to ~1.0
	if req.SplitStrategy == SplitProportional {
		var sum float64
		for _, m := range req.Members {
			if m.Weight <= 0 {
				return fmt.Errorf("proportional strategy requires positive weights for all members")
			}
			sum += m.Weight
		}
		if sum < 0.99 || sum > 1.01 {
			return ErrWeightsSumInvalid
		}
	}

	return nil
}

// --- Split computation ---

// computeShares calculates each member's payout share based on the strategy.
func (s *CoalitionService) computeShares(ce *CoalitionEscrow, totalPayout *big.Int, contributions map[string]float64) map[string]*big.Int {
	switch ce.SplitStrategy {
	case SplitProportional:
		return computeProportionalShares(ce.Members, totalPayout)
	case SplitShapley:
		if len(contributions) > 0 {
			return computeShapleyShares(ce.Members, totalPayout, contributions)
		}
		// Fall through to equal if no contributions provided
		return computeEqualShares(ce.Members, totalPayout)
	default: // SplitEqual
		return computeEqualShares(ce.Members, totalPayout)
	}
}

// computeEqualShares divides payout equally, giving remainder to the last member.
func computeEqualShares(members []CoalitionMember, totalPayout *big.Int) map[string]*big.Int {
	n := int64(len(members))
	if n == 0 {
		return nil
	}
	shares := make(map[string]*big.Int, len(members))
	perMember := new(big.Int).Div(totalPayout, big.NewInt(n))
	distributed := new(big.Int)

	for i, m := range members {
		if i == len(members)-1 {
			// Last member gets the remainder to avoid dust
			shares[m.AgentAddr] = new(big.Int).Sub(totalPayout, distributed)
		} else {
			shares[m.AgentAddr] = new(big.Int).Set(perMember)
			distributed.Add(distributed, perMember)
		}
	}
	return shares
}

// computeProportionalShares divides payout by weights.
func computeProportionalShares(members []CoalitionMember, totalPayout *big.Int) map[string]*big.Int {
	if len(members) == 0 {
		return nil
	}
	shares := make(map[string]*big.Int, len(members))
	distributed := new(big.Int)

	// Use basis points for precision (weight * 10000)
	for i, m := range members {
		if i == len(members)-1 {
			shares[m.AgentAddr] = new(big.Int).Sub(totalPayout, distributed)
		} else {
			bps := int64(m.Weight * 10000)
			share := new(big.Int).Mul(totalPayout, big.NewInt(bps))
			share.Div(share, big.NewInt(10000))
			shares[m.AgentAddr] = share
			distributed.Add(distributed, share)
		}
	}
	return shares
}

// computeShapleyShares divides payout by contribution scores.
// Contributions are normalized to sum to 1.0. Members missing from the
// contributions map receive a minimum floor share (1 basis point per
// missing member) to prevent zero-payout due to omission.
func computeShapleyShares(members []CoalitionMember, totalPayout *big.Int, contributions map[string]float64) map[string]*big.Int {
	if len(members) == 0 {
		return nil
	}

	// Assign a small floor to any member missing from contributions.
	// This ensures no member is zeroed out by omission.
	effectiveContribs := make(map[string]float64, len(members))
	var sum float64
	for _, m := range members {
		c, ok := contributions[m.AgentAddr]
		if !ok || c <= 0 {
			c = 0.01 // Minimum floor contribution
		}
		effectiveContribs[m.AgentAddr] = c
		sum += c
	}

	if sum == 0 {
		return computeEqualShares(members, totalPayout)
	}

	shares := make(map[string]*big.Int, len(members))
	distributed := new(big.Int)

	for i, m := range members {
		if i == len(members)-1 {
			// Last member gets remainder to prevent dust loss
			shares[m.AgentAddr] = new(big.Int).Sub(totalPayout, distributed)
		} else {
			c := effectiveContribs[m.AgentAddr]
			normalizedBps := int64((c / sum) * 10000)
			share := new(big.Int).Mul(totalPayout, big.NewInt(normalizedBps))
			share.Div(share, big.NewInt(10000))
			shares[m.AgentAddr] = share
			distributed.Add(distributed, share)
		}
	}
	return shares
}

// --- Helpers ---

// matchTier finds the highest-paying quality tier matching the score.
// Tiers must be sorted by MinScore descending.
func matchTier(tiers []QualityTier, score float64) (string, float64) {
	for _, t := range tiers {
		if score >= t.MinScore {
			return t.Name, t.PayoutPct
		}
	}
	// Below all tiers → 0% payout
	return "none", 0
}

// computePayout calculates the payout amount from total and percentage.
func computePayout(total *big.Int, pct float64) *big.Int {
	if pct <= 0 {
		return new(big.Int)
	}
	if pct >= 100 {
		return new(big.Int).Set(total)
	}
	bps := int64(pct * 100) // Convert percent to basis points
	payout := new(big.Int).Mul(total, big.NewInt(bps))
	payout.Div(payout, big.NewInt(10000))
	return payout
}

// sortTiersDesc sorts quality tiers by MinScore descending (highest first).
func sortTiersDesc(tiers []QualityTier) {
	for i := 1; i < len(tiers); i++ {
		for j := i; j > 0 && tiers[j].MinScore > tiers[j-1].MinScore; j-- {
			tiers[j], tiers[j-1] = tiers[j-1], tiers[j]
		}
	}
}

func generateCoalitionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return fmt.Sprintf("coa_%x", b)
}
