-- +goose Up
CREATE TABLE balance_alert_configs (
    id          VARCHAR(36) PRIMARY KEY,
    agent_addr  VARCHAR(42) NOT NULL,
    alert_type  VARCHAR(30) NOT NULL,
    threshold   NUMERIC(20,6),
    webhook_url TEXT,
    enabled     BOOLEAN DEFAULT TRUE,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_alert_configs_agent ON balance_alert_configs(agent_addr);

CREATE TABLE balance_alerts (
    id          BIGSERIAL PRIMARY KEY,
    config_id   VARCHAR(36) REFERENCES balance_alert_configs(id),
    agent_addr  VARCHAR(42) NOT NULL,
    alert_type  VARCHAR(30) NOT NULL,
    message     TEXT,
    metadata    JSONB DEFAULT '{}',
    acknowledged BOOLEAN DEFAULT FALSE,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_alerts_agent ON balance_alerts(agent_addr, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS balance_alerts;
DROP TABLE IF EXISTS balance_alert_configs;
