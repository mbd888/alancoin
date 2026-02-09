-- Migration 006: Agent-to-Agent Delegation (A2A)
-- Adds hierarchical session key delegation support

-- Add delegation columns to session_keys table
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS parent_key_id VARCHAR(36) REFERENCES session_keys(id);
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS depth INTEGER DEFAULT 0;
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS root_key_id VARCHAR(36);
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS delegation_label VARCHAR(255);

-- Indexes for tree traversal
CREATE INDEX IF NOT EXISTS idx_session_keys_parent ON session_keys(parent_key_id) WHERE parent_key_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_session_keys_root ON session_keys(root_key_id) WHERE root_key_id IS NOT NULL;

-- Delegation audit log
CREATE TABLE IF NOT EXISTS delegation_log (
    id SERIAL PRIMARY KEY,
    parent_key_id VARCHAR(36) NOT NULL,
    child_key_id VARCHAR(36) NOT NULL,
    root_key_id VARCHAR(36) NOT NULL,
    root_owner_addr VARCHAR(42) NOT NULL,
    depth INTEGER NOT NULL,
    max_total VARCHAR(40),
    reason VARCHAR(255),
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_delegation_log_parent ON delegation_log(parent_key_id);
CREATE INDEX IF NOT EXISTS idx_delegation_log_root ON delegation_log(root_key_id);
