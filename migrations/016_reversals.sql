-- +goose Up
ALTER TABLE ledger_entries ADD COLUMN IF NOT EXISTS reversed_at TIMESTAMPTZ;
ALTER TABLE ledger_entries ADD COLUMN IF NOT EXISTS reversed_by VARCHAR(255);
ALTER TABLE ledger_entries ADD COLUMN IF NOT EXISTS reversal_of VARCHAR(36);

CREATE INDEX IF NOT EXISTS idx_ledger_reversal ON ledger_entries(reversal_of) WHERE reversal_of IS NOT NULL;

-- +goose Down
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS reversed_at;
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS reversed_by;
ALTER TABLE ledger_entries DROP COLUMN IF EXISTS reversal_of;
