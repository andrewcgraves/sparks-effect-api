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
