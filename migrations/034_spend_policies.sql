-- 034_spend_policies.sql
-- Tenant-scoped spend policies for gateway proxy enforcement.

CREATE TABLE IF NOT EXISTS spend_policies (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    rules       JSONB NOT NULL DEFAULT '[]',
    priority    INTEGER NOT NULL DEFAULT 0,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_spend_policies_tenant ON spend_policies(tenant_id);
CREATE UNIQUE INDEX idx_spend_policies_tenant_name ON spend_policies(tenant_id, name);
