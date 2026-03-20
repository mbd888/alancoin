package forensics

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lib/pq"
)

// PostgresStore persists forensics data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed forensics store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) GetBaseline(ctx context.Context, agentAddr string) (*Baseline, error) {
	var b Baseline
	var counterpartiesJSON, servicesJSON []byte
	var activeHours pq.Int64Array

	err := p.db.QueryRowContext(ctx, `
		SELECT agent_addr, tx_count, mean_amount, stddev_amount, mean_velocity, stddev_velocity,
		       known_counterparties, known_services, active_hours, last_updated
		FROM forensics_baselines WHERE agent_addr = $1
	`, agentAddr).Scan(&b.AgentAddr, &b.TxCount, &b.MeanAmount, &b.StdDevAmount,
		&b.MeanVelocity, &b.StdDevVelocity,
		&counterpartiesJSON, &servicesJSON, &activeHours, &b.LastUpdated)
	if err == sql.ErrNoRows {
		return nil, ErrAgentNotTracked
	}
	if err != nil {
		return nil, err
	}

	b.KnownCounterparties = make(map[string]int)
	_ = json.Unmarshal(counterpartiesJSON, &b.KnownCounterparties)
	b.KnownServices = make(map[string]int)
	_ = json.Unmarshal(servicesJSON, &b.KnownServices)
	for i, v := range activeHours {
		if i < 24 {
			b.ActiveHours[i] = int(v)
		}
	}
	return &b, nil
}

func (p *PostgresStore) SaveBaseline(ctx context.Context, b *Baseline) error {
	counterpartiesJSON, _ := json.Marshal(b.KnownCounterparties)
	servicesJSON, _ := json.Marshal(b.KnownServices)

	hours := make(pq.Int64Array, 24)
	for i := 0; i < 24; i++ {
		hours[i] = int64(b.ActiveHours[i])
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO forensics_baselines (agent_addr, tx_count, mean_amount, stddev_amount, mean_velocity, stddev_velocity, known_counterparties, known_services, active_hours, last_updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (agent_addr) DO UPDATE SET
			tx_count = $2, mean_amount = $3, stddev_amount = $4, mean_velocity = $5, stddev_velocity = $6,
			known_counterparties = $7, known_services = $8, active_hours = $9, last_updated = $10
	`, b.AgentAddr, b.TxCount, b.MeanAmount, b.StdDevAmount, b.MeanVelocity, b.StdDevVelocity,
		counterpartiesJSON, servicesJSON, hours, b.LastUpdated)
	return err
}

func (p *PostgresStore) SaveAlert(ctx context.Context, a *Alert) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO forensics_alerts (id, agent_addr, type, severity, message, score, baseline, actual, sigma, detected_at, acknowledged)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, a.ID, a.AgentAddr, a.Type, a.Severity, a.Message, a.Score,
		a.Baseline, a.Actual, a.Sigma, a.DetectedAt, a.Acknowledged)
	return err
}

func (p *PostgresStore) ListAlerts(ctx context.Context, agentAddr string, limit int) ([]*Alert, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, type, severity, message, score, baseline, actual, sigma, detected_at, acknowledged
		FROM forensics_alerts WHERE agent_addr = $1
		ORDER BY detected_at DESC LIMIT $2
	`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAlerts(rows)
}

func (p *PostgresStore) ListAllAlerts(ctx context.Context, severity AlertSeverity, limit int) ([]*Alert, error) {
	var rows *sql.Rows
	var err error
	if severity != "" {
		rows, err = p.db.QueryContext(ctx, `
			SELECT id, agent_addr, type, severity, message, score, baseline, actual, sigma, detected_at, acknowledged
			FROM forensics_alerts WHERE severity = $1
			ORDER BY detected_at DESC LIMIT $2
		`, severity, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `
			SELECT id, agent_addr, type, severity, message, score, baseline, actual, sigma, detected_at, acknowledged
			FROM forensics_alerts ORDER BY detected_at DESC LIMIT $1
		`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAlerts(rows)
}

func (p *PostgresStore) AcknowledgeAlert(ctx context.Context, alertID string) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE forensics_alerts SET acknowledged = TRUE WHERE id = $1
	`, alertID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrAlertNotFound
	}
	return nil
}

func scanAlerts(rows *sql.Rows) ([]*Alert, error) {
	var result []*Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.AgentAddr, &a.Type, &a.Severity, &a.Message,
			&a.Score, &a.Baseline, &a.Actual, &a.Sigma, &a.DetectedAt, &a.Acknowledged); err != nil {
			return nil, err
		}
		result = append(result, &a)
	}
	return result, rows.Err()
}

var _ Store = (*PostgresStore)(nil)
