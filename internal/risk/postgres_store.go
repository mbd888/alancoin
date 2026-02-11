package risk

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// PostgresStore persists risk assessments in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed risk assessment store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the risk_assessments table if it doesn't exist.
func (s *PostgresStore) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS risk_assessments (
			id            VARCHAR(36) PRIMARY KEY,
			key_id        VARCHAR(36) NOT NULL,
			score         NUMERIC(4,3) NOT NULL CHECK (score >= 0 AND score <= 1),
			decision      VARCHAR(10) NOT NULL CHECK (decision IN ('allow', 'warn', 'block')),
			factors       JSONB NOT NULL DEFAULT '{}',
			evaluated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_risk_assessments_key_id
			ON risk_assessments (key_id, evaluated_at DESC);

		CREATE INDEX IF NOT EXISTS idx_risk_assessments_blocks
			ON risk_assessments (evaluated_at DESC) WHERE decision = 'block';
	`)
	return err
}

func (s *PostgresStore) Record(ctx context.Context, assessment *RiskAssessment) error {
	factorsJSON, err := json.Marshal(assessment.Factors)
	if err != nil {
		return fmt.Errorf("failed to marshal factors: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO risk_assessments (id, key_id, score, decision, factors, evaluated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`,
		assessment.ID,
		assessment.KeyID,
		assessment.Score,
		string(assessment.Decision),
		factorsJSON,
		assessment.EvaluatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to record risk assessment: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListByKey(ctx context.Context, keyID string, limit int) ([]*RiskAssessment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, key_id, score, decision, factors, evaluated_at
		FROM risk_assessments
		WHERE key_id = $1
		ORDER BY evaluated_at DESC
		LIMIT $2
	`, keyID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list risk assessments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []*RiskAssessment
	for rows.Next() {
		var a RiskAssessment
		var factorsJSON []byte
		var evaluatedAt time.Time

		if err := rows.Scan(&a.ID, &a.KeyID, &a.Score, &a.Decision, &factorsJSON, &evaluatedAt); err != nil {
			continue
		}
		a.EvaluatedAt = evaluatedAt
		a.Factors = make(map[string]float64)
		_ = json.Unmarshal(factorsJSON, &a.Factors)
		result = append(result, &a)
	}
	return result, nil
}
