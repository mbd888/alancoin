package escrow

import (
	"context"
	"database/sql"
	"time"
)

// PostgresStore persists escrow data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed escrow store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, e *Escrow) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO escrows (
			id, buyer_addr, seller_addr, amount, service_id, session_key_id,
			status, auto_release_at, delivered_at, resolved_at,
			dispute_reason, resolution, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4::NUMERIC(20,6), $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14
		)`,
		e.ID, e.BuyerAddr, e.SellerAddr, e.Amount,
		nullString(e.ServiceID), nullString(e.SessionKeyID),
		string(e.Status), e.AutoReleaseAt, nullTime(e.DeliveredAt), nullTime(e.ResolvedAt),
		nullString(e.DisputeReason), nullString(e.Resolution),
		e.CreatedAt, e.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Escrow, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, buyer_addr, seller_addr, amount, service_id, session_key_id,
		       status, auto_release_at, delivered_at, resolved_at,
		       dispute_reason, resolution, created_at, updated_at
		FROM escrows WHERE id = $1`, id)

	e, err := scanEscrow(row)
	if err == sql.ErrNoRows {
		return nil, ErrEscrowNotFound
	}
	return e, err
}

func (p *PostgresStore) Update(ctx context.Context, e *Escrow) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE escrows SET
			status = $1, delivered_at = $2, resolved_at = $3,
			dispute_reason = $4, resolution = $5, updated_at = $6
		WHERE id = $7`,
		string(e.Status), nullTime(e.DeliveredAt), nullTime(e.ResolvedAt),
		nullString(e.DisputeReason), nullString(e.Resolution), e.UpdatedAt,
		e.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrEscrowNotFound
	}
	return nil
}

func (p *PostgresStore) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Escrow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, seller_addr, amount, service_id, session_key_id,
		       status, auto_release_at, delivered_at, resolved_at,
		       dispute_reason, resolution, created_at, updated_at
		FROM escrows
		WHERE buyer_addr = $1 OR seller_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanEscrows(rows)
}

func (p *PostgresStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*Escrow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, seller_addr, amount, service_id, session_key_id,
		       status, auto_release_at, delivered_at, resolved_at,
		       dispute_reason, resolution, created_at, updated_at
		FROM escrows
		WHERE status IN ('pending', 'delivered')
		  AND auto_release_at < $1
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanEscrows(rows)
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanEscrow(s scanner) (*Escrow, error) {
	e := &Escrow{}
	var (
		serviceID    sql.NullString
		sessionKeyID sql.NullString
		deliveredAt  sql.NullTime
		resolvedAt   sql.NullTime
		disputeRsn   sql.NullString
		resolution   sql.NullString
		status       string
	)

	err := s.Scan(
		&e.ID, &e.BuyerAddr, &e.SellerAddr, &e.Amount,
		&serviceID, &sessionKeyID,
		&status, &e.AutoReleaseAt, &deliveredAt, &resolvedAt,
		&disputeRsn, &resolution, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	e.Status = Status(status)
	e.ServiceID = serviceID.String
	e.SessionKeyID = sessionKeyID.String
	e.DisputeReason = disputeRsn.String
	e.Resolution = resolution.String
	if deliveredAt.Valid {
		e.DeliveredAt = &deliveredAt.Time
	}
	if resolvedAt.Valid {
		e.ResolvedAt = &resolvedAt.Time
	}

	return e, nil
}

func scanEscrows(rows *sql.Rows) ([]*Escrow, error) {
	var result []*Escrow
	for rows.Next() {
		e, err := scanEscrow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// nullString converts an empty Go string to sql.NullString.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullTime converts a *time.Time to sql.NullTime.
func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
