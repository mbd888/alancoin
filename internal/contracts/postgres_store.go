package contracts

import (
	"context"
	"database/sql"
	"time"
)

// PostgresStore persists contract data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed contract store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) Create(ctx context.Context, c *Contract) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO contracts (
			id, buyer_addr, seller_addr, service_type, price_per_call,
			min_volume, buyer_budget, seller_penalty, max_latency_ms,
			min_success_rate, sla_window_size, status, duration,
			starts_at, expires_at, total_calls, successful_calls,
			failed_calls, total_latency_ms, budget_spent,
			terminated_by, terminated_reason, violation_details,
			resolved_at, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5::NUMERIC(20,6),
			$6, $7::NUMERIC(20,6), $8::NUMERIC(20,6), $9,
			$10::NUMERIC(5,2), $11, $12, $13,
			$14, $15, $16, $17,
			$18, $19, $20::NUMERIC(20,6),
			$21, $22, $23,
			$24, $25, $26
		)`,
		c.ID, c.BuyerAddr, c.SellerAddr, c.ServiceType, c.PricePerCall,
		c.MinVolume, c.BuyerBudget, c.SellerPenalty, c.MaxLatencyMs,
		c.MinSuccessRate, c.SLAWindowSize, string(c.Status), c.Duration,
		contractNullTime(c.StartsAt), contractNullTime(c.ExpiresAt),
		c.TotalCalls, c.SuccessfulCalls,
		c.FailedCalls, c.TotalLatencyMs, c.BudgetSpent,
		contractNullString(c.TerminatedBy), contractNullString(c.TerminatedReason),
		contractNullString(c.ViolationDetails),
		contractNullTime(c.ResolvedAt), c.CreatedAt, c.UpdatedAt,
	)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Contract, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, buyer_addr, seller_addr, service_type, price_per_call,
		       min_volume, buyer_budget, seller_penalty, max_latency_ms,
		       min_success_rate, sla_window_size, status, duration,
		       starts_at, expires_at, total_calls, successful_calls,
		       failed_calls, total_latency_ms, budget_spent,
		       terminated_by, terminated_reason, violation_details,
		       resolved_at, created_at, updated_at
		FROM contracts WHERE id = $1`, id)

	c, err := scanContract(row)
	if err == sql.ErrNoRows {
		return nil, ErrContractNotFound
	}
	return c, err
}

func (p *PostgresStore) Update(ctx context.Context, c *Contract) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE contracts SET
			status = $1, starts_at = $2, expires_at = $3,
			total_calls = $4, successful_calls = $5, failed_calls = $6,
			total_latency_ms = $7, budget_spent = $8::NUMERIC(20,6),
			terminated_by = $9, terminated_reason = $10,
			violation_details = $11, resolved_at = $12, updated_at = $13
		WHERE id = $14`,
		string(c.Status), contractNullTime(c.StartsAt), contractNullTime(c.ExpiresAt),
		c.TotalCalls, c.SuccessfulCalls, c.FailedCalls,
		c.TotalLatencyMs, c.BudgetSpent,
		contractNullString(c.TerminatedBy), contractNullString(c.TerminatedReason),
		contractNullString(c.ViolationDetails), contractNullTime(c.ResolvedAt), c.UpdatedAt,
		c.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrContractNotFound
	}
	return nil
}

func (p *PostgresStore) ListByAgent(ctx context.Context, agentAddr string, status string, limit int) ([]*Contract, error) {
	var rows *sql.Rows
	var err error

	if status != "" {
		rows, err = p.db.QueryContext(ctx, `
			SELECT id, buyer_addr, seller_addr, service_type, price_per_call,
			       min_volume, buyer_budget, seller_penalty, max_latency_ms,
			       min_success_rate, sla_window_size, status, duration,
			       starts_at, expires_at, total_calls, successful_calls,
			       failed_calls, total_latency_ms, budget_spent,
			       terminated_by, terminated_reason, violation_details,
			       resolved_at, created_at, updated_at
			FROM contracts
			WHERE (buyer_addr = $1 OR seller_addr = $1)
			  AND status = $2
			ORDER BY created_at DESC
			LIMIT $3`, agentAddr, status, limit)
	} else {
		rows, err = p.db.QueryContext(ctx, `
			SELECT id, buyer_addr, seller_addr, service_type, price_per_call,
			       min_volume, buyer_budget, seller_penalty, max_latency_ms,
			       min_success_rate, sla_window_size, status, duration,
			       starts_at, expires_at, total_calls, successful_calls,
			       failed_calls, total_latency_ms, budget_spent,
			       terminated_by, terminated_reason, violation_details,
			       resolved_at, created_at, updated_at
			FROM contracts
			WHERE buyer_addr = $1 OR seller_addr = $1
			ORDER BY created_at DESC
			LIMIT $2`, agentAddr, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanContracts(rows)
}

func (p *PostgresStore) ListExpiring(ctx context.Context, before time.Time, limit int) ([]*Contract, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, seller_addr, service_type, price_per_call,
		       min_volume, buyer_budget, seller_penalty, max_latency_ms,
		       min_success_rate, sla_window_size, status, duration,
		       starts_at, expires_at, total_calls, successful_calls,
		       failed_calls, total_latency_ms, budget_spent,
		       terminated_by, terminated_reason, violation_details,
		       resolved_at, created_at, updated_at
		FROM contracts
		WHERE status = 'active' AND expires_at < $1
		ORDER BY expires_at
		LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanContracts(rows)
}

func (p *PostgresStore) ListActive(ctx context.Context, limit int) ([]*Contract, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, buyer_addr, seller_addr, service_type, price_per_call,
		       min_volume, buyer_budget, seller_penalty, max_latency_ms,
		       min_success_rate, sla_window_size, status, duration,
		       starts_at, expires_at, total_calls, successful_calls,
		       failed_calls, total_latency_ms, budget_spent,
		       terminated_by, terminated_reason, violation_details,
		       resolved_at, created_at, updated_at
		FROM contracts
		WHERE status = 'active'
		ORDER BY created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanContracts(rows)
}

func (p *PostgresStore) RecordCall(ctx context.Context, call *ContractCall) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO contract_calls (id, contract_id, status, latency_ms, error_message, amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::NUMERIC(20,6), $7)`,
		call.ID, call.ContractID, call.Status,
		call.LatencyMs, contractNullString(call.ErrorMsg),
		call.Amount, call.CreatedAt,
	)
	return err
}

func (p *PostgresStore) ListCalls(ctx context.Context, contractID string, limit int) ([]*ContractCall, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, contract_id, status, latency_ms, error_message, amount, created_at
		FROM contract_calls
		WHERE contract_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, contractID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanCalls(rows)
}

func (p *PostgresStore) GetRecentCalls(ctx context.Context, contractID string, windowSize int) ([]*ContractCall, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, contract_id, status, latency_ms, error_message, amount, created_at
		FROM contract_calls
		WHERE contract_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, contractID, windowSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	return scanCalls(rows)
}

// --- scan helpers ---

// contractScanner is satisfied by both *sql.Row and *sql.Rows.
type contractScanner interface {
	Scan(dest ...interface{}) error
}

func scanContract(s contractScanner) (*Contract, error) {
	c := &Contract{}
	var (
		startsAt     sql.NullTime
		expiresAt    sql.NullTime
		resolvedAt   sql.NullTime
		terminatedBy sql.NullString
		terminatedRn sql.NullString
		violationDt  sql.NullString
		status       string
	)

	err := s.Scan(
		&c.ID, &c.BuyerAddr, &c.SellerAddr, &c.ServiceType, &c.PricePerCall,
		&c.MinVolume, &c.BuyerBudget, &c.SellerPenalty, &c.MaxLatencyMs,
		&c.MinSuccessRate, &c.SLAWindowSize, &status, &c.Duration,
		&startsAt, &expiresAt, &c.TotalCalls, &c.SuccessfulCalls,
		&c.FailedCalls, &c.TotalLatencyMs, &c.BudgetSpent,
		&terminatedBy, &terminatedRn, &violationDt,
		&resolvedAt, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	c.Status = Status(status)
	if startsAt.Valid {
		c.StartsAt = &startsAt.Time
	}
	if expiresAt.Valid {
		c.ExpiresAt = &expiresAt.Time
	}
	if resolvedAt.Valid {
		c.ResolvedAt = &resolvedAt.Time
	}
	c.TerminatedBy = terminatedBy.String
	c.TerminatedReason = terminatedRn.String
	c.ViolationDetails = violationDt.String

	return c, nil
}

func scanContracts(rows *sql.Rows) ([]*Contract, error) {
	var result []*Contract
	for rows.Next() {
		c, err := scanContract(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

func scanCall(s contractScanner) (*ContractCall, error) {
	call := &ContractCall{}
	var (
		latencyMs sql.NullInt32
		errorMsg  sql.NullString
	)

	err := s.Scan(
		&call.ID, &call.ContractID, &call.Status,
		&latencyMs, &errorMsg, &call.Amount, &call.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	if latencyMs.Valid {
		call.LatencyMs = int(latencyMs.Int32)
	}
	call.ErrorMsg = errorMsg.String

	return call, nil
}

func scanCalls(rows *sql.Rows) ([]*ContractCall, error) {
	var result []*ContractCall
	for rows.Next() {
		c, err := scanCall(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}

// --- nullable helpers ---

func contractNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func contractNullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
