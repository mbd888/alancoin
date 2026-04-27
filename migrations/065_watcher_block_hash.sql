-- +goose Up
-- Add block_hash to watcher_checkpoints so the watcher can detect chain reorgs
-- by comparing the stored hash to the current canonical hash for the same block.

ALTER TABLE watcher_checkpoints ADD COLUMN IF NOT EXISTS block_hash TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE watcher_checkpoints DROP COLUMN IF EXISTS block_hash;
