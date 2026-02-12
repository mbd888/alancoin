-- +goose Up

CREATE TABLE escrow_templates (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    creator_addr VARCHAR(42) NOT NULL,
    milestones JSONB NOT NULL,
    total_amount NUMERIC(20,6) NOT NULL CHECK (total_amount > 0),
    auto_release_hours INT NOT NULL DEFAULT 24,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE escrow_milestones (
    id SERIAL PRIMARY KEY,
    escrow_id VARCHAR(36) NOT NULL REFERENCES escrows(id),
    template_id VARCHAR(36) NOT NULL REFERENCES escrow_templates(id),
    milestone_index INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    percentage INT NOT NULL CHECK (percentage > 0 AND percentage <= 100),
    description TEXT,
    criteria TEXT,
    released BOOLEAN NOT NULL DEFAULT FALSE,
    released_at TIMESTAMPTZ,
    released_amount NUMERIC(20,6),
    UNIQUE (escrow_id, milestone_index)
);

ALTER TABLE escrows ADD COLUMN IF NOT EXISTS template_id VARCHAR(36) REFERENCES escrow_templates(id);

CREATE INDEX idx_escrow_templates_creator ON escrow_templates(creator_addr);
CREATE INDEX idx_escrow_milestones_escrow ON escrow_milestones(escrow_id);

-- +goose Down

DROP INDEX IF EXISTS idx_escrow_milestones_escrow;
DROP INDEX IF EXISTS idx_escrow_templates_creator;
ALTER TABLE escrows DROP COLUMN IF EXISTS template_id;
DROP TABLE IF EXISTS escrow_milestones;
DROP TABLE IF EXISTS escrow_templates;
