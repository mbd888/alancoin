-- +goose Up
-- Append-only hash-chain columns for receipts plus HEAD pointer table.

ALTER TABLE receipts ADD COLUMN IF NOT EXISTS scope       VARCHAR(64) NOT NULL DEFAULT 'global';
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS chain_index BIGINT      NOT NULL DEFAULT 0;
ALTER TABLE receipts ADD COLUMN IF NOT EXISTS prev_hash   VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_receipts_scope_chain  ON receipts (scope, chain_index);
CREATE UNIQUE INDEX IF NOT EXISTS uq_receipts_scope_chain ON receipts (scope, chain_index);
CREATE INDEX IF NOT EXISTS idx_receipts_scope_issued ON receipts (scope, issued_at);

CREATE TABLE IF NOT EXISTS receipt_chain_heads (
    scope      VARCHAR(64) PRIMARY KEY,
    head_hash  VARCHAR(64) NOT NULL DEFAULT '',
    head_index BIGINT      NOT NULL DEFAULT -1,
    receipt_id VARCHAR(36),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS receipt_chain_heads;
DROP INDEX IF EXISTS idx_receipts_scope_issued;
DROP INDEX IF EXISTS uq_receipts_scope_chain;
DROP INDEX IF EXISTS idx_receipts_scope_chain;
ALTER TABLE receipts DROP COLUMN IF EXISTS prev_hash;
ALTER TABLE receipts DROP COLUMN IF EXISTS chain_index;
ALTER TABLE receipts DROP COLUMN IF EXISTS scope;
