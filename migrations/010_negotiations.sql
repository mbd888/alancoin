-- Autonomous Negotiation Protocol
-- RFPs and Bids for agent-to-agent deal-making

CREATE TABLE rfps (
    id VARCHAR(36) PRIMARY KEY,
    buyer_addr VARCHAR(42) NOT NULL,
    service_type VARCHAR(100) NOT NULL,
    description TEXT,
    min_budget NUMERIC(20,6) NOT NULL,
    max_budget NUMERIC(20,6) NOT NULL,
    max_latency_ms INTEGER DEFAULT 10000,
    min_success_rate NUMERIC(5,2) DEFAULT 95.00,
    duration VARCHAR(20) NOT NULL,
    min_volume INTEGER DEFAULT 1,
    bid_deadline TIMESTAMPTZ NOT NULL,
    auto_select BOOLEAN DEFAULT FALSE,
    min_reputation NUMERIC(5,2) DEFAULT 0,
    max_counter_rounds INTEGER DEFAULT 3,
    scoring_weight_price NUMERIC(3,2) DEFAULT 0.30,
    scoring_weight_reputation NUMERIC(3,2) DEFAULT 0.40,
    scoring_weight_sla NUMERIC(3,2) DEFAULT 0.30,
    status VARCHAR(20) DEFAULT 'open',
    winning_bid_id VARCHAR(36),
    contract_id VARCHAR(36),
    bid_count INTEGER DEFAULT 0,
    cancel_reason TEXT,
    awarded_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CHECK (status IN ('open','selecting','awarded','expired','cancelled')),
    CHECK (min_budget > 0 AND max_budget >= min_budget)
);

CREATE TABLE bids (
    id VARCHAR(36) PRIMARY KEY,
    rfp_id VARCHAR(36) NOT NULL REFERENCES rfps(id),
    seller_addr VARCHAR(42) NOT NULL,
    price_per_call NUMERIC(20,6) NOT NULL,
    total_budget NUMERIC(20,6) NOT NULL,
    max_latency_ms INTEGER DEFAULT 10000,
    success_rate NUMERIC(5,2) DEFAULT 95.00,
    duration VARCHAR(20) NOT NULL,
    seller_penalty NUMERIC(20,6) DEFAULT 0,
    status VARCHAR(20) DEFAULT 'pending',
    score NUMERIC(8,6) DEFAULT 0,
    counter_round INTEGER DEFAULT 0,
    parent_bid_id VARCHAR(36),
    countered_by_id VARCHAR(36),
    message TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW(),
    CHECK (status IN ('pending','accepted','rejected','withdrawn','countered')),
    CHECK (price_per_call > 0),
    CHECK (total_budget > 0)
);

-- Indexes
CREATE INDEX idx_rfps_buyer ON rfps(buyer_addr);
CREATE INDEX idx_rfps_status ON rfps(status);
CREATE INDEX idx_rfps_service_open ON rfps(service_type) WHERE status = 'open';
CREATE INDEX idx_rfps_deadline ON rfps(bid_deadline) WHERE status = 'open';
CREATE INDEX idx_rfps_auto_select ON rfps(bid_deadline) WHERE status = 'open' AND auto_select = TRUE;
CREATE INDEX idx_bids_rfp ON bids(rfp_id);
CREATE INDEX idx_bids_seller ON bids(seller_addr);
CREATE INDEX idx_bids_rfp_seller ON bids(rfp_id, seller_addr) WHERE status = 'pending';
