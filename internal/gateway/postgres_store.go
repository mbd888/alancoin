package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/lib/pq"
)

// PostgresStore persists gateway session data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed gateway store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) CreateSession(ctx context.Context, session *Session) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO gateway_sessions (
			id, agent_addr, max_total, max_per_request, total_spent,
			request_count, strategy, allowed_types, warn_at_percent,
			status, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3::NUMERIC(20,6), $4::NUMERIC(20,6), $5::NUMERIC(20,6),
			$6, $7, $8, $9,
			$10, $11, $12, $13
		)`,
		session.ID, session.AgentAddr, session.MaxTotal, session.MaxPerRequest, session.TotalSpent,
		session.RequestCount, session.Strategy, pq.Array(session.AllowedTypes), session.WarnAtPercent,
		string(session.Status), session.ExpiresAt, session.CreatedAt, session.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetSession(ctx context.Context, id string) (*Session, error) {
	s, err := scanSession(p.db.QueryRowContext(ctx, `
		SELECT id, agent_addr, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       status, expires_at, created_at, updated_at
		FROM gateway_sessions WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	return s, err
}

func (p *PostgresStore) UpdateSession(ctx context.Context, session *Session) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE gateway_sessions SET
			total_spent = $1::NUMERIC(20,6), request_count = $2,
			status = $3, updated_at = $4
		WHERE id = $5`,
		session.TotalSpent, session.RequestCount,
		string(session.Status), session.UpdatedAt,
		session.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrSessionNotFound
	}
	return nil
}

func (p *PostgresStore) ListSessions(ctx context.Context, agentAddr string, limit int) ([]*Session, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       status, expires_at, created_at, updated_at
		FROM gateway_sessions
		WHERE agent_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSessions(rows)
}

func (p *PostgresStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*Session, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       status, expires_at, created_at, updated_at
		FROM gateway_sessions
		WHERE status = 'active' AND expires_at < $1
		ORDER BY expires_at ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSessions(rows)
}

func (p *PostgresStore) CreateLog(ctx context.Context, log *RequestLog) error {
	var policyJSON []byte
	if log.PolicyResult != nil {
		var err error
		policyJSON, err = json.Marshal(log.PolicyResult)
		if err != nil {
			return err
		}
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO gateway_request_logs (
			id, session_id, service_type, agent_called, amount,
			status, latency_ms, error, policy_result, created_at
		) VALUES (
			$1, $2, $3, $4, $5::NUMERIC(20,6),
			$6, $7, $8, $9, $10
		)`,
		log.ID, log.SessionID, log.ServiceType, log.AgentCalled, log.Amount,
		log.Status, log.LatencyMs, log.Error, policyJSON, log.CreatedAt,
	)
	return err
}

func (p *PostgresStore) ListLogs(ctx context.Context, sessionID string, limit int) ([]*RequestLog, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, session_id, service_type, agent_called, amount,
		       status, latency_ms, error, policy_result, created_at
		FROM gateway_request_logs
		WHERE session_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*RequestLog
	for rows.Next() {
		l, err := scanLog(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, l)
	}
	return result, rows.Err()
}

// --- scanners ---

type sessionScanner interface {
	Scan(dest ...interface{}) error
}

func scanSession(sc sessionScanner) (*Session, error) {
	s := &Session{}
	var (
		allowedTypes pq.StringArray
		status       string
	)

	err := sc.Scan(
		&s.ID, &s.AgentAddr, &s.MaxTotal, &s.MaxPerRequest, &s.TotalSpent,
		&s.RequestCount, &s.Strategy, &allowedTypes, &s.WarnAtPercent,
		&status, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	s.Status = Status(status)
	s.AllowedTypes = []string(allowedTypes)
	if len(s.AllowedTypes) == 0 {
		s.AllowedTypes = nil
	}
	return s, nil
}

func scanSessions(rows *sql.Rows) ([]*Session, error) {
	var result []*Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func scanLog(sc sessionScanner) (*RequestLog, error) {
	l := &RequestLog{}
	var policyJSON []byte

	err := sc.Scan(
		&l.ID, &l.SessionID, &l.ServiceType, &l.AgentCalled, &l.Amount,
		&l.Status, &l.LatencyMs, &l.Error, &policyJSON, &l.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if len(policyJSON) > 0 {
		l.PolicyResult = &PolicyDecision{}
		if err := json.Unmarshal(policyJSON, l.PolicyResult); err != nil {
			l.PolicyResult = nil
		}
	}
	return l, nil
}

// Compile-time assertion.
var _ Store = (*PostgresStore)(nil)
