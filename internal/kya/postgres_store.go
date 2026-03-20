package kya

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// PostgresStore persists KYA certificates in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed KYA store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, cert *Certificate) error {
	orgJSON, _ := json.Marshal(cert.Org)
	permsJSON, _ := json.Marshal(cert.Permissions)
	repJSON, _ := json.Marshal(cert.Reputation)

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO kya_certificates (id, agent_addr, did, org, permissions, reputation, status, signature, issued_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, cert.ID, cert.AgentAddr, cert.DID, orgJSON, permsJSON, repJSON,
		cert.Status, cert.Signature, cert.IssuedAt, cert.ExpiresAt)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Certificate, error) {
	var cert Certificate
	var orgJSON, permsJSON, repJSON []byte
	var revokedAt sql.NullTime

	err := p.db.QueryRowContext(ctx, `
		SELECT id, agent_addr, did, org, permissions, reputation, status, signature, issued_at, expires_at, revoked_at
		FROM kya_certificates WHERE id = $1
	`, id).Scan(
		&cert.ID, &cert.AgentAddr, &cert.DID,
		&orgJSON, &permsJSON, &repJSON,
		&cert.Status, &cert.Signature,
		&cert.IssuedAt, &cert.ExpiresAt, &revokedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrCertNotFound
	}
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal(orgJSON, &cert.Org)
	_ = json.Unmarshal(permsJSON, &cert.Permissions)
	_ = json.Unmarshal(repJSON, &cert.Reputation)
	if revokedAt.Valid {
		cert.RevokedAt = &revokedAt.Time
	}
	return &cert, nil
}

func (p *PostgresStore) GetByAgent(ctx context.Context, agentAddr string) (*Certificate, error) {
	var cert Certificate
	var orgJSON, permsJSON, repJSON []byte
	var revokedAt sql.NullTime

	err := p.db.QueryRowContext(ctx, `
		SELECT id, agent_addr, did, org, permissions, reputation, status, signature, issued_at, expires_at, revoked_at
		FROM kya_certificates WHERE agent_addr = $1 AND status = 'active'
		ORDER BY issued_at DESC LIMIT 1
	`, agentAddr).Scan(
		&cert.ID, &cert.AgentAddr, &cert.DID,
		&orgJSON, &permsJSON, &repJSON,
		&cert.Status, &cert.Signature,
		&cert.IssuedAt, &cert.ExpiresAt, &revokedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrCertNotFound
	}
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal(orgJSON, &cert.Org)
	_ = json.Unmarshal(permsJSON, &cert.Permissions)
	_ = json.Unmarshal(repJSON, &cert.Reputation)
	if revokedAt.Valid {
		cert.RevokedAt = &revokedAt.Time
	}
	return &cert, nil
}

func (p *PostgresStore) Update(ctx context.Context, cert *Certificate) error {
	orgJSON, _ := json.Marshal(cert.Org)
	permsJSON, _ := json.Marshal(cert.Permissions)
	repJSON, _ := json.Marshal(cert.Reputation)

	_, err := p.db.ExecContext(ctx, `
		UPDATE kya_certificates
		SET status = $2, signature = $3, revoked_at = $4, org = $5, permissions = $6, reputation = $7
		WHERE id = $1
	`, cert.ID, cert.Status, cert.Signature, cert.RevokedAt, orgJSON, permsJSON, repJSON)
	return err
}

func (p *PostgresStore) ListByTenant(ctx context.Context, tenantID string, limit int) ([]*Certificate, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, agent_addr, did, org, permissions, reputation, status, signature, issued_at, expires_at, revoked_at
		FROM kya_certificates
		WHERE org->>'tenantId' = $1
		ORDER BY issued_at DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var certs []*Certificate
	for rows.Next() {
		var cert Certificate
		var orgJSON, permsJSON, repJSON []byte
		var revokedAt sql.NullTime

		if err := rows.Scan(
			&cert.ID, &cert.AgentAddr, &cert.DID,
			&orgJSON, &permsJSON, &repJSON,
			&cert.Status, &cert.Signature,
			&cert.IssuedAt, &cert.ExpiresAt, &revokedAt,
		); err != nil {
			return nil, err
		}

		_ = json.Unmarshal(orgJSON, &cert.Org)
		_ = json.Unmarshal(permsJSON, &cert.Permissions)
		_ = json.Unmarshal(repJSON, &cert.Reputation)
		if revokedAt.Valid {
			cert.RevokedAt = &revokedAt.Time
		}
		certs = append(certs, &cert)
	}
	return certs, rows.Err()
}

// CreateTable creates the kya_certificates table (used for testing without migrations).
func (p *PostgresStore) CreateTable(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS kya_certificates (
			id TEXT PRIMARY KEY,
			agent_addr TEXT NOT NULL,
			did TEXT NOT NULL,
			org JSONB NOT NULL,
			permissions JSONB NOT NULL DEFAULT '{}',
			reputation JSONB NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active',
			signature TEXT NOT NULL,
			issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			expires_at TIMESTAMPTZ NOT NULL,
			revoked_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_kya_agent ON kya_certificates(agent_addr, status);
		CREATE INDEX IF NOT EXISTS idx_kya_tenant ON kya_certificates USING GIN (org);
	`)
	return err
}

// Compile-time check that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)

// Suppress unused import warning
var _ = time.Now
