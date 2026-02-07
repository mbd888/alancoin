package ledger

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"
)

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

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
			total_in        NUMERIC(20,6) NOT NULL DEFAULT 0,
			total_out       NUMERIC(20,6) NOT NULL DEFAULT 0,
			updated_at      TIMESTAMPTZ DEFAULT NOW(),
			CONSTRAINT chk_available_nonneg CHECK (available >= 0),
			CONSTRAINT chk_pending_nonneg   CHECK (pending >= 0),
			CONSTRAINT chk_total_in_nonneg  CHECK (total_in >= 0)
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
	`)
	return err
}

// GetBalance retrieves an agent's balance
func (p *PostgresStore) GetBalance(ctx context.Context, agentAddr string) (*Balance, error) {
	bal := &Balance{AgentAddr: agentAddr}

	err := p.db.QueryRowContext(ctx, `
		SELECT available, pending, COALESCE(escrowed, 0), total_in, total_out, updated_at
		FROM agent_balances WHERE agent_address = $1
	`, agentAddr).Scan(&bal.Available, &bal.Pending, &bal.Escrowed, &bal.TotalIn, &bal.TotalOut, &bal.UpdatedAt)

	if err == sql.ErrNoRows {
		return &Balance{
			AgentAddr: agentAddr,
			Available: "0",
			Pending:   "0",
			Escrowed:  "0",
			TotalIn:   "0",
			TotalOut:  "0",
			UpdatedAt: time.Now(),
		}, nil
	}
	if err != nil {
		return nil, err
	}
	return bal, nil
}

// Credit adds funds to an agent's balance
func (p *PostgresStore) Credit(ctx context.Context, agentAddr, amount, txHash, description string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert balance using native NUMERIC arithmetic
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2::NUMERIC(20,6), $2::NUMERIC(20,6), NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available  = agent_balances.available + $2::NUMERIC(20,6),
			total_in   = agent_balances.total_in  + $2::NUMERIC(20,6),
			updated_at = NOW()
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
		VALUES ($1, $2, 'deposit', $3::NUMERIC(20,6), $4, $5, NOW())
	`, generateID(), agentAddr, amount, txHash, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Debit removes funds from an agent's balance with row-level locking.
// The CHECK constraint on available >= 0 prevents overdraft at the DB level.
func (p *PostgresStore) Debit(ctx context.Context, agentAddr, amount, reference, description string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Lock the row and verify sufficient balance in one atomic step.
	// The CHECK constraint (available >= 0) will cause this to fail
	// if the debit would overdraw the account.
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		// CHECK constraint violation means insufficient balance
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'spend', $3::NUMERIC(20,6), $4, $5, NOW())
	`, generateID(), agentAddr, amount, reference, description)
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
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available + $2::NUMERIC(20,6),
			total_out  = total_out - $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record refund entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'refund', $3::NUMERIC(20,6), $4, $5, NOW())
	`, generateID(), agentAddr, amount, reference, description)
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
	defer tx.Rollback()

	// Atomic debit — CHECK constraint prevents overdraft
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
		VALUES ($1, $2, 'withdrawal', $3::NUMERIC(20,6), $4, 'withdrawal', NOW())
	`, generateID(), agentAddr, amount, txHash)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Hold places a hold on funds (moves from available to pending).
// Used for two-phase transactions: hold first, then confirm or release.
func (p *PostgresStore) Hold(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Atomic: decrease available, increase pending
	// CHECK constraint on available >= 0 prevents overdraft
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available - $2::NUMERIC(20,6),
			pending    = pending   + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to place hold: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record hold entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'hold', $3::NUMERIC(20,6), $4, 'pending_transfer', NOW())
	`, generateID(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record hold entry: %w", err)
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
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			pending    = pending   - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to confirm hold: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record confirmation entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'spend', $3::NUMERIC(20,6), $4, 'transfer_confirmed', NOW())
	`, generateID(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record confirmation entry: %w", err)
	}

	return tx.Commit()
}

// ReleaseHold returns held funds to available (transfer failed/timed out).
func (p *PostgresStore) ReleaseHold(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available + $2::NUMERIC(20,6),
			pending    = pending   - $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to release hold: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	// Record release entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'release', $3::NUMERIC(20,6), $4, 'hold_released', NOW())
	`, generateID(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record release entry: %w", err)
	}

	return tx.Commit()
}

// EscrowLock moves funds from available to escrowed.
func (p *PostgresStore) EscrowLock(ctx context.Context, agentAddr, amount, reference string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available  = available - $2::NUMERIC(20,6),
			escrowed   = escrowed  + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to lock escrow: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_lock', $3::NUMERIC(20,6), $4, 'escrow_locked', NOW())
	`, generateID(), agentAddr, amount, reference)
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
	defer tx.Rollback()

	// Debit buyer's escrowed
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			escrowed   = escrowed  - $2::NUMERIC(20,6),
			total_out  = total_out + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, buyerAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to debit buyer escrow: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
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
	`, generateID(), buyerAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record buyer escrow release entry: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_receive', $3::NUMERIC(20,6), $4, 'escrow_payment_received', NOW())
	`, generateID(), sellerAddr, amount, reference)
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
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			escrowed   = escrowed  - $2::NUMERIC(20,6),
			available  = available + $2::NUMERIC(20,6),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to refund escrow: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAgentNotFound
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'escrow_refund', $3::NUMERIC(20,6), $4, 'escrow_refunded', NOW())
	`, generateID(), agentAddr, amount, reference)
	if err != nil {
		return fmt.Errorf("failed to record escrow refund entry: %w", err)
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
	defer rows.Close()

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
