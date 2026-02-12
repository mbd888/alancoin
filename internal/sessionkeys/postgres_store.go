package sessionkeys

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// PostgresStore implements Store using PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed session key store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, key *SessionKey) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO session_keys (
			id, owner_address, public_key,
			max_per_transaction, max_per_day, max_total,
			valid_after, expires_at,
			allowed_recipients, allowed_service_types, allow_any, label,
			transaction_count, total_spent, spent_today, last_used, last_reset_day, last_nonce,
			revoked_at, created_at,
			parent_key_id, depth, root_key_id, delegation_label,
			rotated_from_id, rotated_to_id, rotation_grace_until,
			scopes
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24, $25, $26, $27, $28)
	`,
		key.ID,
		strings.ToLower(key.OwnerAddr),
		key.PublicKey,
		nullString(key.Permission.MaxPerTransaction),
		nullString(key.Permission.MaxPerDay),
		nullString(key.Permission.MaxTotal),
		nullTime(key.Permission.ValidAfter),
		key.Permission.ExpiresAt,
		pq.Array(key.Permission.AllowedRecipients),
		pq.Array(key.Permission.AllowedServiceTypes),
		key.Permission.AllowAny,
		key.Permission.Label,
		key.Usage.TransactionCount,
		key.Usage.TotalSpent,
		key.Usage.SpentToday,
		nullTime(key.Usage.LastUsed),
		key.Usage.LastResetDay,
		key.Usage.LastNonce,
		nullTime(timePtr(key.RevokedAt)),
		key.CreatedAt,
		nullString(key.ParentKeyID),
		key.Depth,
		nullString(key.RootKeyID),
		nullString(key.DelegationLabel),
		nullString(key.RotatedFromID),
		nullString(key.RotatedToID),
		nullTime(timePtr(key.RotationGraceEnd)),
		pq.Array(key.Permission.Scopes),
	)

	if err != nil {
		return fmt.Errorf("failed to create session key: %w", err)
	}
	return nil
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*SessionKey, error) {
	var key SessionKey
	var validAfter, lastUsed, revokedAt, rotationGraceUntil sql.NullTime
	var maxPerTx, maxPerDay, maxTotal, label sql.NullString
	var lastResetDay sql.NullString
	var parentKeyID, rootKeyID, delegationLabel sql.NullString
	var rotatedFromID, rotatedToID sql.NullString

	err := p.db.QueryRowContext(ctx, `
		SELECT
			id, owner_address, public_key,
			max_per_transaction, max_per_day, max_total,
			valid_after, expires_at,
			allowed_recipients, allowed_service_types, allow_any, label,
			transaction_count, total_spent, spent_today, last_used, last_reset_day, COALESCE(last_nonce, 0),
			revoked_at, created_at,
			parent_key_id, COALESCE(depth, 0), root_key_id, delegation_label,
			rotated_from_id, rotated_to_id, rotation_grace_until,
			COALESCE(scopes, ARRAY['spend', 'read'])
		FROM session_keys WHERE id = $1
		AND revoked_at IS NULL AND expires_at > NOW()
	`, id).Scan(
		&key.ID,
		&key.OwnerAddr,
		&key.PublicKey,
		&maxPerTx,
		&maxPerDay,
		&maxTotal,
		&validAfter,
		&key.Permission.ExpiresAt,
		pq.Array(&key.Permission.AllowedRecipients),
		pq.Array(&key.Permission.AllowedServiceTypes),
		&key.Permission.AllowAny,
		&label,
		&key.Usage.TransactionCount,
		&key.Usage.TotalSpent,
		&key.Usage.SpentToday,
		&lastUsed,
		&lastResetDay,
		&key.Usage.LastNonce,
		&revokedAt,
		&key.CreatedAt,
		&parentKeyID,
		&key.Depth,
		&rootKeyID,
		&delegationLabel,
		&rotatedFromID,
		&rotatedToID,
		&rotationGraceUntil,
		pq.Array(&key.Permission.Scopes),
	)

	if err == sql.ErrNoRows {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session key: %w", err)
	}

	// Map nullable fields
	key.Permission.MaxPerTransaction = maxPerTx.String
	key.Permission.MaxPerDay = maxPerDay.String
	key.Permission.MaxTotal = maxTotal.String
	key.Permission.Label = label.String
	key.ParentKeyID = parentKeyID.String
	key.RootKeyID = rootKeyID.String
	key.DelegationLabel = delegationLabel.String
	key.RotatedFromID = rotatedFromID.String
	key.RotatedToID = rotatedToID.String
	if validAfter.Valid {
		key.Permission.ValidAfter = validAfter.Time
	}
	if lastUsed.Valid {
		key.Usage.LastUsed = lastUsed.Time
	}
	if lastResetDay.Valid {
		key.Usage.LastResetDay = lastResetDay.String
	}
	if revokedAt.Valid {
		key.RevokedAt = &revokedAt.Time
	}
	if rotationGraceUntil.Valid {
		key.RotationGraceEnd = &rotationGraceUntil.Time
	}

	return &key, nil
}

func (p *PostgresStore) GetByOwner(ctx context.Context, ownerAddr string) ([]*SessionKey, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id FROM session_keys WHERE owner_address = $1 ORDER BY created_at DESC
	`, strings.ToLower(ownerAddr))
	if err != nil {
		return nil, fmt.Errorf("failed to list session keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []*SessionKey
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		key, err := p.Get(ctx, id)
		if err == nil {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

func (p *PostgresStore) GetByParent(ctx context.Context, parentKeyID string) ([]*SessionKey, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id FROM session_keys WHERE parent_key_id = $1 ORDER BY created_at DESC
	`, parentKeyID)
	if err != nil {
		return nil, fmt.Errorf("failed to list child keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []*SessionKey
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		key, err := p.Get(ctx, id)
		if err == nil {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

func (p *PostgresStore) Update(ctx context.Context, key *SessionKey) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE session_keys SET
			transaction_count = $1,
			total_spent = $2,
			spent_today = $3,
			last_used = $4,
			last_reset_day = $5,
			last_nonce = $6,
			revoked_at = $7,
			rotated_from_id = $8,
			rotated_to_id = $9,
			rotation_grace_until = $10
		WHERE id = $11
	`,
		key.Usage.TransactionCount,
		key.Usage.TotalSpent,
		key.Usage.SpentToday,
		nullTime(key.Usage.LastUsed),
		key.Usage.LastResetDay,
		key.Usage.LastNonce,
		nullTime(timePtr(key.RevokedAt)),
		nullString(key.RotatedFromID),
		nullString(key.RotatedToID),
		nullTime(timePtr(key.RotationGraceEnd)),
		key.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update session key: %w", err)
	}
	return nil
}

// ReParentChildren atomically moves all children from oldParentID to newParentID.
func (p *PostgresStore) ReParentChildren(ctx context.Context, oldParentID, newParentID string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE session_keys SET parent_key_id = $1 WHERE parent_key_id = $2
	`, newParentID, oldParentID)
	if err != nil {
		return fmt.Errorf("failed to re-parent children: %w", err)
	}
	return nil
}

func (p *PostgresStore) Delete(ctx context.Context, id string) error {
	result, err := p.db.ExecContext(ctx, `DELETE FROM session_keys WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("failed to delete session key: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrKeyNotFound
	}
	return nil
}

// CountActive returns the number of active session keys (non-revoked, non-expired)
func (p *PostgresStore) CountActive(ctx context.Context) (int64, error) {
	var count int64
	err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM session_keys
		WHERE revoked_at IS NULL AND expires_at > NOW()
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count active keys: %w", err)
	}
	return count, nil
}

// Helpers

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func timePtr(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
