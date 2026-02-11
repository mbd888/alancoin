-- +goose Up
CREATE TABLE audit_log (
    id           BIGSERIAL PRIMARY KEY,
    agent_addr   VARCHAR(42) NOT NULL,
    actor_type   VARCHAR(20) NOT NULL,
    actor_id     VARCHAR(255),
    operation    VARCHAR(30) NOT NULL,
    amount       NUMERIC(20,6),
    reference    VARCHAR(255),
    before_state JSONB,
    after_state  JSONB,
    request_id   VARCHAR(64),
    ip_address   VARCHAR(45),
    description  TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_audit_agent ON audit_log(agent_addr, created_at DESC);
CREATE INDEX idx_audit_actor ON audit_log(actor_id);
CREATE INDEX idx_audit_operation ON audit_log(operation);

-- +goose Down
DROP TABLE IF EXISTS audit_log;
