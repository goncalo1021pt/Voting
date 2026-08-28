-- +goose Up

-- Google identities are keyed by the provider's stable subject id, never by
-- email: a Google account's email address can change, but `sub` cannot.
ALTER TABLE users ADD COLUMN IF NOT EXISTS google_sub VARCHAR(255);

-- Partial unique index rather than a UNIQUE column: every password-only user
-- has NULL here, and Postgres would allow many NULLs under a plain UNIQUE, but
-- being explicit documents that only real subjects must be distinct.
CREATE UNIQUE INDEX IF NOT EXISTS users_google_sub_key
    ON users (google_sub) WHERE google_sub IS NOT NULL;

-- A Google-only user has no password to hash. The column stays for password
-- users; it simply becomes optional.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;

-- One-time codes handed to the browser after a successful Google callback and
-- immediately traded for a real session. This keeps the long-lived session
-- token out of the redirect URL, and so out of browser history.
CREATE TABLE IF NOT EXISTS oauth_exchanges (
    code       VARCHAR(64) PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS oauth_exchanges_expires_at_idx
    ON oauth_exchanges (expires_at);

-- +goose Down
DROP TABLE IF EXISTS oauth_exchanges;
DROP INDEX IF EXISTS users_google_sub_key;
ALTER TABLE users DROP COLUMN IF EXISTS google_sub;
-- password_hash is deliberately left nullable: re-adding NOT NULL would fail
-- against any Google-only user still in the table.
