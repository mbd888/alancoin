-- +goose Up
ALTER TABLE session_keys ADD COLUMN IF NOT EXISTS scopes TEXT[] DEFAULT ARRAY['spend', 'read'];

-- +goose Down
ALTER TABLE session_keys DROP COLUMN IF EXISTS scopes;
