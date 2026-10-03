-- +goose Up
-- Deleting a user is intentionally unsupported (SPA-384).
--
-- Sign-in is removed by setting users.disabled_at; the row and everything it
-- authored stay. There is no delete-user path, so these foreign keys stay.
-- A delete would follow every live reference to users(id).
--
-- ON DELETE CASCADE: sessions.user_id (00002), user_services.owner_id
-- (00005), user_scenarios.owner_id (00006), routing_jobs.owner_id (00014),
-- routes.owner_id and stations.owner_id (00022), and scenarios.owner_id and
-- services.owner_id (00022). 00001 created those last two ON DELETE SET NULL;
-- 00022 changed them to CASCADE. service_publications does not reference
-- users; it cascades from user_services (00026), so the same delete
-- unpublishes every page they made.
--
-- ON DELETE SET NULL: jobs.owner_id (00001) is the only remaining exception.
--
-- NULL means the account can sign in. No default, so every existing row stays
-- able to sign in.
--
-- ## Re-running
--
-- ADD COLUMN IF NOT EXISTS for 00018's reason: a schema change re-run against
-- data it already wrote must not fail on "already exists". It is also what lets
-- the package's migration-rewind tests unrecord this version with a bare
-- DELETE and bring the database forward again.

ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at timestamptz;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS disabled_at;
