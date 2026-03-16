-- +goose Up
-- Standing offers / agent services order book

CREATE TABLE offers (
    id              TEXT PRIMARY KEY,
    seller_addr     TEXT NOT NULL,
    service_type    TEXT NOT NULL,
    description     TEXT,
    price           NUMERIC(20,6) NOT NULL CHECK (price > 0),
    capacity        INTEGER NOT NULL CHECK (capacity > 0),
    remaining_cap   INTEGER NOT NULL CHECK (remaining_cap >= 0),
    conditions      JSONB NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'exhausted', 'cancelled', 'expired')),
    total_claims    INTEGER NOT NULL DEFAULT 0,
    total_revenue   NUMERIC(20,6) NOT NULL DEFAULT 0,
    endpoint        TEXT,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_offers_seller ON offers (seller_addr);
CREATE INDEX idx_offers_service_type ON offers (service_type) WHERE status = 'active';
CREATE INDEX idx_offers_status ON offers (status) WHERE status = 'active';
CREATE INDEX idx_offers_expires ON offers (expires_at) WHERE status = 'active';
CREATE INDEX idx_offers_price ON offers (price ASC) WHERE status = 'active';

CREATE TABLE offer_claims (
    id          TEXT PRIMARY KEY,
    offer_id    TEXT NOT NULL REFERENCES offers(id),
    buyer_addr  TEXT NOT NULL,
    seller_addr TEXT NOT NULL,
    amount      NUMERIC(20,6) NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'delivered', 'completed', 'disputed', 'refunded')),
    escrow_ref  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX idx_claims_offer ON offer_claims (offer_id);
CREATE INDEX idx_claims_buyer ON offer_claims (buyer_addr);
CREATE INDEX idx_claims_status ON offer_claims (status) WHERE status = 'pending';

-- +goose Down
DROP TABLE IF EXISTS offer_claims;
DROP TABLE IF EXISTS offers;
