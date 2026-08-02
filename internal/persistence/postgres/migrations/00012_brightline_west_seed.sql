-- +goose Up
-- The Brightline West spur (High Desert Corridor) for the seeded ca-hsr
-- scenario: Palmdale → Victor Valley → Las Vegas (SPA-153).
--
-- ## Why a migration and not just the seed
--
-- The spur is authored in internal/transit/data/scenarios/ca-hsr/*.yaml, which
-- is enough for a database that has never been seeded. It is not enough for a
-- deployed one: SeedIfEmpty returns early on a populated store (seed.go) and
-- there is no reseed path, so an already-seeded database would keep its
-- single-corridor ca-hsr forever and the isochrone "time between stations"
-- table would stay single-group — the exact thing SPA-153 exists to fix. This
-- is the same split 00011 called out for segment route ids: the seed serves
-- fresh databases, a migration serves the deployed one.
--
-- ## Why this is a no-op on a fresh database
--
-- Migrations run before SeedIfEmpty, so on an empty database the scenarios
-- table has no ca-hsr row yet and every statement below selects zero rows. The
-- seed then inserts the spur from YAML. On a deployed database the ca-hsr row
-- exists and these statements do the work instead. Either way the spur lands
-- exactly once, and the ids match the YAML so the two paths agree.
--
-- Re-running is safe: ON CONFLICT DO NOTHING covers the id-keyed tables and the
-- segment inserts guard on NOT EXISTS, since segments have a generated id and
-- no natural unique key to conflict on.
--
-- Ids are the ones authored in YAML, deliberately: a row minted here must be
-- the same row the seed would have written, or the two databases diverge.
-- Frequency-window ids are the exception — the PK there is persistence
-- identity, not domain data (see CreateService), so it is minted fresh.

INSERT INTO routes (id, scenario_id, slug, name, mode, geometry, bidirectional)
SELECT
    '00000000-0000-4002-8001-000000000002'::uuid,
    sc.id,
    'brightline-west-palmdale-to-las-vegas',
    'Brightline West — Palmdale to Las Vegas',
    'rail',
    -- First vertex is the Palmdale station coordinate exactly, so the spur
    -- starts at the Phase 1 junction rather than ~1.5 km short of it. Must stay
    -- identical to the YAML geometry — see routes.yaml.
    '{"type":"LineString","coordinates":[
        [-118.119,34.591],[-117.8,34.55],[-117.45,34.51],[-117.185,34.5],
        [-116.9,34.6],[-116.7,34.8],[-116.5,35.1],[-115.8,35.6],
        [-115.3,35.9],[-115.136,36.174]]}'::jsonb,
    true
FROM scenarios sc
WHERE sc.slug = 'ca-hsr'
ON CONFLICT (id) DO NOTHING;

INSERT INTO stations (id, scenario_id, slug, name, location, platform_height)
SELECT v.id::uuid, sc.id, v.slug, v.name, v.location::jsonb, 'high'
FROM scenarios sc
CROSS JOIN (VALUES
    ('00000000-0000-4005-8001-00000000000b', 'victor-valley', 'Victor Valley (Apple Valley)',
     '{"type":"Point","coordinates":[-117.185,34.5]}'),
    ('00000000-0000-4005-8001-00000000000f', 'las-vegas', 'Las Vegas',
     '{"type":"Point","coordinates":[-115.136,36.174]}')
) AS v (id, slug, name, location)
WHERE sc.slug = 'ca-hsr'
ON CONFLICT (id) DO NOTHING;

INSERT INTO services
    (id, scenario_id, route_id, vehicle_type_id, name, direction, active, provenance)
SELECT
    '00000000-0000-4004-8001-000000000003'::uuid,
    sc.id,
    '00000000-0000-4002-8001-000000000002'::uuid,
    '00000000-0000-4003-8001-000000000001'::uuid,
    'Brightline West',
    'both',
    true,
    'calibrated'
FROM scenarios sc
WHERE sc.slug = 'ca-hsr'
ON CONFLICT (id) DO NOTHING;

-- Palmdale is the Phase 1 station the spur branches from, so it is a stop of
-- this service too — that shared stop is what makes the interchange work.
INSERT INTO service_stops (service_id, station_id, sequence)
SELECT '00000000-0000-4004-8001-000000000003'::uuid, v.station_id::uuid, v.sequence
FROM (VALUES
    ('00000000-0000-4005-8001-00000000000a', 1),
    ('00000000-0000-4005-8001-00000000000b', 2),
    ('00000000-0000-4005-8001-00000000000f', 3)
) AS v (station_id, sequence)
WHERE EXISTS (
    SELECT 1 FROM services WHERE id = '00000000-0000-4004-8001-000000000003'::uuid
)
ON CONFLICT (service_id, sequence) DO NOTHING;

INSERT INTO frequency_windows (id, service_id, start_time, end_time, headway_s)
SELECT gen_random_uuid(), '00000000-0000-4004-8001-000000000003'::uuid, '06:00', '22:00', 7200
WHERE EXISTS (
    SELECT 1 FROM services WHERE id = '00000000-0000-4004-8001-000000000003'::uuid
)
AND NOT EXISTS (
    SELECT 1 FROM frequency_windows
    WHERE service_id = '00000000-0000-4004-8001-000000000003'::uuid
);

INSERT INTO scenario_service (scenario_id, service_id)
SELECT sc.id, '00000000-0000-4004-8001-000000000003'::uuid
FROM scenarios sc
WHERE sc.slug = 'ca-hsr'
  AND EXISTS (
      SELECT 1 FROM services WHERE id = '00000000-0000-4004-8001-000000000003'::uuid
  )
ON CONFLICT (scenario_id, service_id) DO NOTHING;

-- Run-only times, matching segment_run_times.yaml; dwell is added at compile
-- time. route_id is the spur's own route, which is what keeps these run times a
-- separate group from Phase 1's.
INSERT INTO segments (scenario_id, from_slug, to_slug, run_seconds, route_id)
SELECT sc.id, v.from_slug, v.to_slug, v.run_seconds,
       '00000000-0000-4002-8001-000000000002'::uuid
FROM scenarios sc
CROSS JOIN (VALUES
    ('palmdale', 'victor-valley', 1050),
    ('victor-valley', 'las-vegas', 5310)
) AS v (from_slug, to_slug, run_seconds)
WHERE sc.slug = 'ca-hsr'
  AND EXISTS (
      SELECT 1 FROM routes WHERE id = '00000000-0000-4002-8001-000000000002'::uuid
  )
  AND NOT EXISTS (
      SELECT 1 FROM segments s
      WHERE s.scenario_id = sc.id
        AND s.from_slug = v.from_slug
        AND s.to_slug = v.to_slug
  );

-- +goose Down
-- Deleting the route cascades to its segments (00011) and the service's own FK
-- is RESTRICT, so the service and its stops/windows/membership go first.
DELETE FROM scenario_service WHERE service_id = '00000000-0000-4004-8001-000000000003'::uuid;
DELETE FROM services WHERE id = '00000000-0000-4004-8001-000000000003'::uuid;
DELETE FROM segments WHERE route_id = '00000000-0000-4002-8001-000000000002'::uuid;
DELETE FROM routes WHERE id = '00000000-0000-4002-8001-000000000002'::uuid;
DELETE FROM stations WHERE id IN (
    '00000000-0000-4005-8001-00000000000b'::uuid,
    '00000000-0000-4005-8001-00000000000f'::uuid
);
