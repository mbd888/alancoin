-- Migration 014: Add bid bonds and withdrawal support to negotiations
--
-- Adds:
--   rfps: required_bond_pct, no_withdraw_window
--   bids: bond_amount, bond_status

-- RFP bond configuration
ALTER TABLE rfps ADD COLUMN required_bond_pct NUMERIC(5,2) DEFAULT 0;
ALTER TABLE rfps ADD COLUMN no_withdraw_window VARCHAR(20) DEFAULT '';

-- Bid bond tracking
ALTER TABLE bids ADD COLUMN bond_amount NUMERIC(20,6) DEFAULT 0;
ALTER TABLE bids ADD COLUMN bond_status VARCHAR(20) DEFAULT 'none';

-- Constraint: bond_status must be a valid value
ALTER TABLE bids ADD CONSTRAINT chk_bond_status
    CHECK (bond_status IN ('none', 'held', 'released', 'forfeited'));

-- Constraint: required_bond_pct must be 0-100
ALTER TABLE rfps ADD CONSTRAINT chk_required_bond_pct
    CHECK (required_bond_pct >= 0 AND required_bond_pct <= 100);

-- Index: find bids with held bonds (for cleanup/audit)
CREATE INDEX idx_bids_bond_held ON bids(seller_addr) WHERE bond_status = 'held';
