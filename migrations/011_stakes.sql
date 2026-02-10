-- +goose Up
-- Agent Revenue Staking: investable revenue shares with automatic distribution

CREATE TABLE stakes (
    id              TEXT PRIMARY KEY,
    agent_addr      TEXT NOT NULL,
    revenue_share_bps INT NOT NULL CHECK (revenue_share_bps > 0 AND revenue_share_bps <= 5000),
    total_shares    INT NOT NULL CHECK (total_shares > 0),
    available_shares INT NOT NULL CHECK (available_shares >= 0),
    price_per_share NUMERIC(20,6) NOT NULL CHECK (price_per_share > 0),
    vesting_period  TEXT NOT NULL DEFAULT '90d',
    distribution_freq TEXT NOT NULL DEFAULT 'weekly' CHECK (distribution_freq IN ('daily', 'weekly', 'monthly')),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'paused', 'closed')),
    total_raised    NUMERIC(20,6) NOT NULL DEFAULT 0,
    total_distributed NUMERIC(20,6) NOT NULL DEFAULT 0,
    undistributed   NUMERIC(20,6) NOT NULL DEFAULT 0,
    last_distributed_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_stakes_agent ON stakes(agent_addr);
CREATE INDEX idx_stakes_status ON stakes(status);
CREATE INDEX idx_stakes_distribution ON stakes(status, undistributed, last_distributed_at)
    WHERE status = 'open';

CREATE TABLE stake_holdings (
    id              TEXT PRIMARY KEY,
    stake_id        TEXT NOT NULL REFERENCES stakes(id),
    investor_addr   TEXT NOT NULL,
    shares          INT NOT NULL CHECK (shares >= 0),
    cost_basis      NUMERIC(20,6) NOT NULL DEFAULT 0,
    vested_at       TIMESTAMPTZ NOT NULL,
    status          TEXT NOT NULL DEFAULT 'vesting' CHECK (status IN ('vesting', 'active', 'liquidated')),
    total_earned    NUMERIC(20,6) NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_holdings_stake ON stake_holdings(stake_id);
CREATE INDEX idx_holdings_investor ON stake_holdings(investor_addr);
CREATE INDEX idx_holdings_active ON stake_holdings(stake_id, status)
    WHERE status IN ('vesting', 'active');

CREATE TABLE stake_distributions (
    id              TEXT PRIMARY KEY,
    stake_id        TEXT NOT NULL REFERENCES stakes(id),
    agent_addr      TEXT NOT NULL,
    revenue_amount  NUMERIC(20,6) NOT NULL,
    share_amount    NUMERIC(20,6) NOT NULL,
    per_share_amount NUMERIC(20,6) NOT NULL,
    share_count     INT NOT NULL,
    holding_count   INT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('completed', 'partial')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_distributions_stake ON stake_distributions(stake_id);

CREATE TABLE stake_orders (
    id              TEXT PRIMARY KEY,
    stake_id        TEXT NOT NULL REFERENCES stakes(id),
    holding_id      TEXT NOT NULL REFERENCES stake_holdings(id),
    seller_addr     TEXT NOT NULL,
    shares          INT NOT NULL CHECK (shares > 0),
    price_per_share NUMERIC(20,6) NOT NULL CHECK (price_per_share > 0),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'filled', 'cancelled')),
    filled_shares   INT NOT NULL DEFAULT 0,
    buyer_addr      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_orders_stake ON stake_orders(stake_id, status);
CREATE INDEX idx_orders_seller ON stake_orders(seller_addr);
CREATE INDEX idx_orders_open ON stake_orders(status) WHERE status = 'open';

-- +goose Down
DROP TABLE IF EXISTS stake_orders;
DROP TABLE IF EXISTS stake_distributions;
DROP TABLE IF EXISTS stake_holdings;
DROP TABLE IF EXISTS stakes;
