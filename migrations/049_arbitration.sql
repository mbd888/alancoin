-- Dispute arbitration cases

CREATE TABLE IF NOT EXISTS arbitration_cases (
    id TEXT PRIMARY KEY,
    escrow_id TEXT NOT NULL,
    buyer_addr TEXT NOT NULL,
    seller_addr TEXT NOT NULL,
    disputed_amount NUMERIC(20,6) NOT NULL CHECK (disputed_amount > 0),
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'auto_resolved', 'assigned', 'resolved', 'appealed')),
    arbiter_addr TEXT,
    outcome TEXT CHECK (outcome IN ('buyer_wins', 'seller_wins', 'split')),
    split_pct INT CHECK (split_pct BETWEEN 0 AND 100),
    decision TEXT,
    fee NUMERIC(20,6) NOT NULL,
    contract_id TEXT,
    auto_resolvable BOOLEAN NOT NULL DEFAULT FALSE,
    evidence JSONB NOT NULL DEFAULT '[]',
    filed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_arb_escrow ON arbitration_cases(escrow_id);
CREATE INDEX IF NOT EXISTS idx_arb_status ON arbitration_cases(status) WHERE status IN ('open', 'assigned');
CREATE INDEX IF NOT EXISTS idx_arb_buyer ON arbitration_cases(buyer_addr);
CREATE INDEX IF NOT EXISTS idx_arb_seller ON arbitration_cases(seller_addr);
