-- +goose Up
-- One-time invite and password-reset links (SPA-387).
--
-- An admin invites someone, or issues a reset, and gets back a link carrying a
-- random token; whoever opens it sets the account's password. Like sessions
-- (00002), only the SHA-256 hash of the token is stored, so a dump of this
-- table yields no link anyone can open.
--
-- used_at makes a token single-use: redeeming it sets used_at in the same
-- transaction that sets the password, and every read filters used_at IS NULL.
-- Rows are kept after use rather than deleted so an admin can see a link was
-- redeemed; unused reset rows are deleted when a newer reset is issued.
--
-- ON DELETE CASCADE for 00002's reason: a token is meaningless without its
-- user. Users are not deleted today (00030), so this only matters to tests.
--
-- ## Re-running
--
-- IF NOT EXISTS for 00018's reason: the migration-rewind tests unrecord a
-- version with a bare DELETE and bring the database forward again.

CREATE TABLE IF NOT EXISTS account_tokens (
    token_hash text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text NOT NULL CHECK (purpose IN ('invite', 'reset')),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX IF NOT EXISTS account_tokens_user_id_idx ON account_tokens (user_id);

-- +goose Down
DROP TABLE IF EXISTS account_tokens;
