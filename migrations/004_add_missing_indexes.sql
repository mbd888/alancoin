-- +goose Up
-- Add indexes on commonly queried columns that were missing

CREATE INDEX IF NOT EXISTS idx_ledger_type ON ledger_entries(type);
CREATE INDEX IF NOT EXISTS idx_session_keys_expires ON session_keys(expires_at) WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_ledger_type;
DROP INDEX IF EXISTS idx_session_keys_expires;
