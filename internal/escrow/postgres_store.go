package escrow

import (
	"context"
	"database/sql"
	"encoding/json"
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
	evidenceJSON, _ := json.Marshal(e.DisputeEvidence)
	if e.DisputeEvidence == nil {
		evidenceJSON = []byte("[]")
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO escrows (
			id, buyer_addr, seller_addr, amount, service_id, session_key_id,
			status, auto_release_at, delivered_at, resolved_at,
			dispute_reason, resolution, created_at, updated_at,
			dispute_evidence, arbitrator_addr, arbitration_deadline,
			partial_release_amount, partial_refund_amount, dispute_window_until
		) VALUES (
			$1, $2, $3, $4::NUMERIC(20,6), $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14,
			$15, $16, $17,
			$18, $19, $20
		)`,
		e.ID, e.BuyerAddr, e.SellerAddr, e.Amount,
		nullString(e.ServiceID), nullString(e.SessionKeyID),
		string(e.Status), e.AutoReleaseAt, nullTime(e.DeliveredAt), nullTime(e.ResolvedAt),
		nullString(e.DisputeReason), nullString(e.Resolution),
		e.CreatedAt, e.UpdatedAt,
		evidenceJSON, nullString(e.ArbitratorAddr), nullTime(e.ArbitrationDeadline),
		nullString(e.PartialReleaseAmount), nullString(e.PartialRefundAmount), nullTime(e.DisputeWindowUntil),
	)
	return err
}

const escrowColumns = `id, buyer_addr, seller_addr, amount, service_id, session_key_id,
		       status, auto_release_at, delivered_at, resolved_at,
		       dispute_reason, resolution, created_at, updated_at,
		       dispute_evidence, arbitrator_addr, arbitration_deadline,
		       partial_release_amount, partial_refund_amount, dispute_window_until`

func (p *PostgresStore) Get(ctx context.Context, id string) (*Escrow, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+escrowColumns+` FROM escrows WHERE id = $1`, id)

	e, err := scanEscrow(row)
	if err == sql.ErrNoRows {
		return nil, ErrEscrowNotFound
	}
	return e, err
}

func (p *PostgresStore) Update(ctx context.Context, e *Escrow) error {
	evidenceJSON, _ := json.Marshal(e.DisputeEvidence)
	if e.DisputeEvidence == nil {
		evidenceJSON = []byte("[]")
	}
	result, err := p.db.ExecContext(ctx, `
		UPDATE escrows SET
			status = $1, delivered_at = $2, resolved_at = $3,
			dispute_reason = $4, resolution = $5, updated_at = $6,
			dispute_evidence = $7, arbitrator_addr = $8, arbitration_deadline = $9,
			partial_release_amount = $10, partial_refund_amount = $11, dispute_window_until = $12
		WHERE id = $13`,
		string(e.Status), nullTime(e.DeliveredAt), nullTime(e.ResolvedAt),
		nullString(e.DisputeReason), nullString(e.Resolution), e.UpdatedAt,
		evidenceJSON, nullString(e.ArbitratorAddr), nullTime(e.ArbitrationDeadline),
		nullString(e.PartialReleaseAmount), nullString(e.PartialRefundAmount), nullTime(e.DisputeWindowUntil),
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
		SELECT `+escrowColumns+`
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
		SELECT `+escrowColumns+`
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

func (p *PostgresStore) ListByStatus(ctx context.Context, status Status, limit int) ([]*Escrow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+escrowColumns+`
		FROM escrows
		WHERE status = $1
		ORDER BY created_at DESC
		LIMIT $2`, string(status), limit)
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
		serviceID            sql.NullString
		sessionKeyID         sql.NullString
		deliveredAt          sql.NullTime
		resolvedAt           sql.NullTime
		disputeRsn           sql.NullString
		resolution           sql.NullString
		status               string
		evidenceJSON         []byte
		arbitratorAddr       sql.NullString
		arbitrationDeadline  sql.NullTime
		partialReleaseAmount sql.NullString
		partialRefundAmount  sql.NullString
		disputeWindowUntil   sql.NullTime
	)

	err := s.Scan(
		&e.ID, &e.BuyerAddr, &e.SellerAddr, &e.Amount,
		&serviceID, &sessionKeyID,
		&status, &e.AutoReleaseAt, &deliveredAt, &resolvedAt,
		&disputeRsn, &resolution, &e.CreatedAt, &e.UpdatedAt,
		&evidenceJSON, &arbitratorAddr, &arbitrationDeadline,
		&partialReleaseAmount, &partialRefundAmount, &disputeWindowUntil,
	)
	if err != nil {
		return nil, err
	}

	e.Status = Status(status)
	e.ServiceID = serviceID.String
	e.SessionKeyID = sessionKeyID.String
	e.DisputeReason = disputeRsn.String
	e.Resolution = resolution.String
	e.ArbitratorAddr = arbitratorAddr.String
	e.PartialReleaseAmount = partialReleaseAmount.String
	e.PartialRefundAmount = partialRefundAmount.String
	if deliveredAt.Valid {
		e.DeliveredAt = &deliveredAt.Time
	}
	if resolvedAt.Valid {
		e.ResolvedAt = &resolvedAt.Time
	}
	if arbitrationDeadline.Valid {
		e.ArbitrationDeadline = &arbitrationDeadline.Time
	}
	if disputeWindowUntil.Valid {
		e.DisputeWindowUntil = &disputeWindowUntil.Time
	}
	if len(evidenceJSON) > 0 {
		_ = json.Unmarshal(evidenceJSON, &e.DisputeEvidence)
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
