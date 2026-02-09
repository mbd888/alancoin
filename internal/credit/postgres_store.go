package credit

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Compile-time check that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed credit store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the credit_lines table if it doesn't exist.
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS credit_lines (
			id                VARCHAR(36) PRIMARY KEY,
			agent_address     VARCHAR(42) NOT NULL,
			credit_limit      NUMERIC(20,6) NOT NULL,
			credit_used       NUMERIC(20,6) NOT NULL DEFAULT 0,
			interest_rate     NUMERIC(10,6) NOT NULL DEFAULT 0,
			status            VARCHAR(20) NOT NULL DEFAULT 'active',
			reputation_tier   VARCHAR(20) NOT NULL,
			reputation_score  NUMERIC(10,2) NOT NULL DEFAULT 0,
			approved_at       TIMESTAMPTZ,
			last_review_at    TIMESTAMPTZ,
			defaulted_at      TIMESTAMPTZ,
			revoked_at        TIMESTAMPTZ,
			created_at        TIMESTAMPTZ DEFAULT NOW(),
			updated_at        TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_credit_lines_agent ON credit_lines(agent_address);
		CREATE INDEX IF NOT EXISTS idx_credit_lines_status ON credit_lines(status);
	`)
	return err
}

// Create inserts a new credit line, rejecting duplicates for active/suspended agents.
func (p *PostgresStore) Create(ctx context.Context, line *CreditLine) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	// Check for existing active or suspended credit line for this agent.
	var existingStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM credit_lines
		WHERE agent_address = $1 AND status IN ('active', 'suspended')
		LIMIT 1
	`, line.AgentAddr).Scan(&existingStatus)
	if err == nil {
		return ErrCreditLineExists
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("check existing: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO credit_lines (
			id, agent_address, credit_limit, credit_used, interest_rate,
			status, reputation_tier, reputation_score,
			approved_at, last_review_at, defaulted_at, revoked_at,
			created_at, updated_at
		) VALUES ($1, $2, $3::NUMERIC(20,6), $4::NUMERIC(20,6), $5,
			$6, $7, $8,
			$9, $10, $11, $12,
			$13, $14)
	`,
		line.ID, line.AgentAddr, line.CreditLimit, line.CreditUsed, line.InterestRate,
		string(line.Status), line.ReputationTier, line.ReputationScore,
		nullTimeOrValue(line.ApprovedAt), nullTimeOrValue(line.LastReviewAt),
		nullTimeOrValue(line.DefaultedAt), nullTimeOrValue(line.RevokedAt),
		line.CreatedAt, line.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert credit line: %w", err)
	}

	return tx.Commit()
}

// Get retrieves a credit line by ID.
func (p *PostgresStore) Get(ctx context.Context, id string) (*CreditLine, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, agent_address, credit_limit, credit_used, interest_rate,
			status, reputation_tier, reputation_score,
			approved_at, last_review_at, defaulted_at, revoked_at,
			created_at, updated_at
		FROM credit_lines WHERE id = $1
	`, id)

	line, err := scanCreditLine(row)
	if err == sql.ErrNoRows {
		return nil, ErrCreditLineNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get credit line: %w", err)
	}
	return line, nil
}

// GetByAgent retrieves the most recent credit line for an agent address.
func (p *PostgresStore) GetByAgent(ctx context.Context, agentAddr string) (*CreditLine, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, agent_address, credit_limit, credit_used, interest_rate,
			status, reputation_tier, reputation_score,
			approved_at, last_review_at, defaulted_at, revoked_at,
			created_at, updated_at
		FROM credit_lines WHERE agent_address = $1
		ORDER BY created_at DESC LIMIT 1
	`, agentAddr)

	line, err := scanCreditLine(row)
	if err == sql.ErrNoRows {
		return nil, ErrCreditLineNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get credit line by agent: %w", err)
	}
	return line, nil
}

// Update modifies a credit line's mutable fields.
func (p *PostgresStore) Update(ctx context.Context, line *CreditLine) error {
	line.UpdatedAt = time.Now()

	// Default empty numeric strings so the NUMERIC cast doesn't fail
	// before Postgres can evaluate the WHERE clause.
	if line.CreditLimit == "" {
		line.CreditLimit = "0.000000"
	}
	if line.CreditUsed == "" {
		line.CreditUsed = "0.000000"
	}

	result, err := p.db.ExecContext(ctx, `
		UPDATE credit_lines SET
			credit_limit     = $2::NUMERIC(20,6),
			credit_used      = $3::NUMERIC(20,6),
			interest_rate    = $4,
			status           = $5,
			reputation_tier  = $6,
			reputation_score = $7,
			approved_at      = $8,
			last_review_at   = $9,
			defaulted_at     = $10,
			revoked_at       = $11,
			updated_at       = $12
		WHERE id = $1
	`,
		line.ID, line.CreditLimit, line.CreditUsed, line.InterestRate,
		string(line.Status), line.ReputationTier, line.ReputationScore,
		nullTimeOrValue(line.ApprovedAt), nullTimeOrValue(line.LastReviewAt),
		nullTimeOrValue(line.DefaultedAt), nullTimeOrValue(line.RevokedAt),
		line.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update credit line: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrCreditLineNotFound
	}
	return nil
}

// ListActive returns active credit lines, ordered by most recently created.
func (p *PostgresStore) ListActive(ctx context.Context, limit int) ([]*CreditLine, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_address, credit_limit, credit_used, interest_rate,
			status, reputation_tier, reputation_score,
			approved_at, last_review_at, defaulted_at, revoked_at,
			created_at, updated_at
		FROM credit_lines WHERE status = 'active'
		ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list active: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanCreditLines(rows)
}

// ListOverdue returns active credit lines with outstanding credit that are older
// than the given number of days.
func (p *PostgresStore) ListOverdue(ctx context.Context, overdueDays int, limit int) ([]*CreditLine, error) {
	cutoff := time.Now().AddDate(0, 0, -overdueDays)
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_address, credit_limit, credit_used, interest_rate,
			status, reputation_tier, reputation_score,
			approved_at, last_review_at, defaulted_at, revoked_at,
			created_at, updated_at
		FROM credit_lines
		WHERE status = 'active' AND credit_used > 0 AND approved_at < $1
		ORDER BY created_at DESC LIMIT $2
	`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list overdue: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanCreditLines(rows)
}

// scannable abstracts *sql.Row and *sql.Rows for shared scanning logic.
type scannable interface {
	Scan(dest ...interface{}) error
}

// scanCreditLine scans a single row into a CreditLine.
func scanCreditLine(row scannable) (*CreditLine, error) {
	var line CreditLine
	var status string
	var approvedAt, lastReviewAt, defaultedAt, revokedAt sql.NullTime
	var createdAt, updatedAt sql.NullTime

	err := row.Scan(
		&line.ID, &line.AgentAddr, &line.CreditLimit, &line.CreditUsed, &line.InterestRate,
		&status, &line.ReputationTier, &line.ReputationScore,
		&approvedAt, &lastReviewAt, &defaultedAt, &revokedAt,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	line.Status = Status(status)
	if approvedAt.Valid {
		line.ApprovedAt = approvedAt.Time
	}
	if lastReviewAt.Valid {
		line.LastReviewAt = lastReviewAt.Time
	}
	if defaultedAt.Valid {
		line.DefaultedAt = defaultedAt.Time
	}
	if revokedAt.Valid {
		line.RevokedAt = revokedAt.Time
	}
	if createdAt.Valid {
		line.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		line.UpdatedAt = updatedAt.Time
	}

	return &line, nil
}

// scanCreditLines scans multiple rows into a slice of CreditLine.
func scanCreditLines(rows *sql.Rows) ([]*CreditLine, error) {
	var result []*CreditLine
	for rows.Next() {
		line, err := scanCreditLine(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, line)
	}
	return result, rows.Err()
}

// nullTimeOrValue returns a sql.NullTime: valid if t is non-zero, null otherwise.
func nullTimeOrValue(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
