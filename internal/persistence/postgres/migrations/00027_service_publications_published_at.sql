-- +goose Up
-- The public index of published services orders by publish time (SPA-358).
--
-- GET /api/published-services lists every publication, most recently published
-- first. It is the one read that walks service_publications rather than
-- looking a row up: the detail, publish and unpublish paths all arrive with a
-- service id and use the primary key, so none of them would use this index and
-- none of them needed it until now.
--
-- ## Why published_at, and not a flag
--
-- 00026 made a publication a row rather than nullable columns on
-- user_services, so "is it published" is the row existing and there is no
-- column to filter on. What the index read does do is sort, and published_at
-- is its sort key. DESC to match that read, though a b-tree scans either way.
--
-- ## Re-running
--
-- CREATE INDEX IF NOT EXISTS for 00025's reason: a schema change re-run against
-- data it already wrote must not fail on "already exists". It is also what lets
-- the package's migration-rewind tests unrecord this version with a bare
-- DELETE and bring the database forward again.

CREATE INDEX IF NOT EXISTS service_publications_published_at_idx
    ON service_publications (published_at DESC);

-- +goose Down
DROP INDEX IF EXISTS service_publications_published_at_idx;
