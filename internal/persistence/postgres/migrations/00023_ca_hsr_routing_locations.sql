-- +goose Up
-- Set routing anchors for the madera, kings-tulare and bakersfield CA HSR
-- stations (SPA-258, audited in SPA-244).
--
-- ## What was wrong
--
-- The routing worker centres every Valhalla call for a station — matrix,
-- isochrone and route alike — on that station's coordinate, which reaches the
-- worker through internal/transit/compile.go's seededNodes as GraphNode's
-- Lat/Lng, or RoutingLat/RoutingLng when a routing anchor is set. For these
-- three the pin snaps onto an edge that is barely connected to anything a car
-- or a pedestrian can use, so the egress isochrone ("how far can someone get
-- from here") collapses:
--
--   station        10-min auto isochrone at `location`   what the pin snaps to
--   madera         0.59 km²                              farm tracks off Avenue 13
--   bakersfield    5.5 km²                               a `railway=proposed` HSR node
--   kings-tulare   8.8 km²                               the planned Hanford Viaduct
--
-- For scale, the same request at Fresno covers 81 km² and at Anaheim 102 km².
-- A polygon two orders of magnitude too small is not a slightly pessimistic
-- answer, it is a wrong one: it says nothing is reachable from a station that
-- will sit in the middle of a mapped street grid.
--
-- ## Why an anchor and not a corrected `location`
--
-- This is the same split 00016 made for las-vegas, and for the same reason.
-- `location` is the accurate place the station is — what the map pin and the
-- route geometry's terminus vertex show, and what the EIR and the surveyed
-- sites say. Moving it onto whatever is mapped today would trade that accuracy
-- for a point that is not the station, and would need correcting again once
-- the real thing is built and its street network is mapped. `routing_location`
-- is an explicitly provisional stand-in that only the routing worker reads;
-- `location` and the route geometry are untouched here.
--
-- In particular, bakersfield's `location` stays on the EIR-cleared F Street /
-- Golden State site. The 2026 business plan's interim 7th Standard Road stop
-- is a question about where the station *is*, not about routing, and it is not
-- this migration's business.
--
-- ## Where the values come from, and their caveat
--
-- Approximate by design, as 00016's were. Each is the nearest mapped arterial
-- or street grid to its pin, chosen because a 10-minute auto isochrone centred
-- there returns a Fresno-scale polygon:
--
--   madera         ~360 m west onto Santa Fe Drive, same latitude    49 km²
--   bakersfield    ~220 m south onto the F Street grid               98 km²
--   kings-tulare   ~720 m east onto the mapped road grid             78 km²
--
-- Caveat, the same one 00016 carried: those areas were measured against public
-- Valhalla (valhalla1.openstreetmap.de 3.8.3, tiles ~2026-08-25) with the
-- worker's snapping parameters (radius 500 m, minimum_reachability 15), not
-- against the cluster's own tiles, which were not reachable from where this
-- was written. The anchors are chosen to sit on long, densely mapped roads
-- precisely so the choice does not hinge on one tile build. If a rebuild ever
-- leaves one of these degenerate again, re-probe and move the anchor; nothing
-- outside routing depends on these values.
--
-- Remove a station's routing_location (leaving it NULL, so seededNodes falls
-- back to `location`) once the built station's own street network is mapped.
--
-- ## Why a migration and not just the seed
--
-- internal/transit/data/scenarios/ca-hsr/stations.yaml carries the same three
-- values, which is enough for a database that has never been seeded. It is not
-- enough for a deployed one: SeedIfEmpty returns early on a populated store
-- (seed.go) and there is no reseed path. Same split 00015 and 00016 called
-- out — the seed serves fresh databases, a migration serves the deployed one.
-- CompileSeededIfNeeded (compile_seeded.go) notices the resulting drift
-- between the stored graph and what the rows now compile to, and recompiles on
-- the next boot.
--
-- No ALTER TABLE here, unlike 00016: the routing_location column already
-- exists from that migration, so this is pure UPDATEs. On an empty database
-- they touch zero rows (migrations run before SeedIfEmpty) and the seed
-- inserts the values from YAML a moment later; on a deployed one the rows
-- exist and the UPDATEs do the work. Re-running is safe either way: assigning
-- a value that is already there is a no-op.

UPDATE stations
SET routing_location = '{"type":"Point","coordinates":[-119.99,36.936]}'::jsonb
WHERE id = '00000000-0000-4005-8001-000000000006'::uuid;

UPDATE stations
SET routing_location = '{"type":"Point","coordinates":[-119.584,36.335]}'::jsonb
WHERE id = '00000000-0000-4005-8001-000000000008'::uuid;

UPDATE stations
SET routing_location = '{"type":"Point","coordinates":[-119.022,35.389]}'::jsonb
WHERE id = '00000000-0000-4005-8001-000000000009'::uuid;

-- +goose Down
-- Down only clears what Up set. The column itself belongs to 00016, and
-- las-vegas's anchor is 00016's to own, so neither is touched here.
UPDATE stations
SET routing_location = NULL
WHERE id IN (
  '00000000-0000-4005-8001-000000000006'::uuid,
  '00000000-0000-4005-8001-000000000008'::uuid,
  '00000000-0000-4005-8001-000000000009'::uuid
);
