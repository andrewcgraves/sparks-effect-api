# Transit reach — triage of user feedback, 2026-09-10

Five observations from a user testing transit isochrones in the Bay Area. All
five were investigated against the code in `sparks-effect-api`,
`sparks-effect-routing-worker`, `sparks-effect-website` and `kustomize-config`,
plus the Linear history.

**Headline: two of the five reports are misdiagnosed, but all five point at real
defects — and three of them share one root cause that the project has already
described in its own words and not yet fixed.**

---

## Verdict at a glance

| # | What they said | Is it real? | Is their diagnosis right? | Root |
|---|---|---|---|---|
| 1 | HSR route highlighted past Gilroy; "conflates HSR with a transit route" | **Yes, but the highlight is correct** | **No** | Map cannot say what the highlight means |
| 2 | SJSU→Diridon reads 30 min, really ~15 | **Yes** | Partly — "something off about finding the transit times" is right | Transit silently degrades to walking |
| 3 | "Transfer/wait times should be 0" | Already true where they mean it | **No** — and the literal request is harmful | Two different waits share one word |
| 4 | Palo Alto / San Mateo directed to Millbrae not Diridon | **Yes, but nothing is "directing" them** | **No** | Map cannot say what the line means |
| 5 | Transit often not found from destination station | **Yes** | Yes — and the part they *liked* is the bug | Transit silently degrades to walking |

---

## The one thing to read first

From `sparks-effect-routing-worker/README.md:354-360`, written before this
report ever arrived:

> Valhalla does not error on a multimodal request it can find no service for. It
> walks the whole way and answers 200, with a plausible shape and a plausible
> duration, so a tileset whose GTFS calendars have all expired is
> indistinguishable from a corridor with genuinely poor service **unless the
> maneuvers are read**.

The worker **does** read the maneuvers. `RouteResponse.RodeTransit`
(`internal/valhalla/http_client.go:170-181`) knows whether a train was boarded.
And then it throws that knowledge away — `internal/isochrone/chainer.go:373-381`
increments a log counter and writes the cell unconditionally:

```go
routed.Add(1)
if route.RodeTransit { rode.Add(1) }
cells[i] = valhalla.Cell(route.Time, route.DistanceKm)
```

`RodeTransit` never reaches `ReachableStation` (`chain.go:36-47` has no such
field), the API, or the UI. On the isochrone path there is no equivalent
detection at all — `IsochroneResponse` is `{type, features}`
(`client.go:46-49`) and nothing inspects a polygon's shape or size.

**A walked leg and a ridden leg are byte-indistinguishable to everything
downstream.** That is claim 2 and claim 5 in one sentence, and it is why this
class of bug has now been filed seven times.

This was stated almost verbatim in **SPA-275**, which was **canceled on
2026-09-09 — the day before this report**:

> the broken state and the working state are indistinguishable: both return HTTP
> 200 with a plausible polygon attached. Nothing in the system inspects whether a
> returned itinerary actually used transit.

---

## Two root systems

Five complaints, two causes.

**System A — transit silently degrades to walking, and nothing notices.**
Claims 2 and 5. Involves the worker (discards `RodeTransit`), the egress cache
(stores the walk-shaped result under a transit key), the tileset (Bay Area
operators are gated behind one optional credential), and the verification script
(its only transit assertion cannot detect the failure).

**System B — the map draws things it cannot explain.**
Claims 1 and 4. Both are the user correctly seeing a line on the map and
reasonably inferring a routing decision that does not exist. Neither line has a
legend entry, a tooltip, or a caption.

Claim 3 is separate: a vocabulary collision between two different waits.

---

## Claim-by-claim

### 1. "It highlighted part of the HSR route a tad past Gilroy — so it conflates HSR with a transit route"

**The highlight is correct. The inference is wrong. The map is at fault.**

What they saw is an *unfinished leg* — `trip_progress`, added in SPA-264 three
days before the report. The budget ran out mid-hop and the app drew how far
along the corridor the rider actually got.

The arithmetic confirms it is the only thing they could have seen:

- There is **no Oakland station**. The nearest are SF Transbay (11.2 km),
  Millbrae (24.9 km), San Jose (61.9 km — outside the 57.1 km transit access
  filter, `chainer.go:73-81`).
- Ride-only times, seeded (`segment_run_times.yaml:45-61`), dwell 90 s per stop,
  boarding wait 0: SF→Gilroy is **4240 s (70 m 40 s)**.
- Budget 7200 s ⇒ Gilroy is reachable if the Oakland→SF access leg is under
  **49 m 20 s**. It is.
- **Merced is arithmetically impossible**: 4240 + 3140 = **7380 s of pure
  riding** from SF, before a single second of access. 7380 > 7200.

So `gilroy→merced` is the *only* hop the budget can die on, and the stub
*necessarily* starts at Gilroy. Reconstructing chainage from `routes.yaml`
(21,598 vertices, 815.3 km): Gilroy sits at 126.71 km, Merced at 273.98 km. An
access leg of 40–49 minutes puts the cut **12–27 km past Gilroy** — "a tad", at
statewide zoom, on an 815 km line.

**Also ruled out:** HSR Express is parked (`services.yaml:30`, `active: false`),
so there is exactly one active service on the Phase 1 corridor — no second
stopping pattern that could read as two lines. And double-counting between the
authored ride and Valhalla is architecturally impossible: access and ride are
combined by `min`, never by sum (`transit/graph.go:139-141`), and egress is
time-disjoint (`chainer.go:530-532`).

**The real defect, and it is the inverse of what you would guess:** a
*fully-ridden* corridor gets **no highlight at all** — it stays plain ink,
identical to track nobody rode (`useRouteLayer.ts:161`, intentional per
`:175-178`). So the unfinished stub is **the only coloured line on the entire
alignment**. Everything the rider actually rode — SF→Millbrae→San Jose→Gilroy,
about 70 minutes of riding — is unmarked.

Worse, the stub is painted `#f28f29` (`theme.css:13`) — the *same token* as the
egress isochrone fills and the lit station dots. **Orange is the app's "you can
get here" colour. The stub is the one orange thing that means "you can't."** It
has no legend entry (`isochroneLegend()` returns exactly two rows,
`useIsochroneLayer.ts:36-41`) and no hover (tooltips bind only to station dots,
`useStationHighlight.ts:102-103`).

The only place it is ever explained is the side panel: *"Got X% of the way toward
Merced"* (`TimeRemaining.vue:244-249`). A user looking at the map has no path to
that sentence.

**A discriminator worth sending back to them:** the stub runs east from Gilroy
through **Pacheco Pass**. There is no railway of any kind through Pacheco Pass —
that gap is the entire reason the HSR project exists. So that geometry cannot
have come from a GTFS feed. If their highlight ran east into the Diablo Range, it
is `trip_progress` and it is correct. If it ran *north* toward Morgan Hill, that
is a Caltrain-shaped egress polygon and needs a second look.

### 2. "SJSU→Diridon reads 30 minutes; it's about 15"

**They are right that the number is wrong.** Two mechanisms each produce ~30
minutes and the code cannot tell them apart.

The access leg is Valhalla's whole-trip wall clock, taken verbatim
(`http_client.go:216`, `Time: int(math.Round(resp.Trip.Summary.Time))`), and
flows untouched to the UI. With `date_time.type=1`, `trip.summary.time` is
arrival-clock minus the requested 08:00 departure.

| | Mechanism | Arithmetic |
|---|---|---|
| **A** | Valhalla **walked the whole way**; a pedestrian time is stored as a "transit" access time | ~2.4–2.8 km ÷ Valhalla's default 5.1 km/h ≈ **28–33 min** |
| **B** | Valhalla rode; `summary.time` includes the wait for the first departure | ~10 min walk + ~7 ride + ~4 walk + ~8–13 wait ≈ **28–32 min** |

**Hypothesis A is now the leading one**, because of a finding from the cluster
config that the access-leg analysis did not have: **VTA — the operator that
actually runs San Jose State → Diridon — reaches the tileset only through 511
operator id `SC`** (`fetch-gtfs-feeds.py:437`), and the entire 511 Bay Area
ingest is gated behind `GTFS_511_API_KEY`, which is empty in git by design and
filled in Lens. Without it, `ingest_511_feeds` is skipped, named in a run
summary, and **never fatal**.

The tell the user handed us without knowing: they say a brisk walk is 35–40
minutes. Valhalla's *unset* `walking_speed` default of 5.1 km/h is faster than a
real human walk, landing at ~30 — not 35–40. **That gap is the signature of a
walked leg.**

A useful corroboration from claim 1: the Oakland query *did* reach Gilroy, which
requires a working transit access leg to SF or Millbrae — and Oakland to SF is
not walkable at all. So transit is **partially** working, which is exactly what
the credential model predicts: Caltrain (`mdb-54`), Capitol Corridor (`mdb-74`)
and SF Muni (`mdb-2886`) are **free catalog rows** that land with no token, while
VTA, SamTrans and AC Transit are token-gated. **Bay Area rail works; San Jose
local transit walks.**

**What makes this un-diagnosable rather than merely wrong:** neither floor
catches it. `GTFS_MIN_FEEDS=150` counts only `mdb-` feeds, excluding 511 and
extras *by design* (`fetch-gtfs-feeds.py:2244-2246`) — the same number with or
without a token. `GTFS_511_MIN_FEEDS` / `MIN_OPERATORS` are **inert without a
token**: `check_511_floors` opens with `if not config.token_511: return []`.

### 3. "If there is an assumption about transfer or wait times, that should be 0 minutes"

**This is already true where they mean it, and the literal request is one you
should decline.** Two different waits share one word.

**The authored ride already charges nothing.** `BOARDING_WAIT_POLICY` defaults to
`none` (`boarding_wait.go:51-52`), is not set in any manifest, and the seeded
`ca-hsr` scenario has no override. Transfers are free by construction — there is
no transfer edge and no transfer table; interchange is just two services emitting
an edge under one node key. `WaitSecs` is charged once on the first hop out of
the origin (`transit/graph.go:193-198`). Riding through an interchange costs
nothing extra.

**The wait they are seeing lives inside Valhalla's `summary.time`, and no
Valhalla parameter removes it.** Adding `costing_options.transit: {transfer_cost:
0, transfer_penalty: 0}` is honest and cheap and **will not move their number** —
`transfer_cost` defaults to 15 *seconds*, not 15 minutes, and applies per
transfer, not to the initial wait.

Stripping the schedule wait properly (parsing maneuvers and subtracting the gap)
costs three things:

1. **The isochrone becomes unrealizable** — no rider can actually be at the
   destination in the reported time.
2. **Egress stamping silently drifts.** `egressDepartAt` is chain departure +
   `journey.TotalSecs`, seeded from access seconds. Strip the wait and every
   egress isochrone is stamped ~15 min early — *and that timestamp is part of the
   cache key* via `departs_on`. Wrong service window, cached.
3. **The polygons will not agree with the numbers.** The isochrones are drawn by
   Valhalla and cannot be told to ignore schedule. Station times would say 15; the
   surface would still be drawn on 30.

⚠️ **Flag for you:** this request runs directly against **SPA-225** (Backlog),
*"Charge a transfer penalty when a rider changes service"*, which wants to make
transfers cost *more*, noting today's model "over-reports how far you can get."
The user wants zero; the backlog wants non-zero. **That is a product decision
someone has to make, not a bug.** Worth resolving explicitly before either
ticket moves.

### 4. "Palo Alto / San Mateo directs you to Millbrae rather than Diridon"

**Nothing is directing them anywhere. But there is a real bug hiding next to
this complaint.**

The starter station is **purely cosmetic**. `pickStarter`
(`chainer.go:185-199`) runs *after* the graph search has finished and feeds
exactly one thing: a single `/route` call whose polyline becomes `starter_walk`.
Nothing downstream filters, seeds, or prioritises on it. The graph search is
multi-source — **every** access station is an independent Dijkstra source and
results merge by minimum (`transit/graph.go:133-145`). Millbrae and Diridon are
both sources; neither is privileged. The commit that added it says so outright
(SPA-196): *"Only the starter station is routed. Every other station is reached
by riding, and its egress isochrone already says what that buys."*

**The real bug: the starter comparison truncates to whole minutes.**
`chainer.go:493` sets `accessMins: aSecs / 60` — integer division — and
`pickStarter` compares *that*. Any two stations whose access times fall in the
same whole minute are a **tie, resolved alphabetically by slug**. The seeded
slugs are `millbrae` and `san-jose`, and `"millbrae" < "san-jose"`.

**So wherever access to the two lands in the same whole minute — roughly the
mid-Peninsula, which is exactly the geography the user named — Millbrae wins
deterministically even when Diridon is up to 59 seconds closer.** The repo's own
test asserts this behaviour (`chainer_test.go:1211-1245`: 612 s vs 600 s both
truncate to 10, and the *slower* station wins the tie).

From Palo Alto proper the times are not tied and Millbrae wins on merit — so this
is not the whole of what they saw, but it is a genuine bias in precisely their
stated area and should be fixed regardless.

**Why they misread it.** The starter walk is painted **top-most** in the
layerStack (index 8, above both isochrone fills, the route line, the progress
stubs and the station dots — `layerStack.ts:26-41`), as a crisp dashed line over
35 %-opacity fills, and it **has no legend entry, no tooltip and no caption**. An
unexplained, top-most, origin-anchored line terminating on one station dot is
very reasonably read as "go here".

Worse, there is *text*: `TimeRemaining.vue:203-210` renders **"Drive to Millbrae
· 27m"**. Two aggravators: that row is `rows[0]` of **every** view tab and its
detail is built once globally, so **even while reading the Diridon tab it still
says "Drive to Millbrae"**; and it is built from `children[0]` ordered by *time
remaining*, not access time, so it is not even guaranteed to name the same
station as `starter_walk` — a second, independently-computed "the one station".

**One caveat that may explain the whole thing.** For a Palo Alto origin, both
Millbrae and Diridon are *directly accessible*, so `viaTransit` is false for both
and **neither gets an egress polygon** — their surroundings are covered by the
origin isochrone. Rail-derived polygons appear only *beyond* them. If their
budget was too small to settle anything past Diridon, they would genuinely see
only a northern blob plus a Millbrae-pointing line. **Worth asking them for the
exact budget and mode.**

**On the literal feature request** ("anything south of Burlingame in San Mateo
County routes to Diridon"): no mechanism exists to express it, and it is a poor
fit for this architecture.

- Every spatial primitive in the project is a lat/lng and a haversine. There is
  no polygon, no boundary, no PostGIS, no region type. This would be the **first
  spatial predicate in the codebase**, for one rule.
- The graph is scenario-scoped and user-authorable; a rule naming `millbrae` and
  `san-jose` is dead weight in every scenario that has neither.
- It contradicts what the tool measures. Forcing a slower first leg makes the
  reported reach *wrong* — and the user concedes Millbrae is closer.
- **Burlingame is not in the data.** The seed has nothing between SF and San Jose
  except Millbrae, so the boundary they name has nothing to hang on.
- `Station` has no priority/rank/preference field, the worker holds no database
  and no geography config, and the queue message is contract-locked at
  `SchemaVersion = 1` with a golden fixture diffed in CI.

The underlying want — *"stop implying one station is the answer; show me the
southbound story too"* — is a UI fix, and the surface **already contains** the
southbound answer.

### 5. "Issues picking up transit from destination station — but I like that it defaults to walking"

**They are right, and the thing they like is the bug.**

**There is no fallback-to-walking code anywhere in the four repos.** Both
`Isochrone` call sites (`chainer.go:285` origin, `:608` egress) send `costing`
unchanged. What they are seeing is Valhalla degrading a `multimodal` request into
a pedestrian answer and returning HTTP 200. Nobody chose that behaviour, nobody
logs it, and nobody can tell it happened.

Three compounding causes:

**(a) Egress is structurally starved of budget.** Egress isochrones are computed
**only for stations reached by riding** (`chainer.go:553-557`), so *by
construction* every egress call is made from the station with the **least budget
left**, and the contour asked of Valhalla is that remainder. A 120-minute budget
with a 90-minute journey gets a **30-minute multimodal contour** — inside which
Valhalla must walk to a stop, wait for a scheduled departure, and ride.

The repo already knows this is below the resolution floor
(`README.md:322-325`):

> a 30-minute multimodal isochrone can miss a stop `/route` reached in 13 minutes
> (valhalla/valhalla#5636) — and are never used as a station filter

It calls these polygons **"visualisation only."** But **nothing downstream
honours that demotion** — the website renders them as the *"From station"* reach
layer (`useIsochroneLayer.ts:39`). A polygon the worker considers decorative is
presented to the user as an answer.

**(b) The walk-shaped polygon is cached under a transit key, so the failure
sticks.** The only acceptance test is feature count (`cache.go:88-90`,
`len(iso.Features) > 0`). The API insert is first-write-wins with **no TTL and no
expiry** (`worker.go:109-112`, `ON CONFLICT ... DO NOTHING`). So a walk-only
polygon returned at 08:15 is served to every request for that
`(compile_job_id, station_slug, transit, contour_mins, departs_on)` **for the
rest of the service day**, and a later *good* result cannot overwrite it. It
self-heals at midnight when `departs_on` rotates — not sooner. That is exactly
the shape of "seems to have issues."

**(c) The tileset may not contain the operators at all** — same 511 credential
gap as claim 2, plus a second gap: `build_transit` only acts when
`transit_tiles/` is absent or empty, so **GTFS reaches the graph only when a new
generation is built**. SPA-305 landed its 20 extra feeds on 2026-09-09 at
19:55, *after* `gen-2026-09-09` was named on 2026-09-08 at 18:53. Its own commit
message concedes it: *"Landing the zips on valhalla-gtfs is not the same as
serving them."* **The repo's HEAD does not describe what production serves.**

---

## Where the user is wrong

Flagging these explicitly, as asked.

1. **"It conflates the HSR with a transit route."** It does not. The highlight is
   a correct partial-corridor render of where a 120-minute budget expired
   mid-hop, and the arithmetic only admits one such hop. HSR Express is parked, so
   there is exactly one active service on the corridor.
2. **"It directs you to Millbrae."** Nothing directs anyone anywhere. The starter
   station is a drawn annotation computed *after* the search; the reachability
   surface already includes everything reachable via Diridon.
3. **"Wait times should be 0 minutes."** On the authored ride they already are —
   boarding wait defaults to `none` and transfers are free. The wait they are
   seeing is real scheduled waiting inside Valhalla's access leg, and zeroing it
   would make the isochrone describe a trip no rider can take.
4. **"I like that it defaults to walking when it can't find transit."** There is
   no such feature. That is an undetected failure being rendered as an answer.

## Where the user is right, and it is unexpected

1. **Transit access times are wrong, and the most likely reason is that no train
   was involved at all.** Not a costing-tuning problem — a data/credential
   problem that the system is structurally unable to report.
2. **Transit egress frequently produces walking surfaces**, and the worker's own
   README predicted this in writing before the report arrived.
3. **A bad transit polygon is cached under a transit key for a whole service
   day**, and a later correct result cannot displace it.
4. **The starter-station tie-break truncates to whole minutes and then sorts
   alphabetically**, systematically favouring Millbrae over Diridon in exactly the
   geography the user named. Nobody was looking for this.
5. **The one transit assertion in the entire system cannot detect the
   failure.** `verify-cycle.sh:26-32` defaults to `expect="Caltrain"` — a *free
   catalog row* that lands with no 511 token. **This check goes green with zero
   511 data on the volume.** It is a Caltrain-corridor regression test, not a Bay
   Area coverage test, and it is structurally blind to missing VTA, SamTrans and
   AC Transit. Nothing anywhere asserts a *local* transit trip.
6. **Acceptance tests purpose-built for this exact complaint exist and have never
   run.** `TestAcceptanceTransitIsochroneIsAtLeastAsLargeAsWalk` compares
   multimodal against pedestrian isochrone area and names the failure "the SPA-297
   inversion (wrong costing-option key, missing feeds, expired calendars)". It is
   skipped unless `TEST_VALHALLA_URL` is set and sits outside both `make test` and
   `make dev-workflow`.
7. **A migration header documents an invalidation that does not exist.**
   `00024_isochrone_cache_departs_on.sql:31-33` asserts `tileset_at` invalidation
   "happens on read in the worker (a mismatched or NULL stamp is a miss)". There is
   no such read-side check — `GetIsochroneCache` never selects `tileset_at`, and
   `EgressCache.lookup` never compares a stamp. **A tile rebuild invalidates
   nothing.** The worker README is honest about this ("Accepted risk"); the
   migration header is not.

---

## Run these before writing any code

Three cheap checks decide which fixes matter.

| # | Check | Decides |
|---|---|---|
| **D1** | Is `GTFS_511_API_KEY` set? `kubectl -n sparks-effect exec deployment/valhalla -c valhalla -- sh -c 'cut -d, -f1 /gtfs_work/manifest.csv \| grep -c "^511-"'` — or read the last refresh CronJob log for `511 ingest skipped: no token for api.511.org` | Whether VTA/SamTrans/AC Transit are in the graph at all. **The single load-bearing unknown.** |
| **D2** | Grep the worker log for `chain: transit access legs routed` and compare `rode_transit` against `routed` for a transit job | Whether Valhalla walked (hypothesis A) or rode (hypothesis B) |
| **D3** | `make acceptance TEST_VALHALLA_URL=…` against the production pod | Whether transit isochrones beat walking isochrones at all. Purpose-built for this complaint; has never run. |

One more worth settling with a live server:
`patch-multimodal-config.sh:22-26` warns that
`min_multimodal_walking_distance` is *also* the fallback applied when a request
omits `multimodal_start_end_max_distance` — and it is pinned to **1 metre**. The
worker omits that option on every request. If loki applies that fallback to
`/isochrone` the way it does to `/route`, **every multimodal isochrone is capped
at 1 m of access/egress walking**, which would produce exactly the reported
symptom. Against this reading, `acceptance_test.go:127-131` observes 2415 m. Send
one isochrone with and one without the option and compare areas. **Potentially a
one-line fix.**

---

## Implementable work items

### P0 — make the failure visible (this is the recurring bug)

- **W1. Stop reporting walk-only legs as transit.** Carry `RodeTransit` onto
  `ReachableStation` (`chain.go:36-47`, `chainer.go:667-678`) and render it —
  *"Walk to San Jose (Diridon) — 30 min"* instead of *"Transit to…"*. Do **not**
  reject the station: that silently shrinks the surface. Labelling costs nothing
  in accuracy and converts a wrong-looking number into a correct, explicable one.
- **W2. Detect walk-shaped egress polygons.** Isochrones have no maneuvers, but
  they have *area*. Compare each egress polygon against a pedestrian isochrone of
  the same contour (or just `speed × contour`) and log a per-station count the way
  `pairwiseAccess` logs `rode_transit`. Carry the flag on the wire.
- **W3. Refuse to cache a polygon that looks like walking under a transit key.**
  `usable()` (`cache.go:88-90`) currently checks only feature count. Today's
  behaviour makes every failure sticky for a service day and un-overwritable.
- **W4. Fix `verify-cycle`'s transit assertion.** Default `EXPECT` to a *local*
  operator trip (SJSU→Diridon on VTA), not Caltrain. As written it passes on
  exactly the data that produces this bug.
- **W5. Make a missing critical operator fatal.** Resurrect SPA-275's
  critical-feed list: name the operators that govern access legs, and fail the
  fetch run when one is absent, unparseable, or not covering the served date.
  Today `GTFS_511_MIN_FEEDS` is inert without a token — the guard is disabled by
  the very condition it should catch.

### P1 — close the operational gap

- **W6. Automate the cycle after a refresh.** Already tracked as **SPA-303**
  (Todo, High). The weekly job refreshes feeds onto a volume Valhalla never reads
  at request time; without a manual `make cycle-tiles` the served graph can lag
  indefinitely, with no staleness signal of any kind. Discovery is by user report
  — which is what happened here.
- **W7. Record per-feed calendar coverage in the manifest and alert when the
  served date falls outside it.** Nothing in the pipeline measures a GTFS calendar
  window today; `MANIFEST_COLUMNS` records bytes, sha256 and fetch time only. The
  tileset is currently fresh (~1–2 days), so this is *not* what is biting now —
  but it is entirely unguarded and will bite.
- **W8. Promote the acceptance tests into a post-cycle gate.** They already
  encode the right assertions and have never run.

### P2 — the map should say what it is showing (claims 1 and 4)

- **W9. Give the unfinished-leg stub a legend row** — e.g. *"Budget ran out
  here"*. `isochroneLegend()` (`useIsochroneLayer.ts:36-41`) is the natural home.
  Highest value-per-effort item on this list; it alone closes claim 1.
- **W10. Move the stub off the "reachable" hue.** It currently uses `#f28f29`,
  the same token as egress fills and lit station dots.
- **W11. Make the progress cap hoverable.** `remaining_secs` and `fraction` are
  already on the wire and already on the feature — a popup would say *"38% of the
  way to Merced — 19 min short"* exactly where the user is looking.
- **W12. Give `starter_walk` a legend entry** — *"First leg — nearest station"*.
  It is currently the top-most layer on the map with no explanation anywhere.
- **W13. Make the origin row's `accessTo` per-view.** Today it is computed once
  globally, so the Diridon tab still reads *"Drive to Millbrae"*.
  (`timeRemaining.ts:307-328`)
- **W14. Fix the starter-station tie-break.** Compare seconds, not
  `aSecs / 60`. Update `chainer_test.go:1211-1245`, which currently pins the
  buggy behaviour.

### P3 — correctness and robustness

- **W15. Give egress per-station error tolerance.** Access legs drop a bad
  station (`chainer.go:366-370`); egress aborts the errgroup and **fails the
  entire job** (`:614-616`). One bad station kills the whole isochrone.
- **W16. Raise `VALHALLA_TIMEOUT_SECONDS`.** Production runs the 30 s default;
  the repo's own acceptance client assumes **five minutes** against a real
  transit-tiled Valhalla. Note this produces failed jobs, not walking — so it is a
  second, louder failure, not the one reported.
- **W17. Fix or implement the `tileset_at` invalidation** that migration 00024's
  header claims exists.
- **W18. DST bug**: `egressDepartAt` uses `.Add()`, so on the two transition days
  the egress wall clock is off by an hour.
- **W19. Doc bug**: Valhalla's `transit_start_end_max_distance` default is
  **2145 m**, not 2415 m — transposed in three places (`README.md:329`,
  `acceptance_test.go:129`, `:225`).
- **W20. Track separately**: `trip_progress` applies a fraction of *time*
  linearly to *distance*. On `gilroy→merced` (Pacheco Pass climb plus the Central
  Valley Wye) speed is far from constant, so the drawn cut is biased **optimistic**
  — "a tad past Gilroy" is if anything overstated.

### Recommended against

- **The literal Burlingame rule.** See claim 4. Wrong layer, wrong architecture,
  and it would make the reported reach incorrect.
- **Stripping the schedule wait from access times.** Makes the isochrone
  unrealizable, drifts egress stamping into the wrong service window, and leaves
  the polygons disagreeing with the numbers.

---

## Open questions for the user

1. **What budget and mode** for the Palo Alto query? If the budget was too small
   to settle anything past Diridon, claim 4 is fully explained by the missing
   legend.
2. **Did the Gilroy stub run east** into the Diablo Range (correct
   `trip_progress`) **or north** toward Morgan Hill (a Caltrain egress polygon
   worth investigating)?
3. **Which of the two waits** do they mean in claim 3 — and do they accept that
   removing the real schedule wait makes the reach unachievable in practice? This
   needs resolving against SPA-225 either way.

## Relation to existing Linear issues

| Issue | Status | Bearing |
|---|---|---|
| **SPA-275** | **Canceled 2026-09-09** | Predicted claims 2 and 5 in detail and specified most of P0. Worth reopening in part. |
| **SPA-303** | Todo, High | Is W6 exactly. |
| **SPA-225** | Backlog | **Directly contradicts claim 3.** Needs a product decision. |
| SPA-302 | Done | Landed `denoise=0` with a regression test. **Contour discarding is ruled out.** |
| SPA-274, SPA-269, SPA-288, SPA-299, SPA-300, SPA-306, SPA-272, SPA-301 | Done / Duplicate | The same class of bug, filed and closed at least six times. |

**That last row is the finding that matters most.** Transit reach has been fixed
repeatedly and keeps regressing, because every fix addressed a *cause* and none
added a standing check that would catch the *symptom*. The one check that does
exist asserts an operator that is present whether or not the bug is. Until W1–W4
land, the seventh recurrence will be found the same way as this one: by a user.
