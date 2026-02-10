-- +goose Up
-- Reputation snapshots for historical score tracking
CREATE TABLE IF NOT EXISTS reputation_snapshots (
    id              SERIAL PRIMARY KEY,
    address         VARCHAR(42) NOT NULL,
    score           NUMERIC(5,1) NOT NULL,
    tier            VARCHAR(20) NOT NULL,
    volume_score    NUMERIC(5,1) DEFAULT 0,
    activity_score  NUMERIC(5,1) DEFAULT 0,
    success_score   NUMERIC(5,1) DEFAULT 0,
    age_score       NUMERIC(5,1) DEFAULT 0,
    diversity_score NUMERIC(5,1) DEFAULT 0,
    total_txns      INTEGER DEFAULT 0,
    total_volume    NUMERIC(20,6) DEFAULT 0,
    success_rate    NUMERIC(5,3) DEFAULT 0,
    unique_peers    INTEGER DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_snapshots_addr_time ON reputation_snapshots(address, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_snapshots_addr_time;
DROP TABLE IF EXISTS reputation_snapshots;
