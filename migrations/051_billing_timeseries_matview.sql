-- Materialized view for dashboard billing timeseries.
-- Replaces full table scans on gateway_request_logs with pre-aggregated hourly data.
-- Refresh every 5-10 minutes via reconciliation timer or standalone worker.

CREATE MATERIALIZED VIEW IF NOT EXISTS billing_timeseries_hourly AS
SELECT
    tenant_id,
    date_trunc('hour', created_at) AS period,
    COUNT(*) AS request_count,
    COALESCE(SUM(amount), 0) AS volume,
    COALESCE(SUM(fee_amount), 0) AS fees
FROM gateway_request_logs
WHERE tenant_id IS NOT NULL
GROUP BY tenant_id, date_trunc('hour', created_at)
ORDER BY period DESC;

CREATE UNIQUE INDEX IF NOT EXISTS idx_billing_ts_tenant_period
    ON billing_timeseries_hourly (tenant_id, period);

-- Also add a summary view for chargeback reports
CREATE MATERIALIZED VIEW IF NOT EXISTS chargeback_summary_monthly AS
SELECT
    cost_center_id,
    date_trunc('month', timestamp) AS period,
    COUNT(*) AS tx_count,
    COALESCE(SUM(amount), 0) AS total_spend,
    MODE() WITHIN GROUP (ORDER BY service_type) AS top_service
FROM chargeback_spend
GROUP BY cost_center_id, date_trunc('month', timestamp);

CREATE UNIQUE INDEX IF NOT EXISTS idx_cb_summary_cc_period
    ON chargeback_summary_monthly (cost_center_id, period);
