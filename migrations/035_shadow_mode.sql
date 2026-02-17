-- 035_shadow_mode.sql
-- Add enforcement mode and shadow expiration to spend policies.

ALTER TABLE spend_policies ADD COLUMN IF NOT EXISTS enforcement_mode TEXT NOT NULL DEFAULT 'enforce';
ALTER TABLE spend_policies ADD COLUMN IF NOT EXISTS shadow_expires_at TIMESTAMPTZ;
