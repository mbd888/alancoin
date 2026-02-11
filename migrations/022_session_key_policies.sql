-- Session Key Policy Engine
-- Named, reusable constraint sets that attach to session keys.

CREATE TABLE IF NOT EXISTS policies (
    id           VARCHAR(36) PRIMARY KEY,
    name         VARCHAR(255) NOT NULL,
    owner_addr   VARCHAR(42) NOT NULL,
    rules        JSONB NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_policies_owner ON policies (owner_addr);

-- Join table: which policies are attached to which session keys.
-- rule_state holds per-attachment mutable counters (e.g. rate limit windows).
CREATE TABLE IF NOT EXISTS session_key_policies (
    session_key_id VARCHAR(36) NOT NULL REFERENCES session_keys(id) ON DELETE CASCADE,
    policy_id      VARCHAR(36) NOT NULL REFERENCES policies(id) ON DELETE CASCADE,
    attached_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rule_state     JSONB NOT NULL DEFAULT '{}',
    PRIMARY KEY (session_key_id, policy_id)
);
