# CONTEXT.md — the Sparks Effect domain glossary

The vocabulary this project's code, tickets and commit messages are written in.
It is the canonical copy: `sparks-effect-routing-worker`, `sparks-effect-website`
and `kustomize-config` each keep a `CONTEXT.md` for the terms *they* own and link
back here for everything else. Every term is defined in exactly one of those four
files, so there is nothing to keep in sync.

Read it before naming a new type, endpoint, column or seam. Where a term already
exists here, use it; where the code and this file disagree, the code is right and
this file is a bug.

> Declaration-level doc comments were deliberately removed from the source in
> SPA-307 — they restated their declarations. Domain meaning lives here; the
> *why* behind a decision lives in [ADRs](docs/adr/0001-api-and-worker-share-no-go-code.md),
> in-function comments, migration headers and the README. Do not reintroduce
> godoc that only repeats a name.

## The one that catches everyone: two scenarios, two services

*Scenario* and *service* each name two different models, and which one you get
depends on the URL space you are in. This is the single most confusing thing in
the codebase and it is not going to be renamed — so know which one you are
holding.

| Term | Model | Lives at | Is |
| --- | --- | --- | --- |
| **Scenario** | `transit.Scenario` | `/api/scenarios` (public), `/api/me/scenarios` (owner-scoped) | The seeded world model: metadata plus routes, stations, services and segment times hanging off it |
| **UserScenario** | `transit.UserScenario` | `/api/user-scenarios` | **A different model entirely** — a named set of `UserService` ids, plus declared interchange pairs and a boarding-wait override. No stations, no routes, no segment times of its own |
| **Service** | `transit.Service` | `/api/scenarios/{slug}/services` (public), `/api/me/services` (owner-scoped) | A stopping pattern over a `Route`, calling at `Station` rows, driven by a `VehicleType` row |
| **UserService** | `transit.UserService` | `/api/services` — *not* `/api/me/services` | The authored counterpart: carries its own stops (name + coordinate, snapped to a route) and inline `VehicleParams` instead of pointing at rows |

Two traps worth stating out loud:

- `/api/services` returns **UserServices**. `/api/me/services` returns
  **Services**. The `/api/me` prefix is not what distinguishes authored from
  seeded — both collections are owner-scoped. The model is different.
- Both scenario models compile to the same `TransitGraph` and both produce a
  compile job, so downstream of a compile the distinction disappears. It only
  matters on the way in.

### Seeded / curated / authored / owned

One more overload, on the same models. Every seeded-model row (`scenarios`,
`routes`, `stations`, `services`) carries a nullable `owner_id`:

- **Curated** — `owner_id IS NULL`. The seeded `ca-hsr` baseline and
  admin-ingested alignments. Served by the public reads, writable only by an
  admin. **Seeded** is the narrower word for the subset that ships embedded in
  the binary under `internal/transit/data/scenarios/`.
- **Owned** / **authored** — `owner_id` set. Someone made it; it is theirs; it
  never appears on a public surface. The two words are interchangeable, and
  "authored" is preferred in prose.

**A scenario and all of its children share one owner.** A curated scenario has
curated children; an owned scenario's routes, stations, services and segments
belong to the same person. The sole exception is a standalone route with no
scenario, which carries its own owner. That invariant is why only
`ListCuratedScenarios` and `ListCuratedRouteSummaries` filter on ownership —
scoping to a scenario has already scoped to its owner.

The person those `owner_id` columns point at is an `account.User`, not a transit
type. `User` and `Session` live in `internal/account`.

## Core nouns

| Term | Definition |
| --- | --- |
| **Route** / **alignment** | The geometry, as a GeoJSON LineString, plus a mode and optional per-segment engineering parameters. Several services can run over one alignment. "Route" is the type name (`transit.Route`); "alignment" is the word used in prose, and is preferred when the geometry rather than the row is meant. A curated alignment is a public building block anyone may point a service at, but admin-only to mutate |
| **Station** | A seeded-model place a service can call at: slug, name, `location`, platform height, optional [routing anchor](#routing-anchor). Belongs to a scenario |
| **Stop** | A *service calling at a place*, not a place. `ServiceStop` on the seeded model (a station id, a sequence number, an optional dwell override); `ServiceStopPoint` on the authored model (its own name, slug, coordinate, seq, chainage and offset). A station is a noun; a stop is a relationship |
| **Node** / **GraphNode** | A vertex of the compiled graph. Stops **merge into** nodes at compile time — see [interchange](#interchange). `GraphNode` is the compiled form (slug, coordinate, optional routing anchor, and every merged-in `Names` entry). The reduced slug-plus-coordinate `Node` is a test-only projection for comparing the embedded store against a compiled graph; it is not a production type |
| **TransitGraph** | The compile output, and the only thing an isochrone is ever plotted over: per-service edge lists, the merged nodes, and a merge report. Persisted as a succeeded compile job's `result`, and travels inline on the queue message so the worker needs no database. This API compiles the graph; it does not compute isochrones |
| **ServiceGraph** | One service's slice of a `TransitGraph`: its edges, plus the boarding wait resolved for it at compile time |
| **Edge** | A directed hop between two node slugs on one service. `Seconds` is **run time plus dwell**; `DwellS` reports the dwell part separately rather than adding to it. Since SPA-264 an edge also records the corridor it runs over (`RouteID`) and its endpoints' chainages. Every hop is emitted in both directions — the reverse edge is the same hop backwards, carrying the same two chainages swapped, so one of the two directions always has descending chainages. Nothing that reads them treats that as a special case |
| **VehicleType** | Rolling stock on the seeded model: top speed, acceleration, deceleration, floor height, and the two dwell figures. The authored model inlines the same numbers as `VehicleParams` instead |
| **Compile job** | A row in `jobs`. Kinds: `compile_scenario`, `compile_user_scenario`, `compile_user_service`. Its `result` is a `TransitGraph`. Compilation runs **in-process in this API**, in `internal/compile`. That package is not the routing worker — the routing worker is a separate repository |
| **Routing job** | A row in `routing_jobs`. Its `result` is the worker's isochrone GeoJSON. Created by the isochrone endpoints, executed **in the routing worker**, written back over `/api/internal/...`. Different table, different owning process — a "job" with no qualifier is ambiguous, so always say which |
| **Prerendered isochrone** | An admin-curated, ready-to-display isochrone stored against a scenario, so a public page can show a result without enqueueing one. Also called a *curated* isochrone |
| **User** | `account.User` — an authenticated person. Authored rows point at them through `owner_id`. `is_admin` is the only privilege bit: it gates curated writes and `/api/admin/users` |
| **Session** | `account.Session` — a hashed bearer token bound to a User, with an expiry. The raw token is returned once at login and never stored |

## Terms of art

Words with a local meaning that is narrower — or just different — than the
everyday one.

### Placement on an alignment

- **Chainage** — distance in metres along an alignment, measured from its first
  vertex, of the point a stop snaps to (`chainage_m`). It is a position, not a
  distance travelled.
- **Chainage order** — the property that a service's stops have monotonic
  chainage in `seq` order. **Monotonicity, not ascent**: a service running the
  alignment backwards is in chainage order with descending values. What fails is
  doubling back — reported as the `chainage_order` stop-placement fault.
- **Offset** — the perpendicular distance in metres from a stop's authored
  coordinate to the alignment it snapped to (`offset_m`). It is a **budget**:
  `transit.OffRouteThresholdM` is 500 m, and a stop past it is an `off_route`
  fault. The same number is also spent a second way — see the merge radius under
  [interchange](#interchange).
- **Snap** — project a stop onto an alignment: rewrite its coordinate to the
  projected point and record the resulting chainage and offset. `SnapToRoute` is
  all-or-nothing; every stop is checked before any is rewritten, so a service is
  never left half-snapped.

### Time

- **Dwell** — seconds a vehicle stands at a station. Folded into `Edge.Seconds`
  and reported separately as `Edge.DwellS`; readers must not add the two.
  Resolved per stop from the vehicle: `dwell_level_s` when the platform height
  matches the vehicle's floor height, otherwise `dwell_step_s`, with a per-stop
  `dwell_s` overriding both.
- **Boarding wait** — seconds charged for waiting for the first vehicle. It is
  charged **once, at the origin of a path**, and is explicitly **not a transfer
  penalty**: the routing worker's graph search — and the test-only
  `TravelTimeBetween` used to check graph equivalence — add a service's
  `WaitSecs` only on the hop leaving the path's origin
  station, so riding through an interchange costs nothing extra.
  Policies are `none` (the default), `half_headway`, `full_headway`, and `fixed`.
  Resolution order is service override → scenario override → global
  `BOARDING_WAIT_POLICY`, reported back as `boarding_wait_source`.
- **Headway** — seconds between consecutive departures. `half_headway` is
  `min(headway) / 2` across a service's windows.
- **Frequency window** — a `start_time`/`end_time` pair with a headway. A
  service carries a list of them.
- **Run time** — time in motion, dwell excluded. The intended semantics of a
  `travel_times.yaml` segment, and what `SegmentTime.RunSeconds` holds.

### Interchange

**There is no transfer edge.** The whole of interchange is *two services
emitting an edge under one node key*. Nothing in the graph represents changing
trains; a path that arrives on one service and leaves on another simply passes
through a node both of them touch.

- **Co-located** — two stops close enough to merge. The test is the **effective
  merge radius**: `MergeRadiusM` (50 m) plus both stops' offsets, capped at
  `MaxMergeRadiusM` (500 m). Widening by offset is deliberate — two stops
  authored at the same real place but snapped to different alignments end up
  offset apart, and merging them is the right answer.
- **Cluster** — the set of stops that merged. Its **key** is its smallest member
  slug, and that key becomes the node's slug; `Names` collects every distinct
  name that merged in. A cluster never contains two stops of the same service.
- **Interchange pair** — a declared merge, on a `UserScenario`, naming two stops
  by `(service_id, slug)`. Declared pairs are folded together after automatic
  clustering regardless of distance, so an author can join what geometry did not.
- **Near miss** — a pair of stops on different services within
  `NearMissRadiusM` (250 m, five times the base radius) that did **not** end up
  in one cluster. Reported in the merge report as a hint, never an error.

### State and freshness

- **Staleness** — a compiled graph no longer matching the inputs it was compiled
  from: membership changed, a member service was updated after the compile, a
  boarding-wait policy changed, or edges predate the corridor decoration. The
  authored isochrone endpoints refuse with 409 and `stale_graph`.
- **Outdated** — the same idea for a prerendered isochrone, and deliberately a
  different word. An outdated entry is still served; `outdated` is a boolean on
  the read, not a refusal. Say *stale* about graphs and *outdated* about
  prerendered isochrones, and neither about the other.
- **Backlog** / **in-flight** — in-flight routing jobs are those `queued` or
  `running` and younger than `handler.RoutingJobStaleAfter`; the age bound is
  what stops a dead worker's abandoned rows wedging the cap shut forever. The
  backlog is the count of them. At `MAX_INFLIGHT_ISOCHRONES` an enqueue is
  refused with 429 and `backlog_full`. The cap is per deployment, not per caller.
- **Park** / **parked** — `Service.Active = false`. A parked service stays in the
  seed and in the database, documented, but is skipped by the compiler. It is how
  a stopping pattern is retired without deleting it. (CA HSR's HSR Express is the
  standing example.)
- **Provenance tier** — how a service's timings were arrived at, and therefore
  which editor levers are honest: `computed` (physics-compiled, all levers),
  `calibrated` (imported timetable run times; dwell, frequency and stops
  editable, vehicle swap disabled), `frozen` (geometry-less import; display and
  frequency/wait only). Beware: `travel_time_sets.provenance` is a *different*,
  free-form field describing where a segment-time set came from (e.g.
  `authored`) — it does not take the three tiers.

### Reach and routing

<a id="routing-anchor"></a>

- **Reach** — `speed × budget`, a straight-line **bound** on how far a mode can
  get in a budget, per `geo.ReachKm` at walk 5, bike 15, drive 80, transit
  40 km/h. Deliberately not an estimate and never an answer: its only job is to
  refuse, before any work is done, an origin that could not reach *any* station
  even travelling straight there. That refusal is `origin_out_of_range`.
- **Routing anchor** — `Station.RoutingLocation`, surfaced on the compiled node
  as `routing_lat`/`routing_lng` and read **only** by the routing worker, which
  centres its Valhalla calls there instead of on `location`. It is a provisional
  stand-in for a station whose true position snaps to nothing routable — farm
  tracks, a `railway=proposed` node, a planned viaduct — where the egress
  isochrone would otherwise collapse to a fraction of its real size.
  **It is never a map pin.** `location` remains the accurate place the station
  is, and is what the map, the route geometry and the record show.
- **Slug** — the URL-safe identity a model is addressed by. Slugs are **globally
  unique across curated and owned rows**, which is why by-slug reads cannot
  filter on ownership and check it themselves instead. Two minting functions
  exist and are not interchangeable: `route.Slugify` and `transit.Slugify`, the
  latter truncating at 80 characters and therefore not idempotent at the margin —
  re-slugifying an already-suffixed slug can cut off the suffix that made it
  unique.

## Travel mode vs costing

The most important pair in the codebase, and the one most often got wrong.

| Domain vocabulary (**mode**) | Valhalla's vocabulary (**costing**) |
| --- | --- |
| `walk` | `pedestrian` |
| `bike` | `bicycle` |
| `drive` | `auto` |
| `transit` | `multimodal` |

**Mode** is the domain's word and the only one that appears in stored data, on
the wire, in the queue message, or in a Postgres CHECK (migration 00021).
**Costing** is Valhalla's word for the same concept, and it stays at the routing
worker's Valhalla client boundary — nothing else in either repository should say
"costing".

`transit` maps to Valhalla's **`multimodal`** costing, **never** to Valhalla's
own `transit` costing. Valhalla's `transit` is stop-to-stop and useless for an
origin that is a house.

`transit` here means *walking plus scheduled local transit*, and like the other
three it covers only the access and egress legs — how a rider reaches and leaves
an authored station. The ride along the authored line is physics-compiled in this
repository and never routed, so no mode applies to it.

## Machine contract

Codes and statuses that clients and the worker match on literally. Changing one
is a breaking change.

### Error codes

Returned as the `code` field of an error body.

| Code | Status | Means |
| --- | --- | --- |
| `origin_out_of_range` | 422 | The origin cannot reach any station within the budget, by straight-line [reach](#reach-and-routing). Detail carries `nearest_station_slug`, `nearest_station_km`, `max_reach_km` |
| `backlog_full` | 429 | `MAX_INFLIGHT_ISOCHRONES` routing jobs are already in flight. Carries `Retry-After` |
| `stop_placement` | 422 | A stop is off-route or out of chainage order. Detail carries the fault kind, route slug, threshold and the offending stops |
| `stale_graph` | 409 | The compiled graph no longer matches its inputs; recompile and retry |
| `publish_failed` | 502 | The routing job row exists but could not be published to the queue, so it was marked failed immediately rather than being stranded in `queued` |

### Job status

The four values of `transit.JobStatus*`, shared by compile jobs and routing jobs:

`queued` → `running` → `succeeded` | `failed`

`succeeded` and `failed` are terminal. The worker's `running` transition
(`POST /api/internal/routing-jobs/{id}/running`) answers 404 for a job that is
missing *or* already terminal — which is how a job the API gave up on and failed
stops a late worker from reviving it.

## Where the rest of the vocabulary lives

| Repository | Owns |
| --- | --- |
| [`sparks-effect-routing-worker`](https://github.com/andrewcgraves/sparks-effect-routing-worker/blob/main/CONTEXT.md) | Chain vocabulary: chaining, access and egress legs, starter station and starter walk, reached vs reachable, journey, leg, publication, the departure clock |
| [`sparks-effect-website`](https://github.com/andrewcgraves/sparks-effect-website/blob/trunk/CONTEXT.md) | The time-remaining graph: view, lane, through, fork |
| [`kustomize-config`](https://github.com/andrewcgraves/kustomize-config/blob/main/CONTEXT.md) | Deployment vocabulary: overlay, pin, generation, cycling the map, tileset |

Decisions — as opposed to definitions — belong in ADRs, not here. A term
explains what a thing is called; an ADR explains why it works the way it does,
and is not to be re-litigated on the strength of a name.

- [ADR-0001 — The API and worker share no Go code](docs/adr/0001-api-and-worker-share-no-go-code.md)
