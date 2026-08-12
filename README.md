# sparks-effect-api

Go REST API for the Sparks Effect project. It serves scenario seed data,
compiles it into transit graphs, and hands multimodal isochrone requests to the
routing worker over a queue.

There is no GTFS in this stack. Travel times come from a **TransitGraph**
compiled from the scenario's seed data and persisted as a compile job's result;
an isochrone is plotted over the graph that job produced.

The API does not compute isochrones. Valhalla runs ClusterIP-only on the home
cluster with no ingress, so nothing deployed elsewhere can reach it — the queue
is the only transport, and the routing fan-out executes inside the cluster in a
separate repository (SPA-182). This repository's job is to resolve a request
down to one immutable compiled graph and publish it.

## Pipeline

```
seed YAML (domain model + segment times)
        │
        ▼
Compile() → TransitGraph, stored as a succeeded compile job
  • per-service edges (run seconds + dwell)
  • boarding wait from BOARDING_WAIT_POLICY (default none → 0)
  • nodes (position + names) so the graph plots on its own
        │
        ▼
POST /api/isochrone  →  202 + routing job
  • resolves auth, ownership, target slug, and the stale-graph check
  • publishes { graph, lat, lng, budget_mins, mode } with publisher confirms
        │
        ▼
routing worker (separate repo, inside the cluster)
  • Valhalla: access matrix + origin/egress isochrones
  • TransitGraph: Dijkstra over union of service edges (seconds)
  • writes the result back onto the routing job
        │
        ▼
GET /api/routing-jobs/{id}  →  status, then the result
```

Runtime unit of truth is **seconds** (`Edge.Seconds`, `WaitSecs`,
`TravelTimeBetween`). HTTP fields that are already minute-labeled
(`budget_mins`, `access_mins`, `remaining_mins`) stay as-is on the wire.

`BOARDING_WAIT_POLICY` is one of `none` (the default), `half_headway`,
`full_headway`, or `fixed` with `BOARDING_WAIT_FIXED_SECS`. A malformed or
incomplete setting falls back to `none` rather than refusing to boot: a typo
should cost a wait, never a deployment, and never charge one nobody asked for.
The service reads report the resolved wait as `boarding_wait_policy` and
`boarding_wait_secs`, so a client never re-derives `min(headway)/2` itself.

### The queue contract

The message is a contract between two repositories with no compiler checking
it, so it is pinned by a golden fixture — `internal/routing/testdata/message.golden.json`,
which this repo asserts it produces and the worker repo asserts it consumes.

```json
{
  "schema_version": 1,
  "routing_job_id": "<uuid>",
  "compile_job_id": "<uuid>",
  "graph": { "...compiled TransitGraph..." },
  "lat": 0.0, "lng": 0.0,
  "budget_mins": 0,
  "mode": "walk | bike | drive"
}
```

The graph travels inline — 2,894 bytes for CA HSR, roughly 30 KB for a large
authored scenario — so the worker needs no database of its own. Publisher
confirms are required: without them the API could insert a routing job, fail to
publish, and strand it in `queued` while a client polls work no worker will ever
see. On a failed confirm the job is marked failed immediately and the caller
gets a 502 carrying the `publish_failed` code.

Travel mode is stored in the domain's own vocabulary — `walk` / `bike` /
`drive`. "Costing" is Valhalla's word for the same concept and stays at the
worker's client boundary.

### Polling a routing job

`GET /api/routing-jobs/{id}` returns the job's status and, once succeeded, its
result. A job with **no owner** came from the public `POST /api/isochrone` and is
readable by anyone holding its id — a v4 UUID, unguessable. An **owned** job,
from one of the authored isochrones, answers 404 to anyone but its owner or an
admin, so a caller cannot probe which job ids exist.

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
# Optionally set AMQP_URL to enable the isochrone endpoints
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

## Logging

Every log line is a single JSON object — timestamp, level, message, and
whatever structured fields the call site attaches — written to stderr, so it
can be forwarded to Grafana through Alloy without scraping free text.

`LOG_LEVEL` sets the minimum level logged: `debug`, `info`, `warn`, or `error`
(case-insensitive). Unset or unrecognised defaults to `info`. `VERBOSE=true`
is a back-compat alias for `LOG_LEVEL=debug`.

At debug level the server additionally logs:

- Each isochrone request's `lat`, `lng`, `budget_mins`, `mode`, and
  `scenario_slug`
- Each routing job as it is published, with the queue it went to and its
  trace id.
- When a seeded scenario compile is skipped on boot because it is already
  compiled.

Every request also gets one `info`-level access log line with its method,
path, status, duration, and trace id, and every internal error is logged with
the full error value before it is mapped to a 502 or 500 response.

```sh
LOG_LEVEL=debug make run
```

### Trace ids

Every request carries a trace id, exposed via the `X-Trace-Id` header: if the
caller (the website) sends one, the API uses it; if not, the API mints one and
echoes it back in the response header. The id is attached to that request's
own log lines and forwarded to the routing worker as `trace_id` on the queue
message for any isochrone the request enqueues, so one request's logs can be
followed across both services in Grafana.

Sample request for San Jose downtown, walk 90 min, ca-hsr scenario. It answers
202 with a routing job; poll that job for the result.

```sh
JOB=$(curl -s -X POST http://localhost:8080/api/isochrone \
  -H 'Content-Type: application/json' \
  -d '{"lat":37.3382,"lng":-121.8863,"budget_mins":90,"mode":"walk","scenario_slug":"ca-hsr"}' \
  | jq -r .id)

curl -s "http://localhost:8080/api/routing-jobs/$JOB" | jq '{status, error}'
```

## Persistence

Domain data (scenarios, routes, stations, vehicle types, services, jobs, users)
is read and written through a storage-agnostic `transit.Repository`. The concrete
implementation is Postgres via `pgx/v5` (pure Go — the `CGO_ENABLED=0` static
build is preserved), with geometry stored as GeoJSON in `jsonb` columns and
native `uuid`/`timestamptz`/`boolean` types throughout.

- **Connection:** set `DATABASE_URL` (Railway injects this via its private
  network). Cap the pool with `DATABASE_MAX_CONNS`. When `DATABASE_URL` is unset,
  the server falls back to the read-only embedded YAML store, so the scenario
  reads work without a database — but the isochrone routes answer `503`, since a
  graph is identified by the compile job that produced it and there are no jobs
  without a database. They answer `503` without `AMQP_URL` too: with no queue to
  publish to there is no isochrone to be had.
- **Migrations:** plain-SQL [`goose`](https://github.com/pressly/goose)
  migrations in `internal/persistence/postgres/migrations/`, embedded into the
  binary and run automatically on boot.
- **Seed:** on first boot against an empty database, the embedded `ca-hsr` seed
  data is written through the repository and then compiled, leaving a succeeded
  compile job whose result is the scenario's graph — no manual step, no admin
  credentials. A boot that finds a graph already there leaves it alone, so
  restarting is not a recompile.

## Authentication

The API is **invite-only**: there is no signup route. Accounts exist only
because an admin created them, and the first admin comes from the environment.

Authentication is bearer-token based. `POST /api/auth/login` returns a token
that clients send as `Authorization: Bearer <token>`. Tokens are opaque and
stored server-side (only as a SHA-256 hash) rather than being JWTs, so logout
genuinely revokes them and there is no signing key to manage. Passwords are
bcrypt-hashed. Sessions expire after `SESSION_TTL_HOURS` (default 24).

Authentication requires `DATABASE_URL`; with the read-only embedded store the
auth endpoints answer `503` rather than pretending to work.

### Endpoints

| Endpoint | Access | Purpose |
| --- | --- | --- |
| `POST /api/auth/login` | public | Exchange email + password for a token |
| `POST /api/auth/logout` | authenticated | Revoke the presented token |
| `GET /api/auth/me` | authenticated | The caller's identity and admin flag |
| `GET /api/me/scenarios` | authenticated | Scenarios the caller owns |
| `GET /api/me/services` | authenticated | Services the caller owns |
| `POST /api/admin/users` | admin | Provision an account |

The existing `GET /api/scenarios/...` reads stay public — they serve curated
data and are unauthenticated by design.

### Bootstrapping the first admin

Set both variables and boot once; the account is created if that email does not
already exist, and is never overwritten on later boots (so leaving the variables
in place cannot silently reset a password).

```sh
BOOTSTRAP_ADMIN_EMAIL=you@example.com
BOOTSTRAP_ADMIN_PASSWORD=<a strong password>
```

Everyone else is then provisioned through `POST /api/admin/users`.

### Authorization

Two rules, both enforced server-side:

- **Admin gating** — `RequireAdmin` protects account provisioning and is the
  gate route-write endpoints register behind.
- **Ownership** — `auth.CanAccess` is the single ownership predicate: admins
  reach everything, other users reach only rows they own, and unowned rows (the
  curated seed data) are admin-only. Owner-scoped reads resolve ownership in
  SQL, so rows the caller does not own are never loaded — and scoping always
  comes from the token's identity, never from a client-supplied parameter.

### Database integration tests

Integration tests need a throwaway Postgres. They skip automatically when
`TEST_DATABASE_URL` (or `DATABASE_URL`) is unset, so `make test` stays green
without a database; in CI a missing URL is a hard failure instead of a silent
skip. Run them locally with one command (starts a container, runs the suite,
tears it down):

```sh
make itest
```

Or manage the containers yourself:

```sh
make db-up            # start throwaway Postgres (postgres:16)
make mq-up            # start throwaway RabbitMQ (rabbitmq:4-alpine)
make test-integration # run the full suite against both
make mq-down
make db-down
```

There are two backing services because there are two things a fake cannot
prove: that the schema is what the worker will find, and that a publish is
actually confirmed by a broker. Both sets of tests skip themselves when their
URL is unset, so `make test` stays green with neither running — except in CI,
where a missing URL is a hard failure so a misconfigured pipeline cannot pass by
silently skipping.

Both the Makefile targets and CI use the same images and settings, so local and
CI environments match. Use `make db-up DOCKER=podman` to use podman.

## Development

| Command                 | Description                                          |
| ----------------------- | ---------------------------------------------------- |
| `make test`             | Run the suite (integration tests skip without their services) |
| `make itest`            | Start Postgres + RabbitMQ, run the full suite, tear them down |
| `make test-integration` | Run the suite against `TEST_DATABASE_URL` and `TEST_AMQP_URL` |
| `make db-up`/`db-down`  | Start / remove the throwaway Postgres container      |
| `make mq-up`/`mq-down`  | Start / remove the throwaway RabbitMQ container      |
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
internal/auth/               password hashing, session tokens, middleware, ownership rule
internal/ids/                UUID generation for runtime-created rows
internal/transit/            domain types, Repository seam, TransitGraph compile, seed
internal/persistence/postgres/  Postgres repository + goose migrations
internal/routing/            queue message contract + confirm-mode AMQP publisher
```

## CI

GitHub Actions runs `test`, `vet`, and `lint` on every push and pull request,
then builds the binary and uploads it as a workflow artifact. On pushes to
`main`, it also builds the Docker image and publishes it to the GitHub
Container Registry at `ghcr.io/andrewcgraves/sparks-effect-api`, tagged with
`latest` and the commit SHA.
