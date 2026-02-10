package stakes

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PostgresStore persists stakes data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL stakes store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// --- Stakes ---

func (s *PostgresStore) CreateStake(ctx context.Context, stake *Stake) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stakes (id, agent_addr, revenue_share_bps, total_shares, available_shares,
			price_per_share, vesting_period, distribution_freq, status, total_raised,
			total_distributed, undistributed, last_distributed_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		stake.ID, stake.AgentAddr, stake.RevenueShareBPS, stake.TotalShares, stake.AvailableShares,
		stake.PricePerShare, stake.VestingPeriod, stake.DistributionFreq, stake.Status,
		stake.TotalRaised, stake.TotalDistributed, stake.Undistributed,
		stake.LastDistributedAt, stake.CreatedAt, stake.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetStake(ctx context.Context, id string) (*Stake, error) {
	stake := &Stake{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, agent_addr, revenue_share_bps, total_shares, available_shares,
			price_per_share, vesting_period, distribution_freq, status, total_raised,
			total_distributed, undistributed, last_distributed_at, created_at, updated_at
		FROM stakes WHERE id = $1`, id,
	).Scan(
		&stake.ID, &stake.AgentAddr, &stake.RevenueShareBPS, &stake.TotalShares, &stake.AvailableShares,
		&stake.PricePerShare, &stake.VestingPeriod, &stake.DistributionFreq, &stake.Status,
		&stake.TotalRaised, &stake.TotalDistributed, &stake.Undistributed,
		&stake.LastDistributedAt, &stake.CreatedAt, &stake.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrStakeNotFound
	}
	return stake, err
}

func (s *PostgresStore) UpdateStake(ctx context.Context, stake *Stake) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stakes SET revenue_share_bps=$2, total_shares=$3, available_shares=$4,
			price_per_share=$5, vesting_period=$6, distribution_freq=$7, status=$8,
			total_raised=$9, total_distributed=$10, undistributed=$11,
			last_distributed_at=$12, updated_at=$13
		WHERE id = $1`,
		stake.ID, stake.RevenueShareBPS, stake.TotalShares, stake.AvailableShares,
		stake.PricePerShare, stake.VestingPeriod, stake.DistributionFreq, stake.Status,
		stake.TotalRaised, stake.TotalDistributed, stake.Undistributed,
		stake.LastDistributedAt, stake.UpdatedAt,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrStakeNotFound
	}
	return nil
}

func (s *PostgresStore) ListByAgent(ctx context.Context, agentAddr string) ([]*Stake, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_addr, revenue_share_bps, total_shares, available_shares,
			price_per_share, vesting_period, distribution_freq, status, total_raised,
			total_distributed, undistributed, last_distributed_at, created_at, updated_at
		FROM stakes WHERE agent_addr = $1 ORDER BY created_at DESC`,
		strings.ToLower(agentAddr),
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStakes(rows)
}

func (s *PostgresStore) ListOpen(ctx context.Context, limit int) ([]*Stake, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_addr, revenue_share_bps, total_shares, available_shares,
			price_per_share, vesting_period, distribution_freq, status, total_raised,
			total_distributed, undistributed, last_distributed_at, created_at, updated_at
		FROM stakes WHERE status = 'open' ORDER BY created_at DESC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanStakes(rows)
}

func (s *PostgresStore) ListDueForDistribution(ctx context.Context, now time.Time, limit int) ([]*Stake, error) {
	// Return open stakes with undistributed > 0 where enough time has passed
	// We fetch all candidates and filter in Go since frequency-based interval
	// comparison is simpler in application code.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_addr, revenue_share_bps, total_shares, available_shares,
			price_per_share, vesting_period, distribution_freq, status, total_raised,
			total_distributed, undistributed, last_distributed_at, created_at, updated_at
		FROM stakes
		WHERE status = 'open' AND undistributed > 0
		ORDER BY created_at ASC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	all, err := scanStakes(rows)
	if err != nil {
		return nil, err
	}

	var due []*Stake
	for _, stake := range all {
		freq := freqToDuration(stake.DistributionFreq)
		if stake.LastDistributedAt == nil || now.Sub(*stake.LastDistributedAt) >= freq {
			due = append(due, stake)
		}
	}
	return due, nil
}

func (s *PostgresStore) GetAgentTotalShareBPS(ctx context.Context, agentAddr string) (int, error) {
	var total sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(revenue_share_bps), 0)
		FROM stakes WHERE agent_addr = $1 AND status != 'closed'`,
		strings.ToLower(agentAddr),
	).Scan(&total)
	if err != nil {
		return 0, err
	}
	return int(total.Int64), nil
}

// --- Holdings ---

func (s *PostgresStore) CreateHolding(ctx context.Context, h *Holding) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stake_holdings (id, stake_id, investor_addr, shares, cost_basis,
			vested_at, status, total_earned, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		h.ID, h.StakeID, h.InvestorAddr, h.Shares, h.CostBasis,
		h.VestedAt, h.Status, h.TotalEarned, h.CreatedAt, h.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetHolding(ctx context.Context, id string) (*Holding, error) {
	h := &Holding{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, stake_id, investor_addr, shares, cost_basis, vested_at,
			status, total_earned, created_at, updated_at
		FROM stake_holdings WHERE id = $1`, id,
	).Scan(
		&h.ID, &h.StakeID, &h.InvestorAddr, &h.Shares, &h.CostBasis,
		&h.VestedAt, &h.Status, &h.TotalEarned, &h.CreatedAt, &h.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrHoldingNotFound
	}
	return h, err
}

func (s *PostgresStore) UpdateHolding(ctx context.Context, h *Holding) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stake_holdings SET shares=$2, cost_basis=$3, vested_at=$4,
			status=$5, total_earned=$6, updated_at=$7
		WHERE id = $1`,
		h.ID, h.Shares, h.CostBasis, h.VestedAt, h.Status, h.TotalEarned, h.UpdatedAt,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrHoldingNotFound
	}
	return nil
}

func (s *PostgresStore) ListHoldingsByStake(ctx context.Context, stakeID string) ([]*Holding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, stake_id, investor_addr, shares, cost_basis, vested_at,
			status, total_earned, created_at, updated_at
		FROM stake_holdings WHERE stake_id = $1 AND status != 'liquidated'
		ORDER BY created_at ASC`, stakeID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHoldings(rows)
}

func (s *PostgresStore) ListHoldingsByInvestor(ctx context.Context, investorAddr string) ([]*Holding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, stake_id, investor_addr, shares, cost_basis, vested_at,
			status, total_earned, created_at, updated_at
		FROM stake_holdings WHERE investor_addr = $1
		ORDER BY created_at DESC`, strings.ToLower(investorAddr),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHoldings(rows)
}

func (s *PostgresStore) GetHoldingByInvestorAndStake(ctx context.Context, investorAddr, stakeID string) (*Holding, error) {
	h := &Holding{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, stake_id, investor_addr, shares, cost_basis, vested_at,
			status, total_earned, created_at, updated_at
		FROM stake_holdings WHERE investor_addr = $1 AND stake_id = $2 AND status != 'liquidated'
		LIMIT 1`, strings.ToLower(investorAddr), stakeID,
	).Scan(
		&h.ID, &h.StakeID, &h.InvestorAddr, &h.Shares, &h.CostBasis,
		&h.VestedAt, &h.Status, &h.TotalEarned, &h.CreatedAt, &h.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrHoldingNotFound
	}
	return h, err
}

// --- Distributions ---

func (s *PostgresStore) CreateDistribution(ctx context.Context, d *Distribution) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stake_distributions (id, stake_id, agent_addr, revenue_amount,
			share_amount, per_share_amount, share_count, holding_count, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		d.ID, d.StakeID, d.AgentAddr, d.RevenueAmount, d.ShareAmount,
		d.PerShareAmount, d.ShareCount, d.HoldingCount, d.Status, d.CreatedAt,
	)
	return err
}

func (s *PostgresStore) ListDistributions(ctx context.Context, stakeID string, limit int) ([]*Distribution, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, stake_id, agent_addr, revenue_amount, share_amount,
			per_share_amount, share_count, holding_count, status, created_at
		FROM stake_distributions WHERE stake_id = $1
		ORDER BY created_at DESC LIMIT $2`, stakeID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*Distribution
	for rows.Next() {
		d := &Distribution{}
		if err := rows.Scan(
			&d.ID, &d.StakeID, &d.AgentAddr, &d.RevenueAmount, &d.ShareAmount,
			&d.PerShareAmount, &d.ShareCount, &d.HoldingCount, &d.Status, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// --- Orders ---

func (s *PostgresStore) CreateOrder(ctx context.Context, o *Order) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO stake_orders (id, stake_id, holding_id, seller_addr, shares,
			price_per_share, status, filled_shares, buyer_addr, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		o.ID, o.StakeID, o.HoldingID, o.SellerAddr, o.Shares,
		o.PricePerShare, o.Status, o.FilledShares, o.BuyerAddr,
		o.CreatedAt, o.UpdatedAt,
	)
	return err
}

func (s *PostgresStore) GetOrder(ctx context.Context, id string) (*Order, error) {
	o := &Order{}
	err := s.db.QueryRowContext(ctx, `
		SELECT id, stake_id, holding_id, seller_addr, shares, price_per_share,
			status, filled_shares, COALESCE(buyer_addr, ''), created_at, updated_at
		FROM stake_orders WHERE id = $1`, id,
	).Scan(
		&o.ID, &o.StakeID, &o.HoldingID, &o.SellerAddr, &o.Shares,
		&o.PricePerShare, &o.Status, &o.FilledShares, &o.BuyerAddr,
		&o.CreatedAt, &o.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrOrderNotFound
	}
	return o, err
}

func (s *PostgresStore) UpdateOrder(ctx context.Context, o *Order) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE stake_orders SET status=$2, filled_shares=$3, buyer_addr=$4, updated_at=$5
		WHERE id = $1`,
		o.ID, o.Status, o.FilledShares, o.BuyerAddr, o.UpdatedAt,
	)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrOrderNotFound
	}
	return nil
}

func (s *PostgresStore) ListOrdersByStake(ctx context.Context, stakeID string, status string, limit int) ([]*Order, error) {
	var query string
	var args []interface{}

	if status != "" {
		query = `SELECT id, stake_id, holding_id, seller_addr, shares, price_per_share,
			status, filled_shares, COALESCE(buyer_addr, ''), created_at, updated_at
			FROM stake_orders WHERE stake_id = $1 AND status = $2
			ORDER BY created_at DESC LIMIT $3`
		args = []interface{}{stakeID, status, limit}
	} else {
		query = `SELECT id, stake_id, holding_id, seller_addr, shares, price_per_share,
			status, filled_shares, COALESCE(buyer_addr, ''), created_at, updated_at
			FROM stake_orders WHERE stake_id = $1
			ORDER BY created_at DESC LIMIT $2`
		args = []interface{}{stakeID, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

func (s *PostgresStore) ListOrdersBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Order, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, stake_id, holding_id, seller_addr, shares, price_per_share,
			status, filled_shares, COALESCE(buyer_addr, ''), created_at, updated_at
		FROM stake_orders WHERE seller_addr = $1
		ORDER BY created_at DESC LIMIT $2`, strings.ToLower(sellerAddr), limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanOrders(rows)
}

// --- scan helpers ---

func scanStakes(rows *sql.Rows) ([]*Stake, error) {
	var result []*Stake
	for rows.Next() {
		s := &Stake{}
		if err := rows.Scan(
			&s.ID, &s.AgentAddr, &s.RevenueShareBPS, &s.TotalShares, &s.AvailableShares,
			&s.PricePerShare, &s.VestingPeriod, &s.DistributionFreq, &s.Status,
			&s.TotalRaised, &s.TotalDistributed, &s.Undistributed,
			&s.LastDistributedAt, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan stake: %w", err)
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func scanHoldings(rows *sql.Rows) ([]*Holding, error) {
	var result []*Holding
	for rows.Next() {
		h := &Holding{}
		if err := rows.Scan(
			&h.ID, &h.StakeID, &h.InvestorAddr, &h.Shares, &h.CostBasis,
			&h.VestedAt, &h.Status, &h.TotalEarned, &h.CreatedAt, &h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan holding: %w", err)
		}
		result = append(result, h)
	}
	return result, rows.Err()
}

func scanOrders(rows *sql.Rows) ([]*Order, error) {
	var result []*Order
	for rows.Next() {
		o := &Order{}
		if err := rows.Scan(
			&o.ID, &o.StakeID, &o.HoldingID, &o.SellerAddr, &o.Shares,
			&o.PricePerShare, &o.Status, &o.FilledShares, &o.BuyerAddr,
			&o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
