-- 056_webhooks_consecutive_failures.sql: Add consecutive_failures column to webhooks table.
-- The postgres_store reads and writes this column but it was missing from migrations.

ALTER TABLE webhooks ADD COLUMN IF NOT EXISTS consecutive_failures INTEGER NOT NULL DEFAULT 0;
