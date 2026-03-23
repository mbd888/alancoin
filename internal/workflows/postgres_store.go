package workflows

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// PostgresStore persists workflow data in PostgreSQL.
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL-backed workflow store.
func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

const workflowColumns = `id, owner_addr, name, description, budget_total, budget_spent,
	budget_remaining, steps, status, escrow_ref, audit_trail, steps_total, steps_done,
	max_cost_per_step, max_velocity, created_at, updated_at, closed_at`

func (p *PostgresStore) Create(ctx context.Context, w *Workflow) error {
	stepsJSON, _ := json.Marshal(w.Steps)
	if w.Steps == nil {
		stepsJSON = []byte("[]")
	}
	auditJSON, _ := json.Marshal(w.AuditTrail)
	if w.AuditTrail == nil {
		auditJSON = []byte("[]")
	}

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO workflows (
			id, owner_addr, name, description, budget_total, budget_spent,
			budget_remaining, steps, status, escrow_ref, audit_trail, steps_total, steps_done,
			max_cost_per_step, max_velocity, created_at, updated_at, closed_at
		) VALUES (
			$1, $2, $3, $4, $5::NUMERIC(20,6), $6::NUMERIC(20,6),
			$7::NUMERIC(20,6), $8, $9, $10, $11, $12, $13,
			$14, $15, $16, $17, $18
		)`,
		w.ID, w.OwnerAddr, w.Name, w.Description, w.BudgetTotal, w.BudgetSpent,
		w.BudgetRemain, stepsJSON, string(w.Status), w.EscrowRef, auditJSON, w.StepsTotal, w.StepsDone,
		nullNumeric(w.MaxCostPerStep), nullFloat(w.MaxVelocity), w.CreatedAt, w.UpdatedAt, nullTime(w.ClosedAt),
	)
	return err
}

func (p *PostgresStore) Get(ctx context.Context, id string) (*Workflow, error) {
	row := p.db.QueryRowContext(ctx, `SELECT `+workflowColumns+` FROM workflows WHERE id = $1`, id)
	w, err := scanWorkflow(row)
	if err == sql.ErrNoRows {
		return nil, ErrWorkflowNotFound
	}
	return w, err
}

func (p *PostgresStore) Update(ctx context.Context, w *Workflow) error {
	stepsJSON, _ := json.Marshal(w.Steps)
	auditJSON, _ := json.Marshal(w.AuditTrail)

	result, err := p.db.ExecContext(ctx, `
		UPDATE workflows SET
			budget_spent = $1::NUMERIC(20,6), budget_remaining = $2::NUMERIC(20,6),
			steps = $3, status = $4, audit_trail = $5, steps_done = $6,
			updated_at = $7, closed_at = $8
		WHERE id = $9`,
		w.BudgetSpent, w.BudgetRemain,
		stepsJSON, string(w.Status), auditJSON, w.StepsDone,
		w.UpdatedAt, nullTime(w.ClosedAt), w.ID,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrWorkflowNotFound
	}
	return nil
}

func (p *PostgresStore) ListByOwner(ctx context.Context, ownerAddr string, limit int) ([]*Workflow, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT `+workflowColumns+`
		FROM workflows
		WHERE owner_addr = $1
		ORDER BY created_at DESC
		LIMIT $2`, ownerAddr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*Workflow
	for rows.Next() {
		w, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, w)
	}
	return result, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...interface{}) error
}

func scanWorkflow(s scanner) (*Workflow, error) {
	w := &Workflow{}
	var (
		status         string
		stepsJSON      []byte
		auditJSON      []byte
		maxCostPerStep sql.NullString
		maxVelocity    sql.NullFloat64
		closedAt       sql.NullTime
	)

	err := s.Scan(
		&w.ID, &w.OwnerAddr, &w.Name, &w.Description, &w.BudgetTotal, &w.BudgetSpent,
		&w.BudgetRemain, &stepsJSON, &status, &w.EscrowRef, &auditJSON, &w.StepsTotal, &w.StepsDone,
		&maxCostPerStep, &maxVelocity, &w.CreatedAt, &w.UpdatedAt, &closedAt,
	)
	if err != nil {
		return nil, err
	}

	w.Status = WorkflowStatus(status)
	w.MaxCostPerStep = maxCostPerStep.String
	if maxVelocity.Valid {
		w.MaxVelocity = maxVelocity.Float64
	}
	if closedAt.Valid {
		w.ClosedAt = &closedAt.Time
	}
	if len(stepsJSON) > 0 {
		_ = json.Unmarshal(stepsJSON, &w.Steps)
	}
	if len(auditJSON) > 0 {
		_ = json.Unmarshal(auditJSON, &w.AuditTrail)
	}

	return w, nil
}

func nullNumeric(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullFloat(f float64) sql.NullFloat64 {
	if f == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

// Compile-time assertion that PostgresStore implements Store.
var _ Store = (*PostgresStore)(nil)
