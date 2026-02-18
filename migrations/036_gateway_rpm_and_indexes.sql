-- +goose Up

-- Persist per-session rate limit so it survives server restarts.
ALTER TABLE gateway_sessions
    ADD COLUMN IF NOT EXISTS max_requests_per_minute INT NOT NULL DEFAULT 100;

-- Billing query indexes for tenant dashboard.
CREATE INDEX IF NOT EXISTS idx_gateway_request_logs_tenant_time
    ON gateway_request_logs (tenant_id, created_at DESC)
    WHERE tenant_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_gateway_request_logs_tenant_status
    ON gateway_request_logs (tenant_id, status)
    WHERE tenant_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_gateway_request_logs_tenant_status;
DROP INDEX IF EXISTS idx_gateway_request_logs_tenant_time;
ALTER TABLE gateway_sessions DROP COLUMN IF EXISTS max_requests_per_minute;
