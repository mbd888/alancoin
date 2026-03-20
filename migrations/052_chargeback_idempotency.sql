-- Add unique constraint on chargeback spend ID for idempotent inserts.
-- When event bus redelivers the same settlement event, the deterministic ID
-- (sp_ + idempotency_key) prevents double-counting.

-- Handle potential duplicates on INSERT with ON CONFLICT DO NOTHING.
-- The application generates deterministic IDs when IdempotencyKey is set.
CREATE UNIQUE INDEX IF NOT EXISTS idx_chargeback_spend_id ON chargeback_spend(id);
