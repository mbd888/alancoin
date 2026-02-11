package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/idgen"
	"github.com/mbd888/alancoin/internal/usdc"
)

// PostgresStore implements Store with PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed ledger store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the ledger tables with NUMERIC columns
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS agent_balances (
			agent_address   VARCHAR(42) PRIMARY KEY,
			available       NUMERIC(20,6) NOT NULL DEFAULT 0,
			pending         NUMERIC(20,6) NOT NULL DEFAULT 0,
			escrowed        NUMERIC(20,6) NOT NULL DEFAULT 0,
			credit_limit    NUMERIC(20,6) NOT NULL DEFAULT 0,
			credit_used     NUMERIC(20,6) NOT NULL DEFAULT 0,
			total_in        NUMERIC(20,6) NOT NULL DEFAULT 0,
			total_out       NUMERIC(20,6) NOT NULL DEFAULT 0,
			updated_at      TIMESTAMPTZ DEFAULT NOW(),
			CONSTRAINT chk_available_nonneg     CHECK (available >= 0),
			CONSTRAINT chk_pending_nonneg       CHECK (pending >= 0),
			CONSTRAINT chk_total_in_nonneg      CHECK (total_in >= 0),
			CONSTRAINT chk_escrowed_nonneg      CHECK (escrowed >= 0),
			CONSTRAINT chk_credit_limit_nonneg  CHECK (credit_limit >= 0),
			CONSTRAINT chk_credit_used_nonneg   CHECK (credit_used >= 0),
			CONSTRAINT chk_credit_used_lte_limit CHECK (credit_used <= credit_limit)
		);

		CREATE TABLE IF NOT EXISTS ledger_entries (
			id              VARCHAR(36) PRIMARY KEY,
			agent_address   VARCHAR(42) NOT NULL,
			type            VARCHAR(20) NOT NULL,
			amount          NUMERIC(20,6) NOT NULL,
			tx_hash         VARCHAR(66),
			reference       VARCHAR(255),
			description     TEXT,
			created_at      TIMESTAMPTZ DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_ledger_agent ON ledger_entries(agent_address);
		CREATE INDEX IF NOT EXISTS idx_ledger_tx ON ledger_entries(tx_hash);
		CREATE INDEX IF NOT EXISTS idx_ledger_created ON ledger_entries(created_at DESC);

		-- Prevent double-crediting of the same deposit (TOCTOU defense)
		CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_deposit_unique
			ON ledger_entries(tx_hash) WHERE type = 'deposit' AND tx_hash IS NOT NULL AND tx_hash != '';
	`)
	return err
}

// GetBalance retrieves an agent's balance
func (p *PostgresStore) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	bal := &Balance{AgentAddr: agentAddr}

	err := p.db.QueryRowContext(ctx, `
		SELECT available, pending, COALESCE(escrowed, 0),
		       COALESCE(credit_limit, 0), COALESCE(credit_used, 0),
		       total_in, total_out, updated_at
		FROM agent_balances WHERE agent_address = $1
	`, agentAddr).Scan(&bal.Available, &bal.Pending, &bal.Escrowed,
		&bal.CreditLimit, &bal.CreditUsed,
		&bal.TotalIn, &bal.TotalOut, &bal.UpdatedAt)

	if err == sql.ErrNoRows {
		return &Balance{
			AgentAddr:   agentAddr,
			Available:   "0",
			Pending:     "0",
			Escrowed:    "0",
			CreditLimit: "0",
			CreditUsed:  "0",
			TotalIn:     "0",
			TotalOut:    "0",
			UpdatedAt:   time.Now(),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return bal, nil
}

// Credit adds funds to an agent's balance, auto-repaying credit first
func (p *PostgresStore) Credit(ctx context.Context, agentAddr, amount, txHash, description string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert balance, auto-repay credit in the same atomic transaction.
	// If credit_used > 0, reduce it by min(amount, credit_used) and add remainder to available.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available    = agent_balances.available
			             + ($2::NUMERIC(20,6) - LEAST($2::NUMERIC(20,6), COALESCE(agent_balances.credit_used, 0))),
			credit_used  = COALESCE(agent_balances.credit_used, 0)
			             - LEAST($2::NUMERIC(20,6), COALESCE(agent_balances.credit_used, 0)),
			total_in     = agent_balances.total_in + $2::NUMERIC(20,6),
			updated_at   = NOW()
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record deposit entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
		VALUES ($1, $2, 'deposit', $3::NUMERIC(20,6), $4, $5, NOW())
	`, idgen.New(), agentAddr, amount, txHash, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Debit removes funds from an agent's balance with credit support.
// Uses available balance first, then draws from credit for any shortfall.
func (p *PostgresStore) Debit(ctx context.Context, agentAddr, amount, reference, description string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Credit-aware debit: debit from available first, draw gap from credit.
	// gap = max(0, amount - available)
	// Fails if available + (credit_limit - credit_used) < amount.
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			credit_used = COALESCE(credit_used, 0)
			            + GREATEST(0, $2::NUMERIC(20,6) - available),
			available   = GREATEST(0, available - $2::NUMERIC(20,6)),
			total_out   = total_out + $2::NUMERIC(20,6),
			updated_at  = NOW()
		WHERE agent_address = $1
		  AND available + (COALESCE(credit_limit, 0) - COALESCE(credit_used, 0)) >= $2::NUMERIC(20,6)
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		// Check if agent exists to distinguish not-found from insufficient balance
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, agentAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'spend', $3::NUMERIC(20,6), $4, $5, NOW())
	`, idgen.New(), agentAddr, amount, reference, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Refund credits back funds to an agent's balance (reverses a failed debit)
func (p *PostgresStore) Refund(ctx context.Context, agentAddr, amount, reference, description string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Idempotency: check if this reference was already refunded
	var exists bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM ledger_entries WHERE agent_address = $1 AND type = 'refund' AND reference = $2)
	`, agentAddr, reference).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check refund idempotency: %w", err)
	}
	if exists {
		return ErrDuplicateRefund
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available + LEAST($2::NUMERIC(20,6), total_out),
			total_out  = GREATEST(0, total_out - $2::NUMERIC(20,6)),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record refund entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'refund', $3::NUMERIC(20,6), $4, $5, NOW())
	`, idgen.New(), agentAddr, amount, reference, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Withdraw processes a withdrawal with row-level locking.
func (p *PostgresStore) Withdraw(ctx context.Context, agentAddr, amount, txHash string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Atomic debit with balance guard — prevents overdraft without relying on CHECK constraint error
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND available >= $2::NUMERIC(20,6)
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, agentAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
		VALUES ($1, $2, 'withdrawal', $3::NUMERIC(20,6), $4, 'withdrawal', NOW())
	`, idgen.New(), agentAddr, amount, txHash)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Transfer atomically debits sender and credits receiver in a single transaction.
// Prevents fund loss from crashes between separate Debit and Credit calls.
func (p *PostgresStore) Transfer(ctx context.Context, fromAddr, toAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Debit sender (credit-aware: draw from credit if available is insufficient)
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			credit_used = COALESCE(credit_used, 0)
			            + GREATEST(0, $2::NUMERIC(20,6) - available),
			available   = GREATEST(0, available - $2::NUMERIC(20,6)),
			total_out   = total_out + $2::NUMERIC(20,6),
			updated_at  = NOW()
		WHERE agent_address = $1
		  AND available + (COALESCE(credit_limit, 0) - COALESCE(credit_used, 0)) >= $2::NUMERIC(20,6)
	`, fromAddr, amount)
	if err != nil {
		return fmt.Errorf("transfer debit failed: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, fromAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Credit receiver (auto-repay credit, upsert balance)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available    = agent_balances.available
			             + ($2::NUMERIC(20,6) - LEAST($2::NUMERIC(20,6), COALESCE(agent_balances.credit_used, 0))),
			credit_used  = COALESCE(agent_balances.credit_used, 0)
			             - LEAST($2::NUMERIC(20,6), COALESCE(agent_balances.credit_used, 0)),
			total_in     = agent_balances.total_in + $2::NUMERIC(20,6),
			updated_at   = NOW()
	`, toAddr, amount)
	if err != nil {
		return fmt.Errorf("transfer credit failed: %w", err)
	}

	// Record both ledger entries
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'transfer_out', $3::NUMERIC(20,6), $4, 'transfer_out', NOW())
	`, idgen.New(), fromAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record transfer_out entry: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'transfer_in', $3::NUMERIC(20,6), $4, 'transfer_in', NOW())
	`, idgen.New(), toAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record transfer_in entry: %w", err)
	}

	return tx.Commit()
}

// Hold places a hold on funds (moves from available to pending) with credit support.
// If available < amount, draws the shortfall from credit line.
// Records a credit_draw_hold entry so ReleaseHold can reverse the credit draw.
func (p *PostgresStore) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Read current balance under row lock to compute credit draw
	var currentAvailable, currentCreditLimit, currentCreditUsed string
	err = tx.QueryRowContext(ctx, `
		SELECT available, COALESCE(credit_limit, 0), COALESCE(credit_used, 0)
		FROM agent_balances
		WHERE agent_address = $1
		FOR UPDATE
	`, agentAddr).Scan(&currentAvailable, &currentCreditLimit, &currentCreditUsed)
	if err == sql.ErrNoRows {
		return ErrAgentNotFound
	}
	if err != nil {
		return fmt.Errorf("failed to read balance: %w", err)
	}

	avail, _ := usdc.Parse(currentAvailable)
	creditLimit, _ := usdc.Parse(currentCreditLimit)
	creditUsed, _ := usdc.Parse(currentCreditUsed)
	holdAmount, _ := usdc.Parse(amount)

	creditAvailable := new(big.Int).Sub(creditLimit, creditUsed)
	totalSpendable := new(big.Int).Add(new(big.Int).Set(avail), creditAvailable)

	if totalSpendable.Cmp(holdAmount) < 0 {
		return ErrInsufficientBalance
	}

	// Compute credit draw: gap = max(0, holdAmount - available)
	var gap *big.Int
	if avail.Cmp(holdAmount) >= 0 {
		gap = big.NewInt(0)
	} else {
		gap = new(big.Int).Sub(holdAmount, avail)
	}

	// Update balance: draw from available + credit, move to pending
	_, err = tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			credit_used = credit_used + $2::NUMERIC(20,6),
			available   = GREATEST(0, available - $3::NUMERIC(20,6)),
			pending     = pending + $3::NUMERIC(20,6),
			updated_at  = NOW()
		WHERE agent_address = $1
	`, agentAddr, usdc.Format(gap), amount)
	if err != nil {
		return fmt.Errorf("failed to place hold: %w", err)
	}

	// Record hold entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'hold', $3::NUMERIC(20,6), $4, 'pending_transfer', NOW())
	`, idgen.New(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record hold entry: %w", err)
	}

	// Record credit draw entry so ReleaseHold can reverse it
	if gap.Sign() > 0 {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
			VALUES ($1, $2, 'credit_draw_hold', $3::NUMERIC(20,6), $4, 'credit_draw_for_hold', NOW())
		`, idgen.New(), agentAddr, usdc.Format(gap), reference)
		if err != nil {
			return fmt.Errorf("failed to record credit draw entry: %w", err)
		}
	}

	return tx.Commit()
}

// ConfirmHold finalizes a held amount (moves from pending to total_out).
// Called after on-chain transfer is confirmed.
func (p *PostgresStore) ConfirmHold(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			pending    = pending   - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND pending >= $2::NUMERIC(20,6)
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to confirm hold: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, agentAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Record confirmation entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'spend', $3::NUMERIC(20,6), $4, 'transfer_confirmed', NOW())
	`, idgen.New(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record confirmation entry: %w", err)
	}

	// Clean up credit_draw_hold tracking entry (credit stays drawn — confirmed spend)
	_, _ = tx.ExecContext(ctx, `
		DELETE FROM ledger_entries
		WHERE agent_address = $1 AND type = 'credit_draw_hold' AND reference = $2
	`, agentAddr, reference)

	return tx.Commit()
}

// ReleaseHold returns held funds to available (transfer failed/timed out).
// Reverses any credit draw that was made during the original Hold.
func (p *PostgresStore) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Look up any credit draw associated with this hold
	var creditDrawAmount sql.NullString
	_ = tx.QueryRowContext(ctx, `
		SELECT amount FROM ledger_entries
		WHERE agent_address = $1 AND type = 'credit_draw_hold' AND reference = $2
	`, agentAddr, reference).Scan(&creditDrawAmount)

	creditDraw := "0"
	if creditDrawAmount.Valid && creditDrawAmount.String != "" {
		creditDraw = creditDrawAmount.String
	}

	// Release hold: return non-credit portion to available, reverse credit draw
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available   = available + ($2::NUMERIC(20,6) - $3::NUMERIC(20,6)),
			pending     = pending   - $2::NUMERIC(20,6),
			credit_used = credit_used - $3::NUMERIC(20,6),
			updated_at  = NOW()
		WHERE agent_address = $1
		  AND pending >= $2::NUMERIC(20,6)
	`, agentAddr, amount, creditDraw)
	if err != nil {
		return fmt.Errorf("failed to release hold: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, agentAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Record release entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'release', $3::NUMERIC(20,6), $4, 'hold_released', NOW())
	`, idgen.New(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record release entry: %w", err)
	}

	return tx.Commit()
}

// SettleHold atomically moves funds from buyer's pending to seller's available.
// Used by gateway and stream settlement to avoid separate ConfirmHold + Deposit calls.
func (p *PostgresStore) SettleHold(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Debit buyer's pending, increment total_out
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			pending    = pending   - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND pending >= $2::NUMERIC(20,6)
	`, buyerAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to debit buyer pending: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, buyerAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Credit seller's available, increment total_in (upsert)
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available  = agent_balances.available + $2::NUMERIC(20,6),
			total_in   = agent_balances.total_in  + $2::NUMERIC(20,6),
			updated_at = NOW()
	`, sellerAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to credit seller: %w", err)
	}

	// Record entries for both parties
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'spend', $3::NUMERIC(20,6), $4, 'settle_hold', NOW())
	`, idgen.New(), buyerAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record buyer entry: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'deposit', $3::NUMERIC(20,6), $4, 'settle_hold_receive', NOW())
	`, idgen.New(), sellerAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record seller entry: %w", err)
	}

	return tx.Commit()
}

// EscrowLock moves funds from available to escrowed.
func (p *PostgresStore) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available - $2::NUMERIC(20,6),
			escrowed   = escrowed  + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND available >= $2::NUMERIC(20,6)
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to lock escrow: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, agentAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_lock', $3::NUMERIC(20,6), $4, 'escrow_locked', NOW())
	`, idgen.New(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record escrow lock entry: %w", err)
	}

	return tx.Commit()
}

// ReleaseEscrow moves funds from buyer's escrowed to seller's available.
func (p *PostgresStore) ReleaseEscrow(ctx context.Context, buyerAddr, sellerAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Debit buyer's escrowed
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			escrowed   = escrowed  - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND escrowed >= $2::NUMERIC(20,6)
	`, buyerAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to debit buyer escrow: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, buyerAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Credit seller's available
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available  = agent_balances.available + $2::NUMERIC(20,6),
			total_in   = agent_balances.total_in  + $2::NUMERIC(20,6),
			updated_at = NOW()
	`, sellerAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to credit seller: %w", err)
	}

	// Record entries for both parties
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_release', $3::NUMERIC(20,6), $4, 'escrow_released_to_seller', NOW())
	`, idgen.New(), buyerAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record buyer escrow release entry: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_receive', $3::NUMERIC(20,6), $4, 'escrow_payment_received', NOW())
	`, idgen.New(), sellerAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record seller escrow receive entry: %w", err)
	}

	return tx.Commit()
}

// RefundEscrow returns escrowed funds to available (dispute refund).
func (p *PostgresStore) RefundEscrow(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			escrowed   = escrowed  - $2::NUMERIC(20,6),
			available  = available + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND escrowed >= $2::NUMERIC(20,6)
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to refund escrow: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, agentAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_refund', $3::NUMERIC(20,6), $4, 'escrow_refunded', NOW())
	`, idgen.New(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record escrow refund entry: %w", err)
	}

	return tx.Commit()
}

// PartialEscrowSettle atomically splits escrowed funds: release to seller, refund to buyer.
// Single serializable transaction prevents money loss from partial failures.
func (p *PostgresStore) PartialEscrowSettle(ctx context.Context, buyerAddr, sellerAddr, releaseAmount, refundAmount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Compute total to deduct from escrowed
	releaseBig, _ := usdc.Parse(releaseAmount)
	refundBig, _ := usdc.Parse(refundAmount)
	totalBig := new(big.Int).Add(releaseBig, refundBig)
	totalStr := usdc.Format(totalBig)

	// Debit buyer's escrowed for total, credit available for refund, total_out for release
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			escrowed   = escrowed  - $2::NUMERIC(20,6),
			total_out  = total_out + $3::NUMERIC(20,6),
			available  = available + $4::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
		  AND escrowed >= $2::NUMERIC(20,6)
	`, buyerAddr, totalStr, releaseAmount, refundAmount)
	if err != nil {
		return fmt.Errorf("failed to debit buyer escrow: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		var exists bool
		_ = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_balances WHERE agent_address = $1)`, buyerAddr).Scan(&exists)
		if !exists {
			return ErrAgentNotFound
		}
		return ErrInsufficientBalance
	}

	// Credit seller's available for release amount
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available  = agent_balances.available + $2::NUMERIC(20,6),
			total_in   = agent_balances.total_in  + $2::NUMERIC(20,6),
			updated_at = NOW()
	`, sellerAddr, releaseAmount)
	if err != nil {
		return fmt.Errorf("failed to credit seller: %w", err)
	}

	// Record entries for buyer: release + refund
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_release', $3::NUMERIC(20,6), $4, 'partial_escrow_release', NOW())
	`, idgen.New(), buyerAddr, releaseAmount, reference)
	if err != nil {
		return fmt.Errorf("failed to record buyer release entry: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_refund', $3::NUMERIC(20,6), $4, 'partial_escrow_refund', NOW())
	`, idgen.New(), buyerAddr, refundAmount, reference)
	if err != nil {
		return fmt.Errorf("failed to record buyer refund entry: %w", err)
	}

	// Record entry for seller
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_receive', $3::NUMERIC(20,6), $4, 'partial_escrow_receive', NOW())
	`, idgen.New(), sellerAddr, releaseAmount, reference)
	if err != nil {
		return fmt.Errorf("failed to record seller entry: %w", err)
	}

	return tx.Commit()
}

// GetHistory retrieves ledger entries for an agent
func (p *PostgresStore) GetHistory(ctx context.Context, agentAddr string, limit int) ([]*Entry, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_address, type, amount, tx_hash, reference, description, created_at
		FROM ledger_entries
		WHERE agent_address = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var entries []*Entry
	for rows.Next() {
		e := &Entry{}
		var txHash, reference, description sql.NullString
		if err := rows.Scan(&e.ID, &e.AgentAddr, &e.Type, &e.Amount, &txHash, &reference, &description, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.TxHash = txHash.String
		e.Reference = reference.String
		e.Description = description.String
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// HasDeposit checks if a deposit tx has already been processed
func (p *PostgresStore) HasDeposit(ctx context.Context, txHash string) (bool, error) {
	var count int
	err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ledger_entries WHERE tx_hash = $1 AND type = 'deposit'
	`, txHash).Scan(&count)
	return count > 0, err
}

// SetCreditLimit sets the maximum credit for an agent
func (p *PostgresStore) SetCreditLimit(ctx context.Context, agentAddr, limit string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, credit_limit, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			credit_limit = $2::NUMERIC(20,6),
			updated_at   = NOW()
	`, agentAddr, limit)
	if err != nil {
		return fmt.Errorf("failed to set credit limit: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, description, created_at)
		VALUES ($1, $2, 'credit_limit_set', $3::NUMERIC(20,6), 'credit_limit_set', NOW())
	`, idgen.New(), agentAddr, limit)
	if err != nil {
		return fmt.Errorf("failed to record credit limit entry: %w", err)
	}

	return tx.Commit()
}

// UseCredit draws from the agent's credit line
func (p *PostgresStore) UseCredit(ctx context.Context, agentAddr, amount string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// CHECK constraint ensures credit_used <= credit_limit
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			credit_used = COALESCE(credit_used, 0) + $2::NUMERIC(20,6),
			updated_at  = NOW()
		WHERE agent_address = $1
		  AND COALESCE(credit_used, 0) + $2::NUMERIC(20,6) <= COALESCE(credit_limit, 0)
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to use credit: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrInsufficientBalance
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, description, created_at)
		VALUES ($1, $2, 'credit_draw', $3::NUMERIC(20,6), 'credit_draw', NOW())
	`, idgen.New(), agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to record credit draw entry: %w", err)
	}

	return tx.Commit()
}

// RepayCredit reduces outstanding credit usage
func (p *PostgresStore) RepayCredit(ctx context.Context, agentAddr, amount string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Capture the actual repay amount (min of requested amount and outstanding credit)
	// before the UPDATE modifies credit_used.
	var actualRepay string
	err = tx.QueryRowContext(ctx, `
		SELECT LEAST($2::NUMERIC(20,6), COALESCE(credit_used, 0))
		FROM agent_balances WHERE agent_address = $1
	`, agentAddr, amount).Scan(&actualRepay)
	if err != nil {
		return ErrAgentNotFound
	}

	// Repay up to what's owed: min(amount, credit_used)
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			credit_used = COALESCE(credit_used, 0) - LEAST($2::NUMERIC(20,6), COALESCE(credit_used, 0)),
			updated_at  = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to repay credit: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return ErrAgentNotFound
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, description, created_at)
		VALUES ($1, $2, 'credit_repay', $3::NUMERIC(20,6), 'credit_repay', NOW())
	`, idgen.New(), agentAddr, actualRepay)
	if err != nil {
		return fmt.Errorf("failed to record credit repay entry: %w", err)
	}

	return tx.Commit()
}

// GetCreditInfo returns the current credit limit and usage
func (p *PostgresStore) GetCreditInfo(ctx context.Context, agentAddr string) (string, string, error) {
	var creditLimit, creditUsed string
	err := p.db.QueryRowContext(ctx, `
		SELECT COALESCE(credit_limit, 0), COALESCE(credit_used, 0)
		FROM agent_balances WHERE agent_address = $1
	`, agentAddr).Scan(&creditLimit, &creditUsed)

	if err == sql.ErrNoRows {
		return "0", "0", nil
	}
	if err != nil {
		return "", "", err
	}
	return creditLimit, creditUsed, nil
}

// SumAllBalances returns the sum of all agent balances.
func (p *PostgresStore) SumAllBalances(ctx context.Context) (string, string, string, error) {
	var available, pending, escrowed string
	err := p.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(available), 0), COALESCE(SUM(pending), 0), COALESCE(SUM(escrowed), 0)
		FROM agent_balances
	`).Scan(&available, &pending, &escrowed)
	if err != nil {
		return "0", "0", "0", err
	}
	return available, pending, escrowed, nil
}

// GetEntry retrieves a single ledger entry by ID.
func (p *PostgresStore) GetEntry(ctx context.Context, entryID string) (*Entry, error) {
	e := &Entry{}
	var txHash, reference, description sql.NullString
	var reversedAt sql.NullTime
	var reversedBy, reversalOf sql.NullString

	err := p.db.QueryRowContext(ctx, `
		SELECT id, agent_address, type, amount, tx_hash, reference, description,
		       reversed_at, reversed_by, reversal_of, created_at
		FROM ledger_entries WHERE id = $1
	`, entryID).Scan(&e.ID, &e.AgentAddr, &e.Type, &e.Amount,
		&txHash, &reference, &description,
		&reversedAt, &reversedBy, &reversalOf, &e.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, ErrEntryNotFound
	}
	if err != nil {
		return nil, err
	}

	e.TxHash = txHash.String
	e.Reference = reference.String
	e.Description = description.String
	if reversedAt.Valid {
		e.ReversedAt = &reversedAt.Time
	}
	e.ReversedBy = reversedBy.String
	e.ReversalOf = reversalOf.String
	return e, nil
}

// Reverse creates a compensating entry and marks the original as reversed.
func (p *PostgresStore) Reverse(ctx context.Context, entryID, reason, adminID string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Get original entry
	var agentAddr, entryType, amount string
	var reversedAt sql.NullTime
	err = tx.QueryRowContext(ctx, `
		SELECT agent_address, type, amount, reversed_at
		FROM ledger_entries WHERE id = $1 FOR UPDATE
	`, entryID).Scan(&agentAddr, &entryType, &amount, &reversedAt)
	if err == sql.ErrNoRows {
		return ErrEntryNotFound
	}
	if err != nil {
		return err
	}
	if reversedAt.Valid {
		return ErrAlreadyReversed
	}

	// Create compensating balance change
	switch entryType {
	case "deposit":
		// Reverse deposit = deduct from available
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_balances SET
				available = available - $2::NUMERIC(20,6),
				total_in  = total_in  - $2::NUMERIC(20,6),
				updated_at = NOW()
			WHERE agent_address = $1 AND available >= $2::NUMERIC(20,6)
		`, agentAddr, amount)
		if err != nil {
			return fmt.Errorf("reversal balance update failed: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return ErrInsufficientBalance
		}
	case "spend", "withdrawal":
		// Reverse spend/withdrawal = credit back
		_, err := tx.ExecContext(ctx, `
			UPDATE agent_balances SET
				available = available + $2::NUMERIC(20,6),
				total_out = GREATEST(0, total_out - $2::NUMERIC(20,6)),
				updated_at = NOW()
			WHERE agent_address = $1
		`, agentAddr, amount)
		if err != nil {
			return fmt.Errorf("reversal balance update failed: %w", err)
		}
	case "refund":
		// Reverse refund = deduct
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_balances SET
				available = available - $2::NUMERIC(20,6),
				updated_at = NOW()
			WHERE agent_address = $1 AND available >= $2::NUMERIC(20,6)
		`, agentAddr, amount)
		if err != nil {
			return fmt.Errorf("reversal balance update failed: %w", err)
		}
		rows, _ := result.RowsAffected()
		if rows == 0 {
			return ErrInsufficientBalance
		}
	default:
		return fmt.Errorf("cannot reverse entry type %q", entryType)
	}

	// Mark original as reversed
	_, err = tx.ExecContext(ctx, `
		UPDATE ledger_entries SET reversed_at = NOW(), reversed_by = $2 WHERE id = $1
	`, entryID, adminID)
	if err != nil {
		return fmt.Errorf("failed to mark entry as reversed: %w", err)
	}

	// Create reversal entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, reversal_of, created_at)
		VALUES ($1, $2, $3, $4::NUMERIC(20,6), $5, $6, $7, NOW())
	`, idgen.New(), agentAddr, "reversal_"+entryType, amount, reason, "reversal: "+reason, entryID)
	if err != nil {
		return fmt.Errorf("failed to create reversal entry: %w", err)
	}

	return tx.Commit()
}
