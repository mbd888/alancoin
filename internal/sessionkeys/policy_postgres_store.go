package sessionkeys

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// PolicyPostgresStore implements PolicyStore using PostgreSQL.
type PolicyPostgresStore struct {
	db *sql.DB
}

// NewPolicyPostgresStore creates a PostgreSQL-backed policy store.
func NewPolicyPostgresStore(db *sql.DB) *PolicyPostgresStore {
	return &PolicyPostgresStore{db: db}
}

func (s *PolicyPostgresStore) CreatePolicy(ctx context.Context, policy *Policy) error {
	rulesJSON, err := json.Marshal(policy.Rules)
	if err != nil {
		return fmt.Errorf("failed to marshal rules: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO policies (id, name, owner_addr, rules, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, policy.ID, policy.Name, strings.ToLower(policy.OwnerAddr), rulesJSON, policy.CreatedAt, policy.UpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to create policy: %w", err)
	}
	return nil
}

func (s *PolicyPostgresStore) GetPolicy(ctx context.Context, id string) (*Policy, error) {
	var p Policy
	var rulesJSON []byte

	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, owner_addr, rules, created_at, updated_at
		FROM policies WHERE id = $1
	`, id).Scan(&p.ID, &p.Name, &p.OwnerAddr, &rulesJSON, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrPolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get policy: %w", err)
	}

	if err := json.Unmarshal(rulesJSON, &p.Rules); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rules: %w", err)
	}
	return &p, nil
}

func (s *PolicyPostgresStore) ListPolicies(ctx context.Context, ownerAddr string) ([]*Policy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, owner_addr, rules, created_at, updated_at
		FROM policies WHERE owner_addr = $1 ORDER BY created_at DESC
	`, strings.ToLower(ownerAddr))
	if err != nil {
		return nil, fmt.Errorf("failed to list policies: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*Policy
	for rows.Next() {
		var p Policy
		var rulesJSON []byte
		if err := rows.Scan(&p.ID, &p.Name, &p.OwnerAddr, &rulesJSON, &p.CreatedAt, &p.UpdatedAt); err != nil {
			continue
		}
		if err := json.Unmarshal(rulesJSON, &p.Rules); err != nil {
			continue
		}
		result = append(result, &p)
	}
	return result, rows.Err()
}

func (s *PolicyPostgresStore) UpdatePolicy(ctx context.Context, policy *Policy) error {
	rulesJSON, err := json.Marshal(policy.Rules)
	if err != nil {
		return fmt.Errorf("failed to marshal rules: %w", err)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE policies SET name = $1, rules = $2, updated_at = $3
		WHERE id = $4
	`, policy.Name, rulesJSON, policy.UpdatedAt, policy.ID)
	if err != nil {
		return fmt.Errorf("failed to update policy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPolicyNotFound
	}
	return nil
}

func (s *PolicyPostgresStore) DeletePolicy(ctx context.Context, id string) error {
	// Cascade: session_key_policies has ON DELETE CASCADE
	res, err := s.db.ExecContext(ctx, `DELETE FROM policies WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete policy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPolicyNotFound
	}
	return nil
}

func (s *PolicyPostgresStore) AttachPolicy(ctx context.Context, att *PolicyAttachment) error {
	stateJSON := att.RuleState
	if stateJSON == nil {
		stateJSON = json.RawMessage(`{}`)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_key_policies (session_key_id, policy_id, attached_at, rule_state)
		VALUES ($1, $2, $3, $4)
	`, att.SessionKeyID, att.PolicyID, att.AttachedAt, stateJSON)
	if err != nil {
		return fmt.Errorf("failed to attach policy: %w", err)
	}
	return nil
}

func (s *PolicyPostgresStore) DetachPolicy(ctx context.Context, sessionKeyID, policyID string) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM session_key_policies
		WHERE session_key_id = $1 AND policy_id = $2
	`, sessionKeyID, policyID)
	if err != nil {
		return fmt.Errorf("failed to detach policy: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPolicyNotFound
	}
	return nil
}

func (s *PolicyPostgresStore) GetAttachments(ctx context.Context, sessionKeyID string) ([]*PolicyAttachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_key_id, policy_id, attached_at, rule_state
		FROM session_key_policies WHERE session_key_id = $1
	`, sessionKeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to get attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*PolicyAttachment
	for rows.Next() {
		var att PolicyAttachment
		var stateJSON []byte
		if err := rows.Scan(&att.SessionKeyID, &att.PolicyID, &att.AttachedAt, &stateJSON); err != nil {
			continue
		}
		att.RuleState = json.RawMessage(stateJSON)
		result = append(result, &att)
	}
	return result, rows.Err()
}

func (s *PolicyPostgresStore) UpdateAttachment(ctx context.Context, att *PolicyAttachment) error {
	stateJSON := att.RuleState
	if stateJSON == nil {
		stateJSON = json.RawMessage(`{}`)
	}

	res, err := s.db.ExecContext(ctx, `
		UPDATE session_key_policies SET rule_state = $1
		WHERE session_key_id = $2 AND policy_id = $3
	`, stateJSON, att.SessionKeyID, att.PolicyID)
	if err != nil {
		return fmt.Errorf("failed to update attachment: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPolicyNotFound
	}

	return nil
}

// Migrate creates the policy tables if the migration hasn't been applied.
func (s *PolicyPostgresStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS policies (
			id           VARCHAR(36) PRIMARY KEY,
			name         VARCHAR(255) NOT NULL,
			owner_addr   VARCHAR(42) NOT NULL,
			rules        JSONB NOT NULL DEFAULT '[]',
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_policies_owner ON policies (owner_addr);

		CREATE TABLE IF NOT EXISTS session_key_policies (
			session_key_id VARCHAR(36) NOT NULL REFERENCES session_keys(id) ON DELETE CASCADE,
			policy_id      VARCHAR(36) NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
			attached_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			rule_state     JSONB NOT NULL DEFAULT '{}',
			PRIMARY KEY (session_key_id, policy_id)
		);
	`)
	if err != nil {
		return fmt.Errorf("policy migration failed: %w", err)
	}
	return nil
}
