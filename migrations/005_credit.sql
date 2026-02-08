-- +goose Up
ALTER TABLE agent_balances ADD COLUMN IF NOT EXISTS credit_limit NUMERIC(20,6) NOT NULL DEFAULT 0;
ALTER TABLE agent_balances ADD COLUMN IF NOT EXISTS credit_used NUMERIC(20,6) NOT NULL DEFAULT 0;
ALTER TABLE agent_balances ADD CONSTRAINT chk_credit_limit_nonneg CHECK (credit_limit >= 0);
ALTER TABLE agent_balances ADD CONSTRAINT chk_credit_used_nonneg CHECK (credit_used >= 0);
ALTER TABLE agent_balances ADD CONSTRAINT chk_credit_used_lte_limit CHECK (credit_used <= credit_limit);

CREATE TABLE IF NOT EXISTS credit_lines (
    id                VARCHAR(36) PRIMARY KEY,
    agent_address     VARCHAR(42) NOT NULL,
    credit_limit      NUMERIC(20,6) NOT NULL,
    credit_used       NUMERIC(20,6) NOT NULL DEFAULT 0,
    interest_rate     NUMERIC(10,6) NOT NULL DEFAULT 0,
    status            VARCHAR(20) NOT NULL DEFAULT 'active',
    reputation_tier   VARCHAR(20) NOT NULL,
    reputation_score  NUMERIC(10,2) NOT NULL DEFAULT 0,
    approved_at       TIMESTAMPTZ,
    last_review_at    TIMESTAMPTZ,
    defaulted_at      TIMESTAMPTZ,
    revoked_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ DEFAULT NOW(),
    updated_at        TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_credit_lines_agent ON credit_lines(agent_address);
CREATE INDEX IF NOT EXISTS idx_credit_lines_status ON credit_lines(status);

-- +goose Down
DROP TABLE IF EXISTS credit_lines;
ALTER TABLE agent_balances DROP CONSTRAINT IF EXISTS chk_credit_used_lte_limit;
ALTER TABLE agent_balances DROP CONSTRAINT IF EXISTS chk_credit_used_nonneg;
ALTER TABLE agent_balances DROP CONSTRAINT IF EXISTS chk_credit_limit_nonneg;
ALTER TABLE agent_balances DROP COLUMN IF EXISTS credit_used;
ALTER TABLE agent_balances DROP COLUMN IF EXISTS credit_limit;
