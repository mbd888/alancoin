-- +goose Up
-- Budgeted multi-agent workflows with cost attribution

CREATE TABLE workflows (
    id                TEXT PRIMARY KEY,
    owner_addr        TEXT NOT NULL,
    name              TEXT NOT NULL,
    description       TEXT,
    budget_total      NUMERIC(20,6) NOT NULL CHECK (budget_total > 0),
    budget_spent      NUMERIC(20,6) NOT NULL DEFAULT 0,
    budget_remaining  NUMERIC(20,6) NOT NULL,
    steps             JSONB NOT NULL DEFAULT '[]',
    status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'completed', 'aborted', 'circuit_broken')),
    escrow_ref        TEXT NOT NULL,
    audit_trail       JSONB NOT NULL DEFAULT '[]',
    steps_total       INTEGER NOT NULL CHECK (steps_total > 0),
    steps_done        INTEGER NOT NULL DEFAULT 0,
    max_cost_per_step NUMERIC(20,6),
    max_velocity      DOUBLE PRECISION,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    closed_at         TIMESTAMPTZ
);

CREATE INDEX idx_workflows_owner ON workflows (owner_addr);
CREATE INDEX idx_workflows_status ON workflows (status) WHERE status = 'active';
CREATE INDEX idx_workflows_created ON workflows (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS workflows;
