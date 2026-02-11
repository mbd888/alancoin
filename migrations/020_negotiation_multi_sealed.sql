-- Multi-winner RFPs and sealed bids support.

-- RFP: add max_winners, sealed_bids, winning_bid_ids, contract_ids
ALTER TABLE rfps ADD COLUMN IF NOT EXISTS max_winners INTEGER NOT NULL DEFAULT 1;
ALTER TABLE rfps ADD COLUMN IF NOT EXISTS sealed_bids BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE rfps ADD COLUMN IF NOT EXISTS winning_bid_ids TEXT;
ALTER TABLE rfps ADD COLUMN IF NOT EXISTS contract_ids TEXT;

-- RFP templates: add max_winners, sealed_bids
ALTER TABLE rfp_templates ADD COLUMN IF NOT EXISTS max_winners INTEGER NOT NULL DEFAULT 1;
ALTER TABLE rfp_templates ADD COLUMN IF NOT EXISTS sealed_bids BOOLEAN NOT NULL DEFAULT FALSE;

-- Index for sealed-bid RFPs (useful for filtering)
CREATE INDEX IF NOT EXISTS idx_rfps_sealed_open ON rfps (sealed_bids) WHERE status = 'open' AND sealed_bids = TRUE;
