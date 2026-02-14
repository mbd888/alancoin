package tenant

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lib/pq"
)

// PostgresStore persists tenants in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed tenant store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, t *Tenant) error {
	settingsJSON, err := json.Marshal(t.Settings)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO tenants (id, name, slug, plan, stripe_customer_id, status, settings, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		t.ID, t.Name, t.Slug, string(t.Plan), t.StripeCustomerID, string(t.Status),
		settingsJSON, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		if pqErr, ok := err.(*pq.Error); ok && pqErr.Code == "23505" {
			return ErrSlugTaken
		}
		return err
	}
	return nil
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Tenant, error) {
	return p.scanTenant(p.db.QueryRowContext(ctx, `
		SELECT id, name, slug, plan, stripe_customer_id, status, settings, created_at, updated_at
		FROM tenants WHERE id = $1`, id))
}

func (p *PostgresStore) GetBySlug(ctx context.Context, slug string) (*Tenant, error) {
	return p.scanTenant(p.db.QueryRowContext(ctx, `
		SELECT id, name, slug, plan, stripe_customer_id, status, settings, created_at, updated_at
		FROM tenants WHERE slug = $1`, slug))
}

func (p *PostgresStore) Update(ctx context.Context, t *Tenant) error {
	settingsJSON, err := json.Marshal(t.Settings)
	if err != nil {
		return err
	}
	result, err := p.db.ExecContext(ctx, `
		UPDATE tenants SET name = $1, plan = $2, stripe_customer_id = $3, status = $4,
			settings = $5, updated_at = $6
		WHERE id = $7`,
		t.Name, string(t.Plan), t.StripeCustomerID, string(t.Status),
		settingsJSON, t.UpdatedAt, t.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTenantNotFound
	}
	return nil
}

// ListAgents returns agent addresses whose API key belongs to a tenant.
func (p *PostgresStore) ListAgents(ctx context.Context, tenantID string) ([]string, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT DISTINCT agent_address FROM api_keys
		WHERE tenant_id = $1 AND revoked = FALSE
		ORDER BY agent_address`, tenantID)
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

// CountAgents returns how many distinct agents belong to a tenant.
func (p *PostgresStore) CountAgents(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT agent_address) FROM api_keys
		WHERE tenant_id = $1 AND revoked = FALSE`, tenantID).Scan(&count)
	return count, err
}

func (p *PostgresStore) scanTenant(row *sql.Row) (*Tenant, error) {
	t := &Tenant{}
	var (
		plan, status string
		stripeID     sql.NullString
		settingsJSON []byte
	)
	err := row.Scan(&t.ID, &t.Name, &t.Slug, &plan, &stripeID, &status, &settingsJSON,
		&t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrTenantNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Plan = Plan(plan)
	t.Status = Status(status)
	if stripeID.Valid {
		t.StripeCustomerID = stripeID.String
	}
	if len(settingsJSON) > 0 {
		_ = json.Unmarshal(settingsJSON, &t.Settings)
	}
	return t, nil
}

// Migrate creates the tenants table (used in dev/test; prod uses migration files).
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS tenants (
			id              TEXT PRIMARY KEY,
			name            TEXT NOT NULL,
			slug            TEXT NOT NULL UNIQUE,
			plan            TEXT NOT NULL DEFAULT 'free',
			stripe_customer_id TEXT,
			status          TEXT NOT NULL DEFAULT 'active',
			settings        JSONB NOT NULL DEFAULT '{}',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_tenants_slug ON tenants(slug);
		CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);
	`)
	return err
}

var _ Store = (*PostgresStore)(nil)
