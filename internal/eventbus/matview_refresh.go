package eventbus

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/mbd888/alancoin/internal/metrics"
)

// MatviewRefresher periodically refreshes materialized views for dashboards.
// This replaces full-table-scan queries with pre-aggregated reads.
type MatviewRefresher struct {
	db       *sql.DB
	interval time.Duration
	logger   *slog.Logger
	views    []string
}

// NewMatviewRefresher creates a refresher for the given views.
func NewMatviewRefresher(db *sql.DB, interval time.Duration, logger *slog.Logger) *MatviewRefresher {
	return &MatviewRefresher{
		db:       db,
		interval: interval,
		logger:   logger,
		views: []string{
			"billing_timeseries_hourly",
			"chargeback_summary_monthly",
		},
	}
}

// Start runs the refresh loop until ctx is cancelled.
func (r *MatviewRefresher) Start(ctx context.Context) {
	// Refresh immediately on startup
	r.refreshAll(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.refreshAll(ctx)
		}
	}
}

func (r *MatviewRefresher) refreshAll(ctx context.Context) {
	for i, view := range r.views {
		// Advisory lock prevents concurrent refreshes of the same view
		// (e.g. multiple server instances running the same refresher).
		// Lock ID is derived from view index + a fixed namespace.
		lockID := int64(900000 + i)
		var acquired bool
		_ = r.db.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockID).Scan(&acquired)
		if !acquired {
			r.logger.Debug("matview refresh skipped (lock held)", "view", view)
			continue
		}

		start := time.Now()
		_, err := r.db.ExecContext(ctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY "+view)
		elapsed := time.Since(start)

		if err != nil {
			// CONCURRENTLY requires unique index; fall back to blocking refresh
			_, err = r.db.ExecContext(ctx, "REFRESH MATERIALIZED VIEW "+view)
			if err != nil {
				r.logger.Error("matview refresh failed", "view", view, "error", err)
				_, _ = r.db.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", lockID)
				continue
			}
		}

		_, _ = r.db.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", lockID)

		metrics.MatviewRefreshDuration.WithLabelValues(view).Observe(elapsed.Seconds())
		r.logger.Debug("matview refreshed", "view", view, "duration", elapsed)
	}
}
