# Prerendered isochrones — `ca-hsr`

Every `*.json` file in this directory is one curated, already-computed
isochrone that ships with the `ca-hsr` scenario. `transit.SeedPrerenderedIsochrones`
walks this directory on every boot (see `internal/transit/seed_prerendered.go`,
called from `cmd/api/main.go` after `CompileSeededIfNeeded`) and inserts any
file whose `id` is not already stored. Dropping a file in here is the whole of
the deployment step — there is no code change, no migration, and no manual
post-deploy action.

## File format

One JSON object per file, self-describing:

```json
{
  "id": "0f1d3a6c-1c9a-4d5b-9f2e-8a1b2c3d4e5f",
  "label": "San Jose — 240 min by bike",
  "lat": 37.3297,
  "lng": -121.9020,
  "budget_mins": 240,
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

Payloads run roughly 300–500 KB each. That is expected and is why the list
endpoint never selects the column.

## What ships here

Both CA HSR sample payloads are present. Each was captured from a real
succeeded routing job against the seeded `ca-hsr` graph; the envelope's
`lat`/`lng`/`budget_mins`/`mode` are that job's own, and `result` is its
payload byte for byte.

| filename | label | origin | budget | mode |
| --- | --- | --- | --- | --- |
| `isochrone-sj-240-bike.json` | San Jose - 240 min by bike | San Jose (`san-jose`) | 240 | `bike` |
| `isochrone-burbank-walk.json` | Burbank Airport - 240 min on foot | Burbank Airport (`burbank-airport`) | 240 | `walk` |

Their ids follow this scenario's hand-written UUID convention, where the third
group names the kind: `4006` for a prerendered isochrone, as `4005` is a
station and `4004` a service.

To add another, capture a succeeded routing job for this scenario, wrap its
payload in the envelope above under a fresh `4006` id, and commit it here. The
next boot picks it up.

## Endpoints they surface on

- `GET /api/scenarios/ca-hsr/prerendered-isochrones` — metadata only, no `result`.
- `GET /api/prerendered-isochrones/{id}` — the same metadata plus `result`.

Both report `"outdated": true` once the scenario's service membership has moved
on from the snapshot taken when the entry was seeded. An outdated entry is
still served in full; see `transit.MembershipStale`.
