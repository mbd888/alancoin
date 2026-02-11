package ledger

import (
	"context"
	"database/sql"
	"time"
)

// PostgresEventStore implements EventStore using PostgreSQL.
type PostgresEventStore struct {
	db *sql.DB
}

// NewPostgresEventStore creates a new PostgreSQL-backed event store.
func NewPostgresEventStore(db *sql.DB) *PostgresEventStore {
	return &PostgresEventStore{db: db}
}

func (s *PostgresEventStore) AppendEvent(ctx context.Context, event *Event) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO ledger_events (agent_addr, event_type, amount, reference, counterparty, metadata, created_at)
		VALUES ($1, $2, $3::NUMERIC(20,6), $4, $5, COALESCE($6::JSONB, '{}'), NOW())
	`, event.AgentAddr, event.EventType, event.Amount, event.Reference, event.Counterparty, event.Metadata)
	return err
}

func (s *PostgresEventStore) GetEvents(ctx context.Context, agentAddr string, since time.Time) ([]*Event, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_addr, event_type, amount, COALESCE(reference, ''), COALESCE(counterparty, ''), COALESCE(metadata::TEXT, '{}'), created_at
		FROM ledger_events
		WHERE agent_addr = $1 AND created_at >= $2
		ORDER BY id ASC
	`, agentAddr, since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var events []*Event
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(&e.ID, &e.AgentAddr, &e.EventType, &e.Amount, &e.Reference, &e.Counterparty, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *PostgresEventStore) GetAllAgents(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT agent_addr FROM ledger_events ORDER BY agent_addr
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var agents []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, err
		}
		agents = append(agents, addr)
	}
	return agents, rows.Err()
}
