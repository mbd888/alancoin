-- +goose Up
-- Composite index for credit_draw_hold lookups in ReleaseHold/ConfirmHold.
-- Replaces sequential scan on idx_ledger_agent (agent_address only) followed
-- by in-memory filter on type + reference.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ledger_agent_type_ref
    ON ledger_entries(agent_address, type, reference);

-- +goose Down
DROP INDEX IF EXISTS idx_ledger_agent_type_ref;
