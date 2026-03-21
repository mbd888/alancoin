-- +goose Up
-- Migration 057: Add credit line columns to agent_balances.
-- These columns support credit-aware debits and auto-repay on deposits.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'agent_balances' AND column_name = 'credit_limit'
    ) THEN
        ALTER TABLE agent_balances ADD COLUMN credit_limit NUMERIC(20,6) NOT NULL DEFAULT 0;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'agent_balances' AND column_name = 'credit_used'
    ) THEN
        ALTER TABLE agent_balances ADD COLUMN credit_used NUMERIC(20,6) NOT NULL DEFAULT 0;
    END IF;
END $$;

DO $$
BEGIN
    ALTER TABLE agent_balances ADD CONSTRAINT chk_credit_limit_nonneg CHECK (credit_limit >= 0);
EXCEPTION WHEN duplicate_object THEN
    NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE agent_balances ADD CONSTRAINT chk_credit_used_nonneg CHECK (credit_used >= 0);
EXCEPTION WHEN duplicate_object THEN
    NULL;
END $$;

DO $$
BEGIN
    ALTER TABLE agent_balances ADD CONSTRAINT chk_credit_used_lte_limit CHECK (credit_used <= credit_limit);
EXCEPTION WHEN duplicate_object THEN
    NULL;
END $$;

-- +goose Down
ALTER TABLE agent_balances DROP CONSTRAINT IF EXISTS chk_credit_used_lte_limit;
ALTER TABLE agent_balances DROP CONSTRAINT IF EXISTS chk_credit_used_nonneg;
ALTER TABLE agent_balances DROP CONSTRAINT IF EXISTS chk_credit_limit_nonneg;
ALTER TABLE agent_balances DROP COLUMN IF EXISTS credit_used;
ALTER TABLE agent_balances DROP COLUMN IF EXISTS credit_limit;
