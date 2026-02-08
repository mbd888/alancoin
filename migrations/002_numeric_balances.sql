-- +goose Up
-- Migration 002: Fix ledger balance types
-- Converts VARCHAR balance columns to NUMERIC(20,6) for safe arithmetic.
-- Adds CHECK constraints to prevent negative balances.
-- This fixes the double-spend vulnerability caused by string-based arithmetic.

-- ============================================================================
-- AGENT BALANCES: VARCHAR → NUMERIC(20,6)
-- ============================================================================

ALTER TABLE agent_balances
    ALTER COLUMN available TYPE NUMERIC(20,6) USING COALESCE(NULLIF(available, ''), '0')::NUMERIC(20,6),
    ALTER COLUMN pending   TYPE NUMERIC(20,6) USING COALESCE(NULLIF(pending, ''), '0')::NUMERIC(20,6),
    ALTER COLUMN total_in  TYPE NUMERIC(20,6) USING COALESCE(NULLIF(total_in, ''), '0')::NUMERIC(20,6),
    ALTER COLUMN total_out TYPE NUMERIC(20,6) USING COALESCE(NULLIF(total_out, ''), '0')::NUMERIC(20,6);

ALTER TABLE agent_balances
    ALTER COLUMN available SET DEFAULT 0,
    ALTER COLUMN pending   SET DEFAULT 0,
    ALTER COLUMN total_in  SET DEFAULT 0,
    ALTER COLUMN total_out SET DEFAULT 0;

ALTER TABLE agent_balances
    ADD CONSTRAINT chk_available_nonneg CHECK (available >= 0),
    ADD CONSTRAINT chk_pending_nonneg   CHECK (pending >= 0),
    ADD CONSTRAINT chk_total_in_nonneg  CHECK (total_in >= 0);

-- ============================================================================
-- LEDGER ENTRIES: amount VARCHAR → NUMERIC(20,6)
-- ============================================================================

ALTER TABLE ledger_entries
    ALTER COLUMN amount TYPE NUMERIC(20,6) USING COALESCE(NULLIF(amount, ''), '0')::NUMERIC(20,6);

-- ============================================================================
-- TRANSACTIONS: amount VARCHAR → NUMERIC(20,6)
-- ============================================================================

ALTER TABLE transactions
    ALTER COLUMN amount TYPE NUMERIC(20,6) USING COALESCE(NULLIF(amount, ''), '0')::NUMERIC(20,6);

-- ============================================================================
-- AGENT STATS: totals VARCHAR → NUMERIC(20,6)
-- ============================================================================

ALTER TABLE agent_stats
    ALTER COLUMN total_received TYPE NUMERIC(20,6) USING COALESCE(NULLIF(total_received, ''), '0')::NUMERIC(20,6),
    ALTER COLUMN total_spent    TYPE NUMERIC(20,6) USING COALESCE(NULLIF(total_spent, ''), '0')::NUMERIC(20,6);

ALTER TABLE agent_stats
    ALTER COLUMN total_received SET DEFAULT 0,
    ALTER COLUMN total_spent    SET DEFAULT 0;

-- ============================================================================
-- SERVICES: price VARCHAR → NUMERIC(20,6)
-- ============================================================================

ALTER TABLE services
    ALTER COLUMN price TYPE NUMERIC(20,6) USING COALESCE(NULLIF(price, ''), '0')::NUMERIC(20,6);

-- ============================================================================
-- SESSION KEYS: spending limits VARCHAR → NUMERIC(20,6)
-- ============================================================================

ALTER TABLE session_keys
    ALTER COLUMN max_per_transaction TYPE NUMERIC(20,6) USING NULLIF(max_per_transaction, '')::NUMERIC(20,6),
    ALTER COLUMN max_per_day         TYPE NUMERIC(20,6) USING NULLIF(max_per_day, '')::NUMERIC(20,6),
    ALTER COLUMN max_total           TYPE NUMERIC(20,6) USING NULLIF(max_total, '')::NUMERIC(20,6),
    ALTER COLUMN total_spent         TYPE NUMERIC(20,6) USING COALESCE(NULLIF(total_spent, ''), '0')::NUMERIC(20,6),
    ALTER COLUMN spent_today         TYPE NUMERIC(20,6) USING COALESCE(NULLIF(spent_today, ''), '0')::NUMERIC(20,6);

ALTER TABLE session_keys
    ALTER COLUMN total_spent SET DEFAULT 0,
    ALTER COLUMN spent_today SET DEFAULT 0;

-- +goose Down
ALTER TABLE session_keys
    ALTER COLUMN max_per_transaction TYPE VARCHAR(32),
    ALTER COLUMN max_per_day         TYPE VARCHAR(32),
    ALTER COLUMN max_total           TYPE VARCHAR(32),
    ALTER COLUMN total_spent         TYPE VARCHAR(32),
    ALTER COLUMN spent_today         TYPE VARCHAR(32);
ALTER TABLE services ALTER COLUMN price TYPE VARCHAR(32);
ALTER TABLE agent_stats
    ALTER COLUMN total_received TYPE VARCHAR(32),
    ALTER COLUMN total_spent    TYPE VARCHAR(32);
ALTER TABLE transactions ALTER COLUMN amount TYPE VARCHAR(32);
ALTER TABLE ledger_entries ALTER COLUMN amount TYPE VARCHAR(32);
ALTER TABLE agent_balances
    DROP CONSTRAINT IF EXISTS chk_available_nonneg,
    DROP CONSTRAINT IF EXISTS chk_pending_nonneg,
    DROP CONSTRAINT IF EXISTS chk_total_in_nonneg;
ALTER TABLE agent_balances
    ALTER COLUMN available TYPE VARCHAR(32),
    ALTER COLUMN pending   TYPE VARCHAR(32),
    ALTER COLUMN total_in  TYPE VARCHAR(32),
    ALTER COLUMN total_out TYPE VARCHAR(32);
