-- +goose Up
-- Composite indexes for common multi-column query patterns.

-- Ledger: agent_address + type (refund idempotency, credit_draw_hold lookups)
CREATE INDEX IF NOT EXISTS idx_ledger_agent_type
    ON ledger_entries(agent_address, type);

-- Escrow: cursor pagination by buyer/seller + created_at
CREATE INDEX IF NOT EXISTS idx_escrow_buyer_created
    ON escrows(buyer_addr, created_at DESC, id);
CREATE INDEX IF NOT EXISTS idx_escrow_seller_created
    ON escrows(seller_addr, created_at DESC, id);

-- Streams: buyer/seller + created_at for ListByAgent
CREATE INDEX IF NOT EXISTS idx_streams_buyer_created
    ON streams(buyer_addr, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_streams_seller_created
    ON streams(seller_addr, created_at DESC);

-- Gateway sessions: agent/tenant cursor pagination + expired session scan
CREATE INDEX IF NOT EXISTS idx_gateway_sessions_agent_created
    ON gateway_sessions(agent_addr, created_at DESC, id);
CREATE INDEX IF NOT EXISTS idx_gateway_sessions_tenant_created
    ON gateway_sessions(tenant_id, created_at DESC, id);
CREATE INDEX IF NOT EXISTS idx_gateway_sessions_active_expires
    ON gateway_sessions(status, expires_at ASC) WHERE status = 'active';

-- Webhooks: GIN index on events for @> containment queries
CREATE INDEX IF NOT EXISTS idx_webhooks_active_events
    ON webhooks USING GIN(events) WHERE active = TRUE;

-- +goose Down
DROP INDEX IF EXISTS idx_webhooks_active_events;
DROP INDEX IF EXISTS idx_gateway_sessions_active_expires;
DROP INDEX IF EXISTS idx_gateway_sessions_tenant_created;
DROP INDEX IF EXISTS idx_gateway_sessions_agent_created;
DROP INDEX IF EXISTS idx_streams_seller_created;
DROP INDEX IF EXISTS idx_streams_buyer_created;
DROP INDEX IF EXISTS idx_escrow_seller_created;
DROP INDEX IF EXISTS idx_escrow_buyer_created;
DROP INDEX IF EXISTS idx_ledger_agent_type;
