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
  • records the result through authenticated /api/internal/... endpoints
        │
        ▼
GET /api/routing-jobs/{id}  →  status, then the result
```

Runtime unit of truth is **seconds** (`Edge.Seconds`, `WaitSecs`).
HTTP fields that are already minute-labeled
(`budget_mins`, `access_mins`, `remaining_mins`) stay as-is on the wire.

`BOARDING_WAIT_POLICY` is one of `none` (the default), `half_headway`,
`full_headway`, or `fixed` with `BOARDING_WAIT_FIXED_SECS`. A malformed or
incomplete setting falls back to `none` rather than refusing to boot: a typo
should cost a wait, never a deployment, and never charge one nobody asked for.
Per-service and per-scenario overrides (SPA-237) are set on create/update via
`boarding_wait` and win in that order over the global default. Service reads
report the resolved wait as `boarding_wait_policy`, `boarding_wait_secs`, and
`boarding_wait_source` (`service` / `scenario` / `global`), so a client never
re-derives `min(headway)/2` itself.

### The queue contract

The message is a contract between two repositories with no compiler checking
it ([ADR-0001](docs/adr/0001-api-and-worker-share-no-go-code.md)), so it is
pinned by a golden fixture — `internal/routing/testdata/message.golden.json` —
which this repo asserts it produces and the worker repo asserts it consumes.

The worker-store HTTP envelope is a second contract of the same kind:
`IsochroneKey`, `CachedIsochrone`, and the request/response wrappers the
worker posts to `/api/internal/...`. A renamed JSON tag decodes as its
zero value — every cache lookup misses, the response is still `200 OK`,
and there is no error anywhere to find it by. That envelope is pinned by
`internal/handler/testdata/worker-store.golden.json`. `departs_on` is
`omitempty` on the wire; the fixture keeps it non-empty so the SPA-269
field cannot hide.

`make check-contract` (and a CI job of the same name) fetches the worker's
copies on `main` and diffs both fixtures, so a field added on either
side turns the other side's pipeline red. It is not part of
`make dev-workflow`: it needs the network. Those files are the on-the-wire
shape; a prose copy of them is a third copy and goes stale.

The graph travels inline — a few kilobytes for CA HSR, roughly 30 KB for a large
authored scenario — so the worker needs no database of its own. Publisher
confirms are required: without them the API could insert a routing job, fail to
publish, and strand it in `queued` while a client polls work no worker will ever
see. On a failed confirm the job is marked failed immediately and the caller
gets a 502 carrying the `publish_failed` code.

The worker does not connect to Postgres (SPA-273). Job transitions and the
egress-polygon cache go through `/api/internal/...`, gated by `WORKER_TOKEN`
(a shared bearer secret, not a user session). Unset, those routes answer 503
rather than being left unauthenticated. Deploy this side before the worker:
its startup ping is `GET /api/internal/worker`. Transit cache rows also carry
`departs_on` (SPA-269); walk/bike/drive omit it.

Travel mode is stored in the domain's own vocabulary — `walk` / `bike` /
`drive` / `transit` ([CONTEXT.md](CONTEXT.md#travel-mode-vs-costing)). "Costing"
is Valhalla's word for the same concept and stays at the worker's client
boundary, which is also where `transit` becomes Valhalla's `multimodal`
(SPA-246). A Postgres CHECK holds the same set (migration 00021), so a mode the
API would refuse is not storable either.

### Capping the backlog

All three isochrone endpoints publish to one queue that one worker consumes
serially, so nothing but a ceiling bounds how much work can be waiting. Since
SPA-219 an enqueue is refused with `429` and the `backlog_full` code, plus a
`Retry-After`, once `MAX_INFLIGHT_ISOCHRONES` routing jobs are already queued or
running (default 20; `0` disables the cap). The refusal happens before the
request body is read and before any graph is fetched, so a flood costs one
indexed `count` each rather than an ever-growing queue that legitimate requests
wait behind.

The signal is the count of unfinished `routing_jobs` rather than the broker's
queue depth: the rows are the record this API already writes on that path, and
they also count a job the worker has picked up but not finished. Only jobs
younger than `handler.RoutingJobStaleAfter` count, which is what keeps a dead
worker's abandoned rows from wedging the cap shut forever — and is why recovery
needs nothing: the count falls as the worker drains, or as jobs age out.

The ceiling is per deployment, not per caller. That is deliberate: bounding
total work is what it is for, and per-caller fairness needs a per-caller key,
which is a separate piece of work.

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
| `segment_run_times.yaml` | Adjacent segment run times (compiler input) |

Until the editor exists, these YAML files are the authoring interface.

### Segment times

`segment_run_times.yaml` holds **run time** only (`run_seconds`; train in
motion). Dwell is resolved separately at compile time from vehicle × platform
height (or a per-stop override) and folded into `Edge.Seconds`. `DwellS` is
the dwell *part* of `Seconds`, not an extra addend. See **Dwell** (and **Run
time** for the seed-field semantics) in [`CONTEXT.md`](CONTEXT.md).

### Provenance tiers

Services carry a provenance tier that gates which editor levers are honest. The
three tiers, and the trap that `travel_time_sets.provenance` is a different
free-form field, are defined under **Provenance tier** in
[`CONTEXT.md`](CONTEXT.md). CA HSR seed services are `calibrated`.

## Branching and releases

One trunk: `main`. Branch from it, PR into it. Production runs an image promoted
from the Actions tab — see [`docs/releases.md`](docs/releases.md). Linear
records which issues landed on staging vs production from those same publishes
and tags.

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

## CORS

Cross-origin requests are allowed from the project's own frontends, always and
regardless of any flag:

- `https://sparks-effect.app` and any subdomain of it (`https://dev.sparks-effect.app`,
  and whatever comes next — the whole domain matches, so a new subdomain needs
  no code change)
- `https://sparks-effect-website.vercel.app`, the production Vercel alias
- Vercel preview deployments on the `andrewcgraves-projects` team

Everything else is refused. The match is HTTPS-only and requires a real
subdomain boundary, so a lookalike such as `notsparks-effect.app` does not get
in.

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
is stored in Postgres via `pgx/v5` (pure Go — the `CGO_ENABLED=0` static
build is preserved), with geometry stored as GeoJSON in `jsonb` columns and
native `uuid`/`timestamptz`/`boolean` types throughout. Handlers depend on
narrow store interfaces; boot-time load and seed talk to `transit.StoreSource`,
`transit.SeedSink`, and `transit.SeedReconciler`. `postgres.Repo` satisfies all
of them.

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
  data is written through `SeedSink` and then compiled, leaving a succeeded
  compile job whose result is the scenario's graph — no manual step, no admin
  credentials. On every boot `ReconcileSeed` upserts embedded YAML rows whose
  content no longer matches what is stored, scoped to seeded scenarios, so a
  YAML correction reaches a deployed database without a migration. A boot that
  finds a graph already matching its source rows leaves it alone, so restarting
  is not a recompile.

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

### Two scenario models

*Scenario* and *service* each name two models: the seeded world model
(`Scenario`, `Service`) and the authored counterparts (`UserScenario`,
`UserService`). `/api/me` is the owner-scoped view of the seeded types, not
the authored ones. The authored surface is `/api/user-scenarios` and
`/api/services`. `/api/me/services` returns **Services**; `/api/services`
returns **UserServices**. Both collections are owner-scoped; the model is
different. The `/api/me` prefix is not the split. See
[CONTEXT.md](CONTEXT.md#the-one-that-catches-everyone-two-scenarios-two-services)
for the map.

### Authenticated endpoints

This is the owner-scoped and admin-gated surface, plus login and the
OptionalAuth snap-stops row — not a full route list, and not the public
scenario/route reads or `/api/internal/*`.

| Endpoint | Access | Purpose |
| --- | --- | --- |
| `POST /api/auth/login` | public | Exchange email + password for a token |
| `POST /api/auth/logout` | authenticated | Revoke the presented token |
| `GET /api/auth/me` | authenticated | The caller's identity and admin flag |
| `GET /api/me/scenarios` | authenticated | Seeded Scenarios the caller owns |
| `GET /api/me/services` | authenticated | Seeded Services the caller owns, not UserServices |
| `POST /api/me/routes` | authenticated | Author an alignment of your own |
| `GET`/`PUT`/`DELETE /api/me/routes/{slug}` | authenticated | Read, edit, or remove one |
| `POST /api/routes/{slug}/snap-stops` | optional | Project stop coordinates onto a route's alignment; public for curated routes, owner-scoped for owned drafts |
| `POST /api/me/scenarios` | authenticated | Author a seeded scenario of your own |
| `GET`/`PUT`/`DELETE /api/me/scenarios/{slug}` | authenticated | Read, edit, or remove one |
| `GET`/`POST /api/me/scenarios/{slug}/stations` | authenticated | Its stations |
| `PUT`/`DELETE /api/me/scenarios/{slug}/stations/{stationSlug}` | authenticated | Edit or remove one |
| `GET`/`PUT /api/me/scenarios/{slug}/travel-times` | authenticated | Its segment run times, read and replaced whole |
| `POST /api/me/services` | authenticated | Author a seeded service inside a scenario you own |
| `GET`/`PUT`/`DELETE /api/me/services/{id}` | authenticated | Read, edit, or remove one |
| `POST /api/admin/users` | admin | Provision an account |
| `POST /api/admin/routes` | admin | Ingest a curated alignment |
| `POST /api/scenarios/{slug}/prerendered-isochrones` | admin | Curate a ready-to-display isochrone for a scenario |

### Owning the seeded models

Scenarios, routes, stations, and services all carry a nullable `owner_id`. No
owner means **curated**: the seeded ca-hsr baseline and the admin-ingested
alignments, served by the public reads and writable only by an admin. An owner
means someone authored it, and it is theirs.

Authoring the **seeded** models works through `/api/me`, and an owned scenario
is a real one — give it routes, stations, and segment run times and it compiles
through the same path the baseline does, via
`POST /api/scenarios/{slug}/compile`. It just never appears on a public
surface. The authored `UserScenario` / `UserService` surface is a different
model; see [Two scenario models](#two-scenario-models).

One invariant holds that together: **a scenario and all of its children share
one owner** ([CONTEXT.md](CONTEXT.md#seeded--curated--authored--owned)). The
create and update handlers enforce it by refusing to attach a child to a
scenario the caller does not own.

That invariant is why containment is cheap. Only two reads filter on ownership —
`ListCuratedScenarios` and `ListCuratedRouteSummaries`, named for what they
return so the contract is visible at the call site. Everything scenario-scoped
stays unfiltered, because scoping to a scenario has already scoped to its owner.
`LoadStore` and `CompileSeededIfNeeded` read the curated list and nothing else,
so no owned row is ever loaded into the public store or served by
`GET /api/scenarios`.

By-slug reads cannot filter — slugs are globally unique across curated and owned
rows, so minting one has to see the whole namespace — so those paths check
ownership themselves and the public ones (`GET /api/routes/{slug}`,
`GET /api/scenarios/{slug}/graph`, `POST /api/isochrone`) sit behind
`auth.OptionalAuth`: still public for the curated data they exist for, while an
owner reaches their own draft and everyone else gets a 404 rather than
confirmation that the slug exists.

The existing `GET /api/scenarios/...` reads stay public — they serve curated
data and are unauthenticated by design. That includes the two prerendered
isochrone reads, `GET /api/scenarios/{slug}/prerendered-isochrones` (metadata
only) and `GET /api/prerendered-isochrones/{id}` (with its payload); only the
write above is gated, which is why it appears in the table despite not living
under `/api/admin/`.

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
- **Referencing** — `auth.CanReference` is its counterpart, and the two answer
  differently for the same unowned row. A curated alignment is a public building
  block anyone may point a service at, but admin-only to *mutate*. So
  referencing a route uses `CanReference`; authoring into a scenario uses
  `CanAccess`, because adding to a scenario is changing it.

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
| `make check-contract`   | Diff the golden fixtures against the worker           |
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
internal/compile/            in-process compile-job runner (not the routing worker)
internal/transit/            domain types, TransitGraph compile, seed
internal/persistence/postgres/  Postgres repository + goose migrations
internal/routing/            queue message contract + confirm-mode AMQP publisher
```

## CI

GitHub Actions runs `test`, `vet`, and `lint` on every pull request and on
pushes to `main`, diffs the golden fixtures against the worker as its own
job (`make check-contract`), then builds the binary and uploads it as a
workflow artifact. The contract job needs `GH_CONTRACT_TOKEN` — a PAT
that can read `sparks-effect-routing-worker` — because that repo is private.
A push to `main` also builds the Docker image and publishes it to the GitHub
Container Registry at `ghcr.io/andrewcgraves/sparks-effect-api`, tagged
`sha-<commit>` (immutable) and `staging` (moving).

Promoting from the Actions tab re-tags that same image as `prd` without
rebuilding it, so production runs the bytes staging ran. There is no `latest`.
See [`docs/releases.md`](docs/releases.md).
