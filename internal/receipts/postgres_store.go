package receipts

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PostgresStore persists receipt data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed receipt store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the receipts table, chain-head table, and indexes.
// Safe to run on an existing receipts table: new columns are added with defaults.
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
			created_at   TIMESTAMPTZ DEFAULT NOW(),
			scope        VARCHAR(64) NOT NULL DEFAULT 'global',
			chain_index  BIGINT NOT NULL DEFAULT 0,
			prev_hash    VARCHAR(64) NOT NULL DEFAULT ''
		);
		ALTER TABLE receipts ADD COLUMN IF NOT EXISTS scope       VARCHAR(64) NOT NULL DEFAULT 'global';
		ALTER TABLE receipts ADD COLUMN IF NOT EXISTS chain_index BIGINT NOT NULL DEFAULT 0;
		ALTER TABLE receipts ADD COLUMN IF NOT EXISTS prev_hash   VARCHAR(64) NOT NULL DEFAULT '';

		CREATE INDEX IF NOT EXISTS idx_receipts_from_addr ON receipts (from_addr, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_receipts_to_addr ON receipts (to_addr, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_receipts_reference ON receipts (reference);
		CREATE INDEX IF NOT EXISTS idx_receipts_payment_path ON receipts (payment_path, created_at DESC);
		CREATE INDEX IF NOT EXISTS idx_receipts_scope_chain ON receipts (scope, chain_index);
		CREATE UNIQUE INDEX IF NOT EXISTS uq_receipts_scope_chain ON receipts (scope, chain_index);
		CREATE INDEX IF NOT EXISTS idx_receipts_scope_issued ON receipts (scope, issued_at);

		CREATE TABLE IF NOT EXISTS receipt_chain_heads (
			scope       VARCHAR(64) PRIMARY KEY,
			head_hash   VARCHAR(64) NOT NULL DEFAULT '',
			head_index  BIGINT      NOT NULL DEFAULT -1,
			receipt_id  VARCHAR(36),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}

func (p *PostgresStore) Create(ctx context.Context, r *Receipt) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO receipts (
			id, payment_path, reference, from_addr, to_addr,
			amount, service_id, status, payload_hash, signature,
			issued_at, expires_at, metadata, created_at,
			scope, chain_index, prev_hash
		) VALUES (
			$1, $2, $3, $4, $5,
			$6::NUMERIC(20,6), $7, $8, $9, $10,
			$11, $12, $13, $14,
			$15, $16, $17
		)`,
		r.ID, string(r.PaymentPath), r.Reference, r.From, r.To,
		r.Amount, nullString(r.ServiceID), r.Status, r.PayloadHash, r.Signature,
		r.IssuedAt, r.ExpiresAt, nullString(r.Metadata), r.CreatedAt,
		scopeOrDefault(r.Scope), r.ChainIndex, r.PrevHash,
	)
	return err
}

// GetChainHead reads the HEAD row for the given scope. Returns an empty-chain
// sentinel (HeadHash="", HeadIndex=-1) when no head exists yet.
func (p *PostgresStore) GetChainHead(ctx context.Context, scope string) (*ChainHead, error) {
	scope = scopeOrDefault(scope)
	head := &ChainHead{Scope: scope, HeadHash: "", HeadIndex: -1}
	var receiptID sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT head_hash, head_index, receipt_id, updated_at
		FROM receipt_chain_heads WHERE scope = $1`, scope).
		Scan(&head.HeadHash, &head.HeadIndex, &receiptID, &head.UpdatedAt)
	if err == sql.ErrNoRows {
		return head, nil
	}
	if err != nil {
		return nil, err
	}
	head.ReceiptID = receiptID.String
	return head, nil
}

// AppendReceipt atomically links r into its scope chain.
// Uses a SERIALIZABLE-level row lock on receipt_chain_heads so concurrent
// writers on the same scope cannot produce a fork.
func (p *PostgresStore) AppendReceipt(ctx context.Context, r *Receipt) error {
	scope := scopeOrDefault(r.Scope)

	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert a placeholder head on first use so FOR UPDATE has a row to lock.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO receipt_chain_heads (scope, head_hash, head_index, receipt_id, updated_at)
		VALUES ($1, '', -1, NULL, NOW())
		ON CONFLICT (scope) DO NOTHING`, scope); err != nil {
		return err
	}

	var curHash string
	var curIndex int64
	if err := tx.QueryRowContext(ctx, `
		SELECT head_hash, head_index FROM receipt_chain_heads
		WHERE scope = $1 FOR UPDATE`, scope).Scan(&curHash, &curIndex); err != nil {
		return err
	}

	if r.PrevHash != curHash || r.ChainIndex != curIndex+1 {
		return ErrChainHeadStale
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO receipts (
			id, payment_path, reference, from_addr, to_addr,
			amount, service_id, status, payload_hash, signature,
			issued_at, expires_at, metadata, created_at,
			scope, chain_index, prev_hash
		) VALUES (
			$1, $2, $3, $4, $5,
			$6::NUMERIC(20,6), $7, $8, $9, $10,
			$11, $12, $13, $14,
			$15, $16, $17
		)`,
		r.ID, string(r.PaymentPath), r.Reference, r.From, r.To,
		r.Amount, nullString(r.ServiceID), r.Status, r.PayloadHash, r.Signature,
		r.IssuedAt, r.ExpiresAt, nullString(r.Metadata), r.CreatedAt,
		scope, r.ChainIndex, r.PrevHash,
	); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE receipt_chain_heads
		SET head_hash = $1, head_index = $2, receipt_id = $3, updated_at = NOW()
		WHERE scope = $4`,
		r.PayloadHash, r.ChainIndex, r.ID, scope); err != nil {
		return err
	}

	return tx.Commit()
}

// ListByChain returns receipts in [lowerIndex, upperIndex] ordered ascending.
// Pass upperIndex = -1 to fetch everything up to HEAD.
func (p *PostgresStore) ListByChain(ctx context.Context, scope string, lowerIndex, upperIndex int64) ([]*Receipt, error) {
	scope = scopeOrDefault(scope)
	var (
		rows *sql.Rows
		err  error
	)
	if upperIndex < 0 {
		rows, err = p.db.QueryContext(ctx, `
			SELECT `+receiptColumns+`
			FROM receipts
			WHERE scope = $1 AND chain_index >= $2
			ORDER BY chain_index ASC`, scope, lowerIndex)
	} else {
		rows, err = p.db.QueryContext(ctx, `
			SELECT `+receiptColumns+`
			FROM receipts
			WHERE scope = $1 AND chain_index BETWEEN $2 AND $3
			ORDER BY chain_index ASC`, scope, lowerIndex, upperIndex)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReceipts(rows)
}

// ListByChainTime returns receipts where IssuedAt falls in [since, until],
// ordered by chain_index ascending. Zero-valued since/until are open.
func (p *PostgresStore) ListByChainTime(ctx context.Context, scope string, since, until time.Time) ([]*Receipt, error) {
	scope = scopeOrDefault(scope)
	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case since.IsZero() && until.IsZero():
		rows, err = p.db.QueryContext(ctx, `
			SELECT `+receiptColumns+`
			FROM receipts WHERE scope = $1
			ORDER BY chain_index ASC`, scope)
	case since.IsZero():
		rows, err = p.db.QueryContext(ctx, `
			SELECT `+receiptColumns+`
			FROM receipts
			WHERE scope = $1 AND issued_at <= $2
			ORDER BY chain_index ASC`, scope, until)
	case until.IsZero():
		rows, err = p.db.QueryContext(ctx, `
			SELECT `+receiptColumns+`
			FROM receipts
			WHERE scope = $1 AND issued_at >= $2
			ORDER BY chain_index ASC`, scope, since)
	default:
		rows, err = p.db.QueryContext(ctx, `
			SELECT `+receiptColumns+`
			FROM receipts
			WHERE scope = $1 AND issued_at BETWEEN $2 AND $3
			ORDER BY chain_index ASC`, scope, since, until)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReceipts(rows)
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Receipt, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT `+receiptColumns+`
		FROM receipts WHERE id = $1`, id)

	r, err := scanReceipt(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrReceiptNotFound
	}
	return r, err
}

func (p *PostgresStore) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Receipt, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+receiptColumns+`
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
		SELECT `+receiptColumns+`
		FROM receipts
		WHERE reference = $1
		ORDER BY created_at DESC`, reference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanReceipts(rows)
}

const receiptColumns = `id, payment_path, reference, from_addr, to_addr,
	       amount, service_id, status, payload_hash, signature,
	       issued_at, expires_at, metadata, created_at,
	       scope, chain_index, prev_hash`

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
		&r.Scope, &r.ChainIndex, &r.PrevHash,
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
var _ ChainStore = (*PostgresStore)(nil)
