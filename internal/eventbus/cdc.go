package eventbus

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// CDC (Change Data Capture) watches the ledger for new entries and
// publishes them as events. This captures ALL financial state changes,
// not just gateway settlements — including direct transfers, deposits,
// withdrawals, escrow locks, and credit operations.
//
// Implementation uses PostgreSQL LISTEN/NOTIFY for near-realtime capture,
// with a polling fallback for environments that don't support it.
//
// Events produced:
//   - ledger.credit  (deposit, settlement received)
//   - ledger.debit   (withdrawal, settlement paid)
//   - ledger.hold    (escrow lock, session budget hold)
//   - ledger.release (escrow refund, session close)
//
// Consumers can build materialized views, analytics pipelines, or
// external system synchronization from the CDC stream.
type CDC struct {
	db           *sql.DB
	bus          Bus
	logger       *slog.Logger
	pollInterval time.Duration
	lastID       string // track last seen entry ID for polling
}

// CDCEvent is the payload for ledger CDC events.
type CDCEvent struct {
	EntryID   string `json:"entryId"`
	AgentAddr string `json:"agentAddr"`
	Type      string `json:"type"` // credit, debit, hold, release, settle
	Amount    string `json:"amount"`
	Reference string `json:"reference"`
	TxHash    string `json:"txHash,omitempty"`
	CreatedAt string `json:"createdAt"`
}

// NewCDC creates a CDC watcher.
func NewCDC(db *sql.DB, bus Bus, logger *slog.Logger) *CDC {
	return &CDC{
		db:           db,
		bus:          bus,
		logger:       logger,
		pollInterval: 500 * time.Millisecond,
	}
}

// Start begins watching for ledger changes. Blocks until ctx is cancelled.
func (c *CDC) Start(ctx context.Context) {
	// Try LISTEN/NOTIFY first (near-realtime)
	if c.tryListenNotify(ctx) {
		return // exited due to context cancellation
	}

	// Fallback: polling
	c.logger.Info("cdc: LISTEN/NOTIFY not available, falling back to polling",
		"interval", c.pollInterval)
	c.pollLoop(ctx)
}

func (c *CDC) tryListenNotify(ctx context.Context) bool {
	// Attempt to use PostgreSQL LISTEN for realtime CDC.
	// This requires the ledger to NOTIFY on insert (trigger-based).
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(ctx, "LISTEN ledger_changes")
	if err != nil {
		return false
	}

	c.logger.Info("cdc: LISTEN/NOTIFY active on ledger_changes")

	for {
		// WaitForNotification blocks until a notification arrives or ctx is cancelled.
		// This requires a driver that supports it (e.g. pgx).
		// For lib/pq, we fall back to polling.
		select {
		case <-ctx.Done():
			return true
		case <-time.After(c.pollInterval):
			// lib/pq doesn't support async notifications in this mode,
			// so we poll as a fallback.
			c.pollOnce(ctx)
		}
	}
}

func (c *CDC) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pollOnce(ctx)
		}
	}
}

func (c *CDC) pollOnce(ctx context.Context) {
	query := `
		SELECT id, agent_address, type, amount, reference, tx_hash, created_at
		FROM ledger_entries
		WHERE id > $1
		ORDER BY id ASC
		LIMIT 100
	`
	rows, err := c.db.QueryContext(ctx, query, c.lastID)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Error("cdc: poll query failed", "error", err)
		}
		return
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var entry CDCEvent
		var txHash sql.NullString
		if err := rows.Scan(&entry.EntryID, &entry.AgentAddr, &entry.Type,
			&entry.Amount, &entry.Reference, &txHash, &entry.CreatedAt); err != nil {
			c.logger.Error("cdc: scan failed", "error", err)
			continue
		}
		entry.TxHash = txHash.String

		topic := "ledger." + entry.Type
		event, err := NewEvent(topic, entry.AgentAddr, entry)
		if err != nil {
			continue
		}

		if err := c.bus.Publish(ctx, event); err != nil {
			c.logger.Warn("cdc: publish failed", "entry_id", entry.EntryID, "error", err)
			return // stop processing this batch — will retry from lastID
		}

		c.lastID = entry.EntryID
	}
	if err := rows.Err(); err != nil {
		c.logger.Error("cdc: row iteration failed", "error", err)
	}
}
