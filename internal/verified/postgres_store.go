package verified

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

// NewPostgresStore creates a new PostgreSQL-backed verified agent store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the verified_agents table if it doesn't exist.
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS verified_agents (
			id                      TEXT PRIMARY KEY,
			agent_addr              TEXT NOT NULL UNIQUE,
			status                  TEXT NOT NULL DEFAULT 'active'
				CHECK (status IN ('active', 'suspended', 'revoked', 'forfeited')),
			bond_amount             NUMERIC(20,6) NOT NULL CHECK (bond_amount >= 0),
			bond_reference          TEXT NOT NULL,
			guaranteed_success_rate NUMERIC(5,2) NOT NULL CHECK (guaranteed_success_rate BETWEEN 0 AND 100),
			sla_window_size         INTEGER NOT NULL DEFAULT 20,
			guarantee_premium_rate  NUMERIC(5,4) NOT NULL DEFAULT 0.05,
			reputation_score        NUMERIC(6,1) NOT NULL DEFAULT 0,
			reputation_tier         TEXT NOT NULL DEFAULT 'new',
			total_calls_monitored   INTEGER NOT NULL DEFAULT 0,
			violation_count         INTEGER NOT NULL DEFAULT 0,
			last_violation_at       TIMESTAMPTZ,
			last_review_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			verified_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			revoked_at              TIMESTAMPTZ,
			created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_verified_agents_status ON verified_agents(status);
		CREATE INDEX IF NOT EXISTS idx_verified_agents_agent_addr ON verified_agents(agent_addr);
	`)
	return err
}

// Create inserts a new verification, rejecting duplicates for non-terminal agents.
func (p *PostgresStore) Create(ctx context.Context, v *Verification) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Check for existing non-terminal verification
	var existingStatus string
	err = tx.QueryRowContext(ctx, `
		SELECT status FROM verified_agents
		WHERE agent_addr = $1 AND status IN ('active', 'suspended')
		LIMIT 1
	`, v.AgentAddr).Scan(&existingStatus)
	if err == nil {
		return ErrAlreadyVerified
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("check existing: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO verified_agents (
			id, agent_addr, status,
			bond_amount, bond_reference,
			guaranteed_success_rate, sla_window_size, guarantee_premium_rate,
			reputation_score, reputation_tier,
			total_calls_monitored, violation_count,
			last_violation_at, last_review_at, verified_at, revoked_at,
			created_at, updated_at
		) VALUES (
			$1, $2, $3,
			$4::NUMERIC(20,6), $5,
			$6, $7, $8,
			$9, $10,
			$11, $12,
			$13, $14, $15, $16,
			$17, $18
		)
	`,
		v.ID, v.AgentAddr, string(v.Status),
		v.BondAmount, v.BondReference,
		v.GuaranteedSuccessRate, v.SLAWindowSize, v.GuaranteePremiumRate,
		v.ReputationScore, v.ReputationTier,
		v.TotalCallsMonitored, v.ViolationCount,
		nullTimeOrValue(v.LastViolationAt), v.LastReviewAt, v.VerifiedAt, nullTimePtrOrValue(v.RevokedAt),
		v.CreatedAt, v.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert verification: %w", err)
	}

	return tx.Commit()
}

// Get retrieves a verification by ID.
func (p *PostgresStore) Get(ctx context.Context, id string) (*Verification, error) {
	row := p.db.QueryRowContext(ctx, selectColumns+" WHERE id = $1", id)
	v, err := scanVerification(row)
	if err == sql.ErrNoRows {
		return nil, ErrVerificationFound
	}
	if err != nil {
		return nil, fmt.Errorf("get verification: %w", err)
	}
	return v, nil
}

// GetByAgent retrieves the most recent verification for an agent address.
func (p *PostgresStore) GetByAgent(ctx context.Context, agentAddr string) (*Verification, error) {
	row := p.db.QueryRowContext(ctx, selectColumns+" WHERE agent_addr = $1 ORDER BY created_at DESC LIMIT 1", agentAddr)
	v, err := scanVerification(row)
	if err == sql.ErrNoRows {
		return nil, ErrVerificationFound
	}
	if err != nil {
		return nil, fmt.Errorf("get verification by agent: %w", err)
	}
	return v, nil
}

// Update modifies a verification's mutable fields.
func (p *PostgresStore) Update(ctx context.Context, v *Verification) error {
	v.UpdatedAt = time.Now()

	result, err := p.db.ExecContext(ctx, `
		UPDATE verified_agents SET
			status                  = $2,
			bond_amount             = $3::NUMERIC(20,6),
			bond_reference          = $4,
			guaranteed_success_rate = $5,
			sla_window_size         = $6,
			guarantee_premium_rate  = $7,
			reputation_score        = $8,
			reputation_tier         = $9,
			total_calls_monitored   = $10,
			violation_count         = $11,
			last_violation_at       = $12,
			last_review_at          = $13,
			revoked_at              = $14,
			updated_at              = $15
		WHERE id = $1
	`,
		v.ID, string(v.Status),
		v.BondAmount, v.BondReference,
		v.GuaranteedSuccessRate, v.SLAWindowSize, v.GuaranteePremiumRate,
		v.ReputationScore, v.ReputationTier,
		v.TotalCallsMonitored, v.ViolationCount,
		nullTimeOrValue(v.LastViolationAt), v.LastReviewAt, nullTimePtrOrValue(v.RevokedAt),
		v.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update verification: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return ErrVerificationFound
	}
	return nil
}

// ListActive returns active verifications, ordered by most recently verified.
func (p *PostgresStore) ListActive(ctx context.Context, limit int) ([]*Verification, error) {
	rows, err := p.db.QueryContext(ctx, selectColumns+" WHERE status = 'active' ORDER BY verified_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("list active: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanVerifications(rows)
}

// ListAll returns all verifications, ordered by most recently created.
func (p *PostgresStore) ListAll(ctx context.Context, limit int) ([]*Verification, error) {
	rows, err := p.db.QueryContext(ctx, selectColumns+" ORDER BY created_at DESC LIMIT $1", limit)
	if err != nil {
		return nil, fmt.Errorf("list all: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanVerifications(rows)
}

// IsVerified checks whether the agent has an active verification.
func (p *PostgresStore) IsVerified(ctx context.Context, agentAddr string) (bool, error) {
	var count int
	err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM verified_agents
		WHERE agent_addr = $1 AND status = 'active'
	`, agentAddr).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("is verified: %w", err)
	}
	return count > 0, nil
}

// --- scanning helpers ---

const selectColumns = `
	SELECT id, agent_addr, status,
		bond_amount, bond_reference,
		guaranteed_success_rate, sla_window_size, guarantee_premium_rate,
		reputation_score, reputation_tier,
		total_calls_monitored, violation_count,
		last_violation_at, last_review_at, verified_at, revoked_at,
		created_at, updated_at
	FROM verified_agents`

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanVerification(row scannable) (*Verification, error) {
	var v Verification
	var status string
	var lastViolationAt, revokedAt sql.NullTime
	var lastReviewAt, verifiedAt, createdAt, updatedAt time.Time

	err := row.Scan(
		&v.ID, &v.AgentAddr, &status,
		&v.BondAmount, &v.BondReference,
		&v.GuaranteedSuccessRate, &v.SLAWindowSize, &v.GuaranteePremiumRate,
		&v.ReputationScore, &v.ReputationTier,
		&v.TotalCallsMonitored, &v.ViolationCount,
		&lastViolationAt, &lastReviewAt, &verifiedAt, &revokedAt,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}

	v.Status = Status(status)
	if lastViolationAt.Valid {
		v.LastViolationAt = lastViolationAt.Time
	}
	v.LastReviewAt = lastReviewAt
	v.VerifiedAt = verifiedAt
	if revokedAt.Valid {
		t := revokedAt.Time
		v.RevokedAt = &t
	}
	v.CreatedAt = createdAt
	v.UpdatedAt = updatedAt

	return &v, nil
}

func scanVerifications(rows *sql.Rows) ([]*Verification, error) {
	var result []*Verification
	for rows.Next() {
		v, err := scanVerification(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func nullTimeOrValue(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func nullTimePtrOrValue(t *time.Time) sql.NullTime {
	if t == nil || t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}
