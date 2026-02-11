package receipts

import (
	"context"
	"database/sql"
)

// PostgresStore persists receipt data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed receipt store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the receipts table and indexes.
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS receipts (
			id           VARCHAR(36) PRIMARY KEY,
			payment_path VARCHAR(20) NOT NULL CHECK (payment_path IN ('gateway','stream','session_key','escrow')),
			reference    VARCHAR(255) NOT NULL,
			from_addr    VARCHAR(42) NOT NULL,
			to_addr      VARCHAR(42) NOT NULL,
			amount       NUMERIC(20,6) NOT NULL CHECK (amount > 0),
			service_id   VARCHAR(255),
			status       VARCHAR(20) NOT NULL CHECK (status IN ('confirmed','failed')),
			payload_hash VARCHAR(64) NOT NULL,
			signature    VARCHAR(128) NOT NULL,
			issued_at    TIMESTAMPTZ NOT NULL,
			expires_at   TIMESTAMPTZ NOT NULL,
			metadata     TEXT,
			created_at   TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_receipts_from_addr ON receipts (from_addr, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_receipts_to_addr ON receipts (to_addr, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_receipts_reference ON receipts (reference);
		CREATE INDEX IF NOT EXISTS idx_receipts_payment_path ON receipts (payment_path, created_at DESC);
	`)
	return err
}

func (p *PostgresStore) Create(ctx context.Context, r *Receipt) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO receipts (
			id, payment_path, reference, from_addr, to_addr,
			amount, service_id, status, payload_hash, signature,
			issued_at, expires_at, metadata, created_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6::NUMERIC(20,6), $7, $8, $9, $10,
			$11, $12, $13, $14
		)`,
		r.ID, string(r.PaymentPath), r.Reference, r.From, r.To,
		r.Amount, nullString(r.ServiceID), r.Status, r.PayloadHash, r.Signature,
		r.IssuedAt, r.ExpiresAt, nullString(r.Metadata), r.CreatedAt,
	)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Receipt, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, payment_path, reference, from_addr, to_addr,
		       amount, service_id, status, payload_hash, signature,
		       issued_at, expires_at, metadata, created_at
		FROM receipts WHERE id = $1`, id)

	r, err := scanReceipt(row)
	if err == sql.ErrNoRows {
		return nil, ErrReceiptNotFound
	}
	return r, err
}

func (p *PostgresStore) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Receipt, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, payment_path, reference, from_addr, to_addr,
		       amount, service_id, status, payload_hash, signature,
		       issued_at, expires_at, metadata, created_at
		FROM receipts
		WHERE from_addr = $1 OR to_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanReceipts(rows)
}

func (p *PostgresStore) ListByReference(ctx context.Context, reference string) ([]*Receipt, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, payment_path, reference, from_addr, to_addr,
		       amount, service_id, status, payload_hash, signature,
		       issued_at, expires_at, metadata, created_at
		FROM receipts
		WHERE reference = $1
		ORDER BY created_at DESC`, reference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanReceipts(rows)
}

// --- scanners ---

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanReceipt(sc scanner) (*Receipt, error) {
	r := &Receipt{}
	var (
		serviceID   sql.NullString
		metadata    sql.NullString
		paymentPath string
	)

	err := sc.Scan(
		&r.ID, &paymentPath, &r.Reference, &r.From, &r.To,
		&r.Amount, &serviceID, &r.Status, &r.PayloadHash, &r.Signature,
		&r.IssuedAt, &r.ExpiresAt, &metadata, &r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	r.PaymentPath = PaymentPath(paymentPath)
	r.ServiceID = serviceID.String
	r.Metadata = metadata.String
	return r, nil
}

func scanReceipts(rows *sql.Rows) ([]*Receipt, error) {
	var result []*Receipt
	for rows.Next() {
		r, err := scanReceipt(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

var _ Store = (*PostgresStore)(nil)
