package sessionkeys

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/lib/pq"
)

// DelegationAuditLogger records delegation lifecycle events
type DelegationAuditLogger interface {
	LogEvent(ctx context.Context, entry *DelegationLogEntry) error
	GetByKey(ctx context.Context, keyID string, limit int) ([]*DelegationLogEntry, error)
	GetByRoot(ctx context.Context, rootKeyID string, limit int) ([]*DelegationLogEntry, error)
}

// --- Memory Implementation ---

// MemoryAuditLogger is an in-memory DelegationAuditLogger for testing/demo
type MemoryAuditLogger struct {
	mu      sync.RWMutex
	entries []*DelegationLogEntry
	nextID  int
}

func NewMemoryAuditLogger() *MemoryAuditLogger {
	return &MemoryAuditLogger{nextID: 1}
}

func (m *MemoryAuditLogger) LogEvent(_ context.Context, entry *DelegationLogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e := *entry
	e.ID = m.nextID
	m.nextID++
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	m.entries = append(m.entries, &e)
	return nil
}

func (m *MemoryAuditLogger) GetByKey(_ context.Context, keyID string, limit int) ([]*DelegationLogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*DelegationLogEntry
	// Iterate in reverse for newest-first
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.ParentKeyID == keyID || e.ChildKeyID == keyID {
			copy := *e
			result = append(result, &copy)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

func (m *MemoryAuditLogger) GetByRoot(_ context.Context, rootKeyID string, limit int) ([]*DelegationLogEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*DelegationLogEntry
	for i := len(m.entries) - 1; i >= 0; i-- {
		e := m.entries[i]
		if e.RootKeyID == rootKeyID {
			copy := *e
			result = append(result, &copy)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}

// --- Postgres Implementation ---

// PostgresAuditLogger is a PostgreSQL-backed DelegationAuditLogger
type PostgresAuditLogger struct {
	db *sql.DB
}

func NewPostgresAuditLogger(db *sql.DB) *PostgresAuditLogger {
	return &PostgresAuditLogger{db: db}
}

func (p *PostgresAuditLogger) LogEvent(ctx context.Context, entry *DelegationLogEntry) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO delegation_log (
			parent_key_id, child_key_id, root_key_id, root_owner_addr,
			depth, max_total, reason, event_type, ancestor_chain, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		entry.ParentKeyID,
		entry.ChildKeyID,
		entry.RootKeyID,
		entry.RootOwnerAddr,
		entry.Depth,
		nullString(entry.MaxTotal),
		nullString(entry.Reason),
		entry.EventType,
		pq.Array(entry.AncestorChain),
		nullString(entry.Metadata),
	)
	if err != nil {
		return fmt.Errorf("failed to log delegation event: %w", err)
	}
	return nil
}

func (p *PostgresAuditLogger) GetByKey(ctx context.Context, keyID string, limit int) ([]*DelegationLogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, parent_key_id, child_key_id, root_key_id, root_owner_addr,
			   depth, COALESCE(max_total, ''), COALESCE(reason, ''),
			   event_type, COALESCE(ancestor_chain, '{}'), COALESCE(metadata::text, '{}'),
			   created_at
		FROM delegation_log
		WHERE parent_key_id = $1 OR child_key_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, keyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query delegation log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanLogEntries(rows)
}

func (p *PostgresAuditLogger) GetByRoot(ctx context.Context, rootKeyID string, limit int) ([]*DelegationLogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, parent_key_id, child_key_id, root_key_id, root_owner_addr,
			   depth, COALESCE(max_total, ''), COALESCE(reason, ''),
			   event_type, COALESCE(ancestor_chain, '{}'), COALESCE(metadata::text, '{}'),
			   created_at
		FROM delegation_log
		WHERE root_key_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, rootKeyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query delegation log: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return scanLogEntries(rows)
}

func scanLogEntries(rows *sql.Rows) ([]*DelegationLogEntry, error) {
	var entries []*DelegationLogEntry
	for rows.Next() {
		var e DelegationLogEntry
		if err := rows.Scan(
			&e.ID, &e.ParentKeyID, &e.ChildKeyID, &e.RootKeyID, &e.RootOwnerAddr,
			&e.Depth, &e.MaxTotal, &e.Reason,
			&e.EventType, pq.Array(&e.AncestorChain), &e.Metadata,
			&e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan delegation log entry: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}
