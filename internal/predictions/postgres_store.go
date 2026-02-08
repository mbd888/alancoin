package predictions

import (
	"context"
	"database/sql"
	"strconv"
)

// PostgresStore implements Store using PostgreSQL
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed predictions store
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Migrate creates the predictions tables
func (p *PostgresStore) Migrate(ctx context.Context) error {
	_, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS predictions (
			id              VARCHAR(36) PRIMARY KEY,
			author_addr     VARCHAR(42) NOT NULL,
			author_name     VARCHAR(255),
			type            VARCHAR(30) NOT NULL,
			statement       TEXT NOT NULL,
			target_type     VARCHAR(20) NOT NULL,
			target_id       VARCHAR(255),
			metric          VARCHAR(50),
			operator        VARCHAR(10),
			target_value    DECIMAL(20,6),
			created_at      TIMESTAMPTZ DEFAULT NOW(),
			resolves_at     TIMESTAMPTZ NOT NULL,
			resolved_at     TIMESTAMPTZ,
			status          VARCHAR(20) DEFAULT 'pending',
			actual_value    DECIMAL(20,6),
			agrees          INTEGER DEFAULT 0,
			disagrees       INTEGER DEFAULT 0,
			confidence      INTEGER DEFAULT 1
		);

		CREATE INDEX IF NOT EXISTS idx_predictions_author ON predictions(author_addr);
		CREATE INDEX IF NOT EXISTS idx_predictions_status ON predictions(status);
		CREATE INDEX IF NOT EXISTS idx_predictions_resolves ON predictions(resolves_at) WHERE status = 'pending';

		CREATE TABLE IF NOT EXISTS prediction_votes (
			prediction_id   VARCHAR(36) REFERENCES predictions(id) ON DELETE CASCADE,
			agent_addr      VARCHAR(42) NOT NULL,
			agrees          BOOLEAN NOT NULL,
			created_at      TIMESTAMPTZ DEFAULT NOW(),
			PRIMARY KEY (prediction_id, agent_addr)
		);

		CREATE TABLE IF NOT EXISTS predictor_stats (
			address         VARCHAR(42) PRIMARY KEY,
			total           INTEGER DEFAULT 0,
			correct         INTEGER DEFAULT 0,
			wrong           INTEGER DEFAULT 0,
			pending         INTEGER DEFAULT 0,
			streak          INTEGER DEFAULT 0,
			best_streak     INTEGER DEFAULT 0,
			reputation      DECIMAL(5,2) DEFAULT 50.0,
			updated_at      TIMESTAMPTZ DEFAULT NOW()
		);
	`)
	return err
}

// Create stores a new prediction
func (p *PostgresStore) Create(ctx context.Context, pred *Prediction) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO predictions (
			id, author_addr, author_name, type, statement,
			target_type, target_id, metric, operator, target_value,
			resolves_at, status, confidence
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`,
		pred.ID, pred.AuthorAddr, pred.AuthorName, pred.Type, pred.Statement,
		pred.TargetType, pred.TargetID, pred.Metric, pred.Operator, pred.TargetValue,
		pred.ResolvesAt, pred.Status, pred.ConfidenceLevel,
	)
	return err
}

// Get retrieves a prediction by ID
func (p *PostgresStore) Get(ctx context.Context, id string) (*Prediction, error) {
	pred := &Prediction{}
	var resolvedAt sql.NullTime
	var actualValue sql.NullFloat64

	err := p.db.QueryRowContext(ctx, `
		SELECT id, author_addr, author_name, type, statement,
		       target_type, target_id, metric, operator, target_value,
		       created_at, resolves_at, resolved_at, status, actual_value,
		       agrees, disagrees, confidence
		FROM predictions WHERE id = $1
	`, id).Scan(
		&pred.ID, &pred.AuthorAddr, &pred.AuthorName, &pred.Type, &pred.Statement,
		&pred.TargetType, &pred.TargetID, &pred.Metric, &pred.Operator, &pred.TargetValue,
		&pred.CreatedAt, &pred.ResolvesAt, &resolvedAt, &pred.Status, &actualValue,
		&pred.Agrees, &pred.Disagrees, &pred.ConfidenceLevel,
	)
	if err != nil {
		return nil, err
	}

	if resolvedAt.Valid {
		pred.ResolvedAt = &resolvedAt.Time
	}
	if actualValue.Valid {
		val := actualValue.Float64
		pred.ActualValue = &val
	}

	return pred, nil
}

// List returns predictions with filters
func (p *PostgresStore) List(ctx context.Context, opts ListOptions) ([]*Prediction, error) {
	query := `SELECT id, author_addr, author_name, type, statement,
	                 target_type, target_id, metric, operator, target_value,
	                 created_at, resolves_at, status, agrees, disagrees, confidence
	          FROM predictions WHERE 1=1`
	args := []interface{}{}
	n := 1

	if opts.Status != "" {
		query += " AND status = $" + strconv.Itoa(n)
		args = append(args, opts.Status)
		n++
	}
	if opts.AuthorAddr != "" {
		query += " AND author_addr = $" + strconv.Itoa(n)
		args = append(args, opts.AuthorAddr)
		n++
	}

	query += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(n)
	args = append(args, opts.Limit)

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var predictions []*Prediction
	for rows.Next() {
		pred := &Prediction{}
		if err := rows.Scan(
			&pred.ID, &pred.AuthorAddr, &pred.AuthorName, &pred.Type, &pred.Statement,
			&pred.TargetType, &pred.TargetID, &pred.Metric, &pred.Operator, &pred.TargetValue,
			&pred.CreatedAt, &pred.ResolvesAt, &pred.Status,
			&pred.Agrees, &pred.Disagrees, &pred.ConfidenceLevel,
		); err != nil {
			return nil, err
		}
		predictions = append(predictions, pred)
	}
	return predictions, rows.Err()
}

// ListByAuthor returns predictions by author
func (p *PostgresStore) ListByAuthor(ctx context.Context, authorAddr string, limit int) ([]*Prediction, error) {
	return p.List(ctx, ListOptions{AuthorAddr: authorAddr, Limit: limit})
}

// ListPending returns pending predictions ready to resolve
func (p *PostgresStore) ListPending(ctx context.Context) ([]*Prediction, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, author_addr, author_name, type, statement,
		       target_type, target_id, metric, operator, target_value,
		       created_at, resolves_at, status, agrees, disagrees, confidence
		FROM predictions
		WHERE status = 'pending' AND resolves_at <= NOW()
		ORDER BY resolves_at ASC
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var predictions []*Prediction
	for rows.Next() {
		pred := &Prediction{}
		if err := rows.Scan(
			&pred.ID, &pred.AuthorAddr, &pred.AuthorName, &pred.Type, &pred.Statement,
			&pred.TargetType, &pred.TargetID, &pred.Metric, &pred.Operator, &pred.TargetValue,
			&pred.CreatedAt, &pred.ResolvesAt, &pred.Status,
			&pred.Agrees, &pred.Disagrees, &pred.ConfidenceLevel,
		); err != nil {
			return nil, err
		}
		predictions = append(predictions, pred)
	}
	return predictions, rows.Err()
}

// Update updates a prediction (mainly for resolution)
func (p *PostgresStore) Update(ctx context.Context, pred *Prediction) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE predictions SET
			status = $2, resolved_at = $3, actual_value = $4
		WHERE id = $1
	`, pred.ID, pred.Status, pred.ResolvedAt, pred.ActualValue)
	return err
}

// RecordVote records a vote on a prediction
func (p *PostgresStore) RecordVote(ctx context.Context, predictionID, agentAddr string, agrees bool) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Check for existing vote to correctly adjust counts
	var previousAgrees sql.NullBool
	err = tx.QueryRowContext(ctx, `
		SELECT agrees FROM prediction_votes WHERE prediction_id = $1 AND agent_addr = $2
	`, predictionID, agentAddr).Scan(&previousAgrees)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	// Upsert vote
	_, err = tx.ExecContext(ctx, `
		INSERT INTO prediction_votes (prediction_id, agent_addr, agrees)
		VALUES ($1, $2, $3)
		ON CONFLICT (prediction_id, agent_addr) DO UPDATE SET agrees = EXCLUDED.agrees
	`, predictionID, agentAddr, agrees)
	if err != nil {
		return err
	}

	// Adjust counts: remove old vote if it existed, then add new vote
	if previousAgrees.Valid {
		if previousAgrees.Bool {
			_, _ = tx.ExecContext(ctx, `UPDATE predictions SET agrees = agrees - 1 WHERE id = $1`, predictionID)
		} else {
			_, _ = tx.ExecContext(ctx, `UPDATE predictions SET disagrees = disagrees - 1 WHERE id = $1`, predictionID)
		}
	}
	if agrees {
		_, err = tx.ExecContext(ctx, `UPDATE predictions SET agrees = agrees + 1 WHERE id = $1`, predictionID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE predictions SET disagrees = disagrees + 1 WHERE id = $1`, predictionID)
	}
	if err != nil {
		return err
	}

	return tx.Commit()
}

// GetPredictorStats returns stats for a predictor
func (p *PostgresStore) GetPredictorStats(ctx context.Context, authorAddr string) (*PredictorStats, error) {
	stats := &PredictorStats{Address: authorAddr}
	err := p.db.QueryRowContext(ctx, `
		SELECT total, correct, wrong, pending, streak, best_streak, reputation
		FROM predictor_stats WHERE address = $1
	`, authorAddr).Scan(
		&stats.TotalPredictions, &stats.Correct, &stats.Wrong,
		&stats.Pending, &stats.Streak, &stats.BestStreak, &stats.ReputationScore,
	)
	if err == sql.ErrNoRows {
		return stats, nil // Return empty stats
	}
	if resolved := stats.Correct + stats.Wrong; resolved > 0 {
		stats.Accuracy = float64(stats.Correct) / float64(resolved)
	}
	return stats, err
}

// GetTopPredictors returns predictors by accuracy
func (p *PostgresStore) GetTopPredictors(ctx context.Context, limit int) ([]*PredictorStats, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT address, total, correct, wrong, pending, streak, best_streak, reputation
		FROM predictor_stats
		WHERE total >= 5
		ORDER BY reputation DESC, correct DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []*PredictorStats
	for rows.Next() {
		s := &PredictorStats{}
		if err := rows.Scan(
			&s.Address, &s.TotalPredictions, &s.Correct, &s.Wrong,
			&s.Pending, &s.Streak, &s.BestStreak, &s.ReputationScore,
		); err != nil {
			return nil, err
		}
		if resolved := s.Correct + s.Wrong; resolved > 0 {
			s.Accuracy = float64(s.Correct) / float64(resolved)
		}
		stats = append(stats, s)
	}
	return stats, rows.Err()
}
