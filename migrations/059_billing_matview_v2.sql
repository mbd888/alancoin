-- Recreate billing timeseries matview with total/settled split.
-- The v1 matview (migration 051) only had request_count (total).
-- Dashboard needs both total_count and settled_count for accurate display.

DROP MATERIALIZED VIEW IF EXISTS billing_timeseries_hourly;

CREATE MATERIALIZED VIEW billing_timeseries_hourly AS
SELECT
    tenant_id,
    date_trunc('hour', created_at) AS period,
    COUNT(*) AS total_count,
    COUNT(*) FILTER (WHERE status = 'success') AS settled_count,
    COALESCE(SUM(amount) FILTER (WHERE status = 'success'), 0) AS volume,
    COALESCE(SUM(fee_amount) FILTER (WHERE status = 'success'), 0) AS fees
FROM gateway_request_logs
WHERE tenant_id IS NOT NULL
GROUP BY tenant_id, date_trunc('hour', created_at);

CREATE UNIQUE INDEX idx_billing_ts_tenant_period
    ON billing_timeseries_hourly (tenant_id, period);
