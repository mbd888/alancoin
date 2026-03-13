-- +goose Up
-- Migration 041: TraceRank tables for graph-based reputation scoring

CREATE TABLE IF NOT EXISTS tracerank_scores (
    address        VARCHAR(42) UNIQUE NOT NULL,
    graph_score    NUMERIC(5,1)       NOT NULL DEFAULT 0,
    raw_rank       DOUBLE PRECISION   NOT NULL DEFAULT 0,
    seed_signal    NUMERIC(3,2)       NOT NULL DEFAULT 0,
    in_degree      INTEGER            NOT NULL DEFAULT 0,
    out_degree     INTEGER            NOT NULL DEFAULT 0,
    in_volume      NUMERIC(20,6)      NOT NULL DEFAULT 0,
    out_volume     NUMERIC(20,6)      NOT NULL DEFAULT 0,
    iterations     INTEGER            NOT NULL DEFAULT 0,
    compute_run_id VARCHAR(50)        NOT NULL DEFAULT '',
    computed_at    TIMESTAMPTZ        NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tracerank_scores_graph_score ON tracerank_scores (graph_score DESC);

CREATE TABLE IF NOT EXISTS tracerank_runs (
    run_id      VARCHAR(50) UNIQUE NOT NULL,
    node_count  INTEGER            NOT NULL DEFAULT 0,
    edge_count  INTEGER            NOT NULL DEFAULT 0,
    iterations  INTEGER            NOT NULL DEFAULT 0,
    converged   BOOLEAN            NOT NULL DEFAULT false,
    duration_ms BIGINT             NOT NULL DEFAULT 0,
    max_score   NUMERIC(5,1)       NOT NULL DEFAULT 0,
    mean_score  NUMERIC(5,1)       NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ        NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tracerank_runs_computed_at ON tracerank_runs (computed_at DESC);

-- +goose Down
DROP TABLE IF EXISTS tracerank_runs;
DROP TABLE IF EXISTS tracerank_scores;
