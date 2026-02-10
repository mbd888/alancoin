package reputation

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// PostgresSnapshotStore implements SnapshotStore backed by PostgreSQL.
type PostgresSnapshotStore struct {
	db *sql.DB
}

// NewPostgresSnapshotStore creates a PostgreSQL-backed snapshot store.
func NewPostgresSnapshotStore(db *sql.DB) *PostgresSnapshotStore {
	return &PostgresSnapshotStore{db: db}
}

func (p *PostgresSnapshotStore) Save(ctx context.Context, snap *Snapshot) error {
	const q = `
		INSERT INTO reputation_snapshots
			(address, score, tier, volume_score, activity_score, success_score,
			 age_score, diversity_score, total_txns, total_volume, success_rate, unique_peers)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at`

	return p.db.QueryRowContext(ctx, q,
		strings.ToLower(snap.Address),
		snap.Score,
		string(snap.Tier),
		snap.VolumeScore,
		snap.ActivityScore,
		snap.SuccessScore,
		snap.AgeScore,
		snap.DiversityScore,
		snap.TotalTxns,
		snap.TotalVolume,
		snap.SuccessRate,
		snap.UniquePeers,
	).Scan(&snap.ID, &snap.CreatedAt)
}

func (p *PostgresSnapshotStore) SaveBatch(ctx context.Context, snaps []*Snapshot) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO reputation_snapshots
			(address, score, tier, volume_score, activity_score, success_score,
			 age_score, diversity_score, total_txns, total_volume, success_rate, unique_peers)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, s := range snaps {
		_, err := stmt.ExecContext(ctx, strings.ToLower(s.Address),
			s.Score, string(s.Tier),
			s.VolumeScore, s.ActivityScore, s.SuccessScore,
			s.AgeScore, s.DiversityScore,
			s.TotalTxns, s.TotalVolume, s.SuccessRate, s.UniquePeers)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (p *PostgresSnapshotStore) Query(ctx context.Context, q HistoryQuery) ([]*Snapshot, error) {
	query := `
		SELECT id, address, score, tier,
			   volume_score, activity_score, success_score, age_score, diversity_score,
			   total_txns, total_volume, success_rate, unique_peers, created_at
		FROM reputation_snapshots
		WHERE address = $1`

	args := []interface{}{strings.ToLower(q.Address)}
	argIdx := 2

	if !q.From.IsZero() {
		query += " AND created_at >= $" + strconv.Itoa(argIdx)
		args = append(args, q.From)
		argIdx++
	}
	if !q.To.IsZero() {
		query += " AND created_at <= $" + strconv.Itoa(argIdx)
		args = append(args, q.To)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	query += " LIMIT $" + strconv.Itoa(argIdx)
	args = append(args, limit)

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanSnapshots(rows)
}

func (p *PostgresSnapshotStore) Latest(ctx context.Context, address string) (*Snapshot, error) {
	const q = `
		SELECT id, address, score, tier,
			   volume_score, activity_score, success_score, age_score, diversity_score,
			   total_txns, total_volume, success_rate, unique_peers, created_at
		FROM reputation_snapshots
		WHERE address = $1
		ORDER BY created_at DESC
		LIMIT 1`

	row := p.db.QueryRowContext(ctx, q, strings.ToLower(address))
	s := &Snapshot{}
	var tier string
	err := row.Scan(&s.ID, &s.Address, &s.Score, &tier,
		&s.VolumeScore, &s.ActivityScore, &s.SuccessScore, &s.AgeScore, &s.DiversityScore,
		&s.TotalTxns, &s.TotalVolume, &s.SuccessRate, &s.UniquePeers, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.Tier = Tier(tier)
	return s, nil
}

func scanSnapshots(rows *sql.Rows) ([]*Snapshot, error) {
	var out []*Snapshot
	for rows.Next() {
		s := &Snapshot{}
		var tier string
		if err := rows.Scan(&s.ID, &s.Address, &s.Score, &tier,
			&s.VolumeScore, &s.ActivityScore, &s.SuccessScore, &s.AgeScore, &s.DiversityScore,
			&s.TotalTxns, &s.TotalVolume, &s.SuccessRate, &s.UniquePeers, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Tier = Tier(tier)
		out = append(out, s)
	}
	return out, rows.Err()
}
