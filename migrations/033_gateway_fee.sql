-- 033_gateway_fee.sql
-- Add platform fee tracking to gateway request logs.
-- fee_amount records the basis-point take rate deducted per settled request.

ALTER TABLE gateway_request_logs
    ADD COLUMN fee_amount NUMERIC(20,6) DEFAULT 0;
