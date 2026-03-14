-- Migration 044: Delegation proof secrets
--
-- Stores root secrets for HMAC-chain delegation proofs. The root secret
-- is used server-side to verify delegation chains without database walks.
-- It is NEVER exposed via API.

ALTER TABLE session_keys
    ADD COLUMN IF NOT EXISTS root_secret BYTEA,
    ADD COLUMN IF NOT EXISTS delegation_proof JSONB;

-- Index for fast root key lookups during proof verification.
CREATE INDEX IF NOT EXISTS idx_session_keys_root_secret
    ON session_keys (id) WHERE root_secret IS NOT NULL;

COMMENT ON COLUMN session_keys.root_secret IS 'HMAC root secret for delegation proof chain. Never exposed via API.';
COMMENT ON COLUMN session_keys.delegation_proof IS 'HMAC-chain delegation proof (JSON). Enables O(1) ancestor verification.';
