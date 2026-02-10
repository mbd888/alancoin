package registry

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// MatviewRefresher periodically refreshes the service_listings_mv materialized view.
type MatviewRefresher struct {
	db       *sql.DB
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

// NewMatviewRefresher creates a refresher for the discovery materialized view.
func NewMatviewRefresher(db *sql.DB, interval time.Duration, logger *slog.Logger) *MatviewRefresher {
	return &MatviewRefresher{
		db:       db,
		interval: interval,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the periodic refresh loop. Call in a goroutine.
func (r *MatviewRefresher) Start(ctx context.Context) {
	// Do an initial refresh so the view is current at startup
	r.refresh(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stop:
			return
		case <-ticker.C:
			r.refresh(ctx)
		}
	}
}

// Stop signals the refresher to stop.
func (r *MatviewRefresher) Stop() {
	select {
	case r.stop <- struct{}{}:
	default:
	}
}

func (r *MatviewRefresher) refresh(ctx context.Context) {
	_, err := r.db.ExecContext(ctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY service_listings_mv")
	if err != nil {
		r.logger.Warn("matview refresh failed", "error", err)
		return
	}
	r.logger.Debug("refreshed service_listings_mv")
}
