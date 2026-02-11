package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sync"
	"time"

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

// --- PostgresBatchStore ---

// PostgresBatchStore implements BatchStore with PostgreSQL.
type PostgresBatchStore struct {
	db *sql.DB
}

// NewPostgresBatchStore creates a PostgreSQL-backed batch store.
func NewPostgresBatchStore(db *sql.DB) *PostgresBatchStore {
	return &PostgresBatchStore{db: db}
}

func (s *PostgresBatchStore) BatchDebit(ctx context.Context, reqs []BatchDebitRequest) []error {
	errs := make([]error, len(reqs))

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	defer func() { _ = tx.Rollback() }()

	for i, req := range reqs {
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_balances SET
				available  = available - $2::NUMERIC(20,6),
				total_out  = total_out + $2::NUMERIC(20,6),
				updated_at = NOW()
			WHERE agent_address = $1 AND available >= $2::NUMERIC(20,6)
		`, req.AgentAddr, req.Amount)
		if err != nil {
			errs[i] = fmt.Errorf("debit failed: %w", err)
			continue
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			errs[i] = ErrInsufficientBalance
			continue
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
			VALUES ($1, $2, 'spend', $3::NUMERIC(20,6), $4, $5, NOW())
		`, idgen.New(), req.AgentAddr, req.Amount, req.Reference, req.Description)
		if err != nil {
			errs[i] = fmt.Errorf("entry failed: %w", err)
		}
	}

	// Check for any errors — if any debit failed, rollback all
	for _, e := range errs {
		if e != nil {
			return errs
		}
	}

	if err := tx.Commit(); err != nil {
		for i := range errs {
			errs[i] = err
		}
	}
	return errs
}

func (s *PostgresBatchStore) BatchDeposit(ctx context.Context, reqs []BatchDepositRequest) []error {
	errs := make([]error, len(reqs))

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		for i := range errs {
			errs[i] = err
		}
		return errs
	}
	defer func() { _ = tx.Rollback() }()

	for i, req := range reqs {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
			VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
			ON CONFLICT (agent_address) DO UPDATE SET
				available  = agent_balances.available + $2::NUMERIC(20,6),
				total_in   = agent_balances.total_in  + $2::NUMERIC(20,6),
				updated_at = NOW()
		`, req.AgentAddr, req.Amount)
		if err != nil {
			errs[i] = fmt.Errorf("deposit failed: %w", err)
			continue
		}

		_, err = tx.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
			VALUES ($1, $2, 'deposit', $3::NUMERIC(20,6), $4, $5, NOW())
		`, idgen.New(), req.AgentAddr, req.Amount, req.TxHash, req.Description)
		if err != nil {
			errs[i] = fmt.Errorf("entry failed: %w", err)
		}
	}

	for _, e := range errs {
		if e != nil {
			return errs
		}
	}

	if err := tx.Commit(); err != nil {
		for i := range errs {
			errs[i] = err
		}
	}
	return errs
}

// --- MemoryBatchStore ---

// MemoryBatchStore implements BatchStore for demo/testing.
type MemoryBatchStore struct {
	store *MemoryStore
}

// NewMemoryBatchStore creates a batch store wrapping a MemoryStore.
func NewMemoryBatchStore(store *MemoryStore) *MemoryBatchStore {
	return &MemoryBatchStore{store: store}
}

func (s *MemoryBatchStore) BatchDebit(ctx context.Context, reqs []BatchDebitRequest) []error {
	errs := make([]error, len(reqs))

	// Pre-check all balances under a single lock
	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	for i, req := range reqs {
		bal, ok := s.store.balances[req.AgentAddr]
		if !ok {
			errs[i] = ErrAgentNotFound
			continue
		}
		avail, _ := usdc.Parse(bal.Available)
		sub, _ := usdc.Parse(req.Amount)
		if avail.Cmp(sub) < 0 {
			errs[i] = ErrInsufficientBalance
		}
	}

	// If any failed, return without modifying
	for _, e := range errs {
		if e != nil {
			return errs
		}
	}

	// Apply all debits
	for _, req := range reqs {
		bal := s.store.balances[req.AgentAddr]
		avail, _ := usdc.Parse(bal.Available)
		totalOut, _ := usdc.Parse(bal.TotalOut)
		sub, _ := usdc.Parse(req.Amount)

		avail.Sub(avail, sub)
		totalOut.Add(totalOut, sub)
		bal.Available = usdc.Format(avail)
		bal.TotalOut = usdc.Format(totalOut)
		bal.UpdatedAt = now()

		s.store.entries = append(s.store.entries, &Entry{
			ID:          idgen.WithPrefix("entry_"),
			AgentAddr:   req.AgentAddr,
			Type:        "spend",
			Amount:      req.Amount,
			Reference:   req.Reference,
			Description: req.Description,
			CreatedAt:   now(),
		})
	}

	return errs
}

func (s *MemoryBatchStore) BatchDeposit(ctx context.Context, reqs []BatchDepositRequest) []error {
	errs := make([]error, len(reqs))

	s.store.mu.Lock()
	defer s.store.mu.Unlock()

	for i, req := range reqs {
		bal, ok := s.store.balances[req.AgentAddr]
		if !ok {
			bal = &Balance{
				AgentAddr:   req.AgentAddr,
				Available:   "0",
				Pending:     "0",
				Escrowed:    "0",
				CreditLimit: "0",
				CreditUsed:  "0",
				TotalIn:     "0",
				TotalOut:    "0",
			}
			s.store.balances[req.AgentAddr] = bal
		}

		avail, _ := usdc.Parse(bal.Available)
		totalIn, _ := usdc.Parse(bal.TotalIn)
		add, _ := usdc.Parse(req.Amount)

		avail.Add(avail, add)
		totalIn.Add(totalIn, add)

		bal.Available = usdc.Format(avail)
		bal.TotalIn = usdc.Format(totalIn)
		bal.UpdatedAt = now()

		s.store.entries = append(s.store.entries, &Entry{
			ID:          "entry_" + req.TxHash,
			AgentAddr:   req.AgentAddr,
			Type:        "deposit",
			Amount:      req.Amount,
			TxHash:      req.TxHash,
			Description: req.Description,
			CreatedAt:   now(),
		})

		_ = i // used for error tracking
	}

	return errs
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

var nowFn = func() time.Time { return time.Now() }

func now() time.Time { return nowFn() }

// mu is used for settlement locking
var settleMu sync.Mutex
