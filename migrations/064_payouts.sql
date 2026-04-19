-- +goose Up
-- Outbound USDC payouts (tracks every tx sent via PayoutService).

CREATE TABLE IF NOT EXISTS payouts (
    client_ref     VARCHAR(128) PRIMARY KEY,
    chain_id       BIGINT       NOT NULL,
    from_addr      VARCHAR(42)  NOT NULL,
    to_addr        VARCHAR(42)  NOT NULL,
    amount         NUMERIC(78,0) NOT NULL CHECK (amount > 0),
    nonce          BIGINT       NOT NULL,
    tx_hash        VARCHAR(66)  NOT NULL,
    status         VARCHAR(20)  NOT NULL CHECK (status IN ('pending','success','failed','dropped')),
    submitted_at   TIMESTAMPTZ  NOT NULL,
    finalized_at   TIMESTAMPTZ,
    receipt_block  BIGINT,
    receipt_status VARCHAR(20),
    receipt_confs  BIGINT,
    last_error     TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payouts_from_addr ON payouts (from_addr, submitted_at DESC);
CREATE INDEX IF NOT EXISTS idx_payouts_tx_hash   ON payouts (tx_hash);
CREATE INDEX IF NOT EXISTS idx_payouts_status    ON payouts (status, submitted_at DESC);

-- +goose Down
DROP TABLE IF EXISTS payouts;
