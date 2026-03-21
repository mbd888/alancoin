-- Event bus write-ahead log for crash recovery.
-- Events are persisted before entering the bus and marked processed after consumption.
-- On startup, pending events are replayed to ensure no settlement events are lost.

CREATE TABLE IF NOT EXISTS eventbus_wal (
    id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    key TEXT NOT NULL,
    payload JSONB NOT NULL,
    request_id TEXT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processed', 'dead_lettered')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

-- Fast lookup for pending events on startup recovery
CREATE INDEX IF NOT EXISTS idx_wal_pending ON eventbus_wal(status, created_at) WHERE status = 'pending';

-- Fast cleanup of old processed events
CREATE INDEX IF NOT EXISTS idx_wal_cleanup ON eventbus_wal(processed_at) WHERE status = 'processed';
