package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// PostgresStore persists spend policies in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed policy store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, sp *SpendPolicy) error {
	rulesJSON, err := json.Marshal(sp.Rules)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO spend_policies (id, tenant_id, name, rules, priority, enabled, enforcement_mode, shadow_expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		sp.ID, sp.TenantID, sp.Name, rulesJSON, sp.Priority, sp.Enabled,
		enforcementModeOrDefault(sp.EnforcementMode), nullTime(sp.ShadowExpiresAt),
		sp.CreatedAt, sp.UpdatedAt,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrNameTaken
		}
		return err
	}
	return nil
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*SpendPolicy, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, rules, priority, enabled, enforcement_mode, shadow_expires_at, created_at, updated_at
		FROM spend_policies WHERE id = $1`, id)
	return scanPolicy(row)
}

func (p *PostgresStore) List(ctx context.Context, tenantID string) ([]*SpendPolicy, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, rules, priority, enabled, enforcement_mode, shadow_expires_at, created_at, updated_at
		FROM spend_policies WHERE tenant_id = $1
		ORDER BY priority ASC, created_at ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*SpendPolicy
	for rows.Next() {
		sp := &SpendPolicy{}
		var rulesJSON []byte
		var enfMode string
		var shadowExp sql.NullTime
		if err := rows.Scan(&sp.ID, &sp.TenantID, &sp.Name, &rulesJSON,
			&sp.Priority, &sp.Enabled, &enfMode, &shadowExp, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return nil, err
		}
		if err := unmarshalRules(rulesJSON, &sp.Rules); err != nil {
			return nil, fmt.Errorf("corrupt rules for policy %s: %w", sp.ID, err)
		}
		sp.EnforcementMode = enfMode
		if shadowExp.Valid {
			sp.ShadowExpiresAt = shadowExp.Time
		}
		result = append(result, sp)
	}
	return result, rows.Err()
}

func (p *PostgresStore) Update(ctx context.Context, sp *SpendPolicy) error {
	rulesJSON, err := json.Marshal(sp.Rules)
	if err != nil {
		return err
	}
	result, err := p.db.ExecContext(ctx, `
		UPDATE spend_policies
		SET name = $1, rules = $2, priority = $3, enabled = $4, enforcement_mode = $5, shadow_expires_at = $6, updated_at = $7
		WHERE id = $8 AND tenant_id = $9`,
		sp.Name, rulesJSON, sp.Priority, sp.Enabled,
		enforcementModeOrDefault(sp.EnforcementMode), nullTime(sp.ShadowExpiresAt),
		sp.UpdatedAt, sp.ID, sp.TenantID,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrNameTaken
		}
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrPolicyNotFound
	}
	return nil
}

func (p *PostgresStore) Delete(ctx context.Context, id string) error {
	result, err := p.db.ExecContext(ctx, `DELETE FROM spend_policies WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrPolicyNotFound
	}
	return nil
}

// Migrate creates the spend_policies table if it doesn't exist.
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS spend_policies (
			id                TEXT PRIMARY KEY,
			tenant_id         TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
			name              TEXT NOT NULL,
			rules             JSONB NOT NULL DEFAULT '[]',
			priority          INTEGER NOT NULL DEFAULT 0,
			enabled           BOOLEAN NOT NULL DEFAULT true,
			enforcement_mode  TEXT NOT NULL DEFAULT 'enforce',
			shadow_expires_at TIMESTAMPTZ,
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_spend_policies_tenant ON spend_policies(tenant_id);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_spend_policies_tenant_name ON spend_policies(tenant_id, name);
	`)
	return err
}

func scanPolicy(row *sql.Row) (*SpendPolicy, error) {
	sp := &SpendPolicy{}
	var rulesJSON []byte
	var enfMode string
	var shadowExp sql.NullTime
	err := row.Scan(&sp.ID, &sp.TenantID, &sp.Name, &rulesJSON,
		&sp.Priority, &sp.Enabled, &enfMode, &shadowExp, &sp.CreatedAt, &sp.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrPolicyNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := unmarshalRules(rulesJSON, &sp.Rules); err != nil {
		return nil, fmt.Errorf("corrupt rules for policy %s: %w", sp.ID, err)
	}
	sp.EnforcementMode = enfMode
	if shadowExp.Valid {
		sp.ShadowExpiresAt = shadowExp.Time
	}
	return sp, nil
}

// unmarshalRules decodes rules JSONB, returning an error on corruption
// instead of silently returning empty rules (which would fail-open).
func unmarshalRules(data []byte, rules *[]Rule) error {
	if len(data) == 0 {
		*rules = nil
		return nil
	}
	return json.Unmarshal(data, rules)
}

// enforcementModeOrDefault returns "enforce" for empty enforcement modes.
func enforcementModeOrDefault(mode string) string {
	if mode == "" {
		return "enforce"
	}
	return mode
}

// nullTime converts a zero time to sql.NullTime{Valid: false}.
func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

var _ Store = (*PostgresStore)(nil)
