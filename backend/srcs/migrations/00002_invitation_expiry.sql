-- Invitation token expiry (PR #30). NULL means the invitation never expires.
--
-- IF NOT EXISTS because databases initialised from schema.sql after #30 merged
-- already have the column, while any created before it do not — this migration
-- is the path that was missing for them.

-- +goose Up

ALTER TABLE invitations ADD COLUMN IF NOT EXISTS expires_at TIMESTAMP;

-- +goose Down

ALTER TABLE invitations DROP COLUMN IF EXISTS expires_at;
