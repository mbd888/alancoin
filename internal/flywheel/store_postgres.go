package flywheel

import (
	"context"
	"database/sql"
	"time"
)

// PostgresStore implements SnapshotStore backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed flywheel snapshot store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// Save persists a flywheel state snapshot to the flywheel_snapshots table.
func (s *PostgresStore) Save(ctx context.Context, state *State) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO flywheel_snapshots (
			health_score, health_tier,
			velocity_score, growth_score, density_score, effectiveness_score, retention_score,
			tx_per_hour, volume_per_hour,
			total_agents, active_agents_7d, total_edges,
			graph_density, retention_rate_7d,
			computed_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		state.HealthScore, state.HealthTier,
		state.VelocityScore, state.GrowthScore, state.DensityScore, state.EffectivenessScore, state.RetentionScore,
		state.TransactionsPerHour, state.VolumePerHourUSD,
		state.TotalAgents, state.ActiveAgents7d, state.TotalEdges,
		state.GraphDensity, state.RetentionRate7d,
		state.ComputedAt,
	)
	return err
}

// Recent returns the most recent snapshots from the database, newest first.
func (s *PostgresStore) Recent(ctx context.Context, limit int) ([]*State, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT health_score, health_tier,
			velocity_score, growth_score, density_score, effectiveness_score, retention_score,
			tx_per_hour, volume_per_hour,
			total_agents, active_agents_7d, total_edges,
			graph_density, retention_rate_7d,
			computed_at
		FROM flywheel_snapshots
		ORDER BY computed_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*State
	for rows.Next() {
		st := &State{}
		var computedAt time.Time
		if err := rows.Scan(
			&st.HealthScore, &st.HealthTier,
			&st.VelocityScore, &st.GrowthScore, &st.DensityScore, &st.EffectivenessScore, &st.RetentionScore,
			&st.TransactionsPerHour, &st.VolumePerHourUSD,
			&st.TotalAgents, &st.ActiveAgents7d, &st.TotalEdges,
			&st.GraphDensity, &st.RetentionRate7d,
			&computedAt,
		); err != nil {
			return nil, err
		}
		st.ComputedAt = computedAt
		results = append(results, st)
	}
	return results, rows.Err()
}
