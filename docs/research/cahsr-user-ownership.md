# Moving California High-Speed Rail into a user's account

Research only — no product code changed. This document describes what would be
required to move the seeded `ca-hsr` scenario, its routes and its services out
of migration-controlled Postgres seed data and into a user account that can
edit them.

## The short answer

The mechanical parts of the move already work. The parts that carry meaning do
not.

I rebuilt `ca-hsr` inside the user-authored model end to end and compiled it.
Every stop snapped onto its alignment, the Palmdale interchange resolved by
itself, and the graph came out with the same 15 nodes as the seeded one. Then
every travel time collapsed: San Francisco → Anaheim went from **306 minutes to
202**, a 34% cut, because the user-authored stack compiles from vehicle physics
and the seeded stack compiles from a calibrated Business Plan timetable. There
is nowhere in `UserService` to put a calibrated run time.

So the work is not plumbing. It is four things the user-authored model cannot
currently express, listed under [Blockers](#blockers). Of the two viable
approaches, adding ownership and CRUD to the *seeded* model (Option A) reaches
the goal — content edited through the API instead of through a goose migration —
without needing any of those four solved first.

## Why this is being asked

Editing `ca-hsr` today means writing a database migration. Six of the nineteen
migrations in `internal/persistence/postgres/migrations/` carry `ca-hsr` content
edits, two of them alongside a genuine schema change:

| Migration | What it changes | Schema too? |
|---|---|---|
| `00012_brightline_west_seed.sql` | Adds a route, 3 stations, a service, 2 segments | no |
| `00013_ca_hsr_phase1_geometry.sql` | Replaces the Phase 1 route geometry | no |
| `00015_las_vegas_station_coordinate.sql` | Moves one station 13.8 km | no |
| `00016_las_vegas_routing_location.sql` | Sets a routing stand-in coordinate | adds `stations.routing_location` |
| `00017_hsr_express_parked.sql` | Sets one service's `active` to `false` | no |
| `00018_segment_reverse_run_seconds.sql` | Two asymmetric run-time overrides | adds `segments.reverse_run_seconds` |

Each needs a rewind-aware migration test, because goose cannot re-apply
migration N while a later version is recorded. `00017` — a single boolean flip —
carries this cost in full.

The reason the migrations exist at all is that `transit.SeedIfEmpty` returns
early when any scenario row is present, so editing the embedded YAML only helps
a fresh database. A deployed one needs the migration. Every content change is
therefore authored twice, in YAML and in SQL, with a geometry parity test
(`cahsrphase1geometry_test.go`, `brightlinewestgeometry_test.go`) holding the
two copies together.

## How `ca-hsr` works today

There are two parallel stacks. They were deliberately kept apart —
`internal/transit/userservice.go` says so directly:

> It is deliberately separate from Service, the seeded aggregate compiled into
> the TransitGraph. [...] Reconciling the two models is future work; keeping
> them apart leaves the seeded CAHSR compile path untouched.

| | Seeded (`ca-hsr` today) | User-authored |
|---|---|---|
| Tables | `scenarios`, `routes`, `stations`, `services`, `segments`, `travel_time_sets` | `user_scenarios`, `user_services` |
| Stops | FK to a shared `stations` catalog | Embedded coordinates, snapped to the route |
| Vehicle | FK to `vehicle_types` | Inline `VehicleParams` |
| Run times | Calibrated table (`segments.run_seconds`) | Derived from physics |
| Compile | `CompileSeededScenario` → `Compile` | `CompileUserScenario` → `CompileServices` |
| Ownership | `owner_id` nullable; `ca-hsr` has none | `owner_id` NOT NULL |
| Reads | Public, unauthenticated | Owner-only, 404 to everyone else |
| Writes | **None** — migrations only | Full CRUD |

`ca-hsr` is 15 stations, 2 routes and 3 services (HSR Express, parked via
`active: false`; HSR Local; Brightline West), with 14 calibrated segments and
two asymmetric mountain hops.

The public seeded read path is worth calling out separately, because it
constrains Option A. `*transit.Store` is built once at boot
(`cmd/api/main.go:166`) and handed to `server.New`; `Store` is documented as
"safe for concurrent read-only use after construction", and every public
handler — `Scenarios`, `ScenarioBySlug`, `ScenarioStations`,
`ScenarioTravelTimes` — closes over it. **The public scenario API is a
boot-time snapshot.** Nothing today can invalidate it, because nothing today can
write to the rows it was built from.

## What I measured

I reconstructed all three services as `UserService` records from the seeded
data, snapped them, validated them, and compiled the two active ones through
`CompileUserScenario`. Full output is in
`/opt/cursor/artifacts/cahsr-model-migration-analysis.log`.

### The structural mechanics work

```
=== ca-hsr rebuilt as a UserScenario: does it hold together? ===
member services: 2
graph nodes: seeded=15 authored=15
   merged node "brightline-west--palmdale" <- [Palmdale] (2 members)
multi-service interchange clusters: 1 (of 1 clusters)
near misses: 0
```

Every station snapped comfortably inside the 500 m off-route threshold — the
worst was Madera at 97.5 m — and chainage stayed monotonic through the Merced
out-and-back, so all three services would be accepted by the write path. The
Palmdale interchange between Phase 1 and Brightline West merged on its own; no
declared `InterchangePair` was needed, because the two snapped positions are
89.4 m apart and the effective merge radius there is 139.4 m.

### The timetable does not survive

```
origin -> destination                                seeded     authored        delta
San Francisco (Transbay) -> Anaheim (ARTIC)       306.0 min    202.2 min   -103.8 min
Anaheim (ARTIC) -> San Francisco (Transbay)       301.3 min    202.2 min    -99.1 min
San Francisco (Transbay) -> Los Angeles           274.5 min    189.1 min    -85.4 min
San Francisco (Transbay) -> Las Vegas             324.3 min    240.1 min    -84.2 min
Palmdale -> Las Vegas                             109.0 min     75.3 min    -33.7 min
```

Three things matter here beyond the headline:

- **306 minutes is the published figure.** `segment_run_times.yaml` is calibrated
  against the CA HSR 2026 Business Plan and recalibrated to that number
  explicitly. Shipping 202 would be publishing a claim the project does not make.
- **Directional asymmetry is lost.** The seeded graph answers 306.0 southbound
  and 301.3 northbound, from `reverse_run_seconds` on the Pacheco Pass and
  Tehachapi hops (`00018`). Physics is symmetric, so both become 202.2.
- **The error is not a constant factor.** Most hops get faster, but
  Fresno → Bakersfield gets *slower*, 35.8 → 41.8 min (+17%). No global scale
  factor repairs this; the calibration is per-segment.

This is the behaviour `compile_seeded.go` already warns about:

> a physics profile over the same alignment produces materially faster,
> uncalibrated times, so compiling that way would change the public answer
> rather than preserve it.

The measurement puts a number on "materially": **104 minutes on the headline
city pair.**

## Blockers

Four capabilities the user-authored model does not have. Each is required for
Option B, and each is independently useful.

### 1. Calibrated run times have nowhere to live

`UserService.Vehicle` is four numbers — max speed, acceleration, deceleration,
dwell. There is no per-segment run-time override, and the choice of compiler is
implied by which stack you are in rather than being configurable:
`CompileUserScenario` always calls `CompileServices`, which always calls
`CompileServicePhysics`.

Closing this means giving the user-authored model an authored run-time tier and
letting a service elect it. The `provenance` vocabulary already exists for
exactly this distinction — `computed` / `calibrated` / `frozen`
(`internal/transit/types.go:140`) — but is only carried on the seeded `Service`
and `TravelTimes`. **SPA-45** is the dormant ticket for the authored/physics
overlay.

### 2. Prerendered isochrones are foreign-keyed to the seeded table

```sql
scenario_slug text NOT NULL REFERENCES scenarios (slug) ON DELETE CASCADE
```

A `user_scenarios` row cannot own a prerendered isochrone. The two curated
payloads (295 KB Burbank walk, 500 KB San Jose bike) are what the scenario page
shows on load, instead of paying for a routing job. Moving `ca-hsr` out of
`scenarios` cascades them away.

### 3. Nothing user-authored can be shown to a logged-out visitor

Every `/api/services/*` and `/api/user-scenarios/*` route is registered as
`authenticated(...)`, and non-owners get 404 rather than 403 so they cannot
confirm a slug exists. The public page `/scenario/ca-hsr` reads the seeded API,
and the website hardcodes the slug in `FEATURED_SCENARIO_SLUGS = ['ca-hsr']`
(`src/api/scenarios.ts:119`).

Move `ca-hsr` into an account without a visibility model and the landing page
404s. This is **SPA-142**, which is still in Backlog and explicitly blocked on
two unmade decisions: the visibility states themselves, and what a public viewer
sees when a shared graph is stale — today staleness answers 409 and the
frontend silently recompiles, which only works because the viewer is the owner.

### 4. The Las Vegas routing stand-in is dropped silently

`GraphNode.RoutingLat` / `RoutingLng` exist so a station whose real coordinate
Valhalla cannot reach still gets an egress isochrone (SPA-234). They are
populated in exactly one place — `seededNodes`, in `compile.go:222` — which only
the calibrated seeded compile calls. The physics path builds its nodes from
`MergeColocatedStops` and never sets them.

`ServiceStopPoint` has no field to carry a routing location either, so this is
not merely unwired — there is nothing to wire. Las Vegas would lose its egress
isochrone with no error raised.

### Secondary consequences

- **Station slugs change.** Graph keys go from `sf` to `hsr-local--san-francisco-transbay`.
  Anything keyed on a station slug — prerendered payloads, saved links,
  `isochrone_cache` rows — stops matching.
- **"Parked" has no equivalent.** `active: false` on HSR Express is how the
  service stays authored but out of the graph. User-scenario membership is just
  a curated ID set, so parking becomes deletion from the set and the distinction
  is lost.
- **No scenario edit UI exists.** `updateScenario` and `deleteScenario` are
  implemented in `src/api/authoring/scenarios.ts` but called from no view.
  `AuthoredScenarioView.vue` is a read-only preview.
- **Users cannot edit route geometry at all.** Route ingestion is
  `adminOnly(handler.CreateRoute(deps))`. If "make changes to it" includes the
  alignment, that is **SPA-123**, also in Backlog.

## Options

### Option A — Give the seeded model ownership and CRUD

Set `owner_id` on the `ca-hsr` rows and build the write endpoints that
`scenarios`, `stations`, `services` and `segments` have never had.

Everything in [Blockers](#blockers) disappears, because nothing moves: the
calibrated compile still runs, the prerendered FK still resolves, the public
reads stay public, `routing_location` still works, and station slugs are
unchanged. `CompileSeededIfNeeded` already recompiles when rows no longer match
the stored graph, so an edit propagates to the public graph without new
machinery.

What has to be built:

- CRUD handlers and repository writes for scenario metadata, stations, services
  (including stops and frequency windows), and `segments` run times. None of
  these exist; `CreateScenario` is used only by the seeder and tests.
- **Store invalidation.** This is the substantial one. The public read path is a
  boot-time `*transit.Store`, so a write would not be visible until restart.
  Either rebuild the store on write behind a lock, or move the public seeded
  reads onto the repository. The second is cleaner and larger.
- Authorization. `auth.CanAccess` returns false for `owner_id = nil`, so
  today's `ca-hsr` is admin-only by default. Assigning an owner is a one-row
  update, but it silently converts curated platform data into user-writable
  data, so the public reads need to stay ungated while writes are owner-gated.
- Validation the seeded model has never needed, because migrations were written
  by hand: stations on a segment path, referenced vehicle types, segment
  connectivity.

Risk: someone's edit changes a public page. That is the point of the request,
but it argues for keeping `ca-hsr` admin-owned initially.

### Option B — Re-home `ca-hsr` on the user-authored stack

Convert the scenario into a `UserScenario` with three `UserService` members.
Reuses all existing CRUD and the authoring UI, and is the only option that ends
with one model instead of two.

It requires all four blockers solved first. As measured, a straight conversion
today ships a 34% travel-time regression, loses both prerendered isochrones,
takes the public page offline, and drops the Las Vegas egress isochrone. The
dependency chain runs through SPA-45, SPA-142 and SPA-123, none of which have
started.

### Option C — Fork: public seeded copy, private editable copy

Keep `ca-hsr` seeded and public; add a "duplicate into my account" action that
clones it into `user_scenarios` / `user_services`.

Cheapest to build — the seeded → user-authored conversion I ran is most of the
work, and no public contract moves. But it does not answer the actual question:
the public CA HSR would still be migration-controlled. It is a good feature
(users experimenting on a real corridor) and a poor substitute for the goal.

## Recommendation

**Option A**, staged, with Option C as an independently valuable side quest.

Option A is the only path where the goal — content edited through the API rather
than through a goose migration — is reachable without first settling the
visibility model and the authored-run-times model. It also preserves the
published 306-minute figure exactly, which Option B cannot do until SPA-45
lands.

A sensible order:

1. Move the public seeded reads off the boot-time `Store` and onto the
   repository. Pure refactor, no behaviour change, and it unblocks every write
   that follows.
2. Add segment run-time CRUD. Retires `00018`-style migrations and is the
   highest-value surface, since run times are the scenario's substance.
3. Add station CRUD, including `location` and `routing_location`. Retires
   `00013`, `00015` and `00016`.
4. Add service CRUD, including the `active` flag. Retires `00017` and `00012`.
5. Assign `owner_id` and decide the read/write gating split.

Steps 2 through 4 each remove a class of migration on their own, so the
sequence is useful even if it stops partway.

Then, separately, treat Option B as the long-term convergence and let SPA-45,
SPA-142 and SPA-123 proceed on their own merits. The measurement above is the
acceptance test for SPA-45: when a user-authored `ca-hsr` compiles to 306.0
minutes southbound and 301.3 northbound, the models have genuinely converged.

## Decisions needed before any of this starts

1. Who owns `ca-hsr` — a real user, or an admin-only service account? This
   decides whether the request is really "make it editable in the UI" or "let
   one specific person edit it".
2. Do the public reads stay unauthenticated once the rows are owned? Assuming
   yes, but `CanAccess` has no notion of "owned but publicly readable".
3. Should edits to a public scenario be immediate, or staged behind a
   publish step? Nothing in the system has a draft concept today.
4. Does "make changes to it" include route geometry (SPA-123), or only stations,
   services and run times?
5. Do the embedded YAML seeds stay authoritative for a fresh database once rows
   are user-editable? If so, a database edited through the API diverges from the
   YAML, and the geometry parity tests need rethinking.

## Evidence

- `/opt/cursor/artifacts/cahsr-model-migration-analysis.log` — compile parity,
  snap feasibility and the full user-authored reconstruction.

Produced with a temporary test harness in `internal/transit`, run against the
embedded seed data and deleted afterwards; the working tree was verified clean.
