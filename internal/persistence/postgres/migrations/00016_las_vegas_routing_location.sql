-- +goose Up
-- Add an optional routing-anchor coordinate to stations, and set the
-- las-vegas station's (SPA-234).
--
-- ## What was wrong
--
-- The routing worker centres each station's egress isochrone ("how far can
-- someone walk from here") on the station's own `location` — see
-- sparks-effect-routing-worker's chainer, and internal/transit/compile.go's
-- seededNodes, which is where that coordinate enters the compiled graph the
-- worker reads. For las-vegas that coordinate is the real, FRA-cleared
-- Brightline West terminus (migration 00015, SPA-222): a 33-acre site on Las
-- Vegas Blvd between Blue Diamond Rd and Warm Springs Rd that is, as of this
-- migration, still in civil-construction phase. There is no walkable network
-- reaching that exact point in OpenStreetMap yet, because on the ground there
-- mostly isn't one yet, so Valhalla rejects a pedestrian isochrone centred on
-- it outright: "Locations are in unconnected regions."
--
-- ## Why a second column and not just moving `location`
--
-- `location` is the accurate place the station is — what the map pin and the
-- Brightline West route geometry's terminus vertex show — and correcting it to
-- the real terminus is exactly what 00015 did after it was wrong. Moving it
-- again to whatever happens to be walkable today would trade that accuracy for
-- a point that is not the station either, and that would need correcting yet
-- again once the real station opens and its own pedestrian network is built
-- and mapped. `routing_location` is a separate, explicitly provisional stand-in
-- that only the routing worker's egress isochrone reads (via
-- GraphNode.RoutingLat/RoutingLng); `location` and the route geometry are
-- untouched.
--
-- The value below is approximate by design: Las Vegas Blvd S, at the station's
-- latitude, on the block the site fronts between Blue Diamond and Warm
-- Springs — one of the most densely mapped, unambiguously connected arterials
-- in the area. An egress isochrone standing in for a site with no pedestrian
-- access yet does not need survey precision, only a point Valhalla's graph
-- actually considers connected. Remove this column's las-vegas value (leaving
-- it NULL, so seededNodes falls back to `location`) once the real station's
-- own pedestrian network exists and is mapped in OSM.
--
-- ## Why a migration and not just the seed
--
-- internal/transit/data/scenarios/ca-hsr/stations.yaml carries the same value
-- in routing_location, which is enough for a database that has never been
-- seeded. It is not enough for a deployed one: SeedIfEmpty returns early on a
-- populated store (seed.go) and there is no reseed path. This is the same
-- split 00015 called out — the seed serves fresh databases, a migration serves
-- the deployed one. CompileSeededIfNeeded (compile_seeded.go) notices the
-- resulting drift between the stored graph and what the rows now compile to
-- and recompiles on the next boot, exactly as it did for 00015.
--
-- ## Why this is inert on a fresh database
--
-- Migrations run before SeedIfEmpty, so on an empty database the UPDATE below
-- touches zero rows and the seed inserts the value from YAML a moment later.
-- On a deployed database the row exists and the UPDATE does the work instead.
--
-- Re-running is safe: assigning a value that is already there is a no-op.

-- IF NOT EXISTS rather than a bare ADD COLUMN: unlike 00015's pure UPDATEs,
-- this migration also changes the schema, and a schema change re-run against
-- data it already wrote (rather than a full rewind) must not fail on "column
-- already exists" — see TestLasVegasRoutingLocationMigrationIsSafeToReRun.
ALTER TABLE stations ADD COLUMN IF NOT EXISTS routing_location jsonb;

UPDATE stations
SET routing_location = '{"type":"Point","coordinates":[-115.1706,36.0545]}'::jsonb
WHERE id = '00000000-0000-4005-8001-00000000000f'::uuid;

-- +goose Down
ALTER TABLE stations DROP COLUMN IF EXISTS routing_location;
