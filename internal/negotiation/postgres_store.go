package negotiation

import (
	"context"
	"database/sql"
	"time"
)

// PostgresStore persists negotiation data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed negotiation store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

// --- RFP operations ---

func (p *PostgresStore) CreateRFP(ctx context.Context, rfp *RFP) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO rfps (
			id, buyer_addr, service_type, description,
			min_budget, max_budget, max_latency_ms, min_success_rate,
			duration, min_volume, bid_deadline, auto_select,
			min_reputation, max_counter_rounds,
			scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
			status, winning_bid_id, contract_id, bid_count,
			cancel_reason, awarded_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5::NUMERIC(20,6), $6::NUMERIC(20,6), $7, $8,
			$9, $10, $11, $12,
			$13, $14,
			$15, $16, $17,
			$18, $19, $20, $21,
			$22, $23, $24, $25
		)`,
		rfp.ID, rfp.BuyerAddr, rfp.ServiceType, nullStr(rfp.Description),
		rfp.MinBudget, rfp.MaxBudget, rfp.MaxLatencyMs, rfp.MinSuccessRate,
		rfp.Duration, rfp.MinVolume, rfp.BidDeadline, rfp.AutoSelect,
		rfp.MinReputation, rfp.MaxCounterRounds,
		rfp.ScoringWeights.Price, rfp.ScoringWeights.Reputation, rfp.ScoringWeights.SLA,
		string(rfp.Status), nullStr(rfp.WinningBidID), nullStr(rfp.ContractID), rfp.BidCount,
		nullStr(rfp.CancelReason), nullTime(rfp.AwardedAt), rfp.CreatedAt, rfp.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetRFP(ctx context.Context, id string) (*RFP, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, buyer_addr, service_type, description,
		       min_budget, max_budget, max_latency_ms, min_success_rate,
		       duration, min_volume, bid_deadline, auto_select,
		       min_reputation, max_counter_rounds,
		       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
		       status, winning_bid_id, contract_id, bid_count,
		       cancel_reason, awarded_at, created_at, updated_at
		FROM rfps WHERE id = $1`, id)

	rfp, err := scanRFP(row)
	if err == sql.ErrNoRows {
		return nil, ErrRFPNotFound
	}
	return rfp, err
}

func (p *PostgresStore) UpdateRFP(ctx context.Context, rfp *RFP) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE rfps SET
			status = $1, winning_bid_id = $2, contract_id = $3,
			bid_count = $4, cancel_reason = $5, awarded_at = $6,
			updated_at = $7
		WHERE id = $8`,
		string(rfp.Status), nullStr(rfp.WinningBidID), nullStr(rfp.ContractID),
		rfp.BidCount, nullStr(rfp.CancelReason), nullTime(rfp.AwardedAt),
		rfp.UpdatedAt, rfp.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrRFPNotFound
	}
	return nil
}

func (p *PostgresStore) ListOpenRFPs(ctx context.Context, serviceType string, limit int) ([]*RFP, error) {
	var query string
	var args []interface{}

	if serviceType != "" {
		query = `
			SELECT id, buyer_addr, service_type, description,
			       min_budget, max_budget, max_latency_ms, min_success_rate,
			       duration, min_volume, bid_deadline, auto_select,
			       min_reputation, max_counter_rounds,
			       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
			       status, winning_bid_id, contract_id, bid_count,
			       cancel_reason, awarded_at, created_at, updated_at
			FROM rfps
			WHERE status = 'open' AND service_type = $1
			ORDER BY created_at DESC
			LIMIT $2`
		args = []interface{}{serviceType, limit}
	} else {
		query = `
			SELECT id, buyer_addr, service_type, description,
			       min_budget, max_budget, max_latency_ms, min_success_rate,
			       duration, min_volume, bid_deadline, auto_select,
			       min_reputation, max_counter_rounds,
			       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
			       status, winning_bid_id, contract_id, bid_count,
			       cancel_reason, awarded_at, created_at, updated_at
			FROM rfps
			WHERE status = 'open'
			ORDER BY created_at DESC
			LIMIT $1`
		args = []interface{}{limit}
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListByBuyer(ctx context.Context, buyerAddr string, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, service_type, description,
		       min_budget, max_budget, max_latency_ms, min_success_rate,
		       duration, min_volume, bid_deadline, auto_select,
		       min_reputation, max_counter_rounds,
		       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
		       status, winning_bid_id, contract_id, bid_count,
		       cancel_reason, awarded_at, created_at, updated_at
		FROM rfps
		WHERE buyer_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, buyerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListExpiredRFPs(ctx context.Context, before time.Time, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, service_type, description,
		       min_budget, max_budget, max_latency_ms, min_success_rate,
		       duration, min_volume, bid_deadline, auto_select,
		       min_reputation, max_counter_rounds,
		       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
		       status, winning_bid_id, contract_id, bid_count,
		       cancel_reason, awarded_at, created_at, updated_at
		FROM rfps
		WHERE status = 'open' AND auto_select = FALSE AND bid_deadline < $1
		ORDER BY bid_deadline ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListAutoSelectReady(ctx context.Context, before time.Time, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, service_type, description,
		       min_budget, max_budget, max_latency_ms, min_success_rate,
		       duration, min_volume, bid_deadline, auto_select,
		       min_reputation, max_counter_rounds,
		       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
		       status, winning_bid_id, contract_id, bid_count,
		       cancel_reason, awarded_at, created_at, updated_at
		FROM rfps
		WHERE status = 'open' AND auto_select = TRUE AND bid_deadline < $1
		ORDER BY bid_deadline ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListStaleSelecting(ctx context.Context, before time.Time, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, service_type, description,
		       min_budget, max_budget, max_latency_ms, min_success_rate,
		       duration, min_volume, bid_deadline, auto_select,
		       min_reputation, max_counter_rounds,
		       scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
		       status, winning_bid_id, contract_id, bid_count,
		       cancel_reason, awarded_at, created_at, updated_at
		FROM rfps
		WHERE status = 'selecting' AND bid_deadline < $1
		ORDER BY bid_deadline ASC
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

// --- Bid operations ---

func (p *PostgresStore) CreateBid(ctx context.Context, bid *Bid) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO bids (
			id, rfp_id, seller_addr, price_per_call, total_budget,
			max_latency_ms, success_rate, duration, seller_penalty,
			status, score, counter_round, parent_bid_id, countered_by_id,
			message, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4::NUMERIC(20,6), $5::NUMERIC(20,6),
			$6, $7, $8, $9::NUMERIC(20,6),
			$10, $11, $12, $13, $14,
			$15, $16, $17
		)`,
		bid.ID, bid.RFPID, bid.SellerAddr, bid.PricePerCall, bid.TotalBudget,
		bid.MaxLatencyMs, bid.SuccessRate, bid.Duration, bid.SellerPenalty,
		string(bid.Status), bid.Score, bid.CounterRound, nullStr(bid.ParentBidID), nullStr(bid.CounteredByID),
		nullStr(bid.Message), bid.CreatedAt, bid.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetBid(ctx context.Context, id string) (*Bid, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, rfp_id, seller_addr, price_per_call, total_budget,
		       max_latency_ms, success_rate, duration, seller_penalty,
		       status, score, counter_round, parent_bid_id, countered_by_id,
		       message, created_at, updated_at
		FROM bids WHERE id = $1`, id)

	bid, err := scanBid(row)
	if err == sql.ErrNoRows {
		return nil, ErrBidNotFound
	}
	return bid, err
}

func (p *PostgresStore) UpdateBid(ctx context.Context, bid *Bid) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE bids SET
			status = $1, score = $2, countered_by_id = $3, updated_at = $4
		WHERE id = $5`,
		string(bid.Status), bid.Score, nullStr(bid.CounteredByID), bid.UpdatedAt, bid.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrBidNotFound
	}
	return nil
}

func (p *PostgresStore) ListBidsByRFP(ctx context.Context, rfpID string, limit int) ([]*Bid, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, rfp_id, seller_addr, price_per_call, total_budget,
		       max_latency_ms, success_rate, duration, seller_penalty,
		       status, score, counter_round, parent_bid_id, countered_by_id,
		       message, created_at, updated_at
		FROM bids
		WHERE rfp_id = $1
		ORDER BY score DESC, created_at ASC
		LIMIT $2`, rfpID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanBids(rows)
}

func (p *PostgresStore) ListActiveBidsByRFP(ctx context.Context, rfpID string) ([]*Bid, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, rfp_id, seller_addr, price_per_call, total_budget,
		       max_latency_ms, success_rate, duration, seller_penalty,
		       status, score, counter_round, parent_bid_id, countered_by_id,
		       message, created_at, updated_at
		FROM bids
		WHERE rfp_id = $1 AND status = 'pending'
		ORDER BY score DESC`, rfpID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanBids(rows)
}

func (p *PostgresStore) GetBidBySellerAndRFP(ctx context.Context, sellerAddr, rfpID string) (*Bid, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, rfp_id, seller_addr, price_per_call, total_budget,
		       max_latency_ms, success_rate, duration, seller_penalty,
		       status, score, counter_round, parent_bid_id, countered_by_id,
		       message, created_at, updated_at
		FROM bids
		WHERE rfp_id = $1 AND seller_addr = $2 AND status = 'pending'
		LIMIT 1`, rfpID, sellerAddr)

	bid, err := scanBid(row)
	if err == sql.ErrNoRows {
		return nil, ErrBidNotFound
	}
	return bid, err
}

func (p *PostgresStore) ListBidsBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Bid, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, rfp_id, seller_addr, price_per_call, total_budget,
		       max_latency_ms, success_rate, duration, seller_penalty,
		       status, score, counter_round, parent_bid_id, countered_by_id,
		       message, created_at, updated_at
		FROM bids
		WHERE seller_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, sellerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanBids(rows)
}

// --- scanners ---

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanRFP(sc scanner) (*RFP, error) {
	rfp := &RFP{}
	var (
		description  sql.NullString
		winningBid   sql.NullString
		contractID   sql.NullString
		cancelReason sql.NullString
		awardedAt    sql.NullTime
		status       string
	)

	err := sc.Scan(
		&rfp.ID, &rfp.BuyerAddr, &rfp.ServiceType, &description,
		&rfp.MinBudget, &rfp.MaxBudget, &rfp.MaxLatencyMs, &rfp.MinSuccessRate,
		&rfp.Duration, &rfp.MinVolume, &rfp.BidDeadline, &rfp.AutoSelect,
		&rfp.MinReputation, &rfp.MaxCounterRounds,
		&rfp.ScoringWeights.Price, &rfp.ScoringWeights.Reputation, &rfp.ScoringWeights.SLA,
		&status, &winningBid, &contractID, &rfp.BidCount,
		&cancelReason, &awardedAt, &rfp.CreatedAt, &rfp.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	rfp.Status = RFPStatus(status)
	rfp.Description = description.String
	rfp.WinningBidID = winningBid.String
	rfp.ContractID = contractID.String
	rfp.CancelReason = cancelReason.String
	if awardedAt.Valid {
		rfp.AwardedAt = &awardedAt.Time
	}

	return rfp, nil
}

func scanRFPs(rows *sql.Rows) ([]*RFP, error) {
	var result []*RFP
	for rows.Next() {
		r, err := scanRFP(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func scanBid(sc scanner) (*Bid, error) {
	bid := &Bid{}
	var (
		parentBid   sql.NullString
		counteredBy sql.NullString
		message     sql.NullString
		status      string
	)

	err := sc.Scan(
		&bid.ID, &bid.RFPID, &bid.SellerAddr, &bid.PricePerCall, &bid.TotalBudget,
		&bid.MaxLatencyMs, &bid.SuccessRate, &bid.Duration, &bid.SellerPenalty,
		&status, &bid.Score, &bid.CounterRound, &parentBid, &counteredBy,
		&message, &bid.CreatedAt, &bid.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	bid.Status = BidStatus(status)
	bid.ParentBidID = parentBid.String
	bid.CounteredByID = counteredBy.String
	bid.Message = message.String

	return bid, nil
}

func scanBids(rows *sql.Rows) ([]*Bid, error) {
	var result []*Bid
	for rows.Next() {
		b, err := scanBid(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, b)
	}
	return result, rows.Err()
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
