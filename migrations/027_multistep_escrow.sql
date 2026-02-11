-- MultiStep Escrow: locks funds upfront for N-step pipelines, releases per-step.
CREATE TABLE IF NOT EXISTS multistep_escrows (
    id VARCHAR(36) PRIMARY KEY,
    buyer_addr VARCHAR(42) NOT NULL,
    total_amount NUMERIC(20,6) NOT NULL,
    spent_amount NUMERIC(20,6) NOT NULL DEFAULT 0,
    total_steps INTEGER NOT NULL,
    confirmed_steps INTEGER NOT NULL DEFAULT 0,
    planned_steps JSONB NOT NULL DEFAULT '[]',
    status VARCHAR(20) NOT NULL DEFAULT 'open',
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CHECK (spent_amount >= 0),
    CHECK (spent_amount <= total_amount),
    CHECK (confirmed_steps >= 0),
    CHECK (confirmed_steps <= total_steps),
    CHECK (total_steps > 0 AND total_steps <= 100),
    CHECK (status IN ('open', 'completed', 'aborted'))
);

CREATE TABLE IF NOT EXISTS multistep_escrow_steps (
    escrow_id VARCHAR(36) NOT NULL REFERENCES multistep_escrows(id),
    step_index INTEGER NOT NULL,
    seller_addr VARCHAR(42) NOT NULL,
    amount NUMERIC(20,6) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (escrow_id, step_index),
    CHECK (amount > 0),
    CHECK (step_index >= 0)
);

CREATE INDEX IF NOT EXISTS idx_mse_buyer ON multistep_escrows(buyer_addr);
CREATE INDEX IF NOT EXISTS idx_mse_status ON multistep_escrows(status);
