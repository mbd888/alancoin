-- +goose Up
-- Migration 058: Add last_nonce column to session_keys for replay protection.

ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS last_nonce BIGINT DEFAULT 0;

-- +goose Down
ALTER TABLE session_keys DROP COLUMN IF EXISTS last_nonce;
