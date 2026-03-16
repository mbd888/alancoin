-- +goose Up
-- Coalition escrows: outcome-triggered multi-agent settlement

CREATE TABLE coalition_escrows (
    id              TEXT PRIMARY KEY,
    buyer_addr      TEXT NOT NULL,
    oracle_addr     TEXT NOT NULL,
    total_amount    NUMERIC(20,6) NOT NULL CHECK (total_amount > 0),
    split_strategy  TEXT NOT NULL CHECK (split_strategy IN ('equal', 'proportional', 'shapley')),
    members         JSONB NOT NULL DEFAULT '[]',
    quality_tiers   JSONB NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'delivered', 'settled', 'aborted', 'expired')),
    quality_score   DOUBLE PRECISION,
    matched_tier    TEXT,
    payout_pct      DOUBLE PRECISION,
    total_payout    NUMERIC(20,6),
    refund_amount   NUMERIC(20,6),
    contributions   JSONB,
    contract_id     TEXT,
    auto_settle_at  TIMESTAMPTZ NOT NULL,
    settled_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_coalition_buyer ON coalition_escrows (buyer_addr);
CREATE INDEX idx_coalition_oracle ON coalition_escrows (oracle_addr);
CREATE INDEX idx_coalition_status ON coalition_escrows (status) WHERE status NOT IN ('settled', 'aborted', 'expired');
CREATE INDEX idx_coalition_auto_settle ON coalition_escrows (auto_settle_at) WHERE status NOT IN ('settled', 'aborted', 'expired');

-- GIN index for member lookup within JSONB
CREATE INDEX idx_coalition_members ON coalition_escrows USING GIN (members jsonb_path_ops);

-- +goose Down
DROP TABLE IF EXISTS coalition_escrows;
