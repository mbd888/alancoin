-- +goose Up
CREATE TABLE ledger_events (
    id           BIGSERIAL PRIMARY KEY,
    agent_addr   VARCHAR(42) NOT NULL,
    event_type   VARCHAR(30) NOT NULL,
    amount       NUMERIC(20,6) NOT NULL DEFAULT 0,
    reference    VARCHAR(255),
    counterparty VARCHAR(42),
    metadata     JSONB DEFAULT '{}',
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_events_agent_time ON ledger_events(agent_addr, created_at);
CREATE INDEX idx_events_type ON ledger_events(event_type);
CREATE INDEX idx_events_reference ON ledger_events(reference);

-- +goose Down
DROP TABLE IF EXISTS ledger_events;
