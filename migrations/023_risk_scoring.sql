-- +goose Up
CREATE TABLE IF NOT EXISTS risk_assessments (
    id            VARCHAR(36) PRIMARY KEY,
    key_id        VARCHAR(36) NOT NULL,
    score         NUMERIC(4,3) NOT NULL CHECK (score >= 0 AND score <= 1),
    decision      VARCHAR(10) NOT NULL CHECK (decision IN ('allow', 'warn', 'block')),
    factors       JSONB NOT NULL DEFAULT '{}',
    evaluated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_risk_assessments_key_id ON risk_assessments (key_id, evaluated_at DESC);
CREATE INDEX idx_risk_assessments_blocks ON risk_assessments (evaluated_at DESC) WHERE decision = 'block';

-- +goose Down
DROP TABLE IF EXISTS risk_assessments;
