-- +goose Up
-- Migration 003: Add escrow support
-- Escrow ensures buyers only pay for services that deliver results.

-- Add escrowed column to agent_balances
ALTER TABLE agent_balances ADD COLUMN IF NOT EXISTS escrowed NUMERIC(20,6) NOT NULL DEFAULT 0;
ALTER TABLE agent_balances ADD CONSTRAINT chk_escrowed_nonneg CHECK (escrowed >= 0);

-- Escrow records table
CREATE TABLE IF NOT EXISTS escrows (
    id               VARCHAR(36) PRIMARY KEY,
    buyer_addr       VARCHAR(42) NOT NULL,
    seller_addr      VARCHAR(42) NOT NULL,
    amount           NUMERIC(20,6) NOT NULL,
    service_id       VARCHAR(255),
    session_key_id   VARCHAR(255),
    status           VARCHAR(20) NOT NULL DEFAULT 'pending',
    auto_release_at  TIMESTAMPTZ NOT NULL,
    delivered_at     TIMESTAMPTZ,
    resolved_at      TIMESTAMPTZ,
    dispute_reason   TEXT,
    resolution       TEXT,
    created_at       TIMESTAMPTZ DEFAULT NOW(),
    updated_at       TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_escrow_buyer ON escrows(buyer_addr);
CREATE INDEX IF NOT EXISTS idx_escrow_seller ON escrows(seller_addr);
CREATE INDEX IF NOT EXISTS idx_escrow_status ON escrows(status);
CREATE INDEX IF NOT EXISTS idx_escrow_auto_release ON escrows(auto_release_at) WHERE status IN ('pending', 'delivered');
