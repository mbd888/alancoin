package eventbus

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/mbd888/alancoin/internal/metrics"
)

// CleanupWorker periodically removes old processed/published rows from the
// eventbus WAL and outbox tables to prevent unbounded growth.
// Uses PostgreSQL advisory locks so only one instance runs cleanup in multi-node deployments.
type CleanupWorker struct {
	db              *sql.DB
	wal             *WALStore
	outbox          *Outbox
	interval        time.Duration
	walRetention    time.Duration
	outboxRetention time.Duration
	logger          *slog.Logger
}

const (
	cleanupLockWAL    = 900010
	cleanupLockOutbox = 900011
)

// NewCleanupWorker creates a cleanup worker.
// wal and outbox may be nil (cleanup for that component is skipped).
func NewCleanupWorker(db *sql.DB, wal *WALStore, outbox *Outbox, interval, walRetention, outboxRetention time.Duration, logger *slog.Logger) *CleanupWorker {
	return &CleanupWorker{
		db:              db,
		wal:             wal,
		outbox:          outbox,
		interval:        interval,
		walRetention:    walRetention,
		outboxRetention: outboxRetention,
		logger:          logger,
	}
}

// Start runs the cleanup loop until ctx is cancelled.
func (w *CleanupWorker) Start(ctx context.Context) {
	// Run once immediately on startup
	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *CleanupWorker) runOnce(ctx context.Context) {
	if w.wal != nil {
		w.cleanupWithLock(ctx, cleanupLockWAL, "wal", func() (int64, error) {
			return w.wal.Cleanup(ctx, w.walRetention)
		})
	}

	if w.outbox != nil {
		w.cleanupWithLock(ctx, cleanupLockOutbox, "outbox", func() (int64, error) {
			return w.outbox.Cleanup(ctx, w.outboxRetention)
		})
	}
}

func (w *CleanupWorker) cleanupWithLock(ctx context.Context, lockID int, name string, fn func() (int64, error)) {
	var acquired bool
	err := w.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&acquired)
	if err != nil || !acquired {
		return
	}
	defer func() {
		_, _ = w.db.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", lockID)
	}()

	deleted, err := fn()
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("eventbus cleanup failed", "component", name, "error", err)
		}
		return
	}
	if deleted > 0 {
		metrics.CleanupDeletedTotal.WithLabelValues(name).Add(float64(deleted))
		w.logger.Info("eventbus cleanup completed", "component", name, "deleted", deleted)
	}
}
