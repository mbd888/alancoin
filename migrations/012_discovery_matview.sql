-- +goose Up
-- Materialized view for fast service discovery (replaces multi-join query)
CREATE MATERIALIZED VIEW IF NOT EXISTS service_listings_mv AS
SELECT
    s.id,
    s.type,
    s.name AS service_name,
    s.description,
    s.price,
    s.endpoint,
    s.active,
    a.address AS agent_address,
    a.name AS agent_name,
    COALESCE(ast.transaction_count, 0) AS tx_count,
    COALESCE(ast.success_rate, 0) AS success_rate,
    COALESCE(rs.score, 0) AS reputation_score,
    COALESCE(rs.tier, 'new') AS reputation_tier
FROM services s
JOIN agents a ON a.address = s.agent_address
LEFT JOIN agent_stats ast ON ast.agent_address = s.agent_address
LEFT JOIN LATERAL (
    SELECT score, tier
    FROM reputation_snapshots
    WHERE address = s.agent_address
    ORDER BY created_at DESC
    LIMIT 1
) rs ON true
WHERE s.active = true;

CREATE UNIQUE INDEX IF NOT EXISTS idx_slmv_id ON service_listings_mv(id);
CREATE INDEX IF NOT EXISTS idx_slmv_type ON service_listings_mv(type);
CREATE INDEX IF NOT EXISTS idx_slmv_price ON service_listings_mv(price);
CREATE INDEX IF NOT EXISTS idx_slmv_agent ON service_listings_mv(agent_address);

-- +goose Down
DROP INDEX IF EXISTS idx_slmv_agent;
DROP INDEX IF EXISTS idx_slmv_price;
DROP INDEX IF EXISTS idx_slmv_type;
DROP INDEX IF EXISTS idx_slmv_id;
DROP MATERIALIZED VIEW IF EXISTS service_listings_mv;
