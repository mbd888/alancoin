-- +goose Up

CREATE TABLE IF NOT EXISTS contracts (
    id                  VARCHAR(36) PRIMARY KEY,
    buyer_addr          VARCHAR(42) NOT NULL,
    seller_addr         VARCHAR(42) NOT NULL,
    service_type        VARCHAR(100) NOT NULL,
    price_per_call      NUMERIC(20,6) NOT NULL,
    min_volume          INTEGER NOT NULL DEFAULT 1,
    buyer_budget        NUMERIC(20,6) NOT NULL,
    seller_penalty      NUMERIC(20,6) NOT NULL DEFAULT 0,
    max_latency_ms      INTEGER NOT NULL DEFAULT 10000,
    min_success_rate    NUMERIC(5,2) NOT NULL DEFAULT 95.00,
    sla_window_size     INTEGER NOT NULL DEFAULT 20,
    status              VARCHAR(20) NOT NULL DEFAULT 'proposed',
    duration            VARCHAR(20) NOT NULL,
    starts_at           TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ,
    total_calls         INTEGER NOT NULL DEFAULT 0,
    successful_calls    INTEGER NOT NULL DEFAULT 0,
    failed_calls        INTEGER NOT NULL DEFAULT 0,
    total_latency_ms    BIGINT NOT NULL DEFAULT 0,
    budget_spent        NUMERIC(20,6) NOT NULL DEFAULT 0,
    terminated_by       VARCHAR(42),
    terminated_reason   TEXT,
    violation_details   TEXT,
    resolved_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT NOW(),
    updated_at          TIMESTAMPTZ DEFAULT NOW(),
    CONSTRAINT chk_status CHECK (status IN ('proposed','accepted','active','completed','terminated','violated','rejected')),
    CONSTRAINT chk_price CHECK (price_per_call > 0),
    CONSTRAINT chk_budget CHECK (buyer_budget > 0),
    CONSTRAINT chk_penalty CHECK (seller_penalty >= 0),
    CONSTRAINT chk_parties CHECK (buyer_addr != seller_addr)
);

CREATE TABLE IF NOT EXISTS contract_calls (
    id              VARCHAR(36) PRIMARY KEY,
    contract_id     VARCHAR(36) NOT NULL REFERENCES contracts(id),
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    latency_ms      INTEGER,
    error_message   TEXT,
    amount          NUMERIC(20,6) NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_contracts_buyer ON contracts(buyer_addr);
CREATE INDEX IF NOT EXISTS idx_contracts_seller ON contracts(seller_addr);
CREATE INDEX IF NOT EXISTS idx_contracts_status ON contracts(status);
CREATE INDEX IF NOT EXISTS idx_contracts_expires ON contracts(expires_at) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_contract_calls_contract ON contract_calls(contract_id);

-- +goose Down
DROP TABLE IF EXISTS contract_calls;
DROP TABLE IF EXISTS contracts;
