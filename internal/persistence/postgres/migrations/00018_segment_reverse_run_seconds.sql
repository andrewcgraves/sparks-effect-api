-- +goose Up
-- Optional reverse-direction run time on travel-time segments (SPA-245).
--
-- ## What this adds
--
-- Seeded hops are stored one row per station pair, in the primary service
-- direction. Until now the compiler mirrored RunSeconds onto the reverse
-- adjacency. Two CA HSR mountain hops are not symmetric — Gilroy↔Merced
-- (Pacheco Pass) and Bakersfield↔Palmdale (Tehachapi) — so the reverse
-- duration needs a place to live. NULL means "same as forward", matching every
-- existing row and every hop that is genuinely symmetric.
--
-- ## Why a migration and not just the seed
--
-- reverse_run_seconds is authored in
-- internal/transit/data/scenarios/ca-hsr/segment_run_times.yaml, which is
-- enough for a database that has never been seeded. It is not enough for a
-- deployed one: SeedIfEmpty returns early on a populated store (seed.go) and
-- there is no reseed path, so an already-seeded database would keep mirroring
-- those two hops forever. This is the same split 00011, 00012, 00015 and 00016
-- called out — the seed serves fresh databases, a migration serves the
-- deployed one.
--
-- ## Why this is inert on a fresh database
--
-- Migrations run before SeedIfEmpty, so on an empty database the UPDATE below
-- touches zero rows and the seed inserts the overrides from YAML a moment
-- later. On a deployed database the rows exist and the UPDATEs do the work
-- instead. The UPDATEs join scenarios on slug 'ca-hsr' so a user-authored
-- hop that happens to share these station slugs is not rewritten.
--
-- CompileSeededIfNeeded (compile_seeded.go) notices the resulting drift
-- between the stored graph and what the rows now compile to and recompiles on
-- the next boot, so the directed edge weights take effect without a manual
-- compile step.
--
-- ## Re-running
--
-- IF NOT EXISTS rather than a bare ADD COLUMN: a schema change re-run against
-- data it already wrote (rather than a full rewind) must not fail on "column
-- already exists" — see TestSegmentReverseRunSecondsMigrationIsSafeToReRun.
-- Assigning a value that is already there is a no-op.

ALTER TABLE segments ADD COLUMN IF NOT EXISTS reverse_run_seconds integer;

UPDATE segments s
SET reverse_run_seconds = 2940
FROM scenarios sc
WHERE s.scenario_id = sc.id
  AND sc.slug = 'ca-hsr'
  AND s.from_slug = 'gilroy' AND s.to_slug = 'merced';

UPDATE segments s
SET reverse_run_seconds = 1490
FROM scenarios sc
WHERE s.scenario_id = sc.id
  AND sc.slug = 'ca-hsr'
  AND s.from_slug = 'bakersfield' AND s.to_slug = 'palmdale';

-- +goose Down
ALTER TABLE segments DROP COLUMN IF EXISTS reverse_run_seconds;
