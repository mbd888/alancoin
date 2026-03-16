package escrow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// CoalitionPostgresStore persists coalition escrow data in PostgreSQL.
type CoalitionPostgresStore struct {
	db *sql.DB
}

// NewCoalitionPostgresStore creates a new PostgreSQL-backed coalition escrow store.
func NewCoalitionPostgresStore(db *sql.DB) *CoalitionPostgresStore {
	return &CoalitionPostgresStore{db: db}
}

func (p *CoalitionPostgresStore) Create(ctx context.Context, ce *CoalitionEscrow) error {
	membersJSON, _ := json.Marshal(ce.Members)
	tiersJSON, _ := json.Marshal(ce.QualityTiers)
	var contribJSON []byte
	if ce.Contributions != nil {
		contribJSON, _ = json.Marshal(ce.Contributions)
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO coalition_escrows (
			id, buyer_addr, oracle_addr, total_amount, split_strategy,
			members, quality_tiers, status,
			quality_score, matched_tier, payout_pct, total_payout, refund_amount,
			contributions, contract_id, auto_settle_at, settled_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4::NUMERIC(20,6), $5,
			$6, $7, $8,
			$9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18, $19
		)`,
		ce.ID, ce.BuyerAddr, ce.OracleAddr, ce.TotalAmount, string(ce.SplitStrategy),
		membersJSON, tiersJSON, string(ce.Status),
		nullFloat64(ce.QualityScore), nullString(ce.MatchedTier),
		ce.PayoutPct, nullString(ce.TotalPayout), nullString(ce.RefundAmount),
		contribJSON, nullString(ce.ContractID), ce.AutoSettleAt, nullTime(ce.SettledAt),
		ce.CreatedAt, ce.UpdatedAt,
	)
	return err
}

const coalitionColumns = `id, buyer_addr, oracle_addr, total_amount, split_strategy,
	members, quality_tiers, status,
	quality_score, matched_tier, payout_pct, total_payout, refund_amount,
	contributions, contract_id, auto_settle_at, settled_at, created_at, updated_at`

func (p *CoalitionPostgresStore) Get(ctx context.Context, id string) (*CoalitionEscrow, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+coalitionColumns+` FROM coalition_escrows WHERE id = $1`, id)
	ce, err := scanCoalition(row)
	if err == sql.ErrNoRows {
		return nil, ErrCoalitionNotFound
	}
	return ce, err
}

func (p *CoalitionPostgresStore) Update(ctx context.Context, ce *CoalitionEscrow) error {
	membersJSON, _ := json.Marshal(ce.Members)
	tiersJSON, _ := json.Marshal(ce.QualityTiers)
	var contribJSON []byte
	if ce.Contributions != nil {
		contribJSON, _ = json.Marshal(ce.Contributions)
	}

	// Optimistic lock: refuse to overwrite a terminal state.
	result, err := p.db.ExecContext(ctx, `
		UPDATE coalition_escrows SET
			members = $1, quality_tiers = $2, status = $3,
			quality_score = $4, matched_tier = $5, payout_pct = $6,
			total_payout = $7, refund_amount = $8,
			contributions = $9, contract_id = $10, settled_at = $11, updated_at = $12
		WHERE id = $13
			AND status NOT IN ('settled', 'aborted', 'expired')`,
		membersJSON, tiersJSON, string(ce.Status),
		nullFloat64(ce.QualityScore), nullString(ce.MatchedTier), ce.PayoutPct,
		nullString(ce.TotalPayout), nullString(ce.RefundAmount),
		contribJSON, nullString(ce.ContractID), nullTime(ce.SettledAt), ce.UpdatedAt,
		ce.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		// Distinguish "not found" from "already terminal": check if the row exists.
		var exists bool
		_ = p.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM coalition_escrows WHERE id = $1)`, ce.ID).Scan(&exists)
		if exists {
			return ErrAlreadyResolved
		}
		return ErrCoalitionNotFound
	}
	return nil
}

func (p *CoalitionPostgresStore) ListByAgent(ctx context.Context, agentAddr string, limit int) ([]*CoalitionEscrow, error) {
	// Use jsonb_build_array + jsonb_build_object to safely parameterize the
	// JSONB search — never concatenate user input into JSON literals.
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+coalitionColumns+` FROM coalition_escrows
		WHERE buyer_addr = $1
		   OR oracle_addr = $1
		   OR members @> jsonb_build_array(jsonb_build_object('agentAddr', $1::text))
		ORDER BY created_at DESC
		LIMIT $2`,
		agentAddr,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*CoalitionEscrow
	for rows.Next() {
		ce, err := scanCoalition(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, ce)
	}
	return result, rows.Err()
}

func (p *CoalitionPostgresStore) ListExpired(ctx context.Context, before time.Time, limit int) ([]*CoalitionEscrow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+coalitionColumns+` FROM coalition_escrows
		WHERE status NOT IN ('settled', 'aborted', 'expired')
		  AND auto_settle_at < $1
		ORDER BY auto_settle_at ASC
		LIMIT $2`,
		before, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*CoalitionEscrow
	for rows.Next() {
		ce, err := scanCoalition(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, ce)
	}
	return result, rows.Err()
}

// scanner allows both *sql.Row and *sql.Rows to use the same scan function.
type coalitionScanner interface {
	Scan(dest ...interface{}) error
}

func scanCoalition(s coalitionScanner) (*CoalitionEscrow, error) {
	ce := &CoalitionEscrow{}
	var (
		splitStrategy string
		status        string
		membersJSON   []byte
		tiersJSON     []byte
		contribJSON   []byte
		qualityScore  sql.NullFloat64
		matchedTier   sql.NullString
		totalPayout   sql.NullString
		refundAmount  sql.NullString
		contractID    sql.NullString
		settledAt     sql.NullTime
	)

	err := s.Scan(
		&ce.ID, &ce.BuyerAddr, &ce.OracleAddr, &ce.TotalAmount, &splitStrategy,
		&membersJSON, &tiersJSON, &status,
		&qualityScore, &matchedTier, &ce.PayoutPct, &totalPayout, &refundAmount,
		&contribJSON, &contractID, &ce.AutoSettleAt, &settledAt,
		&ce.CreatedAt, &ce.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	ce.SplitStrategy = SplitStrategy(splitStrategy)
	ce.Status = CoalitionStatus(status)

	if qualityScore.Valid {
		ce.QualityScore = &qualityScore.Float64
	}
	if matchedTier.Valid {
		ce.MatchedTier = matchedTier.String
	}
	if totalPayout.Valid {
		ce.TotalPayout = totalPayout.String
	}
	if refundAmount.Valid {
		ce.RefundAmount = refundAmount.String
	}
	if contractID.Valid {
		ce.ContractID = contractID.String
	}
	if settledAt.Valid {
		ce.SettledAt = &settledAt.Time
	}

	if len(membersJSON) > 0 {
		if err := json.Unmarshal(membersJSON, &ce.Members); err != nil {
			return nil, fmt.Errorf("corrupt coalition members JSON (id=%s): %w", ce.ID, err)
		}
	}
	if len(tiersJSON) > 0 {
		if err := json.Unmarshal(tiersJSON, &ce.QualityTiers); err != nil {
			return nil, fmt.Errorf("corrupt coalition quality_tiers JSON (id=%s): %w", ce.ID, err)
		}
	}
	if len(contribJSON) > 0 {
		if err := json.Unmarshal(contribJSON, &ce.Contributions); err != nil {
			return nil, fmt.Errorf("corrupt coalition contributions JSON (id=%s): %w", ce.ID, err)
		}
	}

	return ce, nil
}

func nullFloat64(f *float64) sql.NullFloat64 {
	if f == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *f, Valid: true}
}

// Compile-time assertion.
var _ CoalitionStore = (*CoalitionPostgresStore)(nil)
