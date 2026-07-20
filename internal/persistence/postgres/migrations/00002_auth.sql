-- +goose Up
-- Invite-only authentication (SPA-74).
--
-- Accounts are provisioned by an admin; there is no self-serve signup, so this
-- migration adds only the credential column and the session store — no
-- registration or email-verification machinery.

-- Existing rows (and any user provisioned before a password is set) default to
-- an empty hash, which auth.VerifyPassword rejects outright. A NULL-free column
-- keeps "no password yet" and "cannot authenticate" the same state.
ALTER TABLE users ADD COLUMN password_hash text NOT NULL DEFAULT '';

-- Sessions hold only the SHA-256 hash of a bearer token, never the token
-- itself: a dump of this table yields nothing an attacker can present.
-- ON DELETE CASCADE means deprovisioning a user revokes their sessions in the
-- same statement, with no application code to forget.
CREATE TABLE sessions (
    token_hash text PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- +goose Down
DROP TABLE IF EXISTS sessions;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
