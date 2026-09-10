# CLAUDE.md

Use the Makefile for all build/test tasks (Go project).

- `make dev-workflow` — run before pushing: test, vet, lint, build (single verification step)
- `make build` — compile to `./bin/sparks-effect-api`
- `make run` — build and run
- `make test` — `go test ./... -cover` (no race detector; the fast loop)
- `make test-race` — the same suite with `-race`. Run it when a change touches
  concurrency; CI runs it against real services regardless.
- `make itest` — the full raced integration suite against throwaway Postgres and
  RabbitMQ containers, which is what CI gates on
- `make vet` / `make lint` — static checks
- `make tidy` — `go mod tidy`
- `make clean` — remove build artifacts

## Domain vocabulary

[`CONTEXT.md`](CONTEXT.md) is the canonical glossary for the whole project —
scenario, service, station, stop, node, edge, chainage, offset, dwell, boarding
wait, interchange, staleness, reach, routing anchor, mode vs costing, the five
error codes and the four job statuses. Read it before naming a new type,
endpoint, column or seam, and use its words rather than minting synonyms. The
other three repositories keep a `CONTEXT.md` of their own for terms only they
use, and link back here for the rest.

Declaration-level doc comments were deliberately removed from this source in
SPA-307 because they restated their declarations. Domain meaning belongs in
`CONTEXT.md`; rationale belongs in [ADRs](docs/adr/0001-api-and-worker-share-no-go-code.md),
in-function comments, migration headers, and the README. Do not reintroduce
godoc that only repeats a name.

## Tickets

Issues live in Linear, team `Sparks Effect` (`SPA-` prefix).
[`docs/agents/ticket-template.md`](docs/agents/ticket-template.md) is the canonical
ticket shape for the whole project: the front block that says which repos a change
lands in and in what order, the definition of ready behind `ready-for-agent`, six
paste-ready bodies, and the table of cross-repo seams. Read it before filing a
ticket or picking one up. The other three repositories link here rather than
restating it.

## Branching

One trunk: `main`. Branch from it, PR into it. There is no `prd` branch —
production is promoted by pushing a `vX.Y.Z` tag on a commit that is already on
`main`, which re-tags that commit's existing image as `:prd`. Linear staging
and production pipelines record which issues those publishes and tags contain.
See `docs/releases.md`.
