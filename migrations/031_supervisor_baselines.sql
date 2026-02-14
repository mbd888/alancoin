-- Per-agent learned spending profiles (computed hourly by worker)
CREATE TABLE IF NOT EXISTS agent_baselines (
    agent_addr          VARCHAR(42) PRIMARY KEY,
    baseline_hourly_mean   NUMERIC(20,6) NOT NULL DEFAULT 0,
    baseline_hourly_stddev NUMERIC(20,6) NOT NULL DEFAULT 0,
    sample_hours        INT NOT NULL DEFAULT 0,
    last_updated        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Append-only spend events for graph persistence
CREATE TABLE IF NOT EXISTS agent_spend_events (
    id           BIGSERIAL PRIMARY KEY,
    agent_addr   VARCHAR(42) NOT NULL,
    counterparty VARCHAR(42) NOT NULL DEFAULT '',
    amount       NUMERIC(20,6) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_spend_events_agent_time
    ON agent_spend_events (agent_addr, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_spend_events_recent
    ON agent_spend_events (created_at)
    WHERE created_at > NOW() - INTERVAL '8 days';

-- Cross-tenant denial feature vectors
CREATE TABLE IF NOT EXISTS agent_denial_log (
    id              BIGSERIAL PRIMARY KEY,
    agent_addr      VARCHAR(42) NOT NULL,
    rule_name       VARCHAR(50) NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',
    amount          NUMERIC(20,6) NOT NULL,
    op_type         VARCHAR(30) NOT NULL,
    tier            VARCHAR(20) NOT NULL DEFAULT '',
    counterparty    VARCHAR(42) NOT NULL DEFAULT '',
    hourly_total    NUMERIC(20,6) NOT NULL DEFAULT 0,
    baseline_mean   NUMERIC(20,6) NOT NULL DEFAULT 0,
    baseline_stddev NUMERIC(20,6) NOT NULL DEFAULT 0,
    override_allowed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_denial_log_agent_time
    ON agent_denial_log (agent_addr, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_denial_log_rule_time
    ON agent_denial_log (rule_name, created_at DESC);
