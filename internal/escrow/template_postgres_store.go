package escrow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// TemplatePostgresStore persists escrow templates in PostgreSQL.
type TemplatePostgresStore struct {
	db *sql.DB
}

// NewTemplatePostgresStore creates a new PostgreSQL-backed template store.
func NewTemplatePostgresStore(db *sql.DB) *TemplatePostgresStore {
	return &TemplatePostgresStore{db: db}
}

func (s *TemplatePostgresStore) CreateTemplate(ctx context.Context, t *EscrowTemplate) error {
	milestonesJSON, err := json.Marshal(t.Milestones)
	if err != nil {
		return fmt.Errorf("failed to marshal milestones: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO escrow_templates (id, name, creator_addr, milestones, total_amount, auto_release_hours, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		t.ID, t.Name, t.CreatorAddr, milestonesJSON, t.TotalAmount, t.AutoReleaseHours, t.CreatedAt, t.UpdatedAt,
	)
	return err
}

func (s *TemplatePostgresStore) GetTemplate(ctx context.Context, id string) (*EscrowTemplate, error) {
	var t EscrowTemplate
	var milestonesJSON []byte

	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, creator_addr, milestones, total_amount, auto_release_hours, created_at, updated_at
		 FROM escrow_templates WHERE id = $1`, id,
	).Scan(&t.ID, &t.Name, &t.CreatorAddr, &milestonesJSON, &t.TotalAmount, &t.AutoReleaseHours, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(milestonesJSON, &t.Milestones); err != nil {
		return nil, fmt.Errorf("failed to unmarshal milestones: %w", err)
	}
	return &t, nil
}

func (s *TemplatePostgresStore) ListTemplates(ctx context.Context, limit int) ([]*EscrowTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, creator_addr, milestones, total_amount, auto_release_hours, created_at, updated_at
		 FROM escrow_templates ORDER BY created_at DESC LIMIT $1`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTemplates(rows)
}

func (s *TemplatePostgresStore) ListTemplatesByCreator(ctx context.Context, creatorAddr string, limit int) ([]*EscrowTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, creator_addr, milestones, total_amount, auto_release_hours, created_at, updated_at
		 FROM escrow_templates WHERE creator_addr = $1 ORDER BY created_at DESC LIMIT $2`, creatorAddr, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanTemplates(rows)
}

func scanTemplates(rows *sql.Rows) ([]*EscrowTemplate, error) {
	var result []*EscrowTemplate
	for rows.Next() {
		var t EscrowTemplate
		var milestonesJSON []byte
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatorAddr, &milestonesJSON, &t.TotalAmount, &t.AutoReleaseHours, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(milestonesJSON, &t.Milestones); err != nil {
			return nil, fmt.Errorf("failed to unmarshal milestones: %w", err)
		}
		result = append(result, &t)
	}
	return result, rows.Err()
}

func (s *TemplatePostgresStore) CreateMilestone(ctx context.Context, m *EscrowMilestone) error {
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO escrow_milestones (escrow_id, template_id, milestone_index, name, percentage, description, criteria)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		m.EscrowID, m.TemplateID, m.MilestoneIndex, m.Name, m.Percentage, m.Description, m.Criteria,
	).Scan(&m.ID)
	return err
}

func (s *TemplatePostgresStore) GetMilestone(ctx context.Context, escrowID string, index int) (*EscrowMilestone, error) {
	var m EscrowMilestone
	err := s.db.QueryRowContext(ctx,
		`SELECT id, escrow_id, template_id, milestone_index, name, percentage, description, criteria, released, released_at, released_amount
		 FROM escrow_milestones WHERE escrow_id = $1 AND milestone_index = $2`, escrowID, index,
	).Scan(&m.ID, &m.EscrowID, &m.TemplateID, &m.MilestoneIndex, &m.Name, &m.Percentage,
		&m.Description, &m.Criteria, &m.Released, &m.ReleasedAt, &m.ReleasedAmount)
	if err == sql.ErrNoRows {
		return nil, ErrMilestoneNotFound
	}
	return &m, err
}

func (s *TemplatePostgresStore) UpdateMilestone(ctx context.Context, m *EscrowMilestone) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE escrow_milestones SET released = $1, released_at = $2, released_amount = $3
		 WHERE escrow_id = $4 AND milestone_index = $5`,
		m.Released, m.ReleasedAt, m.ReleasedAmount, m.EscrowID, m.MilestoneIndex,
	)
	return err
}

func (s *TemplatePostgresStore) ListMilestones(ctx context.Context, escrowID string) ([]*EscrowMilestone, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, escrow_id, template_id, milestone_index, name, percentage, description, criteria, released, released_at, released_amount
		 FROM escrow_milestones WHERE escrow_id = $1 ORDER BY milestone_index`, escrowID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []*EscrowMilestone
	for rows.Next() {
		var m EscrowMilestone
		if err := rows.Scan(&m.ID, &m.EscrowID, &m.TemplateID, &m.MilestoneIndex, &m.Name, &m.Percentage,
			&m.Description, &m.Criteria, &m.Released, &m.ReleasedAt, &m.ReleasedAmount); err != nil {
			return nil, err
		}
		result = append(result, &m)
	}
	return result, rows.Err()
}
