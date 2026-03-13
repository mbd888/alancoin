-- +goose Up
-- Materialized view for network-level aggregate statistics.
-- Queried by GetNetworkStats() in the registry store and the MCP server.
-- Refreshed periodically by the matview refresher (every 30s in Postgres mode).
CREATE MATERIALIZED VIEW IF NOT EXISTS network_stats AS
SELECT
    (SELECT COUNT(*) FROM agents)       AS total_agents,
    (SELECT COUNT(*) FROM services)     AS total_services,
    (SELECT COUNT(*) FROM transactions) AS total_transactions,
    (SELECT COALESCE(SUM(amount), 0)::TEXT FROM transactions WHERE status IN ('confirmed', 'completed')) AS total_volume;

-- Unique index required for REFRESH MATERIALIZED VIEW CONCURRENTLY
CREATE UNIQUE INDEX IF NOT EXISTS idx_network_stats_single ON network_stats ((1));

-- +goose Down
DROP MATERIALIZED VIEW IF EXISTS network_stats;
