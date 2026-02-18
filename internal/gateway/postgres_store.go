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
			id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
			request_count, strategy, allowed_types, warn_at_percent,
			max_requests_per_minute, status, expires_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4::NUMERIC(20,6), $5::NUMERIC(20,6), $6::NUMERIC(20,6),
			$7, $8, $9, $10,
			$11, $12, $13, $14, $15
		)`,
		session.ID, session.AgentAddr, nullString(session.TenantID),
		session.MaxTotal, session.MaxPerRequest, session.TotalSpent,
		session.RequestCount, session.Strategy, pq.Array(session.AllowedTypes), session.WarnAtPercent,
		session.MaxRequestsPerMinute, string(session.Status), session.ExpiresAt, session.CreatedAt, session.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetSession(ctx context.Context, id string) (*Session, error) {
	s, err := scanSession(p.db.QueryRowContext(ctx, `
		SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       max_requests_per_minute, status, expires_at, created_at, updated_at
		FROM gateway_sessions WHERE id = $1`, id))
	if err == sql.ErrNoRows {
		return nil, ErrSessionNotFound
	}
	return s, err
}

func (p *PostgresStore) UpdateSession(ctx context.Context, session *Session) error {
	// Check existence first so a missing row isn't masked by a cast error
	// (e.g. empty TotalSpent → invalid NUMERIC).
	var exists bool
	if err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM gateway_sessions WHERE id = $1)`,
		session.ID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrSessionNotFound
	}

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

func (p *PostgresStore) ListSessions(ctx context.Context, agentAddr string, limit int, opts ...ListOption) ([]*Session, error) {
	o := applyListOpts(opts)
	if o.cursor != nil {
		rows, err := p.db.QueryContext(ctx, `
			SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
			       request_count, strategy, allowed_types, warn_at_percent,
			       max_requests_per_minute, status, expires_at, created_at, updated_at
			FROM gateway_sessions
			WHERE agent_addr = $1
			  AND (created_at, id) < ($3, $4)
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, agentAddr, limit, o.cursor.CreatedAt, o.cursor.ID)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		return scanSessions(rows)
	}

	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       max_requests_per_minute, status, expires_at, created_at, updated_at
		FROM gateway_sessions
		WHERE agent_addr = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, agentAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSessions(rows)
}

func (p *PostgresStore) ListSessionsByTenant(ctx context.Context, tenantID string, limit int, opts ...ListOption) ([]*Session, error) {
	o := applyListOpts(opts)
	if o.cursor != nil {
		rows, err := p.db.QueryContext(ctx, `
			SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
			       request_count, strategy, allowed_types, warn_at_percent,
			       max_requests_per_minute, status, expires_at, created_at, updated_at
			FROM gateway_sessions
			WHERE tenant_id = $1
			  AND (created_at, id) < ($3, $4)
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, tenantID, limit, o.cursor.CreatedAt, o.cursor.ID)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		return scanSessions(rows)
	}

	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       max_requests_per_minute, status, expires_at, created_at, updated_at
		FROM gateway_sessions
		WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSessions(rows)
}

func (p *PostgresStore) ListByStatus(ctx context.Context, status Status, limit int) ([]*Session, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       max_requests_per_minute, status, expires_at, created_at, updated_at
		FROM gateway_sessions
		WHERE status = $1
		ORDER BY updated_at DESC
		LIMIT $2`, string(status), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSessions(rows)
}

func (p *PostgresStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*Session, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, tenant_id, max_total, max_per_request, total_spent,
		       request_count, strategy, allowed_types, warn_at_percent,
		       max_requests_per_minute, status, expires_at, created_at, updated_at
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
			id, session_id, tenant_id, service_type, agent_called, amount,
			fee_amount, status, latency_ms, error, policy_result, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6::NUMERIC(20,6),
			$7::NUMERIC(20,6), $8, $9, $10, $11, $12
		)`,
		log.ID, log.SessionID, nullString(log.TenantID), log.ServiceType, log.AgentCalled, log.Amount,
		feeOrZero(log.FeeAmount), log.Status, log.LatencyMs, log.Error, policyJSON, log.CreatedAt,
	)
	return err
}

func (p *PostgresStore) ListLogs(ctx context.Context, sessionID string, limit int, opts ...ListOption) ([]*RequestLog, error) {
	o := applyListOpts(opts)
	var query string
	var args []interface{}

	if o.cursor != nil {
		query = `SELECT id, session_id, tenant_id, service_type, agent_called, amount,
		       fee_amount, status, latency_ms, error, policy_result, created_at
		FROM gateway_request_logs
		WHERE session_id = $1
		  AND (created_at, id) < ($3, $4)
		ORDER BY created_at DESC, id DESC
		LIMIT $2`
		args = []interface{}{sessionID, limit, o.cursor.CreatedAt, o.cursor.ID}
	} else {
		query = `SELECT id, session_id, tenant_id, service_type, agent_called, amount,
		       fee_amount, status, latency_ms, error, policy_result, created_at
		FROM gateway_request_logs
		WHERE session_id = $1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`
		args = []interface{}{sessionID, limit}
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
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

func (p *PostgresStore) GetBillingSummary(ctx context.Context, tenantID string) (*BillingSummaryRow, error) {
	row := &BillingSummaryRow{}
	err := p.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'success'),
			COALESCE(SUM(amount) FILTER (WHERE status = 'success'), 0),
			COALESCE(SUM(fee_amount) FILTER (WHERE status = 'success'), 0)
		FROM gateway_request_logs
		WHERE tenant_id = $1`, tenantID,
	).Scan(&row.TotalRequests, &row.SettledRequests, &row.SettledVolume, &row.FeesCollected)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (p *PostgresStore) GetBillingTimeSeries(ctx context.Context, tenantID, interval string, from, to time.Time) ([]BillingTimePoint, error) {
	// Pre-built queries keyed by interval — no SQL string concatenation.
	queries := map[string]string{
		"hour": `SELECT date_trunc('hour', created_at) AS bucket, COUNT(*), COUNT(*) FILTER (WHERE status = 'success'), COALESCE(SUM(amount) FILTER (WHERE status = 'success'), 0), COALESCE(SUM(fee_amount) FILTER (WHERE status = 'success'), 0) FROM gateway_request_logs WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3 GROUP BY bucket ORDER BY bucket ASC`,
		"day":  `SELECT date_trunc('day', created_at) AS bucket, COUNT(*), COUNT(*) FILTER (WHERE status = 'success'), COALESCE(SUM(amount) FILTER (WHERE status = 'success'), 0), COALESCE(SUM(fee_amount) FILTER (WHERE status = 'success'), 0) FROM gateway_request_logs WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3 GROUP BY bucket ORDER BY bucket ASC`,
		"week": `SELECT date_trunc('week', created_at) AS bucket, COUNT(*), COUNT(*) FILTER (WHERE status = 'success'), COALESCE(SUM(amount) FILTER (WHERE status = 'success'), 0), COALESCE(SUM(fee_amount) FILTER (WHERE status = 'success'), 0) FROM gateway_request_logs WHERE tenant_id = $1 AND created_at >= $2 AND created_at < $3 GROUP BY bucket ORDER BY bucket ASC`,
	}
	query, ok := queries[interval]
	if !ok {
		query = queries["day"]
	}

	rows, err := p.db.QueryContext(ctx, query, tenantID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []BillingTimePoint
	for rows.Next() {
		var pt BillingTimePoint
		if err := rows.Scan(&pt.Bucket, &pt.Requests, &pt.SettledRequests, &pt.Volume, &pt.Fees); err != nil {
			return nil, err
		}
		result = append(result, pt)
	}
	return result, rows.Err()
}

func (p *PostgresStore) GetTopServiceTypes(ctx context.Context, tenantID string, limit int) ([]ServiceTypeUsage, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT service_type,
			COUNT(*) FILTER (WHERE status = 'success'),
			COALESCE(SUM(amount) FILTER (WHERE status = 'success'), 0)
		FROM gateway_request_logs
		WHERE tenant_id = $1 AND service_type != ''
		GROUP BY service_type
		ORDER BY 2 DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []ServiceTypeUsage
	for rows.Next() {
		var u ServiceTypeUsage
		if err := rows.Scan(&u.ServiceType, &u.Requests, &u.Volume); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}

func (p *PostgresStore) GetPolicyDenials(ctx context.Context, tenantID string, limit int) ([]*RequestLog, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, session_id, tenant_id, service_type, agent_called, amount,
		       fee_amount, status, latency_ms, error, policy_result, created_at
		FROM gateway_request_logs
		WHERE tenant_id = $1 AND status = 'policy_denied'
		ORDER BY created_at DESC
		LIMIT $2`, tenantID, limit)
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
		tenantID     sql.NullString
		allowedTypes pq.StringArray
		status       string
	)

	err := sc.Scan(
		&s.ID, &s.AgentAddr, &tenantID, &s.MaxTotal, &s.MaxPerRequest, &s.TotalSpent,
		&s.RequestCount, &s.Strategy, &allowedTypes, &s.WarnAtPercent,
		&s.MaxRequestsPerMinute, &status, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	if tenantID.Valid {
		s.TenantID = tenantID.String
	}
	s.Status = Status(status)
	s.AllowedTypes = []string(allowedTypes)
	if len(s.AllowedTypes) == 0 {
		s.AllowedTypes = nil
	}
	s.BuildAllowedTypesSet()
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
	var (
		tenantID   sql.NullString
		feeAmount  sql.NullString
		policyJSON []byte
	)

	err := sc.Scan(
		&l.ID, &l.SessionID, &tenantID, &l.ServiceType, &l.AgentCalled, &l.Amount,
		&feeAmount, &l.Status, &l.LatencyMs, &l.Error, &policyJSON, &l.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if tenantID.Valid {
		l.TenantID = tenantID.String
	}
	if feeAmount.Valid {
		l.FeeAmount = feeAmount.String
	}
	if len(policyJSON) > 0 {
		l.PolicyResult = &PolicyDecision{}
		if err := json.Unmarshal(policyJSON, l.PolicyResult); err != nil {
			l.PolicyResult = nil
		}
	}
	return l, nil
}

// nullString converts an empty string to sql.NullString{Valid: false}.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// feeOrZero returns "0" for empty fee amounts to satisfy NUMERIC casting.
func feeOrZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// Compile-time assertion.
var _ Store = (*PostgresStore)(nil)
