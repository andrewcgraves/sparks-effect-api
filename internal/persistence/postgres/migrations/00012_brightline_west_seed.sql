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
    -- The Palmdale → Victor Valley leg is a High Desert Corridor planned-corridor
    -- approximation; Victor Valley → Las Vegas is traced from the OpenStreetMap
    -- I-15 median (© OpenStreetMap contributors, ODbL) and simplified at 10 m.
    -- The first and last vertices are the Palmdale and Las Vegas station
    -- coordinates exactly, so the spur starts at the Phase 1 junction rather
    -- than short of it. Must stay identical to the YAML geometry — see
    -- routes.yaml, and the test that compares the two literal for literal.
    '{"type":"LineString","coordinates":[
        [-118.119,34.591],[-118.1055,34.5948],[-118.0587,34.5948],[-117.95,34.5948],
        [-117.85,34.595],[-117.75,34.596],[-117.667,34.5975],[-117.6,34.596],
        [-117.52,34.586],[-117.46,34.57],[-117.418,34.5593],[-117.38,34.5572],
        [-117.34,34.5572],[-117.3,34.572],[-117.27,34.592],[-117.245,34.61],
        [-117.228,34.628],[-117.218,34.6443],[-117.216591,34.646499],[-117.216012,34.647926],
        [-117.210021,34.667987],[-117.209927,34.670056],[-117.210062,34.67137],[-117.213044,34.682434],
        [-117.213119,34.685165],[-117.21257,34.687809],[-117.212133,34.688898],[-117.19444,34.721858],
        [-117.192284,34.724791],[-117.167866,34.751629],[-117.165053,34.754385],[-117.100629,34.806405],
        [-117.099115,34.808017],[-117.098314,34.809106],[-117.097312,34.810935],[-117.096828,34.812372],
        [-117.082306,34.863104],[-117.081581,34.864949],[-117.080336,34.866999],[-117.07867,34.868835],
        [-117.063893,34.881861],[-117.061937,34.883221],[-117.060097,34.883957],[-117.058538,34.884329],
        [-117.049528,34.885645],[-117.045251,34.886058],[-117.014445,34.88593],[-117.011172,34.886036],
        [-117.009206,34.88639],[-117.007726,34.886896],[-117.006087,34.887763],[-116.983452,34.904186],
        [-116.980324,34.905592],[-116.972906,34.907898],[-116.97067,34.908362],[-116.969218,34.908425],
        [-116.967672,34.908301],[-116.957989,34.90663],[-116.9366,34.903659],[-116.924835,34.901527],
        [-116.897943,34.901108],[-116.825045,34.911061],[-116.822838,34.911161],[-116.809011,34.9107],
        [-116.806364,34.910896],[-116.72614,34.927357],[-116.72351,34.92808],[-116.682554,34.947125],
        [-116.56158,34.999957],[-116.558152,35.001338],[-116.4713,35.032618],[-116.468921,35.033948],
        [-116.402267,35.077526],[-116.400142,35.078576],[-116.398757,35.078983],[-116.396797,35.079243],
        [-116.389571,35.079322],[-116.386981,35.079626],[-116.385469,35.080089],[-116.379703,35.082763],
        [-116.377829,35.083364],[-116.375368,35.083821],[-116.337606,35.088069],[-116.335155,35.087969],
        [-116.331602,35.087267],[-116.32911,35.086967],[-116.326997,35.08696],[-116.323537,35.087412],
        [-116.319003,35.088265],[-116.317326,35.088747],[-116.295525,35.097457],[-116.293285,35.098128],
        [-116.290988,35.098544],[-116.269047,35.100413],[-116.266298,35.101051],[-116.264975,35.101512],
        [-116.261871,35.102992],[-116.229022,35.121898],[-116.216207,35.129516],[-116.214046,35.131157],
        [-116.163913,35.185384],[-116.161139,35.187849],[-116.154565,35.192762],[-116.152165,35.194028],
        [-116.149713,35.194669],[-116.12827,35.197212],[-116.125105,35.198208],[-116.123609,35.198966],
        [-116.121921,35.200096],[-116.120069,35.201881],[-116.119161,35.203146],[-116.091356,35.250309],
        [-116.089448,35.252718],[-116.087749,35.254183],[-116.051865,35.276897],[-116.050704,35.277815],
        [-116.049061,35.279454],[-116.046924,35.282392],[-116.045622,35.283778],[-116.043405,35.285547],
        [-115.953617,35.342153],[-115.916429,35.365485],[-115.912897,35.367433],[-115.908268,35.369206],
        [-115.829983,35.393218],[-115.810869,35.39754],[-115.791578,35.402685],[-115.739582,35.420783],
        [-115.703753,35.433678],[-115.607737,35.467454],[-115.592597,35.472755],[-115.589084,35.473667],
        [-115.584241,35.474388],[-115.575157,35.475105],[-115.572059,35.475073],[-115.569769,35.474724],
        [-115.56185,35.472658],[-115.555024,35.471271],[-115.544218,35.469783],[-115.525,35.467883],
        [-115.522663,35.467891],[-115.515267,35.468437],[-115.511755,35.469126],[-115.507087,35.470425],
        [-115.505275,35.470672],[-115.503547,35.470654],[-115.500885,35.470181],[-115.488083,35.466981],
        [-115.483236,35.465206],[-115.47701,35.462188],[-115.474434,35.461462],[-115.471582,35.461171],
        [-115.469476,35.461274],[-115.467775,35.461551],[-115.465205,35.462366],[-115.464224,35.462808],
        [-115.453513,35.468945],[-115.452027,35.469958],[-115.44981,35.472034],[-115.448486,35.473851],
        [-115.447917,35.47493],[-115.436593,35.501463],[-115.431051,35.515428],[-115.42464,35.529319],
        [-115.388204,35.614427],[-115.387671,35.616406],[-115.386495,35.623093],[-115.376211,35.683361],
        [-115.355793,35.741667],[-115.354318,35.744951],[-115.352983,35.747098],[-115.343481,35.760113],
        [-115.297236,35.823032],[-115.294558,35.825969],[-115.224451,35.886981],[-115.221682,35.889948],
        [-115.21694,35.896641],[-115.186114,35.940375],[-115.184012,35.944203],[-115.182741,35.947937],
        [-115.182076,35.952401],[-115.180926,35.984645],[-115.180379,36.009755],[-115.180771,36.027305],
        [-115.18081,36.039518],[-115.180674,36.12181],[-115.180612,36.12914],[-115.180454,36.131039],
        [-115.179499,36.134008],[-115.17807,36.136292],[-115.16711,36.148677],[-115.162576,36.153565],
        [-115.161726,36.154813],[-115.161076,36.156256],[-115.160746,36.157685],[-115.160714,36.162654],
        [-115.160307,36.165309],[-115.159175,36.16841],[-115.157525,36.171307],[-115.155329,36.173899],
        [-115.153415,36.175622],[-115.151684,36.176869],[-115.146328,36.17989],[-115.145457,36.180581],
        [-115.136,36.174]]}'::jsonb,
    true
FROM scenarios sc
WHERE sc.slug = 'ca-hsr'
ON CONFLICT (id) DO NOTHING;

INSERT INTO stations (id, scenario_id, slug, name, location, platform_height)
SELECT v.id::uuid, sc.id, v.slug, v.name, v.location::jsonb, 'high'
FROM scenarios sc
CROSS JOIN (VALUES
    -- Victor Valley is the in-line I-15 median station at Dale Evans Parkway
    -- (exit 161), not Apple Valley town centre — see stations.yaml.
    ('00000000-0000-4005-8001-00000000000b', 'victor-valley', 'Victor Valley (Apple Valley)',
     '{"type":"Point","coordinates":[-117.218,34.6443]}'),
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
