-- +goose Up
-- Migration 028: Enhanced Delegation Audit Log
-- Adds event_type, ancestor_chain, and metadata to delegation_log for richer audit trails

ALTER TABLE delegation_log ADD COLUMN IF NOT EXISTS event_type VARCHAR(50) NOT NULL DEFAULT 'create';
ALTER TABLE delegation_log ADD COLUMN IF NOT EXISTS ancestor_chain TEXT[];
ALTER TABLE delegation_log ADD COLUMN IF NOT EXISTS metadata JSONB DEFAULT '{}';

CREATE INDEX IF NOT EXISTS idx_delegation_log_event ON delegation_log(event_type);
CREATE INDEX IF NOT EXISTS idx_delegation_log_child ON delegation_log(child_key_id);

-- +goose Down
DROP INDEX IF EXISTS idx_delegation_log_child;
DROP INDEX IF EXISTS idx_delegation_log_event;
ALTER TABLE delegation_log DROP COLUMN IF EXISTS metadata;
ALTER TABLE delegation_log DROP COLUMN IF EXISTS ancestor_chain;
ALTER TABLE delegation_log DROP COLUMN IF EXISTS event_type;
