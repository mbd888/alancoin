package contracts

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PostgresStore persists contract data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed contract store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

const contractColumns = `id, name, description, preconditions, invariants, recovery,
	status, bound_escrow_id, violations, soft_violations, hard_violations,
	quality_penalty, created_at, updated_at`

func (p *PostgresStore) Create(ctx context.Context, c *Contract) error {
	precondJSON, _ := json.Marshal(c.Preconditions)
	if c.Preconditions == nil {
		precondJSON = []byte("[]")
	}
	invJSON, _ := json.Marshal(c.Invariants)
	if c.Invariants == nil {
		invJSON = []byte("[]")
	}
	violJSON, _ := json.Marshal(c.Violations)
	if c.Violations == nil {
		violJSON = []byte("[]")
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO contracts (
			id, name, description, preconditions, invariants, recovery,
			status, bound_escrow_id, violations, soft_violations, hard_violations,
			quality_penalty, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13, $14
		)`,
		c.ID, c.Name, c.Description, precondJSON, invJSON, string(c.Recovery),
		string(c.Status), nullString(c.BoundEscrowID), violJSON, c.SoftViolations,
		c.HardViolations, c.QualityPenalty, c.CreatedAt, c.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Contract, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+contractColumns+` FROM contracts WHERE id = $1`, id)
	c, err := scanContract(row)
	if err == sql.ErrNoRows {
		return nil, ErrContractNotFound
	}
	return c, err
}

func (p *PostgresStore) Update(ctx context.Context, c *Contract) error {
	precondJSON, _ := json.Marshal(c.Preconditions)
	invJSON, _ := json.Marshal(c.Invariants)
	violJSON, _ := json.Marshal(c.Violations)
	if c.Violations == nil {
		violJSON = []byte("[]")
	}

	result, err := p.db.ExecContext(ctx, `
		UPDATE contracts SET
			name = $1, description = $2, preconditions = $3, invariants = $4,
			recovery = $5, status = $6, bound_escrow_id = $7, violations = $8,
			soft_violations = $9, hard_violations = $10, quality_penalty = $11,
			updated_at = $12
		WHERE id = $13`,
		c.Name, c.Description, precondJSON, invJSON,
		string(c.Recovery), string(c.Status), nullString(c.BoundEscrowID), violJSON,
		c.SoftViolations, c.HardViolations, c.QualityPenalty,
		c.UpdatedAt, c.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrContractNotFound
	}
	return nil
}

func (p *PostgresStore) GetByEscrow(ctx context.Context, escrowID string) (*Contract, error) {
	row := p.db.QueryRowContext(ctx,
		`SELECT `+contractColumns+` FROM contracts WHERE bound_escrow_id = $1`, escrowID)
	c, err := scanContract(row)
	if err == sql.ErrNoRows {
		return nil, ErrContractNotFound
	}
	return c, err
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanContract(s scanner) (*Contract, error) {
	c := &Contract{}
	var (
		status        string
		recovery      string
		boundEscrowID sql.NullString
		precondJSON   []byte
		invJSON       []byte
		violJSON      []byte
	)

	err := s.Scan(
		&c.ID, &c.Name, &c.Description, &precondJSON, &invJSON, &recovery,
		&status, &boundEscrowID, &violJSON, &c.SoftViolations, &c.HardViolations,
		&c.QualityPenalty, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	c.Status = ContractStatus(status)
	c.Recovery = RecoveryAction(recovery)
	c.BoundEscrowID = boundEscrowID.String
	if len(precondJSON) > 0 {
		_ = json.Unmarshal(precondJSON, &c.Preconditions)
	}
	if len(invJSON) > 0 {
		_ = json.Unmarshal(invJSON, &c.Invariants)
	}
	if len(violJSON) > 0 {
		_ = json.Unmarshal(violJSON, &c.Violations)
	}

	return c, nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
