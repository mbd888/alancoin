-- +goose Up
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS rotated_from_id VARCHAR(36);
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS rotated_to_id VARCHAR(36);
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS rotation_grace_until TIMESTAMPTZ;

CREATE INDEX idx_session_keys_rotated_from ON session_keys (rotated_from_id) WHERE rotated_from_id IS NOT NULL;

-- +goose Down
ALTER TABLE session_keys DROP COLUMN IF EXISTS rotated_from_id;
ALTER TABLE session_keys DROP COLUMN IF EXISTS rotated_to_id;
ALTER TABLE session_keys DROP COLUMN IF EXISTS rotation_grace_until;
