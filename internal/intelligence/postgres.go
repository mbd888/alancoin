package intelligence

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// PostgresStore implements Store backed by PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed intelligence store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) SaveProfile(ctx context.Context, profile *AgentProfile) error {
	return p.SaveProfileBatch(ctx, []*AgentProfile{profile})
}

func (p *PostgresStore) SaveProfileBatch(ctx context.Context, profiles []*AgentProfile) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO intelligence_profiles
			(address, credit_score, risk_score, composite_score, tier,
			 tracerank_input, reputation_input, dispute_rate, tx_success_rate, total_volume,
			 anomaly_count_30d, critical_alerts, mean_amount, stddev_amount, forensic_score,
			 in_degree, out_degree, clustering_coeff, bridge_score,
			 total_txns, days_on_network,
			 credit_delta_7d, credit_delta_30d, risk_delta_7d, risk_delta_30d,
			 compute_run_id, computed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27)
		ON CONFLICT (address) DO UPDATE SET
			credit_score = EXCLUDED.credit_score,
			risk_score = EXCLUDED.risk_score,
			composite_score = EXCLUDED.composite_score,
			tier = EXCLUDED.tier,
			tracerank_input = EXCLUDED.tracerank_input,
			reputation_input = EXCLUDED.reputation_input,
			dispute_rate = EXCLUDED.dispute_rate,
			tx_success_rate = EXCLUDED.tx_success_rate,
			total_volume = EXCLUDED.total_volume,
			anomaly_count_30d = EXCLUDED.anomaly_count_30d,
			critical_alerts = EXCLUDED.critical_alerts,
			mean_amount = EXCLUDED.mean_amount,
			stddev_amount = EXCLUDED.stddev_amount,
			forensic_score = EXCLUDED.forensic_score,
			in_degree = EXCLUDED.in_degree,
			out_degree = EXCLUDED.out_degree,
			clustering_coeff = EXCLUDED.clustering_coeff,
			bridge_score = EXCLUDED.bridge_score,
			total_txns = EXCLUDED.total_txns,
			days_on_network = EXCLUDED.days_on_network,
			credit_delta_7d = EXCLUDED.credit_delta_7d,
			credit_delta_30d = EXCLUDED.credit_delta_30d,
			risk_delta_7d = EXCLUDED.risk_delta_7d,
			risk_delta_30d = EXCLUDED.risk_delta_30d,
			compute_run_id = EXCLUDED.compute_run_id,
			computed_at = EXCLUDED.computed_at`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, pr := range profiles {
		_, err := stmt.ExecContext(ctx,
			strings.ToLower(pr.Address),
			pr.CreditScore, pr.RiskScore, pr.CompositeScore, string(pr.Tier),
			pr.Credit.TraceRankInput, pr.Credit.ReputationInput,
			pr.Credit.DisputeRate, pr.Credit.TxSuccessRate, pr.Credit.TotalVolume,
			pr.Risk.AnomalyCount30d, pr.Risk.CriticalAlerts,
			pr.Risk.MeanAmount, pr.Risk.StdDevAmount, pr.Risk.ForensicScore,
			pr.Network.InDegree, pr.Network.OutDegree,
			pr.Network.ClusteringCoefficient, pr.Network.BridgeScore,
			pr.Ops.TotalTxns, pr.Ops.DaysOnNetwork,
			pr.Trends.CreditDelta7d, pr.Trends.CreditDelta30d,
			pr.Trends.RiskDelta7d, pr.Trends.RiskDelta30d,
			pr.ComputeRunID, pr.ComputedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (p *PostgresStore) GetProfile(ctx context.Context, address string) (*AgentProfile, error) {
	const q = `
		SELECT address, credit_score, risk_score, composite_score, tier,
			   tracerank_input, reputation_input, dispute_rate, tx_success_rate, total_volume,
			   anomaly_count_30d, critical_alerts, mean_amount, stddev_amount, forensic_score,
			   in_degree, out_degree, clustering_coeff, bridge_score,
			   total_txns, days_on_network,
			   credit_delta_7d, credit_delta_30d, risk_delta_7d, risk_delta_30d,
			   compute_run_id, computed_at
		FROM intelligence_profiles
		WHERE address = $1`

	pr := &AgentProfile{}
	var tier string
	err := p.db.QueryRowContext(ctx, q, strings.ToLower(address)).Scan(
		&pr.Address, &pr.CreditScore, &pr.RiskScore, &pr.CompositeScore, &tier,
		&pr.Credit.TraceRankInput, &pr.Credit.ReputationInput,
		&pr.Credit.DisputeRate, &pr.Credit.TxSuccessRate, &pr.Credit.TotalVolume,
		&pr.Risk.AnomalyCount30d, &pr.Risk.CriticalAlerts,
		&pr.Risk.MeanAmount, &pr.Risk.StdDevAmount, &pr.Risk.ForensicScore,
		&pr.Network.InDegree, &pr.Network.OutDegree,
		&pr.Network.ClusteringCoefficient, &pr.Network.BridgeScore,
		&pr.Ops.TotalTxns, &pr.Ops.DaysOnNetwork,
		&pr.Trends.CreditDelta7d, &pr.Trends.CreditDelta30d,
		&pr.Trends.RiskDelta7d, &pr.Trends.RiskDelta30d,
		&pr.ComputeRunID, &pr.ComputedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	pr.Tier = Tier(tier)
	return pr, nil
}

func (p *PostgresStore) GetProfiles(ctx context.Context, addresses []string) (map[string]*AgentProfile, error) {
	if len(addresses) == 0 {
		return map[string]*AgentProfile{}, nil
	}

	placeholders := make([]string, len(addresses))
	args := make([]interface{}, len(addresses))
	for i, addr := range addresses {
		placeholders[i] = "$" + itoa(i+1)
		args[i] = strings.ToLower(addr)
	}

	q := `SELECT address, credit_score, risk_score, composite_score, tier,` + //nolint:gosec // placeholders are $1,$2,... not user input
		`	   tracerank_input, reputation_input, dispute_rate, tx_success_rate, total_volume,
			   anomaly_count_30d, critical_alerts, mean_amount, stddev_amount, forensic_score,
			   in_degree, out_degree, clustering_coeff, bridge_score,
			   total_txns, days_on_network,
			   credit_delta_7d, credit_delta_30d, risk_delta_7d, risk_delta_30d,
			   compute_run_id, computed_at
		  FROM intelligence_profiles
		  WHERE address IN (` + strings.Join(placeholders, ",") + `)`

	rows, err := p.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*AgentProfile, len(addresses))
	for rows.Next() {
		pr := &AgentProfile{}
		var tier string
		if err := rows.Scan(
			&pr.Address, &pr.CreditScore, &pr.RiskScore, &pr.CompositeScore, &tier,
			&pr.Credit.TraceRankInput, &pr.Credit.ReputationInput,
			&pr.Credit.DisputeRate, &pr.Credit.TxSuccessRate, &pr.Credit.TotalVolume,
			&pr.Risk.AnomalyCount30d, &pr.Risk.CriticalAlerts,
			&pr.Risk.MeanAmount, &pr.Risk.StdDevAmount, &pr.Risk.ForensicScore,
			&pr.Network.InDegree, &pr.Network.OutDegree,
			&pr.Network.ClusteringCoefficient, &pr.Network.BridgeScore,
			&pr.Ops.TotalTxns, &pr.Ops.DaysOnNetwork,
			&pr.Trends.CreditDelta7d, &pr.Trends.CreditDelta30d,
			&pr.Trends.RiskDelta7d, &pr.Trends.RiskDelta30d,
			&pr.ComputeRunID, &pr.ComputedAt); err != nil {
			return nil, err
		}
		pr.Tier = Tier(tier)
		result[pr.Address] = pr
	}
	return result, rows.Err()
}

func (p *PostgresStore) GetTopProfiles(ctx context.Context, limit int) ([]*AgentProfile, error) {
	if limit <= 0 {
		limit = 100
	}

	const q = `
		SELECT address, credit_score, risk_score, composite_score, tier,
			   tracerank_input, reputation_input, dispute_rate, tx_success_rate, total_volume,
			   anomaly_count_30d, critical_alerts, mean_amount, stddev_amount, forensic_score,
			   in_degree, out_degree, clustering_coeff, bridge_score,
			   total_txns, days_on_network,
			   credit_delta_7d, credit_delta_30d, risk_delta_7d, risk_delta_30d,
			   compute_run_id, computed_at
		FROM intelligence_profiles
		ORDER BY composite_score DESC
		LIMIT $1`

	rows, err := p.db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*AgentProfile
	for rows.Next() {
		pr := &AgentProfile{}
		var tier string
		if err := rows.Scan(
			&pr.Address, &pr.CreditScore, &pr.RiskScore, &pr.CompositeScore, &tier,
			&pr.Credit.TraceRankInput, &pr.Credit.ReputationInput,
			&pr.Credit.DisputeRate, &pr.Credit.TxSuccessRate, &pr.Credit.TotalVolume,
			&pr.Risk.AnomalyCount30d, &pr.Risk.CriticalAlerts,
			&pr.Risk.MeanAmount, &pr.Risk.StdDevAmount, &pr.Risk.ForensicScore,
			&pr.Network.InDegree, &pr.Network.OutDegree,
			&pr.Network.ClusteringCoefficient, &pr.Network.BridgeScore,
			&pr.Ops.TotalTxns, &pr.Ops.DaysOnNetwork,
			&pr.Trends.CreditDelta7d, &pr.Trends.CreditDelta30d,
			&pr.Trends.RiskDelta7d, &pr.Trends.RiskDelta30d,
			&pr.ComputeRunID, &pr.ComputedAt); err != nil {
			return nil, err
		}
		pr.Tier = Tier(tier)
		result = append(result, pr)
	}
	return result, rows.Err()
}

func (p *PostgresStore) SaveScoreHistory(ctx context.Context, points []*ScoreHistoryPoint) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO intelligence_score_history
			(address, credit_score, risk_score, composite_score, tier, compute_run_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, pt := range points {
		_, err := stmt.ExecContext(ctx,
			strings.ToLower(pt.Address),
			pt.CreditScore, pt.RiskScore, pt.CompositeScore,
			string(pt.Tier), pt.ComputeRunID, pt.CreatedAt)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (p *PostgresStore) GetScoreHistory(ctx context.Context, address string, from, to time.Time, limit int) ([]*ScoreHistoryPoint, error) {
	if limit <= 0 {
		limit = 500
	}

	const q = `
		SELECT address, credit_score, risk_score, composite_score, tier, compute_run_id, created_at
		FROM intelligence_score_history
		WHERE address = $1 AND created_at >= $2 AND created_at <= $3
		ORDER BY created_at DESC
		LIMIT $4`

	rows, err := p.db.QueryContext(ctx, q, strings.ToLower(address), from, to, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*ScoreHistoryPoint
	for rows.Next() {
		pt := &ScoreHistoryPoint{}
		var tier string
		if err := rows.Scan(&pt.Address, &pt.CreditScore, &pt.RiskScore,
			&pt.CompositeScore, &tier, &pt.ComputeRunID, &pt.CreatedAt); err != nil {
			return nil, err
		}
		pt.Tier = Tier(tier)
		result = append(result, pt)
	}
	return result, rows.Err()
}

func (p *PostgresStore) DeleteScoreHistoryBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM intelligence_score_history WHERE created_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (p *PostgresStore) SaveBenchmarks(ctx context.Context, b *NetworkBenchmarks) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO intelligence_benchmarks
			(total_agents, avg_credit_score, median_credit_score, avg_risk_score,
			 p90_credit_score, p10_credit_score, avg_composite_score,
			 compute_run_id, computed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		b.TotalAgents, b.AvgCreditScore, b.MedianCreditScore, b.AvgRiskScore,
		b.P90CreditScore, b.P10CreditScore, b.AvgCompositeScore,
		b.ComputeRunID, b.ComputedAt)
	return err
}

func (p *PostgresStore) GetLatestBenchmarks(ctx context.Context) (*NetworkBenchmarks, error) {
	const q = `
		SELECT total_agents, avg_credit_score, median_credit_score, avg_risk_score,
			   p90_credit_score, p10_credit_score, avg_composite_score,
			   compute_run_id, computed_at
		FROM intelligence_benchmarks
		ORDER BY computed_at DESC
		LIMIT 1`

	b := &NetworkBenchmarks{}
	err := p.db.QueryRowContext(ctx, q).Scan(
		&b.TotalAgents, &b.AvgCreditScore, &b.MedianCreditScore, &b.AvgRiskScore,
		&b.P90CreditScore, &b.P10CreditScore, &b.AvgCompositeScore,
		&b.ComputeRunID, &b.ComputedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (p *PostgresStore) DeleteBenchmarksBefore(ctx context.Context, before time.Time) (int64, error) {
	res, err := p.db.ExecContext(ctx,
		`DELETE FROM intelligence_benchmarks WHERE computed_at < $1`, before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// itoa converts an integer to its decimal string representation.
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
