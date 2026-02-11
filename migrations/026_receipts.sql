-- 026_receipts.sql: Cryptographic receipt signing for all payment paths
CREATE TABLE IF NOT EXISTS receipts (
    id           VARCHAR(36) PRIMARY KEY,
    payment_path VARCHAR(20) NOT NULL CHECK (payment_path IN ('gateway','stream','session_key','escrow')),
    reference    VARCHAR(255) NOT NULL,
    from_addr    VARCHAR(42) NOT NULL,
    to_addr      VARCHAR(42) NOT NULL,
    amount       NUMERIC(20,6) NOT NULL CHECK (amount > 0),
    service_id   VARCHAR(255),
    status       VARCHAR(20) NOT NULL CHECK (status IN ('confirmed','failed')),
    payload_hash VARCHAR(64) NOT NULL,
    signature    VARCHAR(128) NOT NULL,
    issued_at    TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    metadata     TEXT,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_receipts_from_addr ON receipts (from_addr, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_receipts_to_addr ON receipts (to_addr, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_receipts_reference ON receipts (reference);
CREATE INDEX IF NOT EXISTS idx_receipts_payment_path ON receipts (payment_path, created_at DESC);
