package usdc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// PostgresPayoutStore persists Payout records in PostgreSQL.
// The client_ref column has a unique constraint so re-sends with the same
// reference are caught at the DB layer even if the service-side idempotency
// cache is cold.
type PostgresPayoutStore struct {
	db *sql.DB
}

// NewPostgresPayoutStore wraps an existing *sql.DB. Caller runs Migrate
// once (or relies on the application's migration tool).
func NewPostgresPayoutStore(db *sql.DB) *PostgresPayoutStore {
	return &PostgresPayoutStore{db: db}
}

// Migrate creates the payouts table and indexes. Idempotent.
func (s *PostgresPayoutStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS payouts (
			client_ref     VARCHAR(128) PRIMARY KEY,
			chain_id       BIGINT       NOT NULL,
			from_addr      VARCHAR(42)  NOT NULL,
			to_addr        VARCHAR(42)  NOT NULL,
			amount         NUMERIC(78,0) NOT NULL CHECK (amount > 0),
			nonce          BIGINT       NOT NULL,
			tx_hash        VARCHAR(66)  NOT NULL,
			status         VARCHAR(20)  NOT NULL CHECK (status IN ('pending','success','failed','dropped')),
			submitted_at   TIMESTAMPTZ  NOT NULL,
			finalized_at   TIMESTAMPTZ,
			receipt_block  BIGINT,
			receipt_status VARCHAR(20),
			receipt_confs  BIGINT,
			last_error     TEXT,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_payouts_from_addr ON payouts (from_addr, submitted_at DESC);
		CREATE INDEX IF NOT EXISTS idx_payouts_tx_hash   ON payouts (tx_hash);
		CREATE INDEX IF NOT EXISTS idx_payouts_status    ON payouts (status, submitted_at DESC);
	`)
	return err
}

// Put upserts a Payout keyed by ClientRef. Later calls overwrite the row
// so Send's "pending → final" transition is persisted in place.
func (s *PostgresPayoutStore) Put(ctx context.Context, p *Payout) error {
	if p == nil || p.ClientRef == "" {
		return errors.New("usdc: payout requires ClientRef")
	}
	if p.Amount == nil {
		return errors.New("usdc: payout requires Amount")
	}

	var receiptBlock sql.NullInt64
	var receiptStatus sql.NullString
	var receiptConfs sql.NullInt64
	if p.Receipt != nil {
		receiptBlock = sql.NullInt64{Int64: int64(p.Receipt.BlockNumber), Valid: true} //nolint:gosec // block numbers fit int64
		receiptStatus = sql.NullString{String: string(p.Receipt.Status), Valid: true}
		receiptConfs = sql.NullInt64{Int64: int64(p.Receipt.Confirmations), Valid: true} //nolint:gosec // confirmation counts fit int64
	}
	var finalizedAt sql.NullTime
	if p.FinalizedAt != nil {
		finalizedAt = sql.NullTime{Time: *p.FinalizedAt, Valid: true}
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO payouts (
			client_ref, chain_id, from_addr, to_addr, amount, nonce,
			tx_hash, status, submitted_at, finalized_at,
			receipt_block, receipt_status, receipt_confs, last_error,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5::NUMERIC(78,0), $6,
			$7, $8, $9, $10,
			$11, $12, $13, $14,
			NOW(), NOW()
		)
		ON CONFLICT (client_ref) DO UPDATE SET
			tx_hash        = EXCLUDED.tx_hash,
			status         = EXCLUDED.status,
			finalized_at   = EXCLUDED.finalized_at,
			receipt_block  = EXCLUDED.receipt_block,
			receipt_status = EXCLUDED.receipt_status,
			receipt_confs  = EXCLUDED.receipt_confs,
			last_error     = EXCLUDED.last_error,
			updated_at     = NOW()
	`,
		p.ClientRef, p.ChainID, p.From, p.To, p.Amount.String(), int64(p.Nonce), //nolint:gosec // nonce is bounded by chain
		p.TxHash, string(p.Status), p.SubmittedAt, finalizedAt,
		receiptBlock, receiptStatus, receiptConfs, nullString(p.LastError),
	)
	return err
}

// GetByClientRef returns the Payout for the given client ref, or nil when
// not present. An actual SQL error surfaces as a non-nil error.
func (s *PostgresPayoutStore) GetByClientRef(ctx context.Context, ref string) (*Payout, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT client_ref, chain_id, from_addr, to_addr, amount, nonce,
		       tx_hash, status, submitted_at, finalized_at,
		       receipt_block, receipt_status, receipt_confs, last_error
		FROM payouts WHERE client_ref = $1`, ref)
	return scanPayout(row)
}

func scanPayout(row *sql.Row) (*Payout, error) {
	var (
		p             Payout
		amountStr     string
		nonce         int64
		finalizedAt   sql.NullTime
		receiptBlock  sql.NullInt64
		receiptStatus sql.NullString
		receiptConfs  sql.NullInt64
		lastError     sql.NullString
	)
	err := row.Scan(
		&p.ClientRef, &p.ChainID, &p.From, &p.To, &amountStr, &nonce,
		&p.TxHash, &p.Status, &p.SubmittedAt, &finalizedAt,
		&receiptBlock, &receiptStatus, &receiptConfs, &lastError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("usdc: scan payout: %w", err)
	}
	amt, ok := new(big.Int).SetString(amountStr, 10)
	if !ok {
		return nil, fmt.Errorf("usdc: invalid amount in row: %q", amountStr)
	}
	p.Amount = amt
	p.Nonce = uint64(nonce) //nolint:gosec // nonce is bounded; cast is safe
	if finalizedAt.Valid {
		t := finalizedAt.Time
		p.FinalizedAt = &t
	}
	if receiptBlock.Valid && receiptStatus.Valid {
		p.Receipt = &TxReceipt{
			TxHash:        p.TxHash,
			BlockNumber:   uint64(receiptBlock.Int64), //nolint:gosec // non-negative by CHECK
			Status:        TxStatus(receiptStatus.String),
			Confirmations: uint64(receiptConfs.Int64), //nolint:gosec // non-negative
			CheckedAt:     time.Now().UTC(),
		}
	}
	p.LastError = lastError.String
	return &p, nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

var _ PayoutStore = (*PostgresPayoutStore)(nil)
