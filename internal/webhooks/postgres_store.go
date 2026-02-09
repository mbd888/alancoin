package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PostgresStore persists webhook subscriptions in PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed webhook store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the webhooks table
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS webhooks (
			id                    VARCHAR(36) PRIMARY KEY,
			agent_address         VARCHAR(42) NOT NULL,
			url                   TEXT NOT NULL,
			secret                VARCHAR(64) NOT NULL,
			events                JSONB NOT NULL,
			active                BOOLEAN DEFAULT TRUE,
			created_at            TIMESTAMPTZ DEFAULT NOW(),
			last_success          TIMESTAMPTZ,
			last_error            TEXT,
			consecutive_failures  INTEGER NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS idx_webhooks_agent ON webhooks(agent_address);
		CREATE INDEX IF NOT EXISTS idx_webhooks_active ON webhooks(active) WHERE active = TRUE;
	`)
	return err
}

func (p *PostgresStore) Create(ctx context.Context, sub *Subscription) error {
	eventsJSON, err := json.Marshal(sub.Events)
	if err != nil {
		return err
	}

	_, err = p.db.ExecContext(ctx, `
		INSERT INTO webhooks (id, agent_address, url, secret, events, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, sub.ID, sub.AgentAddr, sub.URL, sub.Secret, eventsJSON, sub.Active, sub.CreatedAt)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Subscription, error) {
	sub := &Subscription{}
	var eventsJSON []byte
	var lastSuccess sql.NullTime
	var lastError sql.NullString

	err := p.db.QueryRowContext(ctx, `
		SELECT id, agent_address, url, secret, events, active, created_at, last_success, last_error, consecutive_failures
		FROM webhooks WHERE id = $1
	`, id).Scan(
		&sub.ID, &sub.AgentAddr, &sub.URL, &sub.Secret, &eventsJSON,
		&sub.Active, &sub.CreatedAt, &lastSuccess, &lastError, &sub.ConsecutiveFailures,
	)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(eventsJSON, &sub.Events); err != nil {
		return nil, err
	}

	if lastSuccess.Valid {
		sub.LastSuccess = &lastSuccess.Time
	}
	sub.LastError = lastError.String

	return sub, nil
}

func (p *PostgresStore) GetByAgent(ctx context.Context, agentAddr string) ([]*Subscription, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_address, url, secret, events, active, created_at, last_success, last_error, consecutive_failures
		FROM webhooks WHERE agent_address = $1 ORDER BY created_at DESC
	`, agentAddr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return p.scanSubscriptions(rows)
}

func (p *PostgresStore) GetByEvent(ctx context.Context, eventType EventType) ([]*Subscription, error) {
	// Use json.Marshal to safely encode the event type for JSONB query
	eventsJSON, _ := json.Marshal([]string{string(eventType)})

	// Query active webhooks that include this event type
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_address, url, secret, events, active, created_at, last_success, last_error, consecutive_failures
		FROM webhooks
		WHERE active = TRUE AND events @> $1::jsonb
	`, string(eventsJSON))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return p.scanSubscriptions(rows)
}

func (p *PostgresStore) Update(ctx context.Context, sub *Subscription) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE webhooks SET
			active = $1,
			last_success = $2,
			last_error = $3,
			consecutive_failures = $4
		WHERE id = $5
	`, sub.Active, sub.LastSuccess, sub.LastError, sub.ConsecutiveFailures, sub.ID)
	return err
}

func (p *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM webhooks WHERE id = $1`, id)
	return err
}

func (p *PostgresStore) scanSubscriptions(rows *sql.Rows) ([]*Subscription, error) {
	var subs []*Subscription
	for rows.Next() {
		sub := &Subscription{}
		var eventsJSON []byte
		var lastSuccess sql.NullTime
		var lastError sql.NullString

		if err := rows.Scan(
			&sub.ID, &sub.AgentAddr, &sub.URL, &sub.Secret, &eventsJSON,
			&sub.Active, &sub.CreatedAt, &lastSuccess, &lastError, &sub.ConsecutiveFailures,
		); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(eventsJSON, &sub.Events); err != nil {
			return nil, err
		}

		if lastSuccess.Valid {
			sub.LastSuccess = &lastSuccess.Time
		}
		sub.LastError = lastError.String

		subs = append(subs, sub)
	}
	return subs, rows.Err()
}
