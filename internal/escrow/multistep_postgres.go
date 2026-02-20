package escrow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// MultiStepPostgresStore persists multistep escrow data in PostgreSQL.
type MultiStepPostgresStore struct {
	db *sql.DB
}

// NewMultiStepPostgresStore creates a new PostgreSQL-backed multistep escrow store.
func NewMultiStepPostgresStore(db *sql.DB) *MultiStepPostgresStore {
	return &MultiStepPostgresStore{db: db}
}

func (p *MultiStepPostgresStore) Create(ctx context.Context, mse *MultiStepEscrow) error {
	plannedJSON, _ := json.Marshal(mse.PlannedSteps)
	if mse.PlannedSteps == nil {
		plannedJSON = []byte("[]")
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO multistep_escrows (
			id, buyer_addr, total_amount, spent_amount, total_steps,
			confirmed_steps, planned_steps, status, created_at, updated_at
		) VALUES ($1, $2, $3::NUMERIC(20,6), $4::NUMERIC(20,6), $5, $6, $7, $8, $9, $10)`,
		mse.ID, mse.BuyerAddr, mse.TotalAmount, mse.SpentAmount,
		mse.TotalSteps, mse.ConfirmedSteps, plannedJSON, string(mse.Status),
		mse.CreatedAt, mse.UpdatedAt,
	)
	return err
}

func (p *MultiStepPostgresStore) Get(ctx context.Context, id string) (*MultiStepEscrow, error) {
	mse := &MultiStepEscrow{}
	var status string
	var plannedJSON []byte

	err := p.db.QueryRowContext(ctx, `
		SELECT id, buyer_addr, total_amount, spent_amount, total_steps,
		       confirmed_steps, planned_steps, status, created_at, updated_at
		FROM multistep_escrows WHERE id = $1`, id).Scan(
		&mse.ID, &mse.BuyerAddr, &mse.TotalAmount, &mse.SpentAmount,
		&mse.TotalSteps, &mse.ConfirmedSteps, &plannedJSON, &status,
		&mse.CreatedAt, &mse.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrMultiStepNotFound
	}
	if err != nil {
		return nil, err
	}
	mse.Status = MultiStepStatus(status)
	if len(plannedJSON) > 0 {
		_ = json.Unmarshal(plannedJSON, &mse.PlannedSteps)
	}
	return mse, nil
}

func (p *MultiStepPostgresStore) RecordStep(ctx context.Context, id string, step Step) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Check for duplicate step within the transaction
	var exists bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM multistep_escrow_steps
			WHERE escrow_id = $1 AND step_index = $2
		)`, id, step.StepIndex).Scan(&exists)
	if err != nil {
		return err
	}
	if exists {
		return ErrDuplicateStep
	}

	// Insert step
	_, err = tx.ExecContext(ctx, `
		INSERT INTO multistep_escrow_steps (escrow_id, step_index, seller_addr, amount, created_at)
		VALUES ($1, $2, $3, $4::NUMERIC(20,6), $5)`,
		id, step.StepIndex, step.SellerAddr, step.Amount, step.CreatedAt,
	)
	if err != nil {
		return err
	}

	// Update escrow counters atomically
	result, err := tx.ExecContext(ctx, `
		UPDATE multistep_escrows
		SET spent_amount = spent_amount + $1::NUMERIC(20,6),
		    confirmed_steps = confirmed_steps + 1,
		    updated_at = $2
		WHERE id = $3 AND status = 'open'`,
		step.Amount, time.Now(), id,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrMultiStepNotFound
	}

	return tx.Commit()
}

// DeleteStep reverses a RecordStep: removes the step row and decrements counters.
// Used as a rollback when fund release fails after step recording.
func (p *MultiStepPostgresStore) DeleteStep(ctx context.Context, id string, stepIndex int) error {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Get step amount before deleting
	var amount string
	err = tx.QueryRowContext(ctx, `
		SELECT amount FROM multistep_escrow_steps
		WHERE escrow_id = $1 AND step_index = $2`,
		id, stepIndex).Scan(&amount)
	if err != nil {
		return fmt.Errorf("failed to find step for rollback: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		DELETE FROM multistep_escrow_steps
		WHERE escrow_id = $1 AND step_index = $2`,
		id, stepIndex)
	if err != nil {
		return fmt.Errorf("failed to delete step: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE multistep_escrows
		SET spent_amount = spent_amount - $1::NUMERIC(20,6),
		    confirmed_steps = confirmed_steps - 1,
		    updated_at = $2
		WHERE id = $3 AND status = 'open'`,
		amount, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update counters: %w", err)
	}

	return tx.Commit()
}

func (p *MultiStepPostgresStore) Abort(ctx context.Context, id string) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE multistep_escrows SET status = 'aborted', updated_at = $1
		WHERE id = $2 AND status = 'open'`,
		time.Now(), id,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrMultiStepNotFound
	}
	return nil
}

func (p *MultiStepPostgresStore) Complete(ctx context.Context, id string) error {
	result, err := p.db.ExecContext(ctx, `
		UPDATE multistep_escrows SET status = 'completed', updated_at = $1
		WHERE id = $2 AND status = 'open'`,
		time.Now(), id,
	)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrMultiStepNotFound
	}
	return nil
}

// Compile-time assertion.
var _ MultiStepStore = (*MultiStepPostgresStore)(nil)
