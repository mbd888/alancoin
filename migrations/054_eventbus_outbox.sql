-- Transactional outbox for exactly-once event publishing.
-- Events written in the same transaction as payment settlement.
-- Background poller publishes to event bus with SELECT FOR UPDATE SKIP LOCKED.

CREATE TABLE IF NOT EXISTS eventbus_outbox (
    id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    key TEXT NOT NULL,
    payload JSONB NOT NULL,
    request_id TEXT,
    published BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);

-- Fast poll for unpublished events
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON eventbus_outbox(created_at) WHERE published = FALSE;

-- Fast cleanup of old published events
CREATE INDEX IF NOT EXISTS idx_outbox_cleanup ON eventbus_outbox(published_at) WHERE published = TRUE;
