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

// Migrate creates the ledger tables
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS agent_balances (
			agent_address   VARCHAR(42) PRIMARY KEY,
			available       VARCHAR(32) DEFAULT '0',
			pending         VARCHAR(32) DEFAULT '0',
			total_in        VARCHAR(32) DEFAULT '0',
			total_out       VARCHAR(32) DEFAULT '0',
			updated_at      TIMESTAMPTZ DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS ledger_entries (
			id              VARCHAR(36) PRIMARY KEY,
			agent_address   VARCHAR(42) NOT NULL,
			type            VARCHAR(20) NOT NULL,
			amount          VARCHAR(32) NOT NULL,
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
		SELECT available, pending, total_in, total_out, updated_at
		FROM agent_balances WHERE agent_address = $1
	`, agentAddr).Scan(&bal.Available, &bal.Pending, &bal.TotalIn, &bal.TotalOut, &bal.UpdatedAt)

	if err == sql.ErrNoRows {
		// Return zero balance for new agents
		return &Balance{
			AgentAddr: agentAddr,
			Available: "0",
			Pending:   "0",
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
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Upsert balance
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_balances (agent_address, available, total_in, updated_at)
		VALUES ($1, $2, $2, NOW())
		ON CONFLICT (agent_address) DO UPDATE SET
			available = (
				SELECT CAST(
					CAST(REPLACE(agent_balances.available, '.', '') AS BIGINT) +
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			total_in = (
				SELECT CAST(
					CAST(REPLACE(agent_balances.total_in, '.', '') AS BIGINT) +
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			updated_at = NOW()
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
		VALUES ($1, $2, 'deposit', $3, $4, $5, NOW())
	`, generateID(), agentAddr, amount, txHash, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Debit removes funds from an agent's balance
func (p *PostgresStore) Debit(ctx context.Context, agentAddr, amount, reference, description string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update balance (will fail if insufficient - checked at ledger layer)
	_, err = tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available = (
				SELECT CAST(
					CAST(REPLACE(available, '.', '') AS BIGINT) -
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			total_out = (
				SELECT CAST(
					CAST(REPLACE(total_out, '.', '') AS BIGINT) +
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'spend', $3, $4, $5, NOW())
	`, generateID(), agentAddr, amount, reference, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Refund credits back funds to an agent's balance (reverses a failed debit)
func (p *PostgresStore) Refund(ctx context.Context, agentAddr, amount, reference, description string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Credit back the balance and adjust total_out
	_, err = tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available = (
				SELECT CAST(
					CAST(REPLACE(available, '.', '') AS BIGINT) +
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			total_out = (
				SELECT CAST(
					CAST(REPLACE(total_out, '.', '') AS BIGINT) -
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record refund entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, reference, description, created_at)
		VALUES ($1, $2, 'refund', $3, $4, $5, NOW())
	`, generateID(), agentAddr, amount, reference, description)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
	}

	return tx.Commit()
}

// Withdraw processes a withdrawal
func (p *PostgresStore) Withdraw(ctx context.Context, agentAddr, amount, txHash string) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Update balance
	_, err = tx.ExecContext(ctx, `
		UPDATE agent_balances SET
			available = (
				SELECT CAST(
					CAST(REPLACE(available, '.', '') AS BIGINT) -
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			total_out = (
				SELECT CAST(
					CAST(REPLACE(total_out, '.', '') AS BIGINT) +
					CAST(REPLACE($2, '.', '') AS BIGINT)
				AS VARCHAR)
			),
			updated_at = NOW()
		WHERE agent_address = $1
	`, agentAddr, amount)
	if err != nil {
		return fmt.Errorf("failed to update balance: %w", err)
	}

	// Record entry
	_, err = tx.ExecContext(ctx, `
		INSERT INTO ledger_entries (id, agent_address, type, amount, tx_hash, description, created_at)
		VALUES ($1, $2, 'withdrawal', $3, $4, 'withdrawal', NOW())
	`, generateID(), agentAddr, amount, txHash)
	if err != nil {
		return fmt.Errorf("failed to record entry: %w", err)
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
