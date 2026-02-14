package supervisor

import (
	"context"
	"database/sql"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// PostgresBaselineStore implements BaselineStore backed by PostgreSQL.
type PostgresBaselineStore struct {
	db *sql.DB
}

// Compile-time check.
var _ BaselineStore = (*PostgresBaselineStore)(nil)

// NewPostgresBaselineStore creates a new Postgres-backed baseline store.
func NewPostgresBaselineStore(db *sql.DB) *PostgresBaselineStore {
	return &PostgresBaselineStore{db: db}
}

func (s *PostgresBaselineStore) SaveBaselineBatch(ctx context.Context, baselines []*AgentBaseline) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO agent_baselines (agent_addr, baseline_hourly_mean, baseline_hourly_stddev, sample_hours, last_updated)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (agent_addr) DO UPDATE SET
			baseline_hourly_mean   = EXCLUDED.baseline_hourly_mean,
			baseline_hourly_stddev = EXCLUDED.baseline_hourly_stddev,
			sample_hours           = EXCLUDED.sample_hours,
			last_updated           = EXCLUDED.last_updated
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, b := range baselines {
		_, err := stmt.ExecContext(ctx,
			b.AgentAddr,
			usdc.Format(b.HourlyMean),
			usdc.Format(b.HourlyStddev),
			b.SampleHours,
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresBaselineStore) GetAllBaselines(ctx context.Context) ([]*AgentBaseline, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_addr, baseline_hourly_mean, baseline_hourly_stddev, sample_hours, last_updated
		FROM agent_baselines
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*AgentBaseline
	for rows.Next() {
		var (
			addr       string
			meanStr    string
			stddevStr  string
			sampleHrs  int
			lastUpdate time.Time
		)
		if err := rows.Scan(&addr, &meanStr, &stddevStr, &sampleHrs, &lastUpdate); err != nil {
			return nil, err
		}
		mean, _ := usdc.Parse(meanStr)
		stddev, _ := usdc.Parse(stddevStr)
		out = append(out, &AgentBaseline{
			AgentAddr:    addr,
			HourlyMean:   mean,
			HourlyStddev: stddev,
			SampleHours:  sampleHrs,
			LastUpdated:  lastUpdate,
		})
	}
	return out, rows.Err()
}

func (s *PostgresBaselineStore) AppendSpendEvent(ctx context.Context, ev *SpendEventRecord) error {
	return s.db.QueryRowContext(ctx, `
		INSERT INTO agent_spend_events (agent_addr, counterparty, amount, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, ev.AgentAddr, ev.Counterparty, usdc.Format(ev.Amount), ev.CreatedAt).Scan(&ev.ID)
}

func (s *PostgresBaselineStore) AppendSpendEventBatch(ctx context.Context, events []*SpendEventRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO agent_spend_events (agent_addr, counterparty, amount, created_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ev := range events {
		if err := stmt.QueryRowContext(ctx,
			ev.AgentAddr, ev.Counterparty, usdc.Format(ev.Amount), ev.CreatedAt,
		).Scan(&ev.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresBaselineStore) GetRecentSpendEvents(ctx context.Context, since time.Time) ([]*SpendEventRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_addr, counterparty, amount, created_at
		FROM agent_spend_events
		WHERE created_at >= $1
		ORDER BY created_at
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*SpendEventRecord
	for rows.Next() {
		ev := &SpendEventRecord{}
		var amountStr string
		if err := rows.Scan(&ev.ID, &ev.AgentAddr, &ev.Counterparty, &amountStr, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Amount, _ = usdc.Parse(amountStr)
		out = append(out, ev)
	}
	return out, rows.Err()
}

func (s *PostgresBaselineStore) GetAllAgentsWithEvents(ctx context.Context, since time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT agent_addr
		FROM agent_spend_events
		WHERE created_at >= $1
	`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	return out, rows.Err()
}

func (s *PostgresBaselineStore) GetHourlyTotals(ctx context.Context, agentAddr string, since time.Time) (map[time.Time]*big.Int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT date_trunc('hour', created_at) AS hour, SUM(amount)
		FROM agent_spend_events
		WHERE agent_addr = $1 AND created_at >= $2
		GROUP BY 1
	`, agentAddr, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	totals := make(map[time.Time]*big.Int)
	for rows.Next() {
		var (
			hour     time.Time
			totalStr string
		)
		if err := rows.Scan(&hour, &totalStr); err != nil {
			return nil, err
		}
		total, _ := usdc.Parse(totalStr)
		totals[hour] = total
	}
	return totals, rows.Err()
}

func (s *PostgresBaselineStore) LogDenial(ctx context.Context, rec *DenialRecord) error {
	return s.db.QueryRowContext(ctx, `
		INSERT INTO agent_denial_log
			(agent_addr, rule_name, reason, amount, op_type, tier, counterparty,
			 hourly_total, baseline_mean, baseline_stddev, override_allowed, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id
	`,
		rec.AgentAddr, rec.RuleName, rec.Reason,
		usdc.Format(rec.Amount), rec.OpType, rec.Tier, rec.Counterparty,
		usdc.Format(rec.HourlyTotal), usdc.Format(rec.BaselineMean), usdc.Format(rec.BaselineStddev),
		rec.OverrideAllowed, rec.CreatedAt,
	).Scan(&rec.ID)
}

func (s *PostgresBaselineStore) PruneOldEvents(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM agent_spend_events WHERE created_at < $1
	`, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
