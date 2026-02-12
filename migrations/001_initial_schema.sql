-- +goose Up
-- Alancoin Database Schema

-- ============================================================================
-- AGENTS
-- ============================================================================

CREATE TABLE IF NOT EXISTS agents (
    address         VARCHAR(42) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_agents_created_at ON agents(created_at DESC);

-- ============================================================================
-- SERVICES
-- ============================================================================

CREATE TABLE IF NOT EXISTS services (
    id              VARCHAR(36) PRIMARY KEY,
    agent_address   VARCHAR(42) NOT NULL REFERENCES agents(address) ON DELETE CASCADE,
    type            VARCHAR(50) NOT NULL,
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    price           VARCHAR(32) NOT NULL,
    endpoint        TEXT,
    active          BOOLEAN DEFAULT TRUE,
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_services_agent ON services(agent_address);
CREATE INDEX IF NOT EXISTS idx_services_type ON services(type);

-- ============================================================================
-- TRANSACTIONS
-- ============================================================================

CREATE TABLE IF NOT EXISTS transactions (
    id              VARCHAR(36) PRIMARY KEY,
    tx_hash         VARCHAR(66) UNIQUE,
    from_address    VARCHAR(42) NOT NULL,
    to_address      VARCHAR(42) NOT NULL,
    amount          VARCHAR(32) NOT NULL,
    service_id      VARCHAR(36),
    status          VARCHAR(20) DEFAULT 'confirmed',
    metadata        JSONB DEFAULT '{}',
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transactions_from ON transactions(from_address);
CREATE INDEX IF NOT EXISTS idx_transactions_to ON transactions(to_address);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at DESC);

-- ============================================================================
-- AGENT STATS
-- ============================================================================

CREATE TABLE IF NOT EXISTS agent_stats (
    agent_address       VARCHAR(42) PRIMARY KEY REFERENCES agents(address) ON DELETE CASCADE,
    transaction_count   INTEGER DEFAULT 0,
    total_received      VARCHAR(32) DEFAULT '0',
    total_spent         VARCHAR(32) DEFAULT '0',
    success_rate        DECIMAL(5, 4) DEFAULT 1.0,
    last_active         TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ DEFAULT NOW()
);

-- ============================================================================
-- SESSION KEYS
-- ============================================================================

CREATE TABLE IF NOT EXISTS session_keys (
    id                      VARCHAR(36) PRIMARY KEY,
    owner_address           VARCHAR(42) NOT NULL,
    public_key              TEXT,
    max_per_transaction     VARCHAR(32),
    max_per_day             VARCHAR(32),
    max_total               VARCHAR(32),
    valid_after             TIMESTAMPTZ,
    expires_at              TIMESTAMPTZ NOT NULL,
    allowed_recipients      TEXT[],
    allowed_service_types   TEXT[],
    allow_any               BOOLEAN DEFAULT FALSE,
    label                   VARCHAR(255),
    transaction_count       INTEGER DEFAULT 0,
    total_spent             VARCHAR(32) DEFAULT '0',
    spent_today             VARCHAR(32) DEFAULT '0',
    last_reset_day          VARCHAR(10),
    last_used               TIMESTAMPTZ,
    revoked_at              TIMESTAMPTZ,
    created_at              TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_session_keys_owner ON session_keys(owner_address);

-- ============================================================================
-- API KEYS
-- ============================================================================

CREATE TABLE IF NOT EXISTS api_keys (
    id              VARCHAR(36) PRIMARY KEY,
    hash            VARCHAR(64) NOT NULL UNIQUE,
    agent_address   VARCHAR(42) NOT NULL,
    name            VARCHAR(255),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    last_used       TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ,
    revoked         BOOLEAN DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(hash);
CREATE INDEX IF NOT EXISTS idx_api_keys_agent ON api_keys(agent_address);

-- ============================================================================
-- AGENT BALANCES (Platform Ledger)
-- ============================================================================

CREATE TABLE IF NOT EXISTS agent_balances (
    agent_address   VARCHAR(42) PRIMARY KEY,
    available       VARCHAR(32) DEFAULT '0',
    pending         VARCHAR(32) DEFAULT '0',
    total_in        VARCHAR(32) DEFAULT '0',
    total_out       VARCHAR(32) DEFAULT '0',
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id              VARCHAR(36) PRIMARY KEY,
    agent_address   VARCHAR(42) NOT NULL,
    type            VARCHAR(20) NOT NULL,
    amount          VARCHAR(32) NOT NULL,
    tx_hash         VARCHAR(66),
    reference       VARCHAR(255),
    description     TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_ledger_agent ON ledger_entries(agent_address);
CREATE INDEX IF NOT EXISTS idx_ledger_tx ON ledger_entries(tx_hash);
CREATE INDEX IF NOT EXISTS idx_ledger_created ON ledger_entries(created_at DESC);

-- ============================================================================
-- WEBHOOKS
-- ============================================================================

CREATE TABLE IF NOT EXISTS webhooks (
    id              VARCHAR(36) PRIMARY KEY,
    agent_address   VARCHAR(42) NOT NULL,
    url             TEXT NOT NULL,
    secret          VARCHAR(64) NOT NULL,
    events          JSONB NOT NULL,
    active          BOOLEAN DEFAULT TRUE,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    last_success    TIMESTAMPTZ,
    last_error      TEXT
);

CREATE INDEX IF NOT EXISTS idx_webhooks_agent ON webhooks(agent_address);
CREATE INDEX IF NOT EXISTS idx_webhooks_active ON webhooks(active) WHERE active = TRUE;

-- +goose Down
DROP TABLE IF EXISTS webhooks;
DROP TABLE IF EXISTS ledger_entries;
DROP TABLE IF EXISTS agent_balances;
DROP TABLE IF EXISTS api_keys;
DROP TABLE IF EXISTS session_keys;
DROP TABLE IF EXISTS agent_stats;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS services;
DROP TABLE IF EXISTS agents;
