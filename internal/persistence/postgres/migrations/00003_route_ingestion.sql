-- +goose Up
-- Admin route ingestion (SPA-75).
--
-- Extends routes from "geometry + mode" to a fully addressable alignment that
-- carries the per-segment track physics the speed-limit model consumes.

-- Slugs address a route directly (GET /api/routes/{slug}), independent of any
-- scenario, so the constraint is global rather than per-scenario. Existing rows
-- are backfilled from the route id before the NOT NULL lands, since a route
-- name is not guaranteed to slugify to anything unique.
--
-- The backfill uses the *whole* id, not a prefix: seeded route ids are assigned
-- from a shared pattern (00000000-0000-4002-...), so any leading slice of them
-- collides across rows and would fail the UNIQUE constraint added below.
ALTER TABLE routes ADD COLUMN slug text;
UPDATE routes SET slug = 'route-' || id::text WHERE slug IS NULL;
ALTER TABLE routes ALTER COLUMN slug SET NOT NULL;
ALTER TABLE routes ADD CONSTRAINT routes_slug_key UNIQUE (slug);

-- Per-segment physics: one entry per span between consecutive coordinates, so
-- a route of n coordinates has n-1 segments. Stored as jsonb alongside the
-- geometry it describes, matching how geometry itself is persisted. Defaults to
-- an empty array, which means tangent, level, uncanted track throughout —
-- exactly what the seeded routes describe today, since they carry no physics.
ALTER TABLE routes ADD COLUMN segments jsonb NOT NULL DEFAULT '[]'::jsonb;

-- A route ingested by an admin is a standalone alignment with no scenario: the
-- ingestion payload carries no scenario, and such routes are read by slug
-- rather than under a scenario. Seeded routes keep their scenario, so this only
-- relaxes the column rather than dropping the relationship.
ALTER TABLE routes ALTER COLUMN scenario_id DROP NOT NULL;

-- +goose Down
ALTER TABLE routes ALTER COLUMN scenario_id SET NOT NULL;
ALTER TABLE routes DROP COLUMN segments;
ALTER TABLE routes DROP CONSTRAINT routes_slug_key;
ALTER TABLE routes DROP COLUMN slug;
