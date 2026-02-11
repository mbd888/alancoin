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

// rfpColumns is the SELECT column list for RFPs.
const rfpColumns = `id, buyer_addr, service_type, description,
	min_budget, max_budget, max_latency_ms, min_success_rate,
	duration, min_volume, bid_deadline, auto_select,
	min_reputation, max_counter_rounds, required_bond_pct, no_withdraw_window,
	scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
	status, winning_bid_id, contract_id, bid_count,
	cancel_reason, awarded_at, created_at, updated_at`

// bidColumns is the SELECT column list for bids.
const bidColumns = `id, rfp_id, seller_addr, price_per_call, total_budget,
	max_latency_ms, success_rate, duration, seller_penalty,
	status, score, counter_round, parent_bid_id, countered_by_id,
	bond_amount, bond_status,
	message, created_at, updated_at`

// --- RFP operations ---

func (p *PostgresStore) CreateRFP(ctx context.Context, rfp *RFP) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO rfps (
			id, buyer_addr, service_type, description,
			min_budget, max_budget, max_latency_ms, min_success_rate,
			duration, min_volume, bid_deadline, auto_select,
			min_reputation, max_counter_rounds, required_bond_pct, no_withdraw_window,
			scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
			status, winning_bid_id, contract_id, bid_count,
			cancel_reason, awarded_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5::NUMERIC(20,6), $6::NUMERIC(20,6), $7, $8,
			$9, $10, $11, $12,
			$13, $14, $15, $16,
			$17, $18, $19,
			$20, $21, $22, $23,
			$24, $25, $26, $27
		)`,
		rfp.ID, rfp.BuyerAddr, rfp.ServiceType, nullStr(rfp.Description),
		rfp.MinBudget, rfp.MaxBudget, rfp.MaxLatencyMs, rfp.MinSuccessRate,
		rfp.Duration, rfp.MinVolume, rfp.BidDeadline, rfp.AutoSelect,
		rfp.MinReputation, rfp.MaxCounterRounds, rfp.RequiredBondPct, nullStr(rfp.NoWithdrawWindow),
		rfp.ScoringWeights.Price, rfp.ScoringWeights.Reputation, rfp.ScoringWeights.SLA,
		string(rfp.Status), nullStr(rfp.WinningBidID), nullStr(rfp.ContractID), rfp.BidCount,
		nullStr(rfp.CancelReason), nullTime(rfp.AwardedAt), rfp.CreatedAt, rfp.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetRFP(ctx context.Context, id string) (*RFP, error) {
	row := p.db.QueryRowContext(ctx,
		`SELECT `+rfpColumns+` FROM rfps WHERE id = $1`, id)

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
		query = `SELECT ` + rfpColumns + ` FROM rfps
			WHERE status = 'open' AND service_type = $1
			ORDER BY created_at DESC LIMIT $2`
		args = []interface{}{serviceType, limit}
	} else {
		query = `SELECT ` + rfpColumns + ` FROM rfps
			WHERE status = 'open'
			ORDER BY created_at DESC LIMIT $1`
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
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+rfpColumns+` FROM rfps
		WHERE buyer_addr = $1 ORDER BY created_at DESC LIMIT $2`,
		buyerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListExpiredRFPs(ctx context.Context, before time.Time, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+rfpColumns+` FROM rfps
		WHERE status = 'open' AND auto_select = FALSE AND bid_deadline < $1
		ORDER BY bid_deadline ASC LIMIT $2`,
		before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListAutoSelectReady(ctx context.Context, before time.Time, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+rfpColumns+` FROM rfps
		WHERE status = 'open' AND auto_select = TRUE AND bid_deadline < $1
		ORDER BY bid_deadline ASC LIMIT $2`,
		before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRFPs(rows)
}

func (p *PostgresStore) ListStaleSelecting(ctx context.Context, before time.Time, limit int) ([]*RFP, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+rfpColumns+` FROM rfps
		WHERE status = 'selecting' AND bid_deadline < $1
		ORDER BY bid_deadline ASC LIMIT $2`,
		before, limit)
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
			bond_amount, bond_status,
			message, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4::NUMERIC(20,6), $5::NUMERIC(20,6),
			$6, $7, $8, $9::NUMERIC(20,6),
			$10, $11, $12, $13, $14,
			$15::NUMERIC(20,6), $16,
			$17, $18, $19
		)`,
		bid.ID, bid.RFPID, bid.SellerAddr, bid.PricePerCall, bid.TotalBudget,
		bid.MaxLatencyMs, bid.SuccessRate, bid.Duration, bid.SellerPenalty,
		string(bid.Status), bid.Score, bid.CounterRound, nullStr(bid.ParentBidID), nullStr(bid.CounteredByID),
		bid.BondAmount, string(bid.BondStatus),
		nullStr(bid.Message), bid.CreatedAt, bid.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) GetBid(ctx context.Context, id string) (*Bid, error) {
	row := p.db.QueryRowContext(ctx,
		`SELECT `+bidColumns+` FROM bids WHERE id = $1`, id)

	bid, err := scanBid(row)
	if err == sql.ErrNoRows {
		return nil, ErrBidNotFound
	}
	return bid, err
}

func (p *PostgresStore) UpdateBid(ctx context.Context, bid *Bid) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE bids SET
			status = $1, score = $2, countered_by_id = $3,
			bond_amount = $4::NUMERIC(20,6), bond_status = $5,
			updated_at = $6
		WHERE id = $7`,
		string(bid.Status), bid.Score, nullStr(bid.CounteredByID),
		bid.BondAmount, string(bid.BondStatus),
		bid.UpdatedAt, bid.ID,
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
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+bidColumns+` FROM bids
		WHERE rfp_id = $1 ORDER BY score DESC, created_at ASC LIMIT $2`,
		rfpID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanBids(rows)
}

func (p *PostgresStore) ListActiveBidsByRFP(ctx context.Context, rfpID string) ([]*Bid, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+bidColumns+` FROM bids
		WHERE rfp_id = $1 AND status = 'pending' ORDER BY score DESC`,
		rfpID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanBids(rows)
}

func (p *PostgresStore) GetBidBySellerAndRFP(ctx context.Context, sellerAddr, rfpID string) (*Bid, error) {
	row := p.db.QueryRowContext(ctx,
		`SELECT `+bidColumns+` FROM bids
		WHERE rfp_id = $1 AND seller_addr = $2 AND status = 'pending' LIMIT 1`,
		rfpID, sellerAddr)

	bid, err := scanBid(row)
	if err == sql.ErrNoRows {
		return nil, ErrBidNotFound
	}
	return bid, err
}

func (p *PostgresStore) ListBidsBySeller(ctx context.Context, sellerAddr string, limit int) ([]*Bid, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+bidColumns+` FROM bids
		WHERE seller_addr = $1 ORDER BY created_at DESC LIMIT $2`,
		sellerAddr, limit)
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
		description      sql.NullString
		noWithdrawWindow sql.NullString
		winningBid       sql.NullString
		contractID       sql.NullString
		cancelReason     sql.NullString
		awardedAt        sql.NullTime
		status           string
	)

	err := sc.Scan(
		&rfp.ID, &rfp.BuyerAddr, &rfp.ServiceType, &description,
		&rfp.MinBudget, &rfp.MaxBudget, &rfp.MaxLatencyMs, &rfp.MinSuccessRate,
		&rfp.Duration, &rfp.MinVolume, &rfp.BidDeadline, &rfp.AutoSelect,
		&rfp.MinReputation, &rfp.MaxCounterRounds, &rfp.RequiredBondPct, &noWithdrawWindow,
		&rfp.ScoringWeights.Price, &rfp.ScoringWeights.Reputation, &rfp.ScoringWeights.SLA,
		&status, &winningBid, &contractID, &rfp.BidCount,
		&cancelReason, &awardedAt, &rfp.CreatedAt, &rfp.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	rfp.Status = RFPStatus(status)
	rfp.Description = description.String
	rfp.NoWithdrawWindow = noWithdrawWindow.String
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
		bondStatus  string
	)

	err := sc.Scan(
		&bid.ID, &bid.RFPID, &bid.SellerAddr, &bid.PricePerCall, &bid.TotalBudget,
		&bid.MaxLatencyMs, &bid.SuccessRate, &bid.Duration, &bid.SellerPenalty,
		&status, &bid.Score, &bid.CounterRound, &parentBid, &counteredBy,
		&bid.BondAmount, &bondStatus,
		&message, &bid.CreatedAt, &bid.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	bid.Status = BidStatus(status)
	bid.BondStatus = BondStatus(bondStatus)
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

func (p *PostgresStore) GetAnalytics(ctx context.Context) (*Analytics, error) {
	a := &Analytics{}

	// RFP status counts
	row := p.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COUNT(*) FILTER (WHERE status = 'open'),
			COUNT(*) FILTER (WHERE status = 'awarded'),
			COUNT(*) FILTER (WHERE status = 'expired'),
			COUNT(*) FILTER (WHERE status = 'cancelled')
		FROM rfps`)
	if err := row.Scan(&a.TotalRFPs, &a.OpenRFPs, &a.AwardedRFPs, &a.ExpiredRFPs, &a.CancelledRFPs); err != nil {
		return nil, err
	}

	// Average bids per RFP
	row = p.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(cnt), 0) FROM (
			SELECT COUNT(*) AS cnt FROM bids GROUP BY rfp_id
		) sub`)
	_ = row.Scan(&a.AvgBidsPerRFP)

	// Average bid-to-ask spread: avg((bid_budget - min_budget) / max_budget) across all bids
	row = p.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG((b.total_budget - r.min_budget) / NULLIF(r.max_budget, 0)), 0)
		FROM bids b JOIN rfps r ON b.rfp_id = r.id
		WHERE r.max_budget > 0`)
	_ = row.Scan(&a.AvgBidToAskSpread)

	// Average time to award (seconds)
	row = p.db.QueryRowContext(ctx, `
		SELECT COALESCE(AVG(EXTRACT(EPOCH FROM (awarded_at - created_at))), 0)
		FROM rfps WHERE status = 'awarded' AND awarded_at IS NOT NULL`)
	_ = row.Scan(&a.AvgTimeToAwardSecs)

	// Abandonment rate: expired with 0 bids / (expired + awarded)
	terminal := a.ExpiredRFPs + a.AwardedRFPs
	if terminal > 0 {
		var zeroBidExpired int
		row = p.db.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM rfps WHERE status = 'expired' AND bid_count = 0`)
		_ = row.Scan(&zeroBidExpired)
		a.AbandonmentRate = float64(zeroBidExpired) / float64(terminal)
	}

	// Counter-offer efficiency: % of RFPs with countered bids that ended in award
	row = p.db.QueryRowContext(ctx, `
		SELECT CASE
			WHEN COUNT(DISTINCT b.rfp_id) = 0 THEN 0
			ELSE COUNT(DISTINCT CASE WHEN r.status = 'awarded' THEN b.rfp_id END)::float /
			     COUNT(DISTINCT b.rfp_id)::float
		END
		FROM bids b JOIN rfps r ON b.rfp_id = r.id
		WHERE b.status = 'countered'`)
	_ = row.Scan(&a.CounterEfficiency)

	// Top sellers by win rate (top 10)
	rows, err := p.db.QueryContext(ctx, `
		SELECT seller_addr,
			COUNT(*) AS total_bids,
			COUNT(*) FILTER (WHERE status = 'accepted') AS wins
		FROM bids
		GROUP BY seller_addr
		HAVING COUNT(*) >= 1
		ORDER BY COUNT(*) FILTER (WHERE status = 'accepted') DESC, COUNT(*) DESC
		LIMIT 10`)
	if err == nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var s SellerWinSummary
			if err := rows.Scan(&s.SellerAddr, &s.TotalBids, &s.Wins); err != nil {
				break
			}
			if s.TotalBids > 0 {
				s.WinRate = float64(s.Wins) / float64(s.TotalBids)
			}
			a.TopSellers = append(a.TopSellers, s)
		}
	}
	if a.TopSellers == nil {
		a.TopSellers = []SellerWinSummary{}
	}

	return a, nil
}

// --- Template operations ---

const templateColumns = `id, owner_addr, name, service_type, description,
	min_budget, max_budget, max_latency_ms, min_success_rate,
	duration, min_volume, bid_deadline, auto_select,
	min_reputation, max_counter_rounds, required_bond_pct, no_withdraw_window,
	scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
	created_at`

func (p *PostgresStore) CreateTemplate(ctx context.Context, tmpl *RFPTemplate) error {
	var swPrice, swRep, swSLA sql.NullFloat64
	if tmpl.ScoringWeights != nil {
		swPrice = sql.NullFloat64{Float64: tmpl.ScoringWeights.Price, Valid: true}
		swRep = sql.NullFloat64{Float64: tmpl.ScoringWeights.Reputation, Valid: true}
		swSLA = sql.NullFloat64{Float64: tmpl.ScoringWeights.SLA, Valid: true}
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO rfp_templates (
			id, owner_addr, name, service_type, description,
			min_budget, max_budget, max_latency_ms, min_success_rate,
			duration, min_volume, bid_deadline, auto_select,
			min_reputation, max_counter_rounds, required_bond_pct, no_withdraw_window,
			scoring_weight_price, scoring_weight_reputation, scoring_weight_sla,
			created_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6::NUMERIC(20,6), $7::NUMERIC(20,6), $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16, $17,
			$18, $19, $20,
			$21
		)`,
		tmpl.ID, nullStr(tmpl.OwnerAddr), tmpl.Name, tmpl.ServiceType, nullStr(tmpl.Description),
		tmpl.MinBudget, tmpl.MaxBudget, tmpl.MaxLatencyMs, tmpl.MinSuccessRate,
		tmpl.Duration, tmpl.MinVolume, tmpl.BidDeadline, tmpl.AutoSelect,
		tmpl.MinReputation, tmpl.MaxCounterRounds, tmpl.RequiredBondPct, nullStr(tmpl.NoWithdrawWindow),
		swPrice, swRep, swSLA,
		tmpl.CreatedAt,
	)
	return err
}

func (p *PostgresStore) GetTemplate(ctx context.Context, id string) (*RFPTemplate, error) {
	row := p.db.QueryRowContext(ctx,
		`SELECT `+templateColumns+` FROM rfp_templates WHERE id = $1`, id)

	tmpl, err := scanTemplate(row)
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNotFound
	}
	return tmpl, err
}

func (p *PostgresStore) ListTemplates(ctx context.Context, ownerAddr string, limit int) ([]*RFPTemplate, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT `+templateColumns+` FROM rfp_templates
		WHERE owner_addr IS NULL OR owner_addr = '' OR owner_addr = $1
		ORDER BY created_at DESC LIMIT $2`,
		ownerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTemplates(rows)
}

func (p *PostgresStore) DeleteTemplate(ctx context.Context, id string) error {
	result, err := p.db.ExecContext(ctx, `DELETE FROM rfp_templates WHERE id = $1`, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTemplateNotFound
	}
	return nil
}

func scanTemplate(sc scanner) (*RFPTemplate, error) {
	tmpl := &RFPTemplate{}
	var (
		ownerAddr        sql.NullString
		description      sql.NullString
		noWithdrawWindow sql.NullString
		swPrice          sql.NullFloat64
		swRep            sql.NullFloat64
		swSLA            sql.NullFloat64
	)

	err := sc.Scan(
		&tmpl.ID, &ownerAddr, &tmpl.Name, &tmpl.ServiceType, &description,
		&tmpl.MinBudget, &tmpl.MaxBudget, &tmpl.MaxLatencyMs, &tmpl.MinSuccessRate,
		&tmpl.Duration, &tmpl.MinVolume, &tmpl.BidDeadline, &tmpl.AutoSelect,
		&tmpl.MinReputation, &tmpl.MaxCounterRounds, &tmpl.RequiredBondPct, &noWithdrawWindow,
		&swPrice, &swRep, &swSLA,
		&tmpl.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	tmpl.OwnerAddr = ownerAddr.String
	tmpl.Description = description.String
	tmpl.NoWithdrawWindow = noWithdrawWindow.String
	if swPrice.Valid {
		tmpl.ScoringWeights = &ScoringWeights{
			Price:      swPrice.Float64,
			Reputation: swRep.Float64,
			SLA:        swSLA.Float64,
		}
	}

	return tmpl, nil
}

func scanTemplates(rows *sql.Rows) ([]*RFPTemplate, error) {
	var result []*RFPTemplate
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
