package supervisor

import (
	"context"
	"fmt"
	"math/big"

	"github.com/mbd888/alancoin/internal/ledger"
	"github.com/mbd888/alancoin/internal/usdc"
)

// BalanceConcentrationRule flags agents whose funds are dangerously
// concentrated in pending holds or escrow.
//
// An agent with 90%+ of funds in pending has committed nearly all liquidity.
// An agent with 90%+ in escrow has everything locked. These patterns are
// invisible to velocity-based rules and indicate over-commitment risk.
//
// The rule flags (not denies) because concentration isn't always bad —
// it provides signal for operators and the forensics system.
type BalanceConcentrationRule struct {
	// BalanceProvider fetches the current balance for an agent.
	BalanceProvider ledger.Service

	// MinTotal is the minimum total balance (in 6-decimal USDC) before
	// this rule activates. Agents with tiny balances naturally have
	// extreme partitions — this avoids false flags.
	// Default: $10 (10_000_000).
	MinTotal *big.Int

	// PendingConcentrationThreshold flags when pending/total > this value.
	// Default: 0.90 (90% of funds are committed holds).
	PendingConcentrationThreshold float64

	// EscrowConcentrationThreshold flags when escrowed/total > this value.
	// Default: 0.90 (90% of funds are locked in escrow).
	EscrowConcentrationThreshold float64
}

// DefaultMinTotal is $10 in 6-decimal USDC units.
var DefaultMinTotal = big.NewInt(10_000_000)

func (r *BalanceConcentrationRule) Name() string { return "balance_concentration" }

func (r *BalanceConcentrationRule) Evaluate(ctx context.Context, _ *SpendGraph, ec *EvalContext) *Verdict {
	if r.BalanceProvider == nil {
		return nil
	}

	bal, err := r.BalanceProvider.GetBalance(ctx, ec.AgentAddr)
	if err != nil {
		return nil
	}

	available, okA := usdc.Parse(bal.Available)
	pending, okP := usdc.Parse(bal.Pending)
	escrowed, okE := usdc.Parse(bal.Escrowed)
	if !okA || !okP || !okE {
		return nil
	}

	total := new(big.Int).Add(available, pending)
	total.Add(total, escrowed)

	minTotal := r.MinTotal
	if minTotal == nil {
		minTotal = DefaultMinTotal
	}
	if total.Cmp(minTotal) < 0 {
		return nil
	}

	totalF := float64(total.Int64())
	if totalF <= 0 {
		return nil
	}

	pP := float64(pending.Int64()) / totalF
	pE := float64(escrowed.Int64()) / totalF

	pendingThresh := r.PendingConcentrationThreshold
	if pendingThresh <= 0 {
		pendingThresh = 0.90
	}
	if pP > pendingThresh {
		return &Verdict{
			Action: Flag,
			Rule:   r.Name(),
			Reason: fmt.Sprintf(
				"%.1f%% of balance in pending holds (threshold %.0f%%); agent may be over-committed",
				pP*100, pendingThresh*100),
		}
	}

	escrowThresh := r.EscrowConcentrationThreshold
	if escrowThresh <= 0 {
		escrowThresh = 0.90
	}
	if pE > escrowThresh {
		return &Verdict{
			Action: Flag,
			Rule:   r.Name(),
			Reason: fmt.Sprintf(
				"%.1f%% of balance in escrow (threshold %.0f%%); agent has excessive locked funds",
				pE*100, escrowThresh*100),
		}
	}

	return nil
}
