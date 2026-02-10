-- +goose Up

CREATE TABLE IF NOT EXISTS streams (
    id                  VARCHAR(36) PRIMARY KEY,
    buyer_addr          VARCHAR(42) NOT NULL,
    seller_addr         VARCHAR(42) NOT NULL,
    service_id          VARCHAR(255),
    session_key_id      VARCHAR(255),
    hold_amount         NUMERIC(20,6) NOT NULL,
    spent_amount        NUMERIC(20,6) NOT NULL DEFAULT 0,
    price_per_tick      NUMERIC(20,6) NOT NULL,
    tick_count          INTEGER NOT NULL DEFAULT 0,
    status              VARCHAR(20) NOT NULL DEFAULT 'open',
    stale_timeout_secs  INTEGER NOT NULL DEFAULT 60,
    last_tick_at        TIMESTAMPTZ,
    closed_at           TIMESTAMPTZ,
    close_reason        VARCHAR(50),
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT chk_stream_status CHECK (status IN ('open','closed','stale_closed','disputed')),
    CONSTRAINT chk_hold_positive CHECK (hold_amount > 0),
    CONSTRAINT chk_price_positive CHECK (price_per_tick > 0),
    CONSTRAINT chk_spent_nonneg CHECK (spent_amount >= 0),
    CONSTRAINT chk_spent_lte_hold CHECK (spent_amount <= hold_amount),
    CONSTRAINT chk_stream_parties CHECK (buyer_addr != seller_addr)
);

CREATE TABLE IF NOT EXISTS stream_ticks (
    id              VARCHAR(36) PRIMARY KEY,
    stream_id       VARCHAR(36) NOT NULL REFERENCES streams(id),
    seq             INTEGER NOT NULL,
    amount          NUMERIC(20,6) NOT NULL,
    cumulative      NUMERIC(20,6) NOT NULL,
    metadata        TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT chk_tick_amount CHECK (amount > 0),
    CONSTRAINT chk_tick_seq CHECK (seq > 0)
);

CREATE INDEX IF NOT EXISTS idx_streams_buyer ON streams(buyer_addr);
CREATE INDEX IF NOT EXISTS idx_streams_seller ON streams(seller_addr);
CREATE INDEX IF NOT EXISTS idx_streams_status ON streams(status) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_streams_stale ON streams(last_tick_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_stream_ticks_stream ON stream_ticks(stream_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stream_ticks_seq ON stream_ticks(stream_id, seq);

-- +goose Down
DROP TABLE IF EXISTS stream_ticks;
DROP TABLE IF EXISTS streams;
