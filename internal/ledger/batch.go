package ledger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/mbd888/alancoin/internal/traces"
	"github.com/mbd888/alancoin/internal/usdc"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ---------- request / result types ----------

// DebitRequest describes a single debit within a batch.
type DebitRequest struct {
	AgentAddr   string `json:"agentAddr"`
	Amount      string `json:"amount"`
	Reference   string `json:"reference"`
	Description string `json:"description"`
}

// CreditRequest describes a single credit within a batch.
type CreditRequest struct {
	AgentAddr   string `json:"agentAddr"`
	Amount      string `json:"amount"`
	TxHash      string `json:"txHash"`
	Description string `json:"description"`
}

// Transfer describes a directed transfer between two addresses.
type Transfer struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

// NetTransfer is the result of settlement netting -- a minimized transfer.
type NetTransfer struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"` // formatted via usdc.Format
}

// ---------- error sentinels ----------

var (
	ErrEmptyBatch       = errors.New("ledger: batch is empty")
	ErrBatchDebitFailed = errors.New("ledger: batch debit failed (rollback)")
)

// ---------- BatchDebit ----------

// BatchDebit processes multiple debits atomically.  All debits succeed or
// the entire batch is rolled back.  Each debit emits a ledger entry via
// the Store.Debit path so audit, events and metrics stay consistent.
func (l *Ledger) BatchDebit(ctx context.Context, reqs []DebitRequest) error {
	ctx, span := traces.StartSpan(ctx, "ledger.BatchDebit",
		attribute.Int("batch.size", len(reqs)))
	defer span.End()

	if len(reqs) == 0 {
		span.SetStatus(codes.Error, "empty batch")
		return ErrEmptyBatch
	}

	// Validate every request up-front so we don't partially mutate.
	for i, r := range reqs {
		amt, ok := usdc.Parse(r.Amount)
		if !ok || amt.Sign() <= 0 {
			span.SetStatus(codes.Error, "invalid amount")
			return fmt.Errorf("%w: request[%d] invalid amount %q", ErrInvalidAmount, i, r.Amount)
		}
	}

	done := observeOp("batch_debit")
	defer done()

	slog.Info("batch_debit start", "count", len(reqs))

	// Process debits sequentially via the store.  On any failure we
	// compensate previously committed debits by issuing refunds, giving
	// all-or-nothing semantics over the in-memory store.
	type committed struct {
		addr   string
		amount string
		ref    string
	}
	var applied []committed

	for i, r := range reqs {
		addr := strings.ToLower(r.AgentAddr)
		if err := l.store.Debit(ctx, addr, r.Amount, r.Reference, r.Description); err != nil {
			span.RecordError(err)
			slog.Warn("batch_debit item failed, rolling back",
				"index", i, "agent", addr, "error", err)

			// Compensate: refund everything we already debited.
			for j := len(applied) - 1; j >= 0; j-- {
				c := applied[j]
				if refErr := l.store.Refund(ctx, c.addr, c.amount, "batch_rollback:"+c.ref, "batch_debit_rollback"); refErr != nil {
					slog.Error("batch_debit rollback refund failed",
						"agent", c.addr, "amount", c.amount, "error", refErr)
				}
			}

			span.SetStatus(codes.Error, "batch debit rolled back")
			return fmt.Errorf("%w: item %d (%s): %v", ErrBatchDebitFailed, i, addr, err)
		}
		applied = append(applied, committed{addr: addr, amount: r.Amount, ref: r.Reference})

		l.appendEvent(ctx, addr, "batch_debit", r.Amount, r.Reference, "")
	}

	slog.Info("batch_debit complete", "count", len(reqs))
	return nil
}

// ---------- BatchCredit ----------

// BatchCredit processes multiple credits atomically.  Duplicates (same
// txHash already deposited) are silently skipped, making the operation
// idempotent.  Returns the number of credits actually applied.
func (l *Ledger) BatchCredit(ctx context.Context, reqs []CreditRequest) (int, error) {
	ctx, span := traces.StartSpan(ctx, "ledger.BatchCredit",
		attribute.Int("batch.size", len(reqs)))
	defer span.End()

	if len(reqs) == 0 {
		span.SetStatus(codes.Error, "empty batch")
		return 0, ErrEmptyBatch
	}

	// Validate amounts up-front.
	for i, r := range reqs {
		amt, ok := usdc.Parse(r.Amount)
		if !ok || amt.Sign() <= 0 {
			span.SetStatus(codes.Error, "invalid amount")
			return 0, fmt.Errorf("%w: request[%d] invalid amount %q", ErrInvalidAmount, i, r.Amount)
		}
	}

	done := observeOp("batch_credit")
	defer done()

	slog.Info("batch_credit start", "count", len(reqs))

	applied := 0
	for _, r := range reqs {
		addr := strings.ToLower(r.AgentAddr)

		// Skip duplicates (idempotent via txHash check).
		if r.TxHash != "" {
			exists, err := l.store.HasDeposit(ctx, r.TxHash)
			if err != nil {
				span.RecordError(err)
				return applied, fmt.Errorf("batch_credit HasDeposit check: %w", err)
			}
			if exists {
				slog.Info("batch_credit skipping duplicate",
					"agent", addr, "txHash", r.TxHash)
				continue
			}
		}

		if err := l.store.Credit(ctx, addr, r.Amount, r.TxHash, r.Description); err != nil {
			span.RecordError(err)
			// Duplicate constraint from the DB is not fatal; skip it.
			if errors.Is(err, ErrDuplicateDeposit) {
				slog.Info("batch_credit skipping duplicate (constraint)",
					"agent", addr, "txHash", r.TxHash)
				continue
			}
			return applied, fmt.Errorf("batch_credit item %s: %w", addr, err)
		}

		l.appendEvent(ctx, addr, "batch_credit", r.Amount, r.TxHash, "")
		applied++
	}

	slog.Info("batch_credit complete", "applied", applied, "total", len(reqs))
	return applied, nil
}

// ---------- NetSettle (settlement netting) ----------

// NetSettle computes the minimized set of transfers needed to settle all
// obligations.  For every pair of addresses with mutual flows it nets
// the amounts so only the difference is transferred.
//
// Example:
//
//	A->B $5, B->A $3  =>  A->B $2
//
// The function is pure: it only computes the result and does not touch
// the ledger.  Call Ledger.Transfer for each NetTransfer to execute.
func NetSettle(transfers []Transfer) ([]NetTransfer, error) {
	if len(transfers) == 0 {
		return nil, nil
	}

	// Build a net-position map:  net[addr] = total owed TO addr.
	// Positive = net creditor, negative = net debtor.
	net := make(map[string]*big.Int)

	for i, t := range transfers {
		amt, ok := usdc.Parse(t.Amount)
		if !ok || amt.Sign() <= 0 {
			return nil, fmt.Errorf("%w: transfer[%d] invalid amount %q", ErrInvalidAmount, i, t.Amount)
		}

		from := strings.ToLower(t.From)
		to := strings.ToLower(t.To)

		if from == to {
			continue // self-transfer is a no-op
		}

		if net[from] == nil {
			net[from] = big.NewInt(0)
		}
		if net[to] == nil {
			net[to] = big.NewInt(0)
		}

		net[from].Sub(net[from], amt)
		net[to].Add(net[to], amt)
	}

	// Split into creditors (positive) and debtors (negative).
	type party struct {
		addr   string
		amount *big.Int // always positive
	}
	var creditors, debtors []party

	for addr, n := range net {
		switch n.Sign() {
		case 1:
			creditors = append(creditors, party{addr: addr, amount: new(big.Int).Set(n)})
		case -1:
			debtors = append(debtors, party{addr: addr, amount: new(big.Int).Neg(n)})
		}
		// zero positions are dropped
	}

	// Greedy matching: pair debtors with creditors.
	var result []NetTransfer
	ci, di := 0, 0

	for ci < len(creditors) && di < len(debtors) {
		c := &creditors[ci]
		d := &debtors[di]

		// Transfer the smaller of the two remaining amounts.
		transfer := new(big.Int)
		if c.amount.Cmp(d.amount) <= 0 {
			transfer.Set(c.amount)
		} else {
			transfer.Set(d.amount)
		}

		result = append(result, NetTransfer{
			From:   d.addr,
			To:     c.addr,
			Amount: usdc.Format(transfer),
		})

		c.amount.Sub(c.amount, transfer)
		d.amount.Sub(d.amount, transfer)

		if c.amount.Sign() == 0 {
			ci++
		}
		if d.amount.Sign() == 0 {
			di++
		}
	}

	return result, nil
}
