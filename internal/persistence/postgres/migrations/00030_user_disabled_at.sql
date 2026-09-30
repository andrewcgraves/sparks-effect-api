-- +goose Up
-- Deleting a user is intentionally unsupported (SPA-384).
--
-- Cascades from users(id) ON DELETE CASCADE would remove services, scenarios,
-- routing jobs, sessions, and — through user_services — every
-- service_publications row. jobs.owner_id is the SET NULL exception. Access is
-- removed by setting users.disabled_at; the row and everything it authored
-- stay. There is no delete-user path to guard, so the cascades stay.
--
-- NULL means the account can sign in. No default, so every existing row stays
-- able to sign in.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS for 00025's reason: a schema change re-run against
-- data it already wrote must not fail on "already exists". It is also what lets
-- the package's migration-rewind tests unrecord this version with a bare
-- DELETE and bring the database forward again.

ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at timestamptz;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS disabled_at;
