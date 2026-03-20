-- FinOps chargeback: cost centers and spend attribution

CREATE TABLE IF NOT EXISTS chargeback_cost_centers (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    name TEXT NOT NULL,
    department TEXT NOT NULL,
    project_code TEXT,
    monthly_budget NUMERIC(20,6) NOT NULL DEFAULT 0,
    warn_at_percent INT NOT NULL DEFAULT 80,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_cc_tenant ON chargeback_cost_centers(tenant_id);

CREATE TABLE IF NOT EXISTS chargeback_spend (
    id TEXT PRIMARY KEY,
    cost_center_id TEXT NOT NULL REFERENCES chargeback_cost_centers(id),
    tenant_id TEXT NOT NULL,
    agent_addr TEXT NOT NULL,
    amount NUMERIC(20,6) NOT NULL CHECK (amount > 0),
    service_type TEXT NOT NULL,
    workflow_id TEXT,
    session_id TEXT,
    escrow_id TEXT,
    description TEXT,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_spend_cc_time ON chargeback_spend(cost_center_id, timestamp);
CREATE INDEX IF NOT EXISTS idx_spend_tenant ON chargeback_spend(tenant_id, timestamp);
