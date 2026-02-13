-- +goose Up
CREATE TABLE IF NOT EXISTS gateway_sessions (
    id             TEXT        PRIMARY KEY,
    agent_addr     TEXT        NOT NULL,
    max_total      NUMERIC(20,6) NOT NULL,
    max_per_request NUMERIC(20,6) NOT NULL,
    total_spent    NUMERIC(20,6) NOT NULL DEFAULT 0,
    request_count  INT         NOT NULL DEFAULT 0,
    strategy       TEXT        NOT NULL DEFAULT 'cheapest',
    allowed_types  TEXT[],
    warn_at_percent INT        NOT NULL DEFAULT 0,
    status         TEXT        NOT NULL DEFAULT 'active',
    expires_at     TIMESTAMPTZ NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_gateway_sessions_agent ON gateway_sessions (agent_addr);
CREATE INDEX idx_gateway_sessions_expired ON gateway_sessions (expires_at)
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS gateway_request_logs (
    id            TEXT        PRIMARY KEY,
    session_id    TEXT        NOT NULL REFERENCES gateway_sessions(id),
    service_type  TEXT        NOT NULL,
    agent_called  TEXT        NOT NULL DEFAULT '',
    amount        NUMERIC(20,6) NOT NULL DEFAULT 0,
    status        TEXT        NOT NULL,
    latency_ms    BIGINT      NOT NULL DEFAULT 0,
    error         TEXT        NOT NULL DEFAULT '',
    policy_result JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_gateway_request_logs_session ON gateway_request_logs (session_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS gateway_request_logs;
DROP TABLE IF EXISTS gateway_sessions;
