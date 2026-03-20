-- Spend forensics: baselines and alerts

CREATE TABLE IF NOT EXISTS forensics_baselines (
    agent_addr TEXT PRIMARY KEY,
    tx_count INT NOT NULL DEFAULT 0,
    mean_amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    stddev_amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    mean_velocity DOUBLE PRECISION NOT NULL DEFAULT 0,
    stddev_velocity DOUBLE PRECISION NOT NULL DEFAULT 0,
    known_counterparties JSONB NOT NULL DEFAULT '{}',
    known_services JSONB NOT NULL DEFAULT '{}',
    active_hours INT[24] NOT NULL DEFAULT ARRAY[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],
    last_updated TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS forensics_alerts (
    id TEXT PRIMARY KEY,
    agent_addr TEXT NOT NULL,
    type TEXT NOT NULL,
    severity TEXT NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    message TEXT NOT NULL,
    score DOUBLE PRECISION NOT NULL,
    baseline DOUBLE PRECISION NOT NULL DEFAULT 0,
    actual DOUBLE PRECISION NOT NULL DEFAULT 0,
    sigma DOUBLE PRECISION NOT NULL DEFAULT 0,
    detected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acknowledged BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX IF NOT EXISTS idx_forensics_alerts_agent ON forensics_alerts(agent_addr, detected_at DESC);
CREATE INDEX IF NOT EXISTS idx_forensics_alerts_severity ON forensics_alerts(severity, detected_at DESC) WHERE NOT acknowledged;
