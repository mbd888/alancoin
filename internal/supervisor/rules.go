package supervisor

import (
	"context"
	"fmt"
	"math/big"
	"time"
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

	if pct.Int64() > 80 {
		return &Verdict{
			Action: Flag,
			Rule:   r.Name(),
			Reason: fmt.Sprintf("%d%% of volume concentrated on counterparty %s",
				pct.Int64(), ec.Counterparty),
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
