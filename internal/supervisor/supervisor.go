package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/mbd888/alancoin/internal/ledger"
	"github.com/mbd888/alancoin/internal/usdc"
)

// ErrDenied is returned when the supervisor blocks an operation.
var ErrDenied = errors.New("supervisor: operation denied")

// ReputationProvider retrieves an agent's reputation tier.
type ReputationProvider interface {
	GetScore(ctx context.Context, address string) (float64, string, error)
}

// Supervisor is a ledger.Service decorator that evaluates agent spending
// patterns before allowing money-moving operations through to the inner
// service.
type Supervisor struct {
	inner      ledger.Service
	graph      *SpendGraph
	engine     *RuleEngine
	reputation ReputationProvider
	logger     *slog.Logger

	// Baseline learning fields (nil without WithBaselineStore)
	baselineCache map[string]*AgentBaseline
	baselineMu    sync.RWMutex
	eventWriter   *EventWriter
	denialStore   BaselineStore
	denialSem     chan struct{} // bounds concurrent denial-logging goroutines
}

// Compile-time check.
var _ ledger.Service = (*Supervisor)(nil)

// Option configures the Supervisor.
type Option func(*Supervisor)

// WithLogger sets a structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(s *Supervisor) { s.logger = l }
}

// WithReputation sets the reputation provider.
func WithReputation(rp ReputationProvider) Option {
	return func(s *Supervisor) { s.reputation = rp }
}

// WithRules overrides the default rule set.
func WithRules(rules ...EvalRule) Option {
	return func(s *Supervisor) { s.engine = NewRuleEngine(rules...) }
}

// WithBaselineStore enables learned baselines. Injects BaselineRule between
// NewAgentRule and CircularFlowRule and sets up denial logging.
func WithBaselineStore(store BaselineStore) Option {
	return func(s *Supervisor) {
		s.baselineCache = make(map[string]*AgentBaseline)
		s.denialStore = store
		s.denialSem = make(chan struct{}, 16) // max 16 concurrent denial writes
		// Inject BaselineRule into rule set: Velocity, NewAgent, **Baseline**, Circular, Counterparty
		s.engine = NewRuleEngine(
			&VelocityRule{},
			&NewAgentRule{},
			&BaselineRule{provider: s},
			&CircularFlowRule{},
			&CounterpartyConcentrationRule{},
		)
	}
}

// New creates a Supervisor wrapping inner with default rules.
func New(inner ledger.Service, opts ...Option) *Supervisor {
	s := &Supervisor{
		inner:  inner,
		graph:  NewSpendGraph(),
		engine: NewRuleEngine(DefaultRules()...),
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// SetReputation wires the reputation provider after construction.
// Used for late binding in server.setupRoutes().
func (s *Supervisor) SetReputation(rp ReputationProvider) {
	s.reputation = rp
}

// SetEventWriter wires the async event writer after construction.
func (s *Supervisor) SetEventWriter(w *EventWriter) {
	s.eventWriter = w
}

// GetCachedBaseline returns the learned baseline for an agent, or nil.
// Implements BaselineProvider.
func (s *Supervisor) GetCachedBaseline(agentAddr string) *AgentBaseline {
	s.baselineMu.RLock()
	defer s.baselineMu.RUnlock()
	if s.baselineCache == nil {
		return nil
	}
	return s.baselineCache[strings.ToLower(agentAddr)]
}

// RefreshBaselines merges new baselines into the cache.
func (s *Supervisor) RefreshBaselines(updated map[string]*AgentBaseline) {
	s.baselineMu.Lock()
	defer s.baselineMu.Unlock()
	if s.baselineCache == nil {
		s.baselineCache = make(map[string]*AgentBaseline)
	}
	for k, v := range updated {
		s.baselineCache[strings.ToLower(k)] = v
	}
}

// -------------------------------------------------------------------------
// Evaluate + track: Hold, EscrowLock, Spend, Transfer, Withdraw
// -------------------------------------------------------------------------

func (s *Supervisor) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	if err := s.evaluate(ctx, agentAddr, "", amount, "hold"); err != nil {
		return err
	}
	// Atomically reserve a hold slot before calling inner to prevent TOCTOU.
	limit := s.concurrencyLimit(ctx, agentAddr)
	if !s.graph.TryAcquireHold(agentAddr, limit) {
		return fmt.Errorf("%w: at concurrency limit %d for holds/escrows", ErrDenied, limit)
	}
	if err := s.inner.Hold(ctx, agentAddr, amount, reference); err != nil {
		if !s.graph.ReleaseActiveHold(agentAddr) {
			s.logger.Error("hold rollback underflow", "agent", agentAddr)
		}
		return err
	}
	s.record(agentAddr, "", amount)
	return nil
}

func (s *Supervisor) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	if err := s.evaluate(ctx, agentAddr, "", amount, "escrow_lock"); err != nil {
		return err
	}
	// Atomically reserve an escrow slot before calling inner to prevent TOCTOU.
	limit := s.concurrencyLimit(ctx, agentAddr)
	if !s.graph.TryAcquireEscrow(agentAddr, limit) {
		return fmt.Errorf("%w: at concurrency limit %d for holds/escrows", ErrDenied, limit)
	}
	if err := s.inner.EscrowLock(ctx, agentAddr, amount, reference); err != nil {
		if !s.graph.ReleaseActiveEscrow(agentAddr) {
			s.logger.Error("escrow rollback underflow", "agent", agentAddr)
		}
		return err
	}
	s.record(agentAddr, "", amount)
	return nil
}

func (s *Supervisor) Spend(ctx context.Context, agentAddr, amount, reference string) error {
	if err := s.evaluate(ctx, agentAddr, "", amount, "spend"); err != nil {
		return err
	}
	if err := s.inner.Spend(ctx, agentAddr, amount, reference); err != nil {
		return err
	}
	s.record(agentAddr, "", amount)
	s.persistSpend(agentAddr, "", amount)
	return nil
}

func (s *Supervisor) Transfer(ctx context.Context, from, to, amount, reference string) error {
	if err := s.evaluate(ctx, from, to, amount, "transfer"); err != nil {
		return err
	}
	if err := s.inner.Transfer(ctx, from, to, amount, reference); err != nil {
		return err
	}
	s.record(from, to, amount)
	s.persistSpend(from, to, amount)
	return nil
}

func (s *Supervisor) Withdraw(ctx context.Context, agentAddr, amount, txHash string) error {
	if err := s.evaluate(ctx, agentAddr, "", amount, "withdraw"); err != nil {
		return err
	}
	if err := s.inner.Withdraw(ctx, agentAddr, amount, txHash); err != nil {
		return err
	}
	s.record(agentAddr, "", amount)
	s.persistSpend(agentAddr, "", amount)
	return nil
}

// -------------------------------------------------------------------------
// Settlement: edge-only tracking (spend already counted at acquire time)
// -------------------------------------------------------------------------

func (s *Supervisor) Deposit(ctx context.Context, agentAddr, amount, txHash string) error {
	// Deposits are inflows, not outflows — no velocity/edge tracking.
	return s.inner.Deposit(ctx, agentAddr, amount, txHash)
}

func (s *Supervisor) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	if err := s.inner.SettleHold(ctx, buyerAddr, sellerAddr, amount, reference); err != nil {
		return err
	}
	// Edge only: the spend was already counted in velocity when Hold was called.
	s.recordEdge(buyerAddr, sellerAddr, amount)
	// Persist settled amount for baseline learning (money actually moved).
	s.persistSpend(buyerAddr, sellerAddr, amount)
	return nil
}

func (s *Supervisor) SettleHoldWithFee(ctx context.Context, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference string) error {
	if err := s.inner.SettleHoldWithFee(ctx, buyerAddr, sellerAddr, sellerAmount, platformAddr, feeAmount, reference); err != nil {
		return err
	}
	// Track the seller payment for spend graph + baseline learning.
	// Fee goes to platform — no spend-graph edge needed for that.
	s.recordEdge(buyerAddr, sellerAddr, sellerAmount)
	totalBig, ok1 := usdc.Parse(sellerAmount)
	feeBig, ok2 := usdc.Parse(feeAmount)
	if ok1 && ok2 {
		totalBig.Add(totalBig, feeBig)
		s.persistSpend(buyerAddr, sellerAddr, usdc.Format(totalBig))
	} else {
		s.persistSpend(buyerAddr, sellerAddr, sellerAmount)
	}
	return nil
}

func (s *Supervisor) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	if err := s.inner.ReleaseEscrow(ctx, buyerAddr, sellerAddr, amount, reference); err != nil {
		return err
	}
	s.recordEdge(buyerAddr, sellerAddr, amount)
	s.persistSpend(buyerAddr, sellerAddr, amount)
	if !s.graph.ReleaseActiveEscrow(buyerAddr) {
		s.logger.Error("escrow release underflow", "agent", buyerAddr)
	}
	return nil
}

func (s *Supervisor) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	if err := s.inner.PartialEscrowSettle(ctx, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference); err != nil {
		return err
	}
	s.recordEdge(buyerAddr, sellerAddr, releaseAmount)
	s.persistSpend(buyerAddr, sellerAddr, releaseAmount)
	if !s.graph.ReleaseActiveEscrow(buyerAddr) {
		s.logger.Error("partial escrow settle underflow", "agent", buyerAddr)
	}
	return nil
}

// -------------------------------------------------------------------------
// Counter management: ConfirmHold, ReleaseHold, RefundEscrow
// -------------------------------------------------------------------------

func (s *Supervisor) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	if err := s.inner.ConfirmHold(ctx, agentAddr, amount, reference); err != nil {
		return err
	}
	if !s.graph.ReleaseActiveHold(agentAddr) {
		s.logger.Error("confirm hold underflow", "agent", agentAddr)
	}
	return nil
}

func (s *Supervisor) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	if err := s.inner.ReleaseHold(ctx, agentAddr, amount, reference); err != nil {
		return err
	}
	if !s.graph.ReleaseActiveHold(agentAddr) {
		s.logger.Error("release hold underflow", "agent", agentAddr)
	}
	return nil
}

func (s *Supervisor) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	if err := s.inner.RefundEscrow(ctx, agentAddr, amount, reference); err != nil {
		return err
	}
	if !s.graph.ReleaseActiveEscrow(agentAddr) {
		s.logger.Error("refund escrow underflow", "agent", agentAddr)
	}
	return nil
}

// -------------------------------------------------------------------------
// Pure passthrough: Refund, GetBalance, CanSpend, GetHistory
// -------------------------------------------------------------------------

func (s *Supervisor) Refund(ctx context.Context, agentAddr, amount, reference string) error {
	return s.inner.Refund(ctx, agentAddr, amount, reference)
}

func (s *Supervisor) GetBalance(ctx context.Context, agentAddr string) (*ledger.Balance, error) {
	return s.inner.GetBalance(ctx, agentAddr)
}

func (s *Supervisor) CanSpend(ctx context.Context, agentAddr, amount string) (bool, error) {
	return s.inner.CanSpend(ctx, agentAddr, amount)
}

func (s *Supervisor) GetHistory(ctx context.Context, agentAddr string, limit int) ([]*ledger.Entry, error) {
	return s.inner.GetHistory(ctx, agentAddr, limit)
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// evaluate checks rules and returns ErrDenied if blocked.
func (s *Supervisor) evaluate(ctx context.Context, agentAddr, counterparty, amount, opType string) error {
	amountBig, ok := usdc.Parse(amount)
	if !ok {
		return fmt.Errorf("supervisor: invalid amount %q", amount)
	}

	tier := s.getTier(ctx, agentAddr)

	ec := &EvalContext{
		AgentAddr:    agentAddr,
		Counterparty: counterparty,
		Amount:       amountBig,
		OpType:       opType,
		Tier:         tier,
	}

	verdict := s.engine.Evaluate(ctx, s.graph, ec)

	switch verdict.Action {
	case Deny:
		s.logger.Warn("supervisor denied operation",
			"agent", agentAddr, "op", opType, "amount", amount,
			"rule", verdict.Rule, "reason", verdict.Reason)

		// Async denial logging — bounded by semaphore to prevent goroutine explosion
		if s.denialStore != nil {
			rec := s.buildDenialRecord(agentAddr, counterparty, amountBig, opType, tier, verdict)
			select {
			case s.denialSem <- struct{}{}:
				go func() {
					defer func() { <-s.denialSem }()
					s.logDenialAsync(rec)
				}()
			default:
				s.logger.Warn("denial log dropped (at concurrency limit)")
			}
		}

		return fmt.Errorf("%w: %s", ErrDenied, verdict.Reason)
	case Flag:
		s.logger.Warn("supervisor flagged operation",
			"agent", agentAddr, "op", opType, "amount", amount,
			"rule", verdict.Rule, "reason", verdict.Reason)
	}
	return nil
}

// buildDenialRecord constructs a feature vector for a denied operation.
func (s *Supervisor) buildDenialRecord(agentAddr, counterparty string, amount *big.Int, opType, tier string, verdict *Verdict) *DenialRecord {
	rec := &DenialRecord{
		AgentAddr:      agentAddr,
		RuleName:       verdict.Rule,
		Reason:         verdict.Reason,
		Amount:         amount,
		OpType:         opType,
		Tier:           tier,
		Counterparty:   counterparty,
		HourlyTotal:    new(big.Int),
		BaselineMean:   new(big.Int),
		BaselineStddev: new(big.Int),
		CreatedAt:      time.Now(),
	}

	// Enrich with current hourly total
	if snap := s.graph.GetNode(agentAddr); snap != nil {
		rec.HourlyTotal = snap.WindowTotals[2]
	}

	// Enrich with baseline if available
	if baseline := s.GetCachedBaseline(agentAddr); baseline != nil {
		rec.BaselineMean = baseline.HourlyMean
		rec.BaselineStddev = baseline.HourlyStddev
	}

	return rec
}

// logDenialAsync persists a denial record with a bounded timeout.
func (s *Supervisor) logDenialAsync(rec *DenialRecord) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in denial logger", "panic", fmt.Sprint(r))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.denialStore.LogDenial(ctx, rec); err != nil {
		s.logger.Error("failed to log denial", "error", err, "rule", rec.RuleName)
	}
}

// record adds a spend event to the graph (velocity windows + edge).
// Used for operations where the spend is first being committed (Hold, EscrowLock,
// Spend, Transfer, Withdraw).
//
// NOTE: This does NOT persist to the baseline store. Holds and escrow locks are
// reservations, not actual spend. Only persistSpend (called from Spend, Transfer,
// Withdraw, and settlement paths) writes to the baseline store.
func (s *Supervisor) record(agent, counterparty, amount string) {
	amountBig, ok := usdc.Parse(amount)
	if !ok {
		return
	}
	s.graph.RecordEvent(agent, counterparty, amountBig, time.Now())
}

// recordEdge updates only the bilateral flow edge without touching velocity
// windows. Used for settlement operations (SettleHold, ReleaseEscrow,
// PartialEscrowSettle) where the spend was already counted at acquire time.
func (s *Supervisor) recordEdge(agent, counterparty, amount string) {
	amountBig, ok := usdc.Parse(amount)
	if !ok {
		return
	}
	s.graph.RecordEdgeOnly(agent, counterparty, amountBig, time.Now())
}

// persistSpend sends a settled/actual spend event to the async writer for
// baseline learning. Only called for operations where money actually moves
// (Spend, Transfer, Withdraw, SettleHold, ReleaseEscrow, PartialEscrowSettle),
// NOT for reservations (Hold, EscrowLock) which may be released without settling.
func (s *Supervisor) persistSpend(agent, counterparty, amount string) {
	if s.eventWriter == nil {
		return
	}
	amountBig, ok := usdc.Parse(amount)
	if !ok {
		return
	}
	s.eventWriter.Send(agent, counterparty, amountBig, time.Now())
}

// concurrencyLimit resolves the max simultaneous holds+escrows for an agent's tier.
func (s *Supervisor) concurrencyLimit(ctx context.Context, agentAddr string) int {
	tier := s.getTier(ctx, agentAddr)
	limit, ok := concurrencyLimitByTier[tier]
	if !ok {
		limit = concurrencyLimitByTier["established"]
	}
	return limit
}

// getTier returns the agent's reputation tier, defaulting to "established"
// if no reputation provider is available.
func (s *Supervisor) getTier(ctx context.Context, agentAddr string) string {
	if s.reputation == nil {
		return "established"
	}
	_, tier, err := s.reputation.GetScore(ctx, agentAddr)
	if err != nil {
		return "new"
	}
	if tier == "" {
		return "established"
	}
	return tier
}
