-- Invitations that more than one person can redeem, so a host can post one
-- link in a group chat instead of issuing a token per guest.
--
-- The single `redeemed_by` / `redeemed_at` pair on the invitation could only
-- ever describe one redeemer, so redemptions move to their own table and those
-- columns go. Everything already stored is carried across first.

-- +goose Up

-- How many people one link may admit. NULL means unlimited — the same
-- convention `expires_at` already uses on this table for "no limit". Adding
-- the column with DEFAULT 1 backfills every existing row, so links already in
-- circulation stay single-use, which is what the host who issued them chose.
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS max_uses INT DEFAULT 1;

-- One row per person a link admitted. UNIQUE keeps a redemption idempotent per
-- person, so a re-clicked link can never count twice against the cap.
CREATE TABLE IF NOT EXISTS invitation_redemptions (
    id SERIAL PRIMARY KEY,
    invitation_id INT NOT NULL REFERENCES invitations(id) ON DELETE CASCADE,
    user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redeemed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (invitation_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_invitation_redemptions_invitation_id
    ON invitation_redemptions (invitation_id);

-- Carry existing redemptions over before the columns holding them are dropped.
-- Guarded on the column still existing so this migration is a no-op the second
-- time it meets a database that has already moved.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'invitations' AND column_name = 'redeemed_by'
    ) THEN
        INSERT INTO invitation_redemptions (invitation_id, user_id, redeemed_at)
        SELECT id, redeemed_by, COALESCE(redeemed_at, created_at, CURRENT_TIMESTAMP)
        FROM invitations
        WHERE redeemed_by IS NOT NULL
        ON CONFLICT DO NOTHING;
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE invitations DROP COLUMN IF EXISTS redeemed_by;
ALTER TABLE invitations DROP COLUMN IF EXISTS redeemed_at;

-- +goose Down

ALTER TABLE invitations ADD COLUMN IF NOT EXISTS redeemed_by INT REFERENCES users(id);
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS redeemed_at TIMESTAMP;

-- Only the earliest redemption fits in the restored columns. A link that
-- admitted several people cannot be described by them — which is the reason
-- they were replaced.
UPDATE invitations i
SET redeemed_by = first.user_id,
    redeemed_at = first.redeemed_at
FROM (
    SELECT DISTINCT ON (invitation_id) invitation_id, user_id, redeemed_at
    FROM invitation_redemptions
    ORDER BY invitation_id, redeemed_at, id
) first
WHERE first.invitation_id = i.id;

DROP TABLE IF EXISTS invitation_redemptions;
ALTER TABLE invitations DROP COLUMN IF EXISTS max_uses;
