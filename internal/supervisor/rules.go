package supervisor

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// Action is the outcome of a rule evaluation.
type Action int

const (
	Allow Action = iota
	Deny
	Flag
)

// Verdict is the result of evaluating a single rule.
type Verdict struct {
	Action Action
	Rule   string
	Reason string
}

// EvalContext carries the parameters for a rule evaluation.
type EvalContext struct {
	AgentAddr    string
	Counterparty string
	Amount       *big.Int
	OpType       string // "hold", "escrow_lock", "spend", "transfer", "withdraw"
	Tier         string // reputation tier: "new", "emerging", "established", "trusted", "elite"
}

// EvalRule is the interface for behavioral rules.
type EvalRule interface {
	Name() string
	Evaluate(ctx context.Context, graph *SpendGraph, ec *EvalContext) *Verdict
}

// RuleEngine runs all registered rules. First Deny wins.
type RuleEngine struct {
	rules []EvalRule
}

// NewRuleEngine creates an engine with the given rules.
func NewRuleEngine(rules ...EvalRule) *RuleEngine {
	return &RuleEngine{rules: rules}
}

// Evaluate runs all rules and returns the first Deny verdict.
// If no rule denies, returns Allow. Flag verdicts are noted but don't block.
func (e *RuleEngine) Evaluate(ctx context.Context, graph *SpendGraph, ec *EvalContext) *Verdict {
	var flagged *Verdict
	for _, rule := range e.rules {
		v := rule.Evaluate(ctx, graph, ec)
		if v == nil {
			continue
		}
		if v.Action == Deny {
			return v
		}
		if v.Action == Flag && flagged == nil {
			flagged = v
		}
	}
	if flagged != nil {
		return flagged
	}
	return &Verdict{Action: Allow, Rule: "engine", Reason: "all rules passed"}
}

// DefaultRules returns the built-in behavioral rules.
// Note: concurrency limits are enforced atomically in Supervisor.Hold /
// EscrowLock and are not a rule-engine concern.
func DefaultRules() []EvalRule {
	return []EvalRule{
		&VelocityRule{},
		&NewAgentRule{},
		&CircularFlowRule{},
		&CounterpartyConcentrationRule{},
	}
}

// ---------------------------------------------------------------------------
// VelocityRule: spend rate exceeds window limit
// ---------------------------------------------------------------------------

// velocityLimit maps tier to hourly spend limit (in 6-decimal USDC units).
var velocityLimit = map[string]*big.Int{
	"new":         mustParse("50"),     // $50/hr
	"emerging":    mustParse("500"),    // $500/hr
	"established": mustParse("5000"),   // $5,000/hr
	"trusted":     mustParse("25000"),  // $25,000/hr
	"elite":       mustParse("100000"), // $100,000/hr
}

type VelocityRule struct{}

func (r *VelocityRule) Name() string { return "velocity" }

func (r *VelocityRule) Evaluate(_ context.Context, graph *SpendGraph, ec *EvalContext) *Verdict {
	snap := graph.GetNode(ec.AgentAddr)
	if snap == nil {
		return nil // no history, allow
	}

	limit, ok := velocityLimit[ec.Tier]
	if !ok {
		limit = velocityLimit["established"]
	}

	// Check 1hr window (index 2) — project new total
	projected := new(big.Int).Add(snap.WindowTotals[2], ec.Amount)
	if projected.Cmp(limit) > 0 {
		return &Verdict{
			Action: Deny,
			Rule:   r.Name(),
			Reason: fmt.Sprintf("hourly velocity %s would exceed %s limit for tier %q",
				projected.String(), limit.String(), ec.Tier),
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Concurrency limits: used by Supervisor.Hold / EscrowLock via atomic acquire
// ---------------------------------------------------------------------------

// concurrencyLimitByTier maps tier to max simultaneous holds+escrows.
// Enforcement is in Supervisor.Hold / EscrowLock using TryAcquireHold /
// TryAcquireEscrow to avoid the TOCTOU race a rule-based snapshot check has.
var concurrencyLimitByTier = map[string]int{
	"new":         3,
	"emerging":    10,
	"established": 25,
	"trusted":     50,
	"elite":       100,
}

// ---------------------------------------------------------------------------
// NewAgentRule: "new" tier agent with >$5 per transaction
// ---------------------------------------------------------------------------

var newAgentPerTxLimit = mustParse("5") // $5

type NewAgentRule struct{}

func (r *NewAgentRule) Name() string { return "new_agent_limit" }

func (r *NewAgentRule) Evaluate(_ context.Context, _ *SpendGraph, ec *EvalContext) *Verdict {
	if ec.Tier != "new" {
		return nil
	}
	if ec.Amount.Cmp(newAgentPerTxLimit) > 0 {
		return &Verdict{
			Action: Deny,
			Rule:   r.Name(),
			Reason: fmt.Sprintf("new agent per-tx limit exceeded: %s > %s",
				ec.Amount.String(), newAgentPerTxLimit.String()),
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// CircularFlowRule: A->B->C->A cycle within 1 hour
// ---------------------------------------------------------------------------

type CircularFlowRule struct{}

func (r *CircularFlowRule) Name() string { return "circular_flow" }

func (r *CircularFlowRule) Evaluate(_ context.Context, graph *SpendGraph, ec *EvalContext) *Verdict {
	if ec.Counterparty == "" {
		return nil
	}

	cycle := graph.HasCyclicFlow(ec.AgentAddr, 1*time.Hour)
	if cycle != nil {
		return &Verdict{
			Action: Flag,
			Rule:   r.Name(),
			Reason: fmt.Sprintf("circular flow detected: %v", cycle),
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// CounterpartyConcentrationRule: >80% volume to one counterparty
// ---------------------------------------------------------------------------

type CounterpartyConcentrationRule struct{}

func (r *CounterpartyConcentrationRule) Name() string { return "counterparty_concentration" }

func (r *CounterpartyConcentrationRule) Evaluate(_ context.Context, graph *SpendGraph, ec *EvalContext) *Verdict {
	snap := graph.GetNode(ec.AgentAddr)
	if snap == nil || snap.TotalSpent.Sign() == 0 {
		return nil
	}

	if ec.Counterparty == "" {
		return nil
	}

	edge := graph.GetEdge(ec.AgentAddr, ec.Counterparty)
	if edge == nil {
		return nil
	}

	// Calculate concentration: edge.Volume / snap.TotalSpent
	// Use integer math: edge.Volume * 100 / snap.TotalSpent > 80
	scaled := new(big.Int).Mul(edge.Volume, big.NewInt(100))
	pct := new(big.Int).Div(scaled, snap.TotalSpent)

	if pct.Cmp(big.NewInt(80)) > 0 {
		return &Verdict{
			Action: Flag,
			Rule:   r.Name(),
			Reason: fmt.Sprintf("%s%% of volume concentrated on counterparty %s",
				pct.String(), ec.Counterparty),
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// BaselineRule: learned per-agent anomaly detection (mean + 3*stddev)
// ---------------------------------------------------------------------------

// BaselineProvider supplies cached baselines for rule evaluation.
type BaselineProvider interface {
	GetCachedBaseline(agentAddr string) *AgentBaseline
}

// BaselineRule denies when projected hourly spend exceeds the agent's
// learned baseline by more than 3 standard deviations. Falls through
// to VelocityRule when no baseline exists or has insufficient data.
type BaselineRule struct {
	provider BaselineProvider
}

func (r *BaselineRule) Name() string { return "baseline_anomaly" }

// minStddevOneDollar is the absolute minimum stddev ($1) to prevent
// cold-start lock-in when an agent spends consistently.
var minStddevOneDollar = mustParse("1") // $1

func (r *BaselineRule) Evaluate(_ context.Context, graph *SpendGraph, ec *EvalContext) *Verdict {
	if r.provider == nil {
		return nil
	}

	baseline := r.provider.GetCachedBaseline(ec.AgentAddr)
	if baseline == nil || baseline.SampleHours < 24 {
		return nil // insufficient data, fall through to VelocityRule
	}

	// Get current 1hr window total from graph
	snap := graph.GetNode(ec.AgentAddr)
	var currentHourly *big.Int
	if snap != nil {
		currentHourly = snap.WindowTotals[2] // 1hr window
	} else {
		currentHourly = new(big.Int)
	}

	// Project: current + requested amount
	projected := new(big.Int).Add(currentHourly, ec.Amount)

	// Minimum stddev = max(20% of mean, $1) to prevent cold-start lock-in.
	// Without this, an agent spending a consistent amount gets stddev≈0
	// and is permanently capped at their historical rate.
	effectiveStddev := new(big.Int).Set(baseline.HourlyStddev)
	twentyPctMean := new(big.Int).Div(baseline.HourlyMean, big.NewInt(5))
	if effectiveStddev.Cmp(twentyPctMean) < 0 {
		effectiveStddev.Set(twentyPctMean)
	}
	if effectiveStddev.Cmp(minStddevOneDollar) < 0 {
		effectiveStddev.Set(minStddevOneDollar)
	}

	// Threshold = mean + 3*effectiveStddev
	threshold := new(big.Int).Set(baseline.HourlyMean)
	threeStddev := new(big.Int).Mul(effectiveStddev, big.NewInt(3))
	threshold.Add(threshold, threeStddev)

	// Floor = 50% of tier velocity limit (prevents baselines from being
	// more restrictive than half the hard limit)
	tierLimit, ok := velocityLimit[ec.Tier]
	if !ok {
		tierLimit = velocityLimit["established"]
	}
	floor := new(big.Int).Div(tierLimit, big.NewInt(2))

	// Effective threshold = max(threshold, floor)
	effectiveThreshold := threshold
	floorApplied := false
	if floor.Cmp(threshold) > 0 {
		effectiveThreshold = floor
		floorApplied = true
	}

	if projected.Cmp(effectiveThreshold) > 0 {
		reason := fmt.Sprintf(
			"spending rate $%s/hr exceeds learned baseline $%s/hr (mean $%s + 3x stddev $%s); reduce hourly spend or contact your operator",
			usdc.Format(projected), usdc.Format(effectiveThreshold),
			usdc.Format(baseline.HourlyMean), usdc.Format(effectiveStddev))
		if floorApplied {
			reason = fmt.Sprintf(
				"spending rate $%s/hr exceeds tier floor $%s/hr (baseline too low, floor protection active); reduce hourly spend or contact your operator",
				usdc.Format(projected), usdc.Format(floor))
		}
		return &Verdict{
			Action: Deny,
			Rule:   r.Name(),
			Reason: reason,
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// mustParse parses a USDC decimal string into 6-decimal big.Int, panics on error.
func mustParse(s string) *big.Int {
	// Parse as whole dollars: append 6 zeros
	result, ok := new(big.Int).SetString(s+"000000", 10)
	if !ok {
		panic("supervisor: invalid amount: " + s)
	}
	return result
}
