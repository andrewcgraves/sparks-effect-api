# ADR-0001: The API and worker share no Go code

*Recorded 2026-09-10. The decision is older: until SPA-307 it lived as a
doc comment on the speed constants in `internal/geo/geo.go`.*

## Status

Superseded in part by [ADR-0003](0003-shared-contract-module.md). The routing
message, the transit-graph types that travel inside it, and the worker-store
HTTP envelope now live in `github.com/andrewcgraves/sparks-effect-contract`.
`geo`, `config`, and `logger` remain duplicated, as this decision recorded.

## Context

The API and the routing worker are separate repositories. They have to agree
on the queue message, on travel speeds used for origin-range vs destination
pre-filter, and — until SPA-273 — on the Postgres schema. Four packages sit
at identical paths in both repos (`geo`, `config`, `logger`, and the graph
types inside the message).

## Decision

The two repositories share no Go code. Duplication is accepted rather than
extracted.

**Premise:** the only things that cross the boundary are the Postgres schema
and one message body. A third shared thing would have to be a third
hand-maintained copy anyway. The speed constants stay honest because they
are physical constants — a walking pace and a cycling pace — rather than
tuning knobs anyone has a reason to turn.

## Consequences

- The queue message is now a type in the contract module *and* still pinned
  by the cross-repo golden diff until the worker migrates. The fixtures live
  at `internal/routing/testdata/message.golden.json` and
  `internal/handler/testdata/worker-store.golden.json` as well as in the
  contract module, because the worker still curls those paths.
  `make check-contract` diffs both.
- Speed constants, logger field names, and config env-vars are duplicated
  and can drift; they have. ADR-0003 left them duplicated.
- SPA-273 retired the shared schema. The worker talks HTTP
  (`/api/internal/...`) instead of Postgres, so one of the two crossings
  this decision named is gone.
- SPA-280 / ADR-0003 is the deliberate revision of this decision for the
  types that cross the wire.
