-- +goose Up
-- Correct the Las Vegas station coordinate and the Brightline West spur's
-- terminus (SPA-222).
--
-- ## What was wrong
--
-- The `las-vegas` station was seeded at an uncorrected city-centroid geocode,
-- [-115.136, 36.174] — 13.8 km from the real FRA-cleared Brightline West
-- terminus in Enterprise, NV (south Strip, between Blue Diamond Rd and Warm
-- Springs Rd), at [-115.1778, 36.0545]. See SPA-214 for the root-cause
-- writeup.
--
-- The spur's route geometry pins its last vertex to the station coordinate
-- exactly, so the traced I-15 median alignment inherited the same error: its
-- "closest approach to the station" pivot landed 13 km further up the median
-- than it should have, and the straight tail into the terminus ran the rest
-- of the way into the wrong part of the city. Both feed seededNodes
-- (internal/transit/compile.go) directly, so the last-mile isochrone around
-- the Vegas stop and the map pin were centered in the wrong place.
--
-- ## Why a migration and not just the seed
--
-- The correction is authored in stations.yaml and routes.yaml, which is
-- enough for a database that has never been seeded. It is not enough for a
-- deployed one: SeedIfEmpty returns early on a populated store (seed.go) and
-- there is no reseed path, so an already-seeded database would keep the wrong
-- coordinate and the overshooting alignment forever. This is the same split
-- 00012 and 00013 called out — the seed serves fresh databases, a migration
-- serves the deployed one. 00012 itself is left as originally written rather
-- than edited in place, for the same reason 00003's original Phase 1
-- geometry was left alone when 00013 corrected it: a migration is a record of
-- what a deployed database was told to do, and rewriting that record changes
-- what already-applied history means.
--
-- ## Why this is inert on a fresh database
--
-- Migrations run before SeedIfEmpty, so on an empty database neither the
-- las-vegas station nor the Brightline West route exists yet and both
-- UPDATEs below touch zero rows; the seed then inserts the corrected station
-- and geometry from YAML a moment later. On a deployed database the rows
-- exist and the UPDATEs do the work instead. Either way the result ends up
-- identical, which is why routes.yaml and this literal are pinned to each
-- other by TestLasVegasStationCoordinateMigrationGeometryMatchesTheSeed, and
-- the station coordinate by
-- TestLasVegasStationCoordinateMigrationStationMatchesTheSeed.
--
-- Re-running is safe: assigning a value that is already there is a no-op.

UPDATE stations
SET location = '{"type":"Point","coordinates":[-115.1778,36.0545]}'::jsonb
WHERE id = '00000000-0000-4005-8001-00000000000f'::uuid;

UPDATE routes
SET geometry = '{"type":"LineString","coordinates":[
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
        [-115.18081,36.039518],[-115.1778,36.0545]]}'::jsonb
WHERE id = '00000000-0000-4002-8001-000000000002'::uuid;

-- +goose Down
-- Deliberately empty.
--
-- The inverse of this migration is the wrong coordinate and the overshooting
-- alignment it replaced, and there is nothing worth reconstructing from that:
-- the seed now carries the corrected station and geometry, so a down
-- migration would have to embed a second copy of data whose only property is
-- that it is wrong. Rolling the application back does not require rolling
-- the data back — nothing reads these columns in a way that a correct
-- coordinate breaks — so the trade is not worth making.
--
-- If the pre-fix values are genuinely needed, they are in git: stations.yaml
-- and routes.yaml as they stood immediately before SPA-222's fix landed.
