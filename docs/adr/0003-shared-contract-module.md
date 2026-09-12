# ADR-0003: Shared contract module for API↔worker wire types

*Recorded 2026-09-11 for SPA-280.*

## Status

Accepted for this repository's bootstrap. Supersession of
[ADR-0001](0001-api-and-worker-share-no-go-code.md) for the wire types is
complete when both consumers import a tagged `sparks-effect-contract` and
delete their hand copies.

## Context

ADR-0001 recorded a deliberate choice: the API and routing worker share no Go
code. Its premise was that the only things crossing the boundary were the
Postgres schema and one message body, so a third shared thing would have to be
a third hand-maintained copy.

That premise no longer holds. SPA-273 retired the shared schema. What still
crosses is the queue message, the `TransitGraph` that rides inside it, the
worker-store envelope (`IsochroneKey`, `CachedIsochrone`, the cache lookup/put
and job-transition bodies), plus `geo` constants, config env-vars, and
logger field names. A golden fixture plus `make check-contract` (a curl and
diff against the worker's copy) could pin the message; it could not make the
compiler reject a renamed JSON tag on the store envelope.

A versioned Go module is not a third hand-maintained copy. It is a dependency
the compiler checks.

## Decision

The types that ride the queue and the worker-store HTTP envelope live in
`github.com/andrewcgraves/sparks-effect-contract`:

- `routing` — `Message`, `SchemaVersion`
- `transit` — `TransitGraph`, `Edge`, `GraphNode`, `ServiceGraph`, `TravelMode`,
  the merge-report types nested in the graph, and the test-only `Node` projection
- `store` — `IsochroneKey`, `CachedIsochrone`, and the HTTP envelope types

This repository vendors the module via `replace => ./contract` until the
dedicated `sparks-effect-contract` repository is populated and tagged. This
environment cannot push there; the nested tree is the source until that copy
happens. The replace is the bootstrap, not the long-term shape.

`geo`, `config`, and `logger` stay duplicated. They are the weakest members of
the inventory (the worker's `geo` already carries a `DetourFactor` the API
deliberately lacks; the loggers have diverged in size), and shipping the three
packages that cross the wire proves the mechanism.

`SchemaVersion` still exists so the two sides can run different *module*
versions.

The routing-worker companion — import the module, delete its hand copies, retire
its `check-contract` for these types — is a follow-up. This environment cannot
touch that repository.

## Consequences

- `make check-contract` and the CI "API↔worker contract" job stay until
  the worker consumes the module. Nested-module tests pin local shape; they
  do not replace the cross-repo golden diff. The worker still has hand-copied
  types, and its CI still curls `internal/routing/testdata/message.golden.json`
  and `internal/handler/testdata/worker-store.golden.json`.
- The API re-exports the migrated types via aliases (`transit.TransitGraph`,
  `handler.IsochroneKey`, `routing.Message`) so existing call sites keep
  compiling. Struct definitions of those types are deleted from `internal/`.
- Methods cannot be added on an alias of a foreign type, so `Edge.placedOn`
  became the package-level `placeEdge` in `internal/transit`, and
  `ServiceGraph.applyBoardingWait` became `applyBoardingWait`.
- A release step appears once the dedicated repo is tagged: bump the
  contract, `go get` in both consumers. Until then the replace is the whole
  of the coupling.
