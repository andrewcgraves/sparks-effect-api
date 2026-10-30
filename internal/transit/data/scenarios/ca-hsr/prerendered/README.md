# Prerendered isochrones — `ca-hsr`

Every `*.json` file in this directory is one curated, already-computed
isochrone that ships with the `ca-hsr` scenario. `transit.SeedPrerenderedIsochrones`
walks this directory on every boot (see `internal/transit/seed_prerendered.go`,
called from `cmd/api/main.go` after `CompileSeededIfNeeded`) and inserts any
file whose `id` is not already stored. Adding an entry is the whole of the
deployment step: drop a file in here, with no code change, migration or
manual post-deploy action. Retiring one does need a migration; see below.

## File format

One JSON object per file, self-describing:

```json
{
  "id": "00000000-0000-4006-8001-000000000004",
  "label": "San Jose (Diridon) - 60 min by bike",
  "lat": 37.33,
  "lng": -121.903,
  "budget_mins": 60,
  "mode": "bike",
  "result": { }
}
```

| field | type | notes |
| --- | --- | --- |
| `id` | uuid string | **Stable identity.** The seeder skips a file whose id is already stored, which is what makes running it on every boot a no-op. Editing a payload in place does *not* republish it; give the revised entry a new id. |
| `label` | string | Non-empty. What the page calls this isochrone. |
| `lat`, `lng` | number | WGS84 origin the isochrone is centred on. |
| `budget_mins` | integer | Greater than 0. |
| `mode` | string | One of `walk`, `bike`, `drive`, `transit` (`transit.TravelMode`). |
| `result` | any JSON | **Opaque.** The isochrone payload, stored and served byte for byte. Nothing in this API parses, validates, or rewrites it — exactly like `routing_jobs.result`. Whatever the routing worker produced is what belongs here. |

A malformed file — missing id, empty label, unknown mode, non-positive budget,
absent result — aborts the boot rather than being skipped. This is
repo-authored data, so a mistake in it is worth failing loudly on.

Payloads run from tens of kilobytes to roughly 500 KB, growing with budget
and mode. That is why the list endpoint never selects the column.

## What ships here

These are the prerendered isochrones the home page leads with, so a visitor's
first plots cost no routing job. Each was captured from a real succeeded
routing job on staging, whose worker routes on Railway's real Valhalla,
against the `ca-hsr` graph with the HSR Express running (compile
`a5080656`). The envelope's `lat`/`lng`/`budget_mins`/`mode` are that job's
own, and `result` is its payload byte for byte.

| filename | label | origin | budget | mode |
| --- | --- | --- | --- | --- |
| `isochrone-sf-ferry-building-180-transit.json` | San Francisco (Ferry Building) - 180 min by transit | the Ferry Building | 180 | `transit` |
| `isochrone-fresno-chukchansi-park-120-walk.json` | Fresno (Chukchansi Park) - 120 min on foot | the Fresno Grizzlies' ballpark | 120 | `walk` |
| `isochrone-sj-diridon-90-bike.json` | San Jose (Diridon) - 90 min by bike | `san-jose` | 90 | `bike` |
| `isochrone-bakersfield-45-walk.json` | Bakersfield - 45 min on foot | `bakersfield`'s `routing_location` | 45 | `walk` |
| `isochrone-la-union-station-120-transit.json` | Los Angeles (Union Station) - 120 min by transit | `los-angeles` | 120 | `transit` |

Bakersfield starts from the station's `routing_location`, not its `location`:
the latter snaps onto a `railway=proposed` node, which would leave a walk with
almost no street to cover (see `stations.yaml`).

Their ids follow this scenario's hand-written UUID convention, where the third
group names the kind: `4006` for a prerendered isochrone, as `4005` is a
station and `4004` a service. The five above are `…000b` to `…000f`. Every
earlier id is retired, and none may be reused:

- `…0001` (San Jose, 240 min by bike) and `…0002` (Burbank Airport, 240 min
  on foot) were captured before the Express ran. Migration 00035 deletes them.
- `…0003` (San Francisco, 90 min by transit) and `…0006` (Los Angeles, 90 min
  by transit) gave way to 135-minute entries in SPA-532, because SF's plot
  only reached Gilroy. Migration 00038 deletes them.
- `…0004`, `…0005`, `…0007`, `…0008`, `…0009` and `…000a` (San Jose 60 min by
  bike, Fresno 120 min by transit, Anaheim 60 min on foot, Burbank Airport
  120 min on foot, and the SF and LA 135-minute transit plots) gave way to the
  current five when the home page's picks were redrawn. Migration 00039
  deletes them.

Every mode chains through the HSR graph, walk and bike included. So any
change to `ca-hsr`'s services leaves these payloads describing the old
network, and the API flags them outdated. To refresh one, capture it again
and commit it under a new id. Then retire the old id the way 00035 does: the
seeder only inserts, so deleting the file alone leaves the row behind.

To add another, capture a succeeded routing job for this scenario, wrap its
payload in the envelope above under a fresh `4006` id, and commit it here. The
next boot picks it up.

## Endpoints they surface on

- `GET /api/scenarios/ca-hsr/prerendered-isochrones` — metadata only, no `result`.
- `GET /api/prerendered-isochrones/{id}` — the same metadata plus `result`.

Both report `"outdated": true` once the scenario's service membership has moved
on from the snapshot taken when the entry was seeded. An outdated entry is
still served in full; see `transit.MembershipStale`.
