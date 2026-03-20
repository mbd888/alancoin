package arbitration

import (
	"context"
	"database/sql"
	"encoding/json"
)

// PostgresStore persists arbitration cases in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed arbitration store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, c *Case) error {
	evidenceJSON, _ := json.Marshal(c.Evidence)
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO arbitration_cases (id, escrow_id, buyer_addr, seller_addr, disputed_amount, reason, status, fee, contract_id, auto_resolvable, evidence, filed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, c.ID, c.EscrowID, c.BuyerAddr, c.SellerAddr, c.DisputedAmount,
		c.Reason, c.Status, c.Fee, nullString(c.ContractID),
		c.AutoResolvable, evidenceJSON, c.FiledAt)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Case, error) {
	var c Case
	var arbiterAddr, outcome, decision, contractID sql.NullString
	var splitPct sql.NullInt32
	var resolvedAt sql.NullTime
	var evidenceJSON []byte

	err := p.db.QueryRowContext(ctx, `
		SELECT id, escrow_id, buyer_addr, seller_addr, disputed_amount, reason, status,
		       arbiter_addr, outcome, split_pct, decision, fee, contract_id, auto_resolvable, evidence, filed_at, resolved_at
		FROM arbitration_cases WHERE id = $1
	`, id).Scan(&c.ID, &c.EscrowID, &c.BuyerAddr, &c.SellerAddr, &c.DisputedAmount,
		&c.Reason, &c.Status, &arbiterAddr, &outcome, &splitPct,
		&decision, &c.Fee, &contractID, &c.AutoResolvable, &evidenceJSON, &c.FiledAt, &resolvedAt)
	if err == sql.ErrNoRows {
		return nil, ErrCaseNotFound
	}
	if err != nil {
		return nil, err
	}

	c.ArbiterAddr = arbiterAddr.String
	c.Outcome = Outcome(outcome.String)
	c.SplitPct = int(splitPct.Int32)
	c.Decision = decision.String
	c.ContractID = contractID.String
	if resolvedAt.Valid {
		c.ResolvedAt = &resolvedAt.Time
	}
	_ = json.Unmarshal(evidenceJSON, &c.Evidence)
	return &c, nil
}

func (p *PostgresStore) Update(ctx context.Context, c *Case) error {
	evidenceJSON, _ := json.Marshal(c.Evidence)
	_, err := p.db.ExecContext(ctx, `
		UPDATE arbitration_cases
		SET status = $2, arbiter_addr = $3, outcome = $4, split_pct = $5, decision = $6, evidence = $7, resolved_at = $8
		WHERE id = $1
	`, c.ID, c.Status, nullString(c.ArbiterAddr), nullString(string(c.Outcome)),
		c.SplitPct, nullString(c.Decision), evidenceJSON, c.ResolvedAt)
	return err
}

func (p *PostgresStore) ListByEscrow(ctx context.Context, escrowID string) ([]*Case, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, escrow_id, buyer_addr, seller_addr, disputed_amount, reason, status, fee, auto_resolvable, filed_at
		FROM arbitration_cases WHERE escrow_id = $1 ORDER BY filed_at DESC
	`, escrowID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCaseList(rows)
}

func (p *PostgresStore) ListOpen(ctx context.Context, limit int) ([]*Case, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, escrow_id, buyer_addr, seller_addr, disputed_amount, reason, status, fee, auto_resolvable, filed_at
		FROM arbitration_cases WHERE status IN ('open', 'assigned')
		ORDER BY filed_at DESC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanCaseList(rows)
}

func scanCaseList(rows *sql.Rows) ([]*Case, error) {
	var result []*Case
	for rows.Next() {
		var c Case
		if err := rows.Scan(&c.ID, &c.EscrowID, &c.BuyerAddr, &c.SellerAddr,
			&c.DisputedAmount, &c.Reason, &c.Status, &c.Fee, &c.AutoResolvable, &c.FiledAt); err != nil {
			return nil, err
		}
		result = append(result, &c)
	}
	return result, rows.Err()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

var _ Store = (*PostgresStore)(nil)
