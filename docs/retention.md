# Retention

Housekeeping for the isochrone cache and for routing job results. It is an
authenticated admin call, `POST /api/admin/retention`, behind the same admin
gate as `POST /api/admin/routes`.

Nothing in this runs at boot, and deploying it deletes nothing. The API has no
cron. The only other housekeeping, `DeleteExpiredSessions`, runs at boot
because a missed session prune is harmless — a missed dry-run here is not.
Dry-run the selection against production, read the counts, then enable by
calling the endpoint with `apply`.

## Dry-run

An empty body and `{"apply":false}` are the same request. The handler counts
and rolls the transaction back. Rows stay.

```sh
curl -s -X POST "$API/api/admin/retention" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json"
```

```sh
curl -s -X POST "$API/api/admin/retention" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"apply":false}'
```

The response is the counts, with `dry_run` true:

```json
{
  "dry_run": true,
  "isochrone_cache_rows": 0,
  "isochrone_cache_superseded": 0,
  "isochrone_cache_stale_transit": 0,
  "routing_job_results_cleared": 0
}
```

A malformed body, a non-bool `apply` (`"true"`, `1`, `null`), or any unknown
field is 400. Unknown fields are rejected so a typo cannot silently dry-run.
The body cap is the same 4 KiB as the other small admin bodies.

## Reading the counts

`isochrone_cache_rows` is the distinct cache rows matching either rule below.
That is what apply deletes.

`isochrone_cache_superseded` is rows whose compile job is outside the live
set: not the latest succeeded compile for that target, and not a
publication's pinned compile. `isochrone_cache_stale_transit` is transit
rows whose service date is outside the service-date window, including rows
that still hang off a live compile or a pin.

The two predicate counts overlap. A row that is both superseded and stale is
counted in each of them, and once in `isochrone_cache_rows`. Do not add the
two predicate counts and expect the total.

`routing_job_results_cleared` is routing job results that would be set NULL,
or that were. The row stays.

A dry-run and the apply that follows report the same counts when nothing else
writes in between. On apply, the two totals are the rows the statements
actually changed.

## Enable

```sh
curl -s -X POST "$API/api/admin/retention" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"apply":true}'
```

`dry_run` is false. A second apply, with nothing else writing, reports zeros.

Both the dry-run and the apply are logged at info with the same counts.

## The two windows

They differ because the rows mean different things.

**Service-date window, 7 calendar days**
(`TransitCacheServiceDateWindowDays`). A cache row is deleted when
`mode = 'transit'`, `departs_on` is set, and `departs_on` is older than
`CURRENT_DATE` minus 7 days. That includes rows on a live compile: the
polygon was cut for a service date the worker will not ask for again. Seven
days covers about five weekday service dates (the worker's WeekdayDepartAt
clock), the weekend roll onto Monday, and a day of timezone skew between the
worker's local date and the database `CURRENT_DATE`. Walk, bike and drive
store NULL `departs_on` and do not match.

The other cache rule is not a window. A row goes when its `compile_job_id` is
outside the live set. That set is the latest **succeeded** compile for its
target — the same ordering as `GetLatestSucceededJob`, the user-scenario and
user-service equivalents, and `LatestSucceededCompileJob`: same kind,
non-null target, greatest `created_at`, tie-break `id DESC`. The three
targets are `compile_scenario` / `scenario_id`, `compile_user_scenario` /
`user_scenario_id`, and `compile_user_service` / `user_service_id`. A newer
running or failed compile does not supersede. A job whose target was SET NULL
(a seeded scenario deleted) is not in that live set, so its cache is
unreachable and this rule removes it.

A publication's pinned compile stays live for this rule even when it is not
the latest succeeded draft. `service_publications.compile_job_id` is the
graph `POST /api/services/{slug}/publication/isochrone` still plots after a
newer `compile_user_service` succeeds. The pin is not spared from the
service-date window: a stale transit row on it is still deleted, and its
walk, bike and drive rows stay. The delete is `NOT EXISTS` against those
ids. It is never `DELETE FROM jobs`. The publication foreign key is
`ON DELETE NO ACTION`, so a sweep that deleted a pinned job would fail
rather than drop the publication.

**Routing job result, 30 days** (`RoutingJobResultRetentionDays`). `result`
is set NULL when it is non-null, `status` is `succeeded` or `failed`, and
`updated_at` is older than 30 days. `queued` and `running` are left alone.
`updated_at` is not bumped: it is when the worker wrote the payload, and a
result written recently must survive even if the row was inserted earlier.

The payload is 300–500 KB and is the growth; the row itself is history, so
the row stays. Deleting a `jobs` row would cascade into `isochrone_cache` and
`routing_jobs` and destroy the reuse window below.

SPA-331 will serve an identical repeat from a previous succeeded routing job
result, keyed on compile job, origin, budget, mode, service date and tileset.
That reuse window is still open — SPA-331 has not shipped. The service-date
clock is at most a few days (today, or Monday when today is a weekend).
Thirty days was chosen to sit well outside that clock, so this sweep cannot
eat a transit result the lookup would still serve. A non-transit repeat older
than 30 days recomputes. A NULL result must be treated as a miss by that
lookup.

## What is never swept

- `jobs`. Cache for a superseded compile is deleted from `isochrone_cache`
  directly.
- `routing_jobs` rows. Only `result` is cleared, and only on terminal rows
  past the 30-day window.
- Prerendered isochrones. That is a different table, curated by hand and
  served until someone removes it. No statement in this sweep mentions
  `prerendered_isochrones`.
