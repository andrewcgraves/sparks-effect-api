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

Visitors see neither word: the website's copy says *line* for a service,
*network* for a scenario and *route* for an alignment, recorded in its
[product vocabulary](https://github.com/andrewcgraves/sparks-effect-website/blob/trunk/CONTEXT.md#product-vocabulary).
That is deliberate, so do not "correct" the copy back to the words here.

| Term | Model | Lives at | Is |
| --- | --- | --- | --- |
| **Scenario** | `transit.Scenario` | `/api/scenarios` (public), `/api/me/scenarios` (owner-scoped) | The seeded world model: metadata plus routes, stations, services and segment times hanging off it |
| **UserScenario** | `transit.UserScenario` | `/api/user-scenarios` | **A different model entirely** — a named set of `UserService` ids, plus declared interchange pairs and a boarding-wait override. No stations, no routes, no segment times of its own |
| **Service** | `transit.Service` | `/api/scenarios/{slug}/services` (public), `/api/me/services` (owner-scoped) | A stopping pattern over a `Route`, calling at `Station` rows, driven by a `VehicleType` row |
| **UserService** | `transit.UserService` | `/api/services` — *not* `/api/me/services`. Its publication, once published, is public at `/api/services/{slug}/publication` | The authored counterpart: carries its own stops (name + coordinate, snapped to a route) and inline `VehicleParams` instead of pointing at rows |

Three traps worth stating out loud:

- `/api/services` returns **UserServices**. `/api/me/services` returns
  **Services**. The `/api/me` prefix is not what distinguishes authored from
  seeded — both collections are owner-scoped. The model is different.
- Both scenario models compile to the same `TransitGraph` and both produce a
  compile job, so downstream of a compile the distinction disappears. It only
  matters on the way in.
- *Owned* in a handler's name does not say which model either.
  `internal/handler/ownedservice.go` (`CreateOwnedService` and friends) serves
  the seeded **Service** at `/api/me/services`, while `ownedService` in
  `authoredtarget.go` wraps a **UserService** for the compile, compiled-graph
  and isochrone paths under `/api/services/{slug}`. The publication handlers
  load the same **UserService** with `loadService` and no wrapper. Read the
  type it holds, not the file it is in.

### Seeded / curated / authored / owned

One more overload, on the same models. Every seeded-model row (`scenarios`,
`routes`, `stations`, `services`) carries a nullable `owner_id`:

- **Curated** — `owner_id IS NULL`. The seeded `ca-hsr` baseline and
  admin-ingested alignments. Served by the public reads, writable only by an
  admin. **Seeded** is the narrower word for the subset that ships embedded in
  the binary under `internal/transit/data/scenarios/`.
- **Owned** / **authored** — `owner_id` set. Someone made it; it is theirs; it
  never appears on a public surface unless it is published. The two words are
  interchangeable, and "authored" is preferred in prose.
- **Published** — an authored `UserService` that has a **publication**: a frozen
  snapshot of a specific succeeded compile job plus copies of its name,
  `subtext`, description and route geometry, taken when the owner published it.
  Public reads of it, and the isochrones anyone may plot over it, use the
  publication and never the **draft** — the live, editable row the owner keeps
  working on — so an edit stays invisible until it is republished. The one
  live part is the **byline**'s author name. **Unpublishing** deletes the publication. Only a `UserService`
  can be published today; a curated row is public without being published. Not to be confused
  with the routing worker's *publication* of a queue message — the same idea one
  level down, and in this repository spelled *enqueue*. See
  [ADR-0005](docs/adr/0005-publishing-an-authored-service.md).
- **First published** — when a service's current publication began:
  `first_published_at`, set on publishing and kept by every republish until the
  service is unpublished. The published index orders by it, newest first, so a
  republish keeps a service's place. *Published at* is the last publish.
- **Byline** — who made a publication and when: `author_name`, the owner's
  current display name (`users.name`, read live through `owner_id`, so a rename
  or a transfer changes it without a republish), and `published_at`. Both public
  reads carry it; neither carries the owner's email or id.

**Public** is the adjective for what anyone may read: curated rows, and
publications.

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
| **Route** / **alignment** | The geometry, as a GeoJSON LineString, plus a mode and optional per-segment engineering parameters. Several services can run over one alignment. "Route" is the type name (`transit.Route`); "alignment" is the word used in prose, and is preferred when the geometry rather than the row is meant. **Corridor** is a third name for the same thing, met where something runs *along* it: an edge's `RouteID` ("the corridor it runs over"), a segment's `route_id` (migration 00011), the corridor decoration under [staleness](#state-and-freshness). It is not a separate model and not a planning-sense corridor of several alignments; prefer *alignment* in new prose. A publication's copied geometry is its `routes` field. A curated alignment is a public building block anyone may point a service at, but admin-only to mutate |
| **Station** | A seeded-model place a service can call at: slug, name, `location`, platform height, optional [routing anchor](#routing-anchor). Belongs to a scenario |
| **Stop** | A *service calling at a place*, not a place. `ServiceStop` on the seeded model (a station id, a sequence number, an optional dwell override); `ServiceStopPoint` on the authored model (its own name, slug, coordinate, seq, chainage and offset). A station is a noun; a stop is a relationship |
| **Node** / **GraphNode** | A vertex of the compiled graph. Stops **merge into** nodes at compile time — see [interchange](#interchange). `GraphNode` is the compiled form (slug, coordinate, optional routing anchor, and every merged-in `Names` entry). The reduced slug-plus-coordinate `Node` is a test-only projection for comparing the embedded store against a compiled graph; it is not a production type |
| **TransitGraph** | The compile output, and the only thing an isochrone is ever plotted over: per-service edge lists, the merged nodes, and a merge report. Persisted as a succeeded compile job's `result`, and travels inline on the queue message so the worker needs no database. This API compiles the graph; it does not compute isochrones. The wire types (`TransitGraph`, `Edge`, `GraphNode`, `ServiceGraph`, `TravelMode`) are defined in the contract module and re-exported from `internal/transit` |
| **ServiceGraph** | One service's slice of a `TransitGraph`: its edges, plus the boarding wait resolved for it at compile time |
| **Edge** | A directed hop between two node slugs on one service. `Seconds` is **run time plus dwell**; `DwellS` reports the dwell part separately rather than adding to it. Since SPA-264 an edge also records the corridor it runs over (`RouteID`) and its endpoints' chainages. Every hop is emitted in both directions — the reverse edge is the same hop backwards, carrying the same two chainages swapped, so one of the two directions always has descending chainages. Nothing that reads them treats that as a special case |
| **Subtext** | The one-line descriptor an authored `UserService` or a curated `Scenario` shows under its name — ca-hsr's is `CAHSR + Brightline West · Electrified high-speed rail`. With the name and the **description** (free prose, a few paragraphs) it is the service's prose, and what a [publication](#seeded--curated--authored--owned) copies. Both are optional and empty by default; `Validate()` bounds them at 140 and 4,000 characters as `max_length` faults. A curated scenario's comes from `subtext:` in its `scenario.yaml`, held to the same 140 at seed load and reconciled on boot; authored scenarios have none yet. `subtext` everywhere — column, JSON, TypeScript — not *subtitle* or *tagline*. See [ADR-0005](docs/adr/0005-publishing-an-authored-service.md) |
| **VehicleType** | Rolling stock on the seeded model: top speed, acceleration, deceleration, floor height, and the two dwell figures. The authored model inlines the same numbers as `VehicleParams` instead |
| **Compile job** | A row in `jobs`, polled at `GET /api/jobs/{id}`. Kinds: `compile_scenario`, `compile_user_scenario`, `compile_user_service`. Its `result` is a `TransitGraph`. Compilation runs **in-process in this API**, in `internal/compile`. That package is not the routing worker — the routing worker is a separate repository |
| **Routing job** | A row in `routing_jobs`, polled at `GET /api/routing-jobs/{id}`. Its `result` is the worker's isochrone GeoJSON. Created by the isochrone endpoints, executed **in the routing worker**, written back over `/api/internal/...`. Different table, different owning process — a "job" with no qualifier is ambiguous, so always say which |
| **Prerendered isochrone** | An admin-curated, ready-to-display isochrone stored against a scenario, so a public page can show a result without enqueueing one. Also called a *curated* isochrone |
| **User** | `account.User` — an authenticated person. Authored rows point at them through `owner_id`. `name` is the display name attribution shows; the user sets it through `PATCH /api/auth/me`, trimmed and bounded at 1–80 characters (`required` / `max_length` faults). `is_admin` is the only privilege bit: it gates curated writes and `/api/admin/users`. `disabled_at` (nullable timestamptz) removes sign-in without deleting the person or their authored rows; NULL means the account can sign in. Publications stay published; attribution still shows the display name |
| **Session** | `account.Session` — a hashed bearer token bound to a User, with an expiry. The raw token is returned once at login and never stored |
| **Account token** | `account.Token` — a one-time link that lets whoever holds it set a User's password. Purpose `invite` (made with the account, lasts 7 days) or `reset` (issued by an admin, lasts 1 hour, revokes the account's earlier unused reset). Stored only as a hash in `account_tokens`, like a Session. Redeeming one sets the password, marks it used, signs out every Session and starts a new one. A used, expired or disabled-account token answers exactly as an unknown one does |
| **Handover** | `transit.ServiceHandover` — moving a `UserService`, with its publication and compile history, from one owner to another. **Offered** by the owner (or an admin, on the owner's behalf) to another account by email, then **accepted** or **declined** by the recipient, or **cancelled** by the owner while it is **pending**. A pending offer past `expires_at` (14 days) reads as **expired**, though nothing rewrites the row: expiry is lazy. A service has at most one pending handover, and its slug and public URL survive one. Accepting re-owns the service and its compile jobs in one transaction; a route the recipient could not reference is **copied** to them (never moved), and a service still in a sender's scenario is refused with `service_in_scenarios`. An admin may also **force** one: `PUT /api/admin/services/{slug}/owner` moves the service to a named account with no offer, cancels any pending offer on it, and records the move as an accepted handover with the admin's id in `forced_by` (NULL on one the recipient accepted). Table `service_handovers`, routes under `/api/.../handovers`; the owner move itself is `Repo.TransferUserService`, which keeps its ticket's name. Not a *transfer*: see [interchange](#interchange) |

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
  service carries a list of them. Times are `HH:MM` from `00:00` to `23:59`,
  `end_time` is after `start_time` (no window crosses midnight), and windows
  are half-open, so one ending at `10:00` may meet one starting at `10:00` but
  no two may overlap.
- **Run time** — time in motion, dwell excluded. The intended semantics of a
  `segment_run_times.yaml` segment (`run_seconds`, and `reverse_run_seconds`
  when present), and what `SegmentTime.RunSeconds` holds.
- **Travel time** — overloaded, so always say which. On the model side it is
  the per-segment *run times* above: `TravelTimes`, the `travel_time_sets`
  table, and `GET /api/scenarios/{slug}/travel-times` (and its `/api/me` twin)
  all hold `run_seconds`, not a rider's elapsed time. On the request side it is
  the visitor's **budget** (`budget_mins`), which is what the
  `origin_out_of_range` message's "within this travel time" means and what the
  website's copy calls *travel time*. Prefer *run time* and *budget* in code and
  prose; *travel time* survives in those names and in copy.

### Interchange

**Transfer** is reserved for this sense, a rider changing service (SPA-225,
SPA-347), and is otherwise not a domain word here. Moving a service between
owners is a [handover](#core-nouns).

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
  Three names break that rule and keep their spelling. `RoutingJobStaleAfter`
  and `staleRoutingJobMessage` are about a routing job past its deadline,
  which the poll fails (an abandoned job, not a graph). The retention report's
  `isochrone_cache_stale_transit` counts transit cache rows whose service date
  has left the window — a calendar, not a compile. None of them is staleness
  of a graph, and none answers `stale_graph`.
- **Cache generation rotated** — the log line `CompileSeededIfNeeded` writes
  when a seeded scenario already had a succeeded compile and a new one is
  stored. The previous job is the superseded compile. The new compile job id
  does not exist until that compile is recorded, and a first compile has no
  previous generation, so it does not log the line. A newer running or failed
  compile does not supersede.
- **Retention** — housekeeping an admin runs by hand with
  `POST /api/admin/retention`. Nothing runs at boot, and a deploy deletes
  nothing: the call dry-runs unless the body is `{"apply":true}`. It deletes
  isochrone cache rows whose compile job is a superseded compile (not the
  latest succeeded compile for that scenario, user scenario, or user service,
  and not a publication's pinned compile, which stays live for this rule; a
  target set NULL is not live) and transit rows outside the service-date
  window of 7 calendar days, including rows on a live compile and on a pin.
  Walk, bike and drive store a NULL service date and are not swept by that
  window. It NULLs a routing job result older than 30 days and keeps the row.
  SPA-331's reuse window is still open; 30 days sits well outside a
  service-date reuse clock of a few days, and a NULL result must be treated
  as a miss by that lookup.
  Prerendered isochrones are never swept.
- **Backlog** / **in-flight** — in-flight routing jobs are those `queued` or
  `running` and younger than `handler.RoutingJobStaleAfter`; the age bound is
  what stops a dead worker's abandoned rows wedging the cap shut forever. The
  backlog is the count of them. At `MAX_INFLIGHT_ISOCHRONES` an enqueue is
  refused with 429 and `backlog_full`. The cap is per deployment, not per caller.
  Per-caller floods of the expensive POSTs are refused separately with 429 and
  `rate_limited`.
- **Queue position** — for a `queued` routing job, the count of in-flight jobs
  created before it: the jobs a FIFO worker taking one at a time will finish
  first. `0` means next. Reported on the poll, never stored (SPA-437).
- **Reuse** / **reused** — answering an isochrone request with an earlier
  succeeded routing job instead of minting one (SPA-331). Reused only when the
  graph, mode, budget, owner and origin (to five decimal places) agree, and the
  result is still fresh by the worker's own clocks: not past its
  `reusable_until` service-date rollover, and stamped with the newest tileset
  any job has reported. A reused job is answered 200, not 202, and costs no
  backlog slot. Migration `00034` holds the reasoning.
- **Park** / **parked** — `Service.Active = false`. A parked service stays in the
  seed and in the database, documented, but is skipped by the compiler. It is how
  a stopping pattern is retired without deleting it. Un-parking reaches a
  deployed database through `ReconcileSeed`, since `active` is seed content.
  (CA HSR's HSR Express was parked from SPA-223 until SPA-464.)
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
- **Network** (street) — the OSM street and path network Valhalla routes the
  access and egress legs over: a station's "pedestrian network" or "street
  network" in migrations 00016 and 00023. An unrelated sense from the website's
  *network* for a scenario. Qualify it when that is meant, and say *scenario*
  for a set of services; a bare *network* in older prose (ADR-0004's
  "hypothetical network") means the transit one.
- **Slug** — the URL-safe identity a model is addressed by. Slugs are **globally
  unique across curated and owned rows**, which is why by-slug reads cannot
  filter on ownership and check it themselves instead. A slug is minted once, at
  create, and **never re-minted** — not on rename, not on publish — so a renamed
  service's title and URL diverge on purpose. Two minting functions
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

A route's `mode` is a different field: the **vehicle mode** of an alignment —
`rail`, `metro`, `tram`, `bus`, `ferry`, `funicular` (`internal/route`'s
`validModes`) — which says what runs on the track, never how a rider gets to a
station. Say *travel mode* when the two could meet; *mode* on a request, job or
cache row is always the travel mode.

`transit` maps to Valhalla's **`multimodal`** costing, **never** to Valhalla's
own `transit` costing. Valhalla's `transit` is stop-to-stop and useless for an
origin that is a house.

`transit` here means *walking plus scheduled local transit*, and like the other
three it covers only the access and egress legs — how a rider reaches and leaves
a node of the compiled graph: a seeded station, or the node an authored
service's stops merged into (the authored model has stops, not stations). The
ride along the service itself is physics-compiled in this repository and never
routed, so no mode applies to it.

What the resulting surface *claims*, and what the time reported against it
means, is settled in
[ADR-0004](docs/adr/0004-transit-isochrone-claim-and-reported-time.md): a
representative weekday morning, and door-to-door elapsed time with the wait to
board included.

## Machine contract

Codes and statuses that clients and the worker match on literally. Changing one
is a breaking change.

### Error codes

Returned as the `code` field of an error body, beside an `error` message.
The message is for developers and logs, written in this file's words (service,
scenario, compiled graph); clients match the code and say it in their own — the
website never shows the message, and maps each code it can meet to copy. Two
messages were written for a person, and a client with no mapping may still show
them: `backlog_full`'s, and the `error` a stale routing job is failed with. Both
name the *routing service*, the process that does the work, never an
"isochrone service".

| Code | Status | Means |
| --- | --- | --- |
| `origin_out_of_range` | 422 | The origin cannot reach any station within the budget, by straight-line [reach](#reach-and-routing). Detail carries `nearest_station_slug`, `nearest_station_km`, `max_reach_km` |
| `backlog_full` | 429 | `MAX_INFLIGHT_ISOCHRONES` routing jobs are already in flight. Carries `Retry-After` |
| `rate_limited` | 429 | The per-caller limiter refused the request. Carries `Retry-After` |
| `stop_placement` | 422 | A stop is off-route or out of chainage order. Detail carries the fault kind, route slug, threshold and the offending stops |
| `stale_graph` | 409 | The compiled graph no longer matches its inputs; recompile and retry. Also the answer to publishing a service with no succeeded, non-stale compile |
| `route_in_use` | 409 | The route still has services, user services or segments built on it, so it cannot be deleted, and a `PUT` may not change its `coordinates` or `segments`. Detail carries the `{services, user_services, segments}` counts, the same `dependents` the owner reads report. Name, description, mode and `bidirectional` stay editable while in use |
| `publish_failed` | 502 | The routing job row exists but could not be published to the queue, so it was marked failed immediately rather than being stranded in `queued`. The queue sense only — publishing a service never answers it |
| `validation` | 422 | A request body failed `Validate()`: an authored service or scenario, a route, a display name, a password. Detail carries `faults`, a list of `{field, index?, rule, message}`: `field` is a dotted JSON path (`stops.lat`, `interchange_pairs.a`, `vehicle.dwell_s`), `index` counts entries of a list from zero and is absent for a whole field, and `rule` is one of `internal/fault`'s `Rule*` values. Clients name the field from `field` and `index`, never from `message` |
| `scenario_holds_curated_content` | 409 | `DELETE /api/me/scenarios/{slug}` on an owned scenario that still holds curated (unowned) child rows, so deleting it would delete content its owner does not own. No detail |
| `route_in_use` | 409 | `DELETE /api/me/routes/{slug}` while something is built on the route. Detail carries the counts `services`, `user_services` and `segments` |
| `station_in_use` | 409 | `DELETE /api/me/scenarios/{slug}/stations/{stationSlug}` while a service's stopping pattern still calls at the station. Detail carries `service_stops`, the count |
| `service_in_scenarios` | 409 | A [handover](#core-nouns) cannot be accepted, nor forced by an admin, while the service is a member of any of the sender's scenarios. Detail carries `scenarios`, their slugs; the sender removes it from them first |

### Job status

The four values of `transit.JobStatus*`, shared by compile jobs and routing jobs:

`queued` → `running` → `succeeded` | `failed`

`succeeded` and `failed` are terminal. The worker's `running` transition
(`POST /api/internal/routing-jobs/{id}/running`) answers 404 for a job that is
missing *or* already terminal — which is how a job the API gave up on and failed
stops a late worker from reviving it.

An **interrupted** compile job is one left `queued` or `running` with no
goroutine left to finish it, because compiles run inside the API process and
that process exited mid-compile. Shutdown waits for in-flight compiles up to
its grace period. Boot fails every unfinished job before it enqueues any of its own, with
`error = "interrupted by restart"`. `GET /api/jobs/{id}` fails one older than
`handler.CompileJobInterruptedAfter`, which a boot never reached, with a
message telling the author to compile again. Say *interrupted* about compile
jobs, not *stale*: that word belongs to graphs.

### Compile outcome

The `outcome` label on the `compile_jobs_total` metric (SPA-433). It is the
terminal job status the compile wrote, `succeeded` or `failed`, or `error`
when no status could be written at all, for example when the database was
unreachable. `error` is a metric value only and never a job status: that job
is left wherever it was.

### Routing status

The three values `GET /api/routing/status` answers in `status`
(`handler.RoutingStatus*`). *Contact* is the last authenticated
`/api/internal/*` request from the worker. A job is *waiting* when it has been
queued and in flight for over 60 s.

| Status | Means |
| --- | --- |
| `offline` | There has been no contact for 2 minutes, whether or not a job is waiting. Live plotting is down |
| `degraded` | A job is waiting but the worker is in contact. It is up and behind |
| `ok` | Neither |

An API process counts its boot as contact, so it reads `ok` for its first 2
minutes.

## Where the rest of the vocabulary lives

| Repository | Owns |
| --- | --- |
| [`sparks-effect-routing-worker`](https://github.com/andrewcgraves/sparks-effect-routing-worker/blob/main/CONTEXT.md) | Chain vocabulary: chaining, access and egress legs, starter station and starter walk, reached vs reachable, journey (*trip* on the wire, as in `trip_progress`), leg, publication (of a queue message — a service's publication is defined here), the departure clock |
| [`sparks-effect-website`](https://github.com/andrewcgraves/sparks-effect-website/blob/trunk/CONTEXT.md) | Product vocabulary (line, network, route, splash zone, stop vs station, travel time, starting point); the time-remaining graph: view, lane, through, fork |
| [`kustomize-config`](https://github.com/andrewcgraves/kustomize-config/blob/main/CONTEXT.md) | Deployment vocabulary: overlay, pin, generation, cycling the map, tileset |

Decisions — as opposed to definitions — belong in ADRs, not here. A term
explains what a thing is called; an ADR explains why it works the way it does,
and is not to be re-litigated on the strength of a name.

- [ADR-0001 — The API and worker share no Go code](docs/adr/0001-api-and-worker-share-no-go-code.md)
- [ADR-0002 — Seeded rows reconcile from YAML at boot](docs/adr/0002-seed-reconciliation.md)
- [ADR-0003 — Shared contract module for API↔worker wire types](docs/adr/0003-shared-contract-module.md)
- [ADR-0004 — What a transit isochrone claims, and what a reported access time means](docs/adr/0004-transit-isochrone-claim-and-reported-time.md)
- [ADR-0005 — What publishing an authored service means, and where it lives](docs/adr/0005-publishing-an-authored-service.md)
