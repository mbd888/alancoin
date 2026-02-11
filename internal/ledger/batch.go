package ledger

import (
	"context"
	"fmt"
	"math/big"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/usdc"
)

// BatchDebitRequest represents a single debit in a batch.
type BatchDebitRequest struct {
	AgentAddr   string `json:"agentAddr"`
	Amount      string `json:"amount"`
	Reference   string `json:"reference"`
	Description string `json:"description"`
}

// BatchDepositRequest represents a single deposit in a batch.
type BatchDepositRequest struct {
	AgentAddr   string `json:"agentAddr"`
	Amount      string `json:"amount"`
	TxHash      string `json:"txHash"`
	Description string `json:"description"`
}

// Transfer represents a directed payment for settlement netting.
type Transfer struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

// NetSettlement represents a netted payment between two parties.
type NetSettlement struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

// ComputeNetSettlements computes net settlements from a list of transfers.
// e.g., A→B $5 + B→A $3 = net A→B $2
func ComputeNetSettlements(transfers []Transfer) []NetSettlement {
	// Build net flows: key is "min:max", value is net amount (positive = min→max direction)
	type pair struct{ a, b string }
	nets := make(map[pair]*big.Int)

	for _, t := range transfers {
		amt, ok := usdc.Parse(t.Amount)
		if !ok || amt.Sign() <= 0 {
			continue
		}

		// Normalize pair ordering
		a, b := t.From, t.To
		if a > b {
			a, b = b, a
			amt.Neg(amt) // reverse direction
		}

		p := pair{a, b}
		if existing, ok := nets[p]; ok {
			existing.Add(existing, amt)
		} else {
			nets[p] = new(big.Int).Set(amt)
		}
	}

	var settlements []NetSettlement
	for p, net := range nets {
		if net.Sign() == 0 {
			continue
		}

		from, to := p.a, p.b
		amount := net
		if amount.Sign() < 0 {
			from, to = to, from
			amount = new(big.Int).Neg(amount)
		}

		settlements = append(settlements, NetSettlement{
			From:   from,
			To:     to,
			Amount: usdc.Format(amount),
		})
	}

	return settlements
}

// ExecuteSettlement applies net settlements using the Ledger's Transfer method,
// ensuring all cross-cutting concerns (events, audit, alerts) fire.
func ExecuteSettlement(ctx context.Context, l *Ledger, settlements []NetSettlement) error {
	for _, s := range settlements {
		ref := "settlement:" + idgen.New()
		if err := l.Transfer(ctx, s.From, s.To, s.Amount, ref); err != nil {
			return fmt.Errorf("settlement %s→%s failed: %w", s.From, s.To, err)
		}
	}
	return nil
}
