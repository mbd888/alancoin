-- +goose Up
-- Behavioral contracts for coalition escrow SLA enforcement

CREATE TABLE contracts (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT,
    preconditions   JSONB NOT NULL DEFAULT '[]',
    invariants      JSONB NOT NULL DEFAULT '[]',
    recovery        TEXT NOT NULL CHECK (recovery IN ('abort', 'degrade', 'alert')),
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'passed', 'violated', 'expired')),
    bound_escrow_id TEXT,
    violations      JSONB NOT NULL DEFAULT '[]',
    soft_violations INTEGER NOT NULL DEFAULT 0,
    hard_violations INTEGER NOT NULL DEFAULT 0,
    quality_penalty DOUBLE PRECISION NOT NULL DEFAULT 0.0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_contracts_status ON contracts (status) WHERE status IN ('draft', 'active');
CREATE INDEX idx_contracts_bound_escrow ON contracts (bound_escrow_id) WHERE bound_escrow_id IS NOT NULL;
CREATE INDEX idx_contracts_created ON contracts (created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS contracts;
