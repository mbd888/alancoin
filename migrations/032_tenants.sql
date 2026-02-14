-- 032_tenants.sql: Multi-tenancy data model
-- Adds tenants table and tenant_id columns to existing tables.
-- All tenant_id columns are nullable for backward compatibility.

-- Tenants table
CREATE TABLE IF NOT EXISTS tenants (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    slug            TEXT NOT NULL UNIQUE,
    plan            TEXT NOT NULL DEFAULT 'free',
    stripe_customer_id TEXT,
    status          TEXT NOT NULL DEFAULT 'active',
    settings        JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_tenants_slug ON tenants(slug);
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

-- Add tenant_id to api_keys (nullable, FK)
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS tenant_id TEXT REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant ON api_keys(tenant_id) WHERE tenant_id IS NOT NULL;

-- Add tenant_id to gateway_sessions (nullable, FK)
ALTER TABLE gateway_sessions ADD COLUMN IF NOT EXISTS tenant_id TEXT REFERENCES tenants(id);
CREATE INDEX IF NOT EXISTS idx_gateway_sessions_tenant ON gateway_sessions(tenant_id) WHERE tenant_id IS NOT NULL;

-- Add tenant_id to gateway_request_logs (nullable, no FK for write performance)
ALTER TABLE gateway_request_logs ADD COLUMN IF NOT EXISTS tenant_id TEXT;
CREATE INDEX IF NOT EXISTS idx_gateway_request_logs_tenant ON gateway_request_logs(tenant_id) WHERE tenant_id IS NOT NULL;
