package auth

import (
	"context"
	"database/sql"
)

// PostgresStore persists API keys in PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed auth store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Create stores a new API key
func (p *PostgresStore) Create(ctx context.Context, key *APIKey) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, hash, agent_address, name, created_at, expires_at, revoked)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, key.ID, key.Hash, key.AgentAddr, key.Name, key.CreatedAt, key.ExpiresAt, key.Revoked)
	return err
}

// GetByHash retrieves an API key by its hash
func (p *PostgresStore) GetByHash(ctx context.Context, hash string) (*APIKey, error) {
	key := &APIKey{}
	var expiresAt sql.NullTime
	var lastUsed sql.NullTime

	err := p.db.QueryRowContext(ctx, `
		SELECT id, hash, agent_address, name, created_at, last_used, expires_at, revoked
		FROM api_keys WHERE hash = $1
		  AND revoked = FALSE
		  AND (expires_at IS NULL OR expires_at > NOW())
	`, hash).Scan(
		&key.ID, &key.Hash, &key.AgentAddr, &key.Name,
		&key.CreatedAt, &lastUsed, &expiresAt, &key.Revoked,
	)
	if err == sql.ErrNoRows {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, err
	}

	if expiresAt.Valid {
		key.ExpiresAt = &expiresAt.Time
	}
	if lastUsed.Valid {
		key.LastUsed = lastUsed.Time
	}
	return key, nil
}

// GetByAgent retrieves all API keys for an agent
func (p *PostgresStore) GetByAgent(ctx context.Context, addr string) ([]*APIKey, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, hash, agent_address, name, created_at, last_used, expires_at, revoked
		FROM api_keys WHERE agent_address = $1 ORDER BY created_at DESC
	`, addr)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var keys []*APIKey
	for rows.Next() {
		key := &APIKey{}
		var expiresAt sql.NullTime
		var lastUsed sql.NullTime

		if err := rows.Scan(
			&key.ID, &key.Hash, &key.AgentAddr, &key.Name,
			&key.CreatedAt, &lastUsed, &expiresAt, &key.Revoked,
		); err != nil {
			return nil, err
		}

		if expiresAt.Valid {
			key.ExpiresAt = &expiresAt.Time
		}
		if lastUsed.Valid {
			key.LastUsed = lastUsed.Time
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

// Update updates an API key
func (p *PostgresStore) Update(ctx context.Context, key *APIKey) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE api_keys SET last_used = $1, revoked = $2 WHERE id = $3
	`, key.LastUsed, key.Revoked, key.ID)
	return err
}

// Delete removes an API key
func (p *PostgresStore) Delete(ctx context.Context, id string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM api_keys WHERE id = $1`, id)
	return err
}

// Migrate creates the api_keys table if it doesn't exist
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS api_keys (
			id              VARCHAR(36) PRIMARY KEY,
			hash            VARCHAR(64) NOT NULL UNIQUE,
			agent_address   VARCHAR(42) NOT NULL,
			name            VARCHAR(255),
			created_at      TIMESTAMPTZ DEFAULT NOW(),
			last_used       TIMESTAMPTZ,
			expires_at      TIMESTAMPTZ,
			revoked         BOOLEAN DEFAULT FALSE
		);
		CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(hash);
		CREATE INDEX IF NOT EXISTS idx_api_keys_agent ON api_keys(agent_address);
	`)
	return err
}
