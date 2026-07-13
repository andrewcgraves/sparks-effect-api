# sparks-effect-api

Go REST API for the Sparks Effect project. It will act as a proxy in front of
a local GTFS-backed routing/isochrone service, exposing endpoints the
frontend can call to compute transit reach for a given route.

## Requirements

- [Go](https://go.dev/dl/) 1.25+
- [Docker](https://www.docker.com/) (optional, for containerized runs)
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

## Development

| Command             | Description                                       |
| ------------------- | -------------------------------------------------- |
| `make test`         | Run unit tests                                     |
| `make build`        | Build the binary to `bin/`                         |
| `make run`          | Build and run the API locally                      |
| `make lint`         | Run `golangci-lint`                                |
| `make vet`          | Run `go vet`                                       |
| `make dev-workflow` | Run test, vet, lint, and build — full verification |
| `make tidy`         | Sync `go.mod`/`go.sum` with imports                |
| `make clean`        | Remove build output                                |

## Docker

Build and run the API in a container:

```sh
docker build -t sparks-effect-api .
docker run -p 8080:8080 sparks-effect-api
```

## Project layout

```
cmd/api/            entrypoint (main.go)
internal/config/     environment-based configuration
internal/server/     HTTP server and route registration
internal/handler/    HTTP handlers
```

## CI

GitHub Actions runs `test`, `vet`, and `lint` on every push and pull request,
then builds the binary and uploads it as a workflow artifact. On pushes to
`main`, it also builds the Docker image and publishes it to the GitHub
Container Registry at `ghcr.io/andrewcgraves/sparks-effect-api`, tagged with
`latest` and the commit SHA.
