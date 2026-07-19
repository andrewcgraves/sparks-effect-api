# sparks-effect-api

Go REST API for the Sparks Effect project. It serves scenario seed data and
computes multimodal isochrones by chaining compiled rail travel times with
Stadia Maps access/egress isochrones.

There is no GTFS in this stack. Travel times come from an in-process
**TransitGraph** compiled at store construction from embedded YAML seed data.

## Pipeline

```
seed YAML (domain model + segment times)
        │
        ▼
Compile() → TransitGraph
  • per-service edges (run seconds + dwell)
  • boarding wait = best headway / 2
        │
        ▼
POST /api/isochrone (chainer)
  • Stadia: access matrix + origin/egress isochrones
  • TransitGraph: Dijkstra over union of service edges (seconds)
  • remaining budget → egress isochrones
```

Runtime unit of truth is **seconds** (`Edge.Seconds`, `WaitSecs`,
`TravelTimeBetween`). HTTP fields that are already minute-labeled
(`budget_mins`, `access_mins`, `remaining_mins`) stay as-is on the wire; the
chainer converts at the boundary.

## Seed data

Scenarios live under `internal/transit/data/scenarios/<slug>/` and are embedded
into the binary. Each scenario directory contains:

| File | Role |
| --- | --- |
| `scenario.yaml` | Scenario metadata |
| `vehicle_types.yaml` | Rolling stock (speed, accel, dwell level/step) |
| `routes.yaml` | Alignments (geometry + mode) |
| `stations.yaml` | Stations (slug, location, platform height) |
| `services.yaml` | Stopping patterns, frequency windows, vehicle |
| `travel_times.yaml` | Adjacent segment times (compiler input) |

Until the editor exists, these YAML files are the authoring interface.

### Segment times

`travel_times.yaml` segments use `minutes` today. The intended semantics are
**run time only** (train in motion); dwell is resolved separately at compile
time from vehicle × platform height (or a per-stop override). A follow-up
renames the seed field to `run_seconds` and recalibrates values so dwell is not
double-counted.

### Provenance tiers

Services will carry a provenance tier that gates which levers are honest in the
editor:

| Tier | Meaning |
| --- | --- |
| `computed` | Physics-compiled; all levers |
| `calibrated` | Imported timetable run times; dwell/frequency/stops editable; vehicle swap disabled |
| `frozen` | Geometry-less import; display + frequency/wait only |

CA HSR seed services are calibrated (Business Plan matrix). The field is wired
via the scenario API as that contract lands.

## Requirements

- [Go](https://go.dev/dl/) 1.25+
- [Docker](https://www.docker.com/) (optional; for containerized runs and the
  database integration tests — podman also works)
- `golangci-lint` (installed automatically by `make lint` if missing)

## Getting started

Clone the repo, set up your local environment, and run:

```sh
cp .env.example .env
# Edit .env and fill in STADIA_API_KEY
make run
```

This builds the binary to `bin/sparks-effect-api` and starts it, listening on
`:8080` by default. The server loads `.env` automatically on startup if the
file exists — variables already set in the shell take precedence. Override the
port with `PORT` in `.env` or your shell:

```sh
PORT=9090 make run
```

Check it's up:

```sh
curl localhost:8080/healthz
```

## Local SPA testing (CORS)

When running the Vue frontend locally (e.g. `npm run dev` on `http://localhost:5173`), the browser will block cross-origin requests unless CORS headers are present. Enable them for localhost origins only:

```sh
ALLOW_LOCALHOST_CORS=true make run
```

Or add `ALLOW_LOCALHOST_CORS=true` to `.env`. The flag is **off by default** and must never be set in production — it only allows `localhost` and `127.0.0.1` origins, never a wildcard.

## Verbose / debug logging

Set `LOG_LEVEL=debug` (or `VERBOSE=true`) to enable detailed logging for local
debugging. When enabled the server logs:

- Each isochrone request's `lat`, `lng`, `budget_mins`, `mode`, and
  `scenario_slug`
- Every Stadia HTTP call: endpoint name, HTTP status, latency, and — on
  failure — a snippet of the response body. The API key and `Authorization`
  header are never logged.
- Chain progress: station count, matrix reachable count, egress fan-out size,
  and final GeoJSON feature count.
- The full error value before it is mapped to a 502 or 500 response.

```sh
LOG_LEVEL=debug make run
```

Sample request for San Jose downtown, walk 90 min, ca-hsr scenario:

```sh
curl -s -X POST http://localhost:8080/api/isochrone \
  -H 'Content-Type: application/json' \
  -d '{"lat":37.3382,"lng":-121.8863,"budget_mins":90,"mode":"walk","scenario_slug":"ca-hsr"}' \
  | jq .type
```

## Persistence

Domain data (scenarios, routes, stations, vehicle types, services, jobs, users)
is read and written through a storage-agnostic `transit.Repository`. The concrete
implementation is Postgres via `pgx/v5` (pure Go — the `CGO_ENABLED=0` static
build is preserved), with geometry stored as GeoJSON in `jsonb` columns and
native `uuid`/`timestamptz`/`boolean` types throughout.

- **Connection:** set `DATABASE_URL` (Railway injects this via its private
  network). Cap the pool with `DATABASE_MAX_CONNS`. When `DATABASE_URL` is unset,
  the server falls back to the read-only embedded YAML store so local dev works
  without a database.
- **Migrations:** plain-SQL [`goose`](https://github.com/pressly/goose)
  migrations in `internal/persistence/postgres/migrations/`, embedded into the
  binary and run automatically on boot.
- **Seed:** on first boot against an empty database, the embedded `ca-hsr` seed
  data is written through the repository. The compiled-`TransitGraph` read path
  then loads those rows and produces isochrones as before.

### Database integration tests

Integration tests need a throwaway Postgres. They skip automatically when
`TEST_DATABASE_URL` (or `DATABASE_URL`) is unset, so `make test` stays green
without a database; in CI a missing URL is a hard failure instead of a silent
skip. Run them locally with one command (starts a container, runs the suite,
tears it down):

```sh
make itest
```

Or manage the container yourself:

```sh
make db-up            # start throwaway Postgres (postgres:16)
make test-integration # run the full suite against it
make db-down          # remove the container
```

Both the Makefile targets and CI use the same `postgres:16` image and settings,
so local and CI databases match. Use `make db-up DOCKER=podman` to use podman.

## Development

| Command                 | Description                                          |
| ----------------------- | ---------------------------------------------------- |
| `make test`             | Run the suite (DB integration tests skip without a DB) |
| `make itest`            | Start Postgres, run the full suite, tear it down     |
| `make test-integration` | Run the suite against `TEST_DATABASE_URL`            |
| `make db-up`/`db-down`  | Start / remove the throwaway Postgres container      |
| `make build`            | Build the binary to `bin/`                           |
| `make run`              | Build and run the API locally                        |
| `make lint`             | Run `golangci-lint`                                  |
| `make vet`              | Run `go vet`                                         |
| `make dev-workflow`     | Run test, vet, lint, and build — full verification   |
| `make tidy`             | Sync `go.mod`/`go.sum` with imports                  |
| `make clean`            | Remove build output                                  |

## Docker

Build and run the API in a container:

```sh
docker build -t sparks-effect-api .
docker run -p 8080:8080 sparks-effect-api
```

## Project layout

```
cmd/api/                     entrypoint (main.go)
internal/config/             environment-based configuration
internal/server/             HTTP server and route registration
internal/handler/            HTTP handlers
internal/transit/            domain types, Repository seam, TransitGraph compile, seed
internal/persistence/postgres/  Postgres repository + goose migrations
internal/isochrone/          Stadia + transit chainer
internal/stadia/             Stadia Maps client
```

## CI

GitHub Actions runs `test`, `vet`, and `lint` on every push and pull request,
then builds the binary and uploads it as a workflow artifact. On pushes to
`main`, it also builds the Docker image and publishes it to the GitHub
Container Registry at `ghcr.io/andrewcgraves/sparks-effect-api`, tagged with
`latest` and the commit SHA.
