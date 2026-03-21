-- +goose Up
-- Migration 055: Agent Intelligence Engine
-- Unified intelligence profiles combining TraceRank, Forensics, Reputation,
-- and operational metrics into a single queryable profile per agent.

-- Current intelligence profile per agent (upserted on each computation)
CREATE TABLE IF NOT EXISTS intelligence_profiles (
    address           VARCHAR(42) UNIQUE NOT NULL,
    credit_score      NUMERIC(5,1)       NOT NULL DEFAULT 0,
    risk_score        NUMERIC(5,1)       NOT NULL DEFAULT 0,
    composite_score   NUMERIC(5,1)       NOT NULL DEFAULT 0,
    tier              VARCHAR(20)        NOT NULL DEFAULT 'unknown',

    -- Credit factors
    tracerank_input   NUMERIC(5,1)       NOT NULL DEFAULT 0,
    reputation_input  NUMERIC(5,1)       NOT NULL DEFAULT 0,
    dispute_rate      NUMERIC(7,4)       NOT NULL DEFAULT 0,
    tx_success_rate   NUMERIC(7,4)       NOT NULL DEFAULT 0,
    total_volume      NUMERIC(20,6)      NOT NULL DEFAULT 0,

    -- Risk factors
    anomaly_count_30d INTEGER            NOT NULL DEFAULT 0,
    critical_alerts   INTEGER            NOT NULL DEFAULT 0,
    mean_amount       DOUBLE PRECISION   NOT NULL DEFAULT 0,
    stddev_amount     DOUBLE PRECISION   NOT NULL DEFAULT 0,
    forensic_score    NUMERIC(5,1)       NOT NULL DEFAULT 0,

    -- Network position
    in_degree         INTEGER            NOT NULL DEFAULT 0,
    out_degree        INTEGER            NOT NULL DEFAULT 0,
    clustering_coeff  DOUBLE PRECISION   NOT NULL DEFAULT 0,
    bridge_score      DOUBLE PRECISION   NOT NULL DEFAULT 0,

    -- Operational metrics
    total_txns        INTEGER            NOT NULL DEFAULT 0,
    days_on_network   INTEGER            NOT NULL DEFAULT 0,

    -- Trend indicators
    credit_delta_7d   NUMERIC(6,1)       NOT NULL DEFAULT 0,
    credit_delta_30d  NUMERIC(6,1)       NOT NULL DEFAULT 0,
    risk_delta_7d     NUMERIC(6,1)       NOT NULL DEFAULT 0,
    risk_delta_30d    NUMERIC(6,1)       NOT NULL DEFAULT 0,

    compute_run_id    VARCHAR(50)        NOT NULL DEFAULT '',
    computed_at       TIMESTAMPTZ        NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_intelligence_profiles_composite
    ON intelligence_profiles (composite_score DESC);
CREATE INDEX IF NOT EXISTS idx_intelligence_profiles_credit
    ON intelligence_profiles (credit_score DESC);
CREATE INDEX IF NOT EXISTS idx_intelligence_profiles_tier
    ON intelligence_profiles (tier);

-- Historical score snapshots for time-series trends
CREATE TABLE IF NOT EXISTS intelligence_score_history (
    id              BIGSERIAL PRIMARY KEY,
    address         VARCHAR(42)  NOT NULL,
    credit_score    NUMERIC(5,1) NOT NULL,
    risk_score      NUMERIC(5,1) NOT NULL,
    composite_score NUMERIC(5,1) NOT NULL,
    tier            VARCHAR(20)  NOT NULL,
    compute_run_id  VARCHAR(50)  NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_intelligence_history_addr_time
    ON intelligence_score_history (address, created_at DESC);

-- Network-wide benchmarks (one row per computation run)
CREATE TABLE IF NOT EXISTS intelligence_benchmarks (
    id                  BIGSERIAL PRIMARY KEY,
    total_agents        INTEGER       NOT NULL DEFAULT 0,
    avg_credit_score    NUMERIC(5,1)  NOT NULL DEFAULT 0,
    median_credit_score NUMERIC(5,1)  NOT NULL DEFAULT 0,
    avg_risk_score      NUMERIC(5,1)  NOT NULL DEFAULT 0,
    p90_credit_score    NUMERIC(5,1)  NOT NULL DEFAULT 0,
    p10_credit_score    NUMERIC(5,1)  NOT NULL DEFAULT 0,
    avg_composite_score NUMERIC(5,1)  NOT NULL DEFAULT 0,
    compute_run_id      VARCHAR(50)   NOT NULL DEFAULT '',
    computed_at         TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_intelligence_benchmarks_time
    ON intelligence_benchmarks (computed_at DESC);

-- +goose Down
DROP TABLE IF EXISTS intelligence_benchmarks;
DROP TABLE IF EXISTS intelligence_score_history;
DROP TABLE IF EXISTS intelligence_profiles;
