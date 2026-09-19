# ADR-0004: What a transit isochrone claims, and what a reported access time means

*Recorded 2026-09-16 for SPA-346.*

## Status

Accepted. Records the semantics [SPA-275](https://linear.app/sparks-effect-personal/issue/SPA-275)
proposed and never got to write down before it was canceled, and settles the
second question that report left open.

## Context

A user reported that San Jose State → Diridon reads 30 minutes when the trip is
"about 15". Both numbers are defensible, because they answer different
questions, and this project had never decided which question it was answering.

Two facts make that ambiguity structural rather than cosmetic:

- The departure is invented, not chosen. `RoutingJob`
  (`internal/transit/types.go:177`) carries lat, lng, budget, mode and compile
  job and **no departure field**; the worker's consumer builds its `ChainRequest`
  from exactly those (`internal/worker/handler.go:103`), so the zero-value branch
  at `internal/isochrone/chainer.go:97` always fires and resolves 08:00 on the
  next weekday via `WeekdayDepartAt` (`internal/valhalla/http_client.go:51`).
  Nothing in the UI says so.
- The reported time is Valhalla's whole-trip wall clock, verbatim. Multimodal
  requests are sent with `date_time.type = 1` (depart-at), and the access cell is
  `valhalla.Cell(route.Time, …)` (`internal/isochrone/chainer.go:380`) where
  `route.Time` is `int(math.Round(resp.Trip.Summary.Time))`
  (`internal/valhalla/http_client.go:216`). Arrival clock minus an 08:00
  departure is elapsed time — **the wait for the first scheduled departure is
  inside the number.**

The user's 15 minutes is walk-plus-ride with that wait removed. Neither figure
is wrong; they are different claims.

## Decision

**1. A transit isochrone represents a representative weekday morning.** It
answers "what does this network reach on a typical weekday morning?" It is a
planning instrument for a hypothetical network, not a trip planner for a
specific departure.

**2. A reported access time is door-to-door elapsed wall clock** — from leaving
the origin to arriving, *including the wait for the first scheduled departure*.
It is not time in motion.

The in-motion figure the user expected becomes a named *component* of the
reported number, never a replacement for it.

### Why door-to-door rather than time in motion

Door-to-door is the only claim the drawn surface can honour.

An isochrone asserts *"you can be here within the budget."* Strip the schedule
wait and that assertion is false at every transit-reached point: no rider can be
there in the stated time, because the wait is not optional. The polygon would
promise reach nobody can realise.

It also could not be made self-consistent. Valhalla draws the polygons and
cannot be told to ignore schedule while still routing on it, so stripping the
wait from the *numbers* would leave the *shapes* beside them still including it.
The two would disagree permanently and invisibly.

Finally it would corrupt the egress cache. An egress polygon is stamped for the
moment the rider steps off — chain departure plus the whole journey
(`egressDepartAt`, `internal/isochrone/chainer.go:530`) — and that timestamp's
service date is part of the cache key as `departs_on`. Seed the journey total
from wait-free access seconds and every egress polygon is computed for the wrong
service window, then cached under a key that does not record the error.

So the answer to the original report is *"30 minutes is right, and here is where
it goes"* — not *"30 minutes is wrong."*

## Consequences

### The breakdown becomes mandatory, not optional

Door-to-door is only defensible if the composition is visible. A single
unexplainable number that contradicts the rider's intuition is worse than a
wrong one, because there is no way to argue with it.

- **The claim must be stated in the UI.** A surface that silently means "08:00 on
  a weekday" while the reader assumes "now" is misleading however exact its
  arithmetic.
- **The worker cannot show the breakdown today.** `routeBody`
  (`internal/valhalla/http_client.go:155`) decodes only the leg shape, each
  maneuver's `travel_mode`, and `summary.time` / `summary.length`. Per-maneuver
  `time` and `transit_info.departure_date_time` are never read, so the walk/wait/
  ride split exists nowhere downstream. Parsing them is the implementation
  ticket this ADR unblocks (SPA-348).
- **Both waits must be presented alike.** The system is currently inconsistent
  with itself: an authored ride reports its boarding wait *beside* the total as
  `ReachableStation.BoardWaitSecs` (`internal/isochrone/chain.go:45`), while the
  Valhalla access leg buries an equivalent wait *inside* `AccessSecs` (`:39`).
  Under this decision both are door-to-door components and both are shown;
  neither is subtracted from the headline figure.
- **Walk-versus-ride must reach the surface.** `RouteResponse.RodeTransit`
  (`internal/valhalla/client.go:85`) is computed and then dropped into a log
  counter (`internal/isochrone/chainer.go:377`, `:393`). A leg Valhalla walked
  end to end is otherwise indistinguishable from one it rode, so a breakdown
  could report "0 wait, 0 ride, 30 minutes walking" as though it were a transit
  answer.

### The departure stays a modelling parameter

It is resolved once per chain and threaded through every routing call that chain
makes, so one chain cannot draw its origin and its egress polygons against two
different service dates. The routing job contract stays closed: no departure
field on `RoutingJob`, and user-selected departures, time-sliced comparisons and
any UI for choosing a departure are out of scope *by this decision* rather than
merely unbuilt.

One gap is knowingly left open. "Representative" implies the same pin answers
the same way twice, but the departure is resolved from the wall clock at chain
entry — a rolling date, so the answer drifts as days pass and can walk off the
end of feed coverage. Pinning it would deliver the reproducibility this claim
implies, but it couples to tileset rebuild cadence and feed coverage. **This ADR
fixes the claim; it does not fix the mechanism.**

### Arrival-clock egress becomes correct rather than optional

If the reported time is genuine elapsed wall clock, the rider really is standing
at that station at chain departure plus the journey total, and their onward reach
must be computed against service at *that* hour — which is what the worker
already does.

This supersedes the "one clock for every leg" proposal in SPA-275, which would
have computed every egress polygon against morning peak service and recorded the
mismatch as a deliberate optimism. That spec was canceled before its decisions
were recorded. A future reader finding it should not treat one-clock as the
intended design and "fix" the arrival clock back.

### Relationship to the spike

[SPA-335](https://linear.app/sparks-effect-personal/issue/SPA-335) asks whether
the reported legs are ridden or merely walked. It does not gate this ADR:

- **Ridden** → decision 2 is exactly the explanation of the user's report.
- **Walked** → the 30 minutes is a pedestrian time mislabelled as transit, a
  defect rather than a semantics question, and decision 2 is moot *for that
  report*. It still governs every correctly-ridden leg.

Decision 1 is unaffected either way.

## Considered and rejected

- **Report time in motion.** Matches the user's intuition; rejected for the three
  reasons above — it makes the isochrone unrealizable, it cannot be reconciled
  with the polygons Valhalla draws, and it corrupts egress stamping and the cache
  key derived from it.
- **Report both numbers with equal weight.** Rejected as a non-decision. Two
  headline figures for one journey leaves the reader to pick, which is the
  ambiguity this ADR exists to remove. The in-motion time survives as a named
  component, not an alternative headline.
- **Treat the isochrone as a trip planner for a chosen departure.** Rejected: the
  ride along the authored line is physics-compiled from track geometry and has no
  timetable at all, so a specific-departure claim would be fiction on the main leg
  however precisely the access leg were modelled.

## Vocabulary

*Access time* (door-to-door elapsed, wait included) and *in-motion time* are
chain vocabulary, which `sparks-effect-routing-worker` owns per the ownership
table in `CONTEXT.md`. They are added to that repository's glossary, not this
one. Note that this repository's existing **run time** — "time in motion, dwell
excluded" — is the compiled-edge analogue and a different level of the model;
the two must not be conflated.
