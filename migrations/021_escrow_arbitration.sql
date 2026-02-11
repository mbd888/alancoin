-- +goose Up
-- Phase 2A: Escrow arbitration / dispute resolution

ALTER TABLE escrows ADD COLUMN dispute_evidence JSONB DEFAULT '[]';
ALTER TABLE escrows ADD COLUMN arbitrator_addr VARCHAR(42);
ALTER TABLE escrows ADD COLUMN arbitration_deadline TIMESTAMPTZ;
ALTER TABLE escrows ADD COLUMN partial_release_amount NUMERIC(20,6);
ALTER TABLE escrows ADD COLUMN partial_refund_amount NUMERIC(20,6);
ALTER TABLE escrows ADD COLUMN dispute_window_until TIMESTAMPTZ;

-- Index for timer queries on arbitrating escrows
CREATE INDEX idx_escrows_status_arbitrating ON escrows(status) WHERE status = 'arbitrating';
CREATE INDEX idx_escrows_status_disputed ON escrows(status) WHERE status = 'disputed';

-- +goose Down
DROP INDEX IF EXISTS idx_escrows_status_disputed;
DROP INDEX IF EXISTS idx_escrows_status_arbitrating;
ALTER TABLE escrows DROP COLUMN IF EXISTS dispute_window_until;
ALTER TABLE escrows DROP COLUMN IF EXISTS partial_refund_amount;
ALTER TABLE escrows DROP COLUMN IF EXISTS partial_release_amount;
ALTER TABLE escrows DROP COLUMN IF EXISTS arbitration_deadline;
ALTER TABLE escrows DROP COLUMN IF EXISTS arbitrator_addr;
ALTER TABLE escrows DROP COLUMN IF EXISTS dispute_evidence;
