# CLAUDE.md

Use the Makefile for all build/test tasks (Go project).

- `make dev-workflow` — run before pushing: test, vet, lint, build (single verification step)
- `make build` — compile to `./bin/sparks-effect-api`
- `make run` — build and run
- `make test` — `go test ./... -race -cover`
- `make vet` / `make lint` — static checks
- `make tidy` — `go mod tidy`
- `make clean` — remove build artifacts
