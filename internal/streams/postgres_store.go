package streams

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// PostgresStore persists stream data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed stream store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, s *Stream) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO streams (
			id, buyer_addr, seller_addr, service_id, session_key_id,
			hold_amount, spent_amount, price_per_tick, tick_count,
			status, stale_timeout_secs, last_tick_at, closed_at, close_reason,
			created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6::NUMERIC(20,6), $7::NUMERIC(20,6), $8::NUMERIC(20,6), $9,
			$10, $11, $12, $13, $14,
			$15, $16
		)`,
		s.ID, s.BuyerAddr, s.SellerAddr, nullString(s.ServiceID), nullString(s.SessionKeyID),
		s.HoldAmount, s.SpentAmount, s.PricePerTick, s.TickCount,
		string(s.Status), s.StaleTimeoutSec, nullTime(s.LastTickAt), nullTime(s.ClosedAt), nullString(s.CloseReason),
		s.CreatedAt, s.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Stream, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, buyer_addr, seller_addr, service_id, session_key_id,
		       hold_amount, spent_amount, price_per_tick, tick_count,
		       status, stale_timeout_secs, last_tick_at, closed_at, close_reason,
		       created_at, updated_at
		FROM streams WHERE id = $1`, id)

	s, err := scanStream(row)
	if err == sql.ErrNoRows {
		return nil, ErrStreamNotFound
	}
	return s, err
}

func (p *PostgresStore) Update(ctx context.Context, s *Stream) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE streams SET
			spent_amount = $1::NUMERIC(20,6), tick_count = $2,
			status = $3, last_tick_at = $4, closed_at = $5,
			close_reason = $6, updated_at = $7
		WHERE id = $8`,
		s.SpentAmount, s.TickCount,
		string(s.Status), nullTime(s.LastTickAt), nullTime(s.ClosedAt),
		nullString(s.CloseReason), s.UpdatedAt,
		s.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrStreamNotFound
	}
	return nil
}

func (p *PostgresStore) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*Stream, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, seller_addr, service_id, session_key_id,
		       hold_amount, spent_amount, price_per_tick, tick_count,
		       status, stale_timeout_secs, last_tick_at, closed_at, close_reason,
		       created_at, updated_at
		FROM streams
		WHERE buyer_addr = $1 OR seller_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStreams(rows)
}

func (p *PostgresStore) ListStale(ctx context.Context, before time.Time, limit int) ([]*Stream, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, seller_addr, service_id, session_key_id,
		       hold_amount, spent_amount, price_per_tick, tick_count,
		       status, stale_timeout_secs, last_tick_at, closed_at, close_reason,
		       created_at, updated_at
		FROM streams
		WHERE status = 'open'
		  AND (
		    (last_tick_at IS NOT NULL AND last_tick_at + (stale_timeout_secs || ' seconds')::INTERVAL < $1)
		    OR
		    (last_tick_at IS NULL AND created_at + (stale_timeout_secs || ' seconds')::INTERVAL < $1)
		  )
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanStreams(rows)
}

func (p *PostgresStore) CreateTick(ctx context.Context, t *Tick) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO stream_ticks (id, stream_id, seq, amount, cumulative, metadata, created_at)
		VALUES ($1, $2, $3, $4::NUMERIC(20,6), $5::NUMERIC(20,6), $6, $7)`,
		t.ID, t.StreamID, t.Seq, t.Amount, t.Cumulative, nullString(t.Metadata), t.CreatedAt,
	)
	if err != nil && strings.Contains(err.Error(), "unique constraint") {
		return ErrDuplicateTickSeq
	}
	return err
}

func (p *PostgresStore) ListTicks(ctx context.Context, streamID string, limit int) ([]*Tick, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, stream_id, seq, amount, cumulative, metadata, created_at
		FROM stream_ticks
		WHERE stream_id = $1
		ORDER BY seq ASC
		LIMIT $2`, streamID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanTicks(rows)
}

func (p *PostgresStore) GetLastTick(ctx context.Context, streamID string) (*Tick, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, stream_id, seq, amount, cumulative, metadata, created_at
		FROM stream_ticks
		WHERE stream_id = $1
		ORDER BY seq DESC
		LIMIT 1`, streamID)

	t, err := scanTick(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return t, err
}

// --- scanners ---

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanStream(sc scanner) (*Stream, error) {
	s := &Stream{}
	var (
		serviceID    sql.NullString
		sessionKeyID sql.NullString
		lastTickAt   sql.NullTime
		closedAt     sql.NullTime
		closeReason  sql.NullString
		status       string
	)

	err := sc.Scan(
		&s.ID, &s.BuyerAddr, &s.SellerAddr, &serviceID, &sessionKeyID,
		&s.HoldAmount, &s.SpentAmount, &s.PricePerTick, &s.TickCount,
		&status, &s.StaleTimeoutSec, &lastTickAt, &closedAt, &closeReason,
		&s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	s.Status = Status(status)
	s.ServiceID = serviceID.String
	s.SessionKeyID = sessionKeyID.String
	s.CloseReason = closeReason.String
	if lastTickAt.Valid {
		s.LastTickAt = &lastTickAt.Time
	}
	if closedAt.Valid {
		s.ClosedAt = &closedAt.Time
	}

	return s, nil
}

func scanStreams(rows *sql.Rows) ([]*Stream, error) {
	var result []*Stream
	for rows.Next() {
		s, err := scanStream(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func scanTick(sc scanner) (*Tick, error) {
	t := &Tick{}
	var metadata sql.NullString

	err := sc.Scan(
		&t.ID, &t.StreamID, &t.Seq, &t.Amount, &t.Cumulative, &metadata, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	t.Metadata = metadata.String
	return t, nil
}

func scanTicks(rows *sql.Rows) ([]*Tick, error) {
	var result []*Tick
	for rows.Next() {
		t, err := scanTick(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
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
