package registry

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// PartitionMaintainer auto-creates monthly transaction partitions ahead of time.
type PartitionMaintainer struct {
	db       *sql.DB
	interval time.Duration
	logger   *slog.Logger
	stop     chan struct{}
}

var partitionNameRe = regexp.MustCompile(`\A[a-z0-9_]+\z`)

func quoteIdentifier(ident string) string {
	return `"` + strings.ReplaceAll(ident, `"`, `""`) + `"`
}

// NewPartitionMaintainer creates a maintainer that ensures future partitions exist.
func NewPartitionMaintainer(db *sql.DB, interval time.Duration, logger *slog.Logger) *PartitionMaintainer {
	return &PartitionMaintainer{
		db:       db,
		interval: interval,
		logger:   logger,
		stop:     make(chan struct{}),
	}
}

// Start begins the periodic partition check. Call in a goroutine.
func (m *PartitionMaintainer) Start(ctx context.Context) {
	// Ensure partitions exist on startup
	m.ensurePartitions(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stop:
			return
		case <-ticker.C:
			m.ensurePartitions(ctx)
		}
	}
}

// Stop signals the maintainer to stop.
func (m *PartitionMaintainer) Stop() {
	select {
	case m.stop <- struct{}{}:
	default:
	}
}

// ensurePartitions creates partitions for the next 3 months if they don't exist.
func (m *PartitionMaintainer) ensurePartitions(ctx context.Context) {
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		t := now.AddDate(0, i, 0)
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, 0)
		name := fmt.Sprintf("transactions_%04d_%02d", start.Year(), start.Month())

		if !partitionNameRe.MatchString(name) {
			m.logger.Warn("invalid partition name", "partition", name)
			continue
		}

		// Identifiers (table names) can't be parameterized. We generate `name` ourselves,
		// validate it strictly, and quote it as a SQL identifier.
		// #nosec G201
		query := fmt.Sprintf(
			`CREATE TABLE IF NOT EXISTS %s PARTITION OF transactions FOR VALUES FROM ('%s') TO ('%s')`,
			quoteIdentifier(name),
			start.Format("2006-01-02"),
			end.Format("2006-01-02"),
		)

		if _, err := m.db.ExecContext(ctx, query); err != nil {
			m.logger.Warn("partition create failed", "partition", name, "error", err)
		}
	}
}
