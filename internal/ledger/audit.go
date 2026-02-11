package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

type contextKey string

const (
	ctxActorType contextKey = "audit_actor_type"
	ctxActorID   contextKey = "audit_actor_id"
	ctxIPAddress contextKey = "audit_ip"
	ctxRequestID contextKey = "audit_request_id"
)

// WithActor attaches actor info to the context for audit logging.
func WithActor(ctx context.Context, actorType, actorID string) context.Context {
	ctx = context.WithValue(ctx, ctxActorType, actorType)
	ctx = context.WithValue(ctx, ctxActorID, actorID)
	return ctx
}

// WithAuditIP attaches the client IP for audit logging.
func WithAuditIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxIPAddress, ip)
}

// WithAuditRequestID attaches a request ID for audit correlation.
func WithAuditRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, ctxRequestID, requestID)
}

func actorFromCtx(ctx context.Context) (actorType, actorID, ip, requestID string) {
	if v, ok := ctx.Value(ctxActorType).(string); ok {
		actorType = v
	} else {
		actorType = "system"
	}
	if v, ok := ctx.Value(ctxActorID).(string); ok {
		actorID = v
	}
	if v, ok := ctx.Value(ctxIPAddress).(string); ok {
		ip = v
	}
	if v, ok := ctx.Value(ctxRequestID).(string); ok {
		requestID = v
	}
	return
}

// AuditEntry represents a single audit log record.
type AuditEntry struct {
	ID          int64     `json:"id"`
	AgentAddr   string    `json:"agentAddr"`
	ActorType   string    `json:"actorType"`
	ActorID     string    `json:"actorId,omitempty"`
	Operation   string    `json:"operation"`
	Amount      string    `json:"amount,omitempty"`
	Reference   string    `json:"reference,omitempty"`
	BeforeState string    `json:"beforeState,omitempty"`
	AfterState  string    `json:"afterState,omitempty"`
	RequestID   string    `json:"requestId,omitempty"`
	IPAddress   string    `json:"ipAddress,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AuditLogger persists audit entries.
type AuditLogger interface {
	LogAudit(ctx context.Context, entry *AuditEntry) error
	QueryAudit(ctx context.Context, agentAddr string, from, to time.Time, operation string, limit int) ([]*AuditEntry, error)
}

// balanceSnapshot returns a JSON string representing the balance state.
func balanceSnapshot(bal *Balance) string {
	if bal == nil {
		return "{}"
	}
	m := map[string]string{
		"available": bal.Available,
		"pending":   bal.Pending,
		"escrowed":  bal.Escrowed,
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// --- PostgresAuditLogger ---

// PostgresAuditLogger writes audit entries to PostgreSQL.
type PostgresAuditLogger struct {
	db *sql.DB
}

// NewPostgresAuditLogger creates an audit logger backed by PostgreSQL.
func NewPostgresAuditLogger(db *sql.DB) *PostgresAuditLogger {
	return &PostgresAuditLogger{db: db}
}

func (l *PostgresAuditLogger) LogAudit(ctx context.Context, entry *AuditEntry) error {
	_, err := l.db.ExecContext(ctx, `
		INSERT INTO audit_log (agent_addr, actor_type, actor_id, operation, amount, reference, before_state, after_state, request_id, ip_address, description, created_at)
		VALUES ($1, $2, $3, $4, $5::NUMERIC(20,6), $6, $7::JSONB, $8::JSONB, $9, $10, $11, NOW())
	`, entry.AgentAddr, entry.ActorType, entry.ActorID, entry.Operation, entry.Amount, entry.Reference,
		entry.BeforeState, entry.AfterState, entry.RequestID, entry.IPAddress, entry.Description)
	return err
}

func (l *PostgresAuditLogger) QueryAudit(ctx context.Context, agentAddr string, from, to time.Time, operation string, limit int) ([]*AuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}

	var query string
	var args []interface{}

	if operation != "" {
		query = `SELECT id, agent_addr, actor_type, COALESCE(actor_id, ''), operation,
			COALESCE(amount, 0), COALESCE(reference, ''),
			COALESCE(before_state::TEXT, '{}'), COALESCE(after_state::TEXT, '{}'),
			COALESCE(request_id, ''), COALESCE(ip_address, ''), COALESCE(description, ''), created_at
			FROM audit_log WHERE agent_addr = $1 AND created_at >= $2 AND created_at <= $3 AND operation = $4
			ORDER BY created_at DESC LIMIT $5`
		args = []interface{}{agentAddr, from, to, operation, limit}
	} else {
		query = `SELECT id, agent_addr, actor_type, COALESCE(actor_id, ''), operation,
			COALESCE(amount, 0), COALESCE(reference, ''),
			COALESCE(before_state::TEXT, '{}'), COALESCE(after_state::TEXT, '{}'),
			COALESCE(request_id, ''), COALESCE(ip_address, ''), COALESCE(description, ''), created_at
			FROM audit_log WHERE agent_addr = $1 AND created_at >= $2 AND created_at <= $3
			ORDER BY created_at DESC LIMIT $4`
		args = []interface{}{agentAddr, from, to, limit}
	}

	rows, err := l.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanAuditRows(rows)
}

// --- MemoryAuditLogger ---

// MemoryAuditLogger stores audit entries in memory for demo/testing.
type MemoryAuditLogger struct {
	entries []*AuditEntry
	nextID  int64
	mu      sync.RWMutex
}

// NewMemoryAuditLogger creates an in-memory audit logger.
func NewMemoryAuditLogger() *MemoryAuditLogger {
	return &MemoryAuditLogger{}
}

func (l *MemoryAuditLogger) LogAudit(_ context.Context, entry *AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.nextID++
	cp := *entry
	cp.ID = l.nextID
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}
	l.entries = append(l.entries, &cp)
	return nil
}

func (l *MemoryAuditLogger) QueryAudit(_ context.Context, agentAddr string, from, to time.Time, operation string, limit int) ([]*AuditEntry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if limit <= 0 {
		limit = 100
	}

	var result []*AuditEntry
	// Iterate in reverse for descending order
	for i := len(l.entries) - 1; i >= 0 && len(result) < limit; i-- {
		e := l.entries[i]
		if e.AgentAddr != agentAddr {
			continue
		}
		if !from.IsZero() && e.CreatedAt.Before(from) {
			continue
		}
		if !to.IsZero() && e.CreatedAt.After(to) {
			continue
		}
		if operation != "" && e.Operation != operation {
			continue
		}
		cp := *e
		result = append(result, &cp)
	}
	return result, nil
}

// Entries returns all stored audit entries (for testing).
func (l *MemoryAuditLogger) Entries() []*AuditEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make([]*AuditEntry, len(l.entries))
	copy(result, l.entries)
	return result
}

func scanAuditRows(rows *sql.Rows) ([]*AuditEntry, error) {
	var entries []*AuditEntry
	for rows.Next() {
		e := &AuditEntry{}
		if err := rows.Scan(&e.ID, &e.AgentAddr, &e.ActorType, &e.ActorID, &e.Operation,
			&e.Amount, &e.Reference, &e.BeforeState, &e.AfterState,
			&e.RequestID, &e.IPAddress, &e.Description, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
