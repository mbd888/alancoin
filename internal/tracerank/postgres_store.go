package tracerank

import (
	"context"
	"database/sql"
	"strings"
)

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed TraceRank store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) SaveScores(ctx context.Context, scores []*AgentScore, runID string) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Upsert scores (one row per agent, latest wins)
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO tracerank_scores
			(address, graph_score, raw_rank, seed_signal,
			 in_degree, out_degree, in_volume, out_volume,
			 iterations, compute_run_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (address) DO UPDATE SET
			graph_score = EXCLUDED.graph_score,
			raw_rank = EXCLUDED.raw_rank,
			seed_signal = EXCLUDED.seed_signal,
			in_degree = EXCLUDED.in_degree,
			out_degree = EXCLUDED.out_degree,
			in_volume = EXCLUDED.in_volume,
			out_volume = EXCLUDED.out_volume,
			iterations = EXCLUDED.iterations,
			compute_run_id = EXCLUDED.compute_run_id,
			computed_at = NOW()`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, s := range scores {
		_, err := stmt.ExecContext(ctx,
			strings.ToLower(s.Address),
			s.GraphScore, s.RawRank, s.SeedSignal,
			s.InDegree, s.OutDegree, s.InVolume, s.OutVolume,
			s.Iterations, runID)
		if err != nil {
			return err
		}
	}

	// Also insert into history table for auditing.
	// Cast $1 to TEXT to avoid PG16 type-inference ambiguity in INSERT...SELECT with ON CONFLICT.
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tracerank_runs (run_id, node_count, edge_count, iterations, converged, duration_ms, max_score, mean_score)
		SELECT $1::TEXT, COUNT(*), 0, MAX(iterations), true, 0,
			   MAX(graph_score), AVG(graph_score)
		FROM tracerank_scores WHERE compute_run_id = $1::TEXT
		ON CONFLICT (run_id) DO NOTHING`, runID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (p *PostgresStore) GetScore(ctx context.Context, address string) (*AgentScore, error) {
	const q = `
		SELECT address, graph_score, raw_rank, seed_signal,
			   in_degree, out_degree, in_volume, out_volume,
			   iterations, compute_run_id, computed_at
		FROM tracerank_scores
		WHERE address = $1`

	s := &AgentScore{}
	err := p.db.QueryRowContext(ctx, q, strings.ToLower(address)).Scan(
		&s.Address, &s.GraphScore, &s.RawRank, &s.SeedSignal,
		&s.InDegree, &s.OutDegree, &s.InVolume, &s.OutVolume,
		&s.Iterations, &s.ComputeRunID, &s.ComputedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (p *PostgresStore) GetScores(ctx context.Context, addresses []string) (map[string]*AgentScore, error) {
	if len(addresses) == 0 {
		return map[string]*AgentScore{}, nil
	}

	// Build parameterized query for IN clause
	placeholders := make([]string, len(addresses))
	args := make([]interface{}, len(addresses))
	for i, addr := range addresses {
		placeholders[i] = "$" + itoa(i+1)
		args[i] = strings.ToLower(addr)
	}

	q := `SELECT address, graph_score, raw_rank, seed_signal,` + //nolint:gosec // placeholders are $1,$2,... not user input
		`	   in_degree, out_degree, in_volume, out_volume,
			   iterations, compute_run_id, computed_at
		  FROM tracerank_scores
		  WHERE address IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*AgentScore, len(addresses))
	for rows.Next() {
		s := &AgentScore{}
		if err := rows.Scan(
			&s.Address, &s.GraphScore, &s.RawRank, &s.SeedSignal,
			&s.InDegree, &s.OutDegree, &s.InVolume, &s.OutVolume,
			&s.Iterations, &s.ComputeRunID, &s.ComputedAt); err != nil {
			return nil, err
		}
		result[s.Address] = s
	}
	return result, rows.Err()
}

func (p *PostgresStore) GetTopScores(ctx context.Context, limit int) ([]*AgentScore, error) {
	if limit <= 0 {
		limit = 100
	}

	const q = `
		SELECT address, graph_score, raw_rank, seed_signal,
			   in_degree, out_degree, in_volume, out_volume,
			   iterations, compute_run_id, computed_at
		FROM tracerank_scores
		ORDER BY graph_score DESC
		LIMIT $1`

	rows, err := p.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*AgentScore
	for rows.Next() {
		s := &AgentScore{}
		if err := rows.Scan(
			&s.Address, &s.GraphScore, &s.RawRank, &s.SeedSignal,
			&s.InDegree, &s.OutDegree, &s.InVolume, &s.OutVolume,
			&s.Iterations, &s.ComputeRunID, &s.ComputedAt); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func (p *PostgresStore) GetRunHistory(ctx context.Context, limit int) ([]*RunMetadata, error) {
	if limit <= 0 {
		limit = 10
	}

	const q = `
		SELECT run_id, node_count, edge_count, iterations, converged,
			   duration_ms, max_score, mean_score, computed_at
		FROM tracerank_runs
		ORDER BY computed_at DESC
		LIMIT $1`

	rows, err := p.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*RunMetadata
	for rows.Next() {
		r := &RunMetadata{}
		if err := rows.Scan(
			&r.RunID, &r.NodeCount, &r.EdgeCount, &r.Iterations, &r.Converged,
			&r.DurationMs, &r.MaxScore, &r.MeanScore, &r.ComputedAt); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// itoa converts an integer to its decimal string representation.
// Avoids importing strconv for this single use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
