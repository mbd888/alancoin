package chargeback

import (
	"context"
	"database/sql"
	"math/big"
	"time"

	"github.com/mbd888/alancoin/internal/usdc"
)

// PostgresStore persists chargeback data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a PostgreSQL-backed chargeback store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (p *PostgresStore) CreateCostCenter(ctx context.Context, cc *CostCenter) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO chargeback_cost_centers (id, tenant_id, name, department, project_code, monthly_budget, warn_at_percent, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, cc.ID, cc.TenantID, cc.Name, cc.Department, cc.ProjectCode,
		cc.MonthlyBudget, cc.WarnAtPercent, cc.Active, cc.CreatedAt)
	return err
}

func (p *PostgresStore) GetCostCenter(ctx context.Context, id string) (*CostCenter, error) {
	var cc CostCenter
	var projectCode sql.NullString
	err := p.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, name, department, project_code, monthly_budget, warn_at_percent, active, created_at
		FROM chargeback_cost_centers WHERE id = $1
	`, id).Scan(&cc.ID, &cc.TenantID, &cc.Name, &cc.Department, &projectCode,
		&cc.MonthlyBudget, &cc.WarnAtPercent, &cc.Active, &cc.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrCostCenterNotFound
	}
	if err != nil {
		return nil, err
	}
	cc.ProjectCode = projectCode.String
	return &cc, nil
}

func (p *PostgresStore) ListCostCenters(ctx context.Context, tenantID string) ([]*CostCenter, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, tenant_id, name, department, project_code, monthly_budget, warn_at_percent, active, created_at
		FROM chargeback_cost_centers WHERE tenant_id = $1 ORDER BY name
	`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*CostCenter
	for rows.Next() {
		var cc CostCenter
		var projectCode sql.NullString
		if err := rows.Scan(&cc.ID, &cc.TenantID, &cc.Name, &cc.Department, &projectCode,
			&cc.MonthlyBudget, &cc.WarnAtPercent, &cc.Active, &cc.CreatedAt); err != nil {
			return nil, err
		}
		cc.ProjectCode = projectCode.String
		result = append(result, &cc)
	}
	return result, rows.Err()
}

func (p *PostgresStore) UpdateCostCenter(ctx context.Context, cc *CostCenter) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE chargeback_cost_centers
		SET name = $2, department = $3, monthly_budget = $4, warn_at_percent = $5, active = $6
		WHERE id = $1
	`, cc.ID, cc.Name, cc.Department, cc.MonthlyBudget, cc.WarnAtPercent, cc.Active)
	return err
}

func (p *PostgresStore) RecordSpend(ctx context.Context, entry *SpendEntry) error {
	// ON CONFLICT DO NOTHING makes this idempotent — safe for event bus redelivery.
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO chargeback_spend (id, cost_center_id, tenant_id, agent_addr, amount, service_type, workflow_id, session_id, escrow_id, description, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (id) DO NOTHING
	`, entry.ID, entry.CostCenterID, entry.TenantID, entry.AgentAddr,
		entry.Amount, entry.ServiceType, nullStr(entry.WorkflowID),
		nullStr(entry.SessionID), nullStr(entry.EscrowID),
		nullStr(entry.Description), entry.Timestamp)
	return err
}

func (p *PostgresStore) GetSpendForPeriod(ctx context.Context, costCenterID string, from, to time.Time) ([]*SpendEntry, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT id, cost_center_id, tenant_id, agent_addr, amount, service_type, workflow_id, session_id, escrow_id, description, timestamp
		FROM chargeback_spend
		WHERE cost_center_id = $1 AND timestamp >= $2 AND timestamp < $3
		ORDER BY timestamp DESC
	`, costCenterID, from, to)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*SpendEntry
	for rows.Next() {
		var e SpendEntry
		var wfID, sessID, escID, desc sql.NullString
		if err := rows.Scan(&e.ID, &e.CostCenterID, &e.TenantID, &e.AgentAddr,
			&e.Amount, &e.ServiceType, &wfID, &sessID, &escID, &desc, &e.Timestamp); err != nil {
			return nil, err
		}
		e.WorkflowID = wfID.String
		e.SessionID = sessID.String
		e.EscrowID = escID.String
		e.Description = desc.String
		result = append(result, &e)
	}
	return result, rows.Err()
}

func (p *PostgresStore) GetTotalForPeriod(ctx context.Context, costCenterID string, from, to time.Time) (*big.Int, int, error) {
	var totalStr sql.NullString
	var count int
	err := p.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0)::TEXT, COUNT(*)
		FROM chargeback_spend
		WHERE cost_center_id = $1 AND timestamp >= $2 AND timestamp < $3
	`, costCenterID, from, to).Scan(&totalStr, &count)
	if err != nil {
		return big.NewInt(0), 0, err
	}
	total, _ := usdc.Parse(totalStr.String)
	return total, count, nil
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

var _ Store = (*PostgresStore)(nil)
