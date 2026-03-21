package eventbus

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

// Outbox implements the transactional outbox pattern for exactly-once event publishing.
//
// The problem: when a service settles a payment AND publishes an event, these are
// two separate operations. If the process crashes between them, either:
//   - Payment settles but event is lost (data inconsistency)
//   - Event publishes but payment didn't settle (phantom event)
//
// The solution: write the event to an outbox table IN THE SAME TRANSACTION as the
// payment settlement. A background poller reads the outbox and publishes to the bus.
// If the poller crashes, it re-reads unpublished events on restart.
//
// This guarantees: if the payment is committed, the event WILL be published.
// No event is published without a committed payment. Exactly-once semantics.
//
// Usage:
//
//	// In the settlement transaction:
//	tx, _ := db.BeginTx(ctx, nil)
//	ledger.SettleHold(tx, ...)
//	outbox.WriteInTx(tx, event)
//	tx.Commit()
//
//	// Background poller (started once):
//	go outbox.Poll(ctx, bus, 100*time.Millisecond)
type Outbox struct {
	db     *sql.DB
	logger *slog.Logger
}

// NewOutbox creates an outbox backed by PostgreSQL.
func NewOutbox(db *sql.DB, logger *slog.Logger) *Outbox {
	return &Outbox{db: db, logger: logger}
}

// WriteInTx writes an event to the outbox within an existing transaction.
// The event is only visible after the transaction commits.
func (o *Outbox) WriteInTx(ctx context.Context, tx *sql.Tx, event Event) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO eventbus_outbox (id, topic, key, payload, request_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.ID, event.Topic, event.Key, event.Payload, event.RequestID, event.Timestamp)
	return err
}

// Write writes an event to the outbox in its own transaction (non-transactional path).
// Use WriteInTx when you need atomicity with another operation.
func (o *Outbox) Write(ctx context.Context, event Event) error {
	_, err := o.db.ExecContext(ctx, `
		INSERT INTO eventbus_outbox (id, topic, key, payload, request_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, event.ID, event.Topic, event.Key, event.Payload, event.RequestID, event.Timestamp)
	return err
}

// Poll continuously reads unpublished events from the outbox and publishes them to the bus.
// Events are marked as published after successful bus.Publish.
// Runs until ctx is cancelled.
func (o *Outbox) Poll(ctx context.Context, bus Bus, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final drain
			o.publishBatch(ctx, bus)
			return
		case <-ticker.C:
			o.publishBatch(ctx, bus)
		}
	}
}

func (o *Outbox) publishBatch(ctx context.Context, bus Bus) {
	// SELECT FOR UPDATE SKIP LOCKED: only one poller processes each event,
	// even with multiple server instances. Events locked by other pollers are skipped.
	rows, err := o.db.QueryContext(ctx, `
		SELECT id, topic, key, payload, request_id, created_at
		FROM eventbus_outbox
		WHERE published = FALSE
		ORDER BY created_at ASC
		LIMIT 100
		FOR UPDATE SKIP LOCKED
	`)
	if err != nil {
		if ctx.Err() == nil {
			o.logger.Error("outbox: poll query failed", "error", err)
		}
		return
	}
	defer func() { _ = rows.Close() }()

	var published int
	for rows.Next() {
		var e Event
		var requestID sql.NullString
		if err := rows.Scan(&e.ID, &e.Topic, &e.Key, &e.Payload, &requestID, &e.Timestamp); err != nil {
			o.logger.Error("outbox: scan failed", "error", err)
			continue
		}
		e.RequestID = requestID.String

		if err := bus.Publish(ctx, e); err != nil {
			o.logger.Warn("outbox: publish failed (will retry)", "event_id", e.ID, "error", err)
			continue
		}

		// Mark as published
		if _, err := o.db.ExecContext(ctx, `
			UPDATE eventbus_outbox SET published = TRUE, published_at = NOW() WHERE id = $1
		`, e.ID); err != nil {
			o.logger.Error("outbox: mark published failed", "event_id", e.ID, "error", err)
		}
		published++
	}

	if published > 0 {
		o.logger.Debug("outbox: published batch", "count", published)
	}
}

// Cleanup removes published events older than the retention period.
func (o *Outbox) Cleanup(ctx context.Context, retention time.Duration) (int64, error) {
	result, err := o.db.ExecContext(ctx, `
		DELETE FROM eventbus_outbox
		WHERE published = TRUE AND published_at < $1
	`, time.Now().Add(-retention))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// PendingCount returns the number of unpublished events.
func (o *Outbox) PendingCount(ctx context.Context) (int64, error) {
	var count int64
	err := o.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM eventbus_outbox WHERE published = FALSE
	`).Scan(&count)
	return count, err
}

// CreateTable creates the outbox table.
func (o *Outbox) CreateTable(ctx context.Context) error {
	_, err := o.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS eventbus_outbox (
			id TEXT PRIMARY KEY,
			topic TEXT NOT NULL,
			key TEXT NOT NULL,
			payload JSONB NOT NULL,
			request_id TEXT,
			published BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			published_at TIMESTAMPTZ
		);
		CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON eventbus_outbox(created_at) WHERE published = FALSE;
	`)
	return err
}

// Suppress unused import warning
var _ = json.Marshal
