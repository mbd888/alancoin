-- Migration 040: Watcher checkpoint storage for on-chain deposit monitoring

CREATE TABLE IF NOT EXISTS watcher_checkpoints (
    key          TEXT PRIMARY KEY,
    block_number BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
