-- KYA (Know Your Agent) identity certificates
-- Stores signed identity documents for agent verification and EU AI Act compliance.

CREATE TABLE IF NOT EXISTS kya_certificates (
    id TEXT PRIMARY KEY,
    agent_addr TEXT NOT NULL,
    did TEXT NOT NULL,
    org JSONB NOT NULL,
    permissions JSONB NOT NULL DEFAULT '{}',
    reputation JSONB NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'revoked', 'expired')),
    signature TEXT NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_kya_agent_status ON kya_certificates(agent_addr, status);
CREATE INDEX IF NOT EXISTS idx_kya_tenant ON kya_certificates USING GIN (org);
CREATE INDEX IF NOT EXISTS idx_kya_expires ON kya_certificates(expires_at) WHERE status = 'active';
