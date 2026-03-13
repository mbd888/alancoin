-- +goose Up
-- Flywheel metrics: periodic snapshots of network health and flywheel state.
-- Used by the flywheel dashboard to track whether the data flywheel is spinning.
CREATE TABLE IF NOT EXISTS flywheel_snapshots (
    id                  SERIAL PRIMARY KEY,
    health_score        NUMERIC(5,1) NOT NULL DEFAULT 0,
    health_tier         VARCHAR(20) NOT NULL DEFAULT 'cold',
    velocity_score      NUMERIC(5,1) DEFAULT 0,
    growth_score        NUMERIC(5,1) DEFAULT 0,
    density_score       NUMERIC(5,1) DEFAULT 0,
    effectiveness_score NUMERIC(5,1) DEFAULT 0,
    retention_score     NUMERIC(5,1) DEFAULT 0,
    tx_per_hour         NUMERIC(10,2) DEFAULT 0,
    volume_per_hour     NUMERIC(20,6) DEFAULT 0,
    total_agents        INTEGER DEFAULT 0,
    active_agents_7d    INTEGER DEFAULT 0,
    total_edges         INTEGER DEFAULT 0,
    graph_density       DOUBLE PRECISION DEFAULT 0,
    retention_rate_7d   DOUBLE PRECISION DEFAULT 0,
    computed_at         TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_flywheel_time ON flywheel_snapshots(computed_at DESC);

-- Add graph_score column to reputation_snapshots for blended scoring.
-- The flywheel's core gear: discovery uses these blended scores to rank services.
ALTER TABLE reputation_snapshots ADD COLUMN IF NOT EXISTS graph_score NUMERIC(5,1) DEFAULT 0;

-- +goose Down
ALTER TABLE reputation_snapshots DROP COLUMN IF EXISTS graph_score;
DROP INDEX IF EXISTS idx_flywheel_time;
DROP TABLE IF EXISTS flywheel_snapshots;
