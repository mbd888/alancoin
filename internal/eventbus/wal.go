package eventbus

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// WAL (Write-Ahead Log) provides crash recovery for the event bus.
// Events are persisted to postgres before acknowledgment, and replayed
// on startup if they were not processed before the last shutdown.
//
// Table: eventbus_wal
//   id TEXT PRIMARY KEY
//   topic TEXT NOT NULL
//   key TEXT NOT NULL
//   payload JSONB NOT NULL
//   request_id TEXT
//   status TEXT NOT NULL DEFAULT 'pending' -- pending, processed, dead_lettered
//   created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
//   processed_at TIMESTAMPTZ
//
// The WAL is optional. Without a database, the MemoryBus operates
// without persistence (events lost on crash, acceptable for development).

// WALStore persists events for crash recovery.
type WALStore struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewWALStore creates a WAL backed by PostgreSQL.
func NewWALStore(db *sql.DB, logger *slog.Logger) *WALStore {
	return &WALStore{db: db, logger: logger}
}

// Write persists an event before it enters the bus.
func (w *WALStore) Write(ctx context.Context, event Event) error {
	_, err := w.db.ExecContext(ctx, `
		INSERT INTO eventbus_wal (id, topic, key, payload, request_id, status, created_at)
		VALUES ($1, $2, $3, $4, $5, 'pending', $6)
		ON CONFLICT (id) DO NOTHING
	`, event.ID, event.Topic, event.Key, event.Payload, event.RequestID, event.Timestamp)
	return err
}

// MarkProcessed marks an event as successfully consumed.
func (w *WALStore) MarkProcessed(ctx context.Context, eventID string) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE eventbus_wal SET status = 'processed', processed_at = NOW()
		WHERE id = $1
	`, eventID)
	return err
}

// MarkDeadLettered marks an event as failed all retries.
func (w *WALStore) MarkDeadLettered(ctx context.Context, eventID string) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE eventbus_wal SET status = 'dead_lettered', processed_at = NOW()
		WHERE id = $1
	`, eventID)
	return err
}

// RecoverPending returns all events that were written but not processed.
// Called on startup to replay events that were in-flight when the process crashed.
func (w *WALStore) RecoverPending(ctx context.Context) ([]Event, error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT id, topic, key, payload, request_id, created_at
		FROM eventbus_wal
		WHERE status = 'pending'
		ORDER BY created_at ASC
		LIMIT 10000
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []Event
	for rows.Next() {
		var e Event
		var requestID sql.NullString
		if err := rows.Scan(&e.ID, &e.Topic, &e.Key, &e.Payload, &requestID, &e.Timestamp); err != nil {
			return nil, err
		}
		e.RequestID = requestID.String
		events = append(events, e)
	}
	return events, rows.Err()
}

// Cleanup removes processed events older than the retention period.
// Call periodically (e.g. daily) to prevent the WAL table from growing unbounded.
func (w *WALStore) Cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	result, err := w.db.ExecContext(ctx, `
		DELETE FROM eventbus_wal
		WHERE status = 'processed' AND processed_at < $1
	`, time.Now().Add(-retention))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CreateTable creates the WAL table (used for testing without migrations).
func (w *WALStore) CreateTable(ctx context.Context) error {
	_, err := w.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS eventbus_wal (
			id TEXT PRIMARY KEY,
			topic TEXT NOT NULL,
			key TEXT NOT NULL,
			payload JSONB NOT NULL,
			request_id TEXT,
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processed', 'dead_lettered')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			processed_at TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_wal_status ON eventbus_wal(status) WHERE status = 'pending';
		CREATE INDEX IF NOT EXISTS idx_wal_cleanup ON eventbus_wal(processed_at) WHERE status = 'processed';
	`)
	return err
}
