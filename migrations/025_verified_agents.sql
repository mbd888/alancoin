-- Verified agents: performance-guaranteed agents with bonds
-- Agents with proven reputation can post a bond and offer platform-backed
-- performance guarantees. If their rolling success rate drops below the
-- guaranteed threshold, the bond is partially forfeited to compensate buyers.

CREATE TABLE IF NOT EXISTS verified_agents (
    id                      TEXT PRIMARY KEY,
    agent_addr              TEXT NOT NULL UNIQUE,
    status                  TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'suspended', 'revoked', 'forfeited')),
    bond_amount             NUMERIC(20,6) NOT NULL CHECK (bond_amount >= 0),
    bond_reference          TEXT NOT NULL,
    guaranteed_success_rate NUMERIC(5,2) NOT NULL CHECK (guaranteed_success_rate BETWEEN 0 AND 100),
    sla_window_size         INTEGER NOT NULL DEFAULT 20,
    guarantee_premium_rate  NUMERIC(5,4) NOT NULL DEFAULT 0.05,
    reputation_score        NUMERIC(6,1) NOT NULL DEFAULT 0,
    reputation_tier         TEXT NOT NULL DEFAULT 'new',
    total_calls_monitored   INTEGER NOT NULL DEFAULT 0,
    violation_count         INTEGER NOT NULL DEFAULT 0,
    last_violation_at       TIMESTAMPTZ,
    last_review_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    verified_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at              TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_verified_agents_status ON verified_agents(status);
CREATE INDEX IF NOT EXISTS idx_verified_agents_agent_addr ON verified_agents(agent_addr);
