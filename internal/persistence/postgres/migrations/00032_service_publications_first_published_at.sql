-- +goose Up
-- The published index pages with a cursor, so its order must not move under a
-- walk (SPA-434).
--
-- GET /api/published-services used to sort by published_at, which a republish
-- sets to now(). A keyset cursor over that order skips any service republished
-- before the walk reaches it: the service jumps ahead of the cursor, and every
-- later page starts after it. first_published_at is set once, when the row is
-- inserted, and the upsert in PublishUserService never names it, so a
-- republish keeps the service's place. Unpublishing deletes the row, so
-- publishing again after that starts a new first_published_at — the service
-- was absent from the index in between.
--
-- published_at keeps its meaning (the last publish) and 00027's index stays;
-- only the index read stops sorting by it.
--
-- ## Backfill
--
-- Every existing row takes its published_at. That is the closest record there
-- is of when it was first published; a service republished before this
-- migration keeps the place its last publish gave it.
--
-- ## Deploying
--
-- Additive. DEFAULT now() means an API still running the old insert, which
-- does not name the column, writes a valid row during a rolling deploy.
--
-- ## Re-running
--
-- IF NOT EXISTS and a backfill limited to NULLs, for 00025's reason: a schema
-- change re-run against data it already wrote must not fail on "already
-- exists", and must not overwrite a first publish with a later one. It is also
-- what lets the package's migration-rewind tests unrecord this version with a
-- bare DELETE and bring the database forward again.

ALTER TABLE service_publications ADD COLUMN IF NOT EXISTS first_published_at timestamptz;

UPDATE service_publications
   SET first_published_at = published_at
 WHERE first_published_at IS NULL;

ALTER TABLE service_publications
    ALTER COLUMN first_published_at SET DEFAULT now(),
    ALTER COLUMN first_published_at SET NOT NULL;

CREATE INDEX IF NOT EXISTS service_publications_first_published_at_idx
    ON service_publications (first_published_at DESC);

-- +goose Down
DROP INDEX IF EXISTS service_publications_first_published_at_idx;
ALTER TABLE service_publications DROP COLUMN IF EXISTS first_published_at;
