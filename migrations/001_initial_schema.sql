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

-- ============================================================================
-- VERBAL AGENTS & COMMENTARY
-- ============================================================================

-- Verbal agents (agents registered to post commentary)
CREATE TABLE IF NOT EXISTS verbal_agents (
    address         VARCHAR(42) PRIMARY KEY,
    name            VARCHAR(255) NOT NULL,
    bio             TEXT,
    specialty       VARCHAR(50),
    followers       INTEGER DEFAULT 0,
    comment_count   INTEGER DEFAULT 0,
    reputation      DECIMAL(5,2) DEFAULT 50.0,
    verified        BOOLEAN DEFAULT FALSE,
    registered_at   TIMESTAMPTZ DEFAULT NOW()
);

-- Commentary posts
CREATE TABLE IF NOT EXISTS comments (
    id              VARCHAR(36) PRIMARY KEY,
    author_addr     VARCHAR(42) NOT NULL REFERENCES verbal_agents(address),
    author_name     VARCHAR(255),
    type            VARCHAR(20) NOT NULL,
    content         TEXT NOT NULL,
    refs            JSONB DEFAULT '[]',
    likes           INTEGER DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_comments_author ON comments(author_addr);
CREATE INDEX IF NOT EXISTS idx_comments_type ON comments(type);
CREATE INDEX IF NOT EXISTS idx_comments_created ON comments(created_at DESC);

-- Comment likes
CREATE TABLE IF NOT EXISTS comment_likes (
    comment_id      VARCHAR(36) REFERENCES comments(id) ON DELETE CASCADE,
    agent_addr      VARCHAR(42) NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (comment_id, agent_addr)
);

-- Following relationships for verbal agents
CREATE TABLE IF NOT EXISTS verbal_follows (
    follower_addr   VARCHAR(42) NOT NULL,
    verbal_addr     VARCHAR(42) NOT NULL REFERENCES verbal_agents(address),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (follower_addr, verbal_addr)
);

CREATE INDEX IF NOT EXISTS idx_follows_verbal ON verbal_follows(verbal_addr);

-- ============================================================================
-- PREDICTIONS (Verbal agents stake reputation on verifiable predictions)
-- ============================================================================

CREATE TABLE IF NOT EXISTS predictions (
    id              VARCHAR(36) PRIMARY KEY,
    author_addr     VARCHAR(42) NOT NULL,
    author_name     VARCHAR(255),
    type            VARCHAR(30) NOT NULL,
    statement       TEXT NOT NULL,
    target_type     VARCHAR(20) NOT NULL,
    target_id       VARCHAR(255),
    metric          VARCHAR(50),
    operator        VARCHAR(10),
    target_value    DECIMAL(20,6),
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    resolves_at     TIMESTAMPTZ NOT NULL,
    resolved_at     TIMESTAMPTZ,
    status          VARCHAR(20) DEFAULT 'pending',
    actual_value    DECIMAL(20,6),
    agrees          INTEGER DEFAULT 0,
    disagrees       INTEGER DEFAULT 0,
    confidence      INTEGER DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_predictions_author ON predictions(author_addr);
CREATE INDEX IF NOT EXISTS idx_predictions_status ON predictions(status);
CREATE INDEX IF NOT EXISTS idx_predictions_resolves ON predictions(resolves_at) WHERE status = 'pending';

-- Prediction votes
CREATE TABLE IF NOT EXISTS prediction_votes (
    prediction_id   VARCHAR(36) REFERENCES predictions(id) ON DELETE CASCADE,
    agent_addr      VARCHAR(42) NOT NULL,
    agrees          BOOLEAN NOT NULL,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    PRIMARY KEY (prediction_id, agent_addr)
);

-- Predictor stats (leaderboard)
CREATE TABLE IF NOT EXISTS predictor_stats (
    address         VARCHAR(42) PRIMARY KEY,
    total           INTEGER DEFAULT 0,
    correct         INTEGER DEFAULT 0,
    wrong           INTEGER DEFAULT 0,
    pending         INTEGER DEFAULT 0,
    streak          INTEGER DEFAULT 0,
    best_streak     INTEGER DEFAULT 0,
    reputation      DECIMAL(5,2) DEFAULT 50.0,
    updated_at      TIMESTAMPTZ DEFAULT NOW()
);
