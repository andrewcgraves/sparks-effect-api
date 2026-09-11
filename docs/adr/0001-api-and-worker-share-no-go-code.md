# ADR-0001: The API and worker share no Go code

*Recorded 2026-09-10. The decision is older: until SPA-307 it lived as a
doc comment on the speed constants in `internal/geo/geo.go`.*

## Status

Accepted. SPA-280 proposes to revise the premise, not to discover that this
was never decided.

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

- The queue message is a contract the compiler cannot check. It is pinned by
  a golden fixture both repositories assert (`internal/routing/testdata/message.golden.json`).
- Speed constants, logger field names, and config env-vars are duplicated
  and can drift; they have.
- SPA-273 retired the shared schema. The worker talks HTTP
  (`/api/internal/...`) instead of Postgres, so one of the two crossings
  this decision named is gone. What still crosses is the message, the
  worker-store envelope, the geo constants, config, and logger field names.
- SPA-280 revisits the premise. A versioned Go module is not a third
  hand-maintained copy; it is a dependency the compiler checks. That work
  is a deliberate revision of this decision.
