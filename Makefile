BINARY_NAME := sparks-effect-api
BUILD_DIR := ./bin
CMD_DIR := ./cmd/api

GOLANGCI_LINT_VERSION := v2.12.2
GOBIN := $(shell go env GOPATH)/bin
GOLANGCI_LINT := $(GOBIN)/golangci-lint

# --- Throwaway Postgres for integration tests (single source of truth) ---
# These same values drive `make db-up` locally AND the CI job, so the local and
# CI databases always match. CI reuses these targets rather than redefining them.
POSTGRES_IMAGE   := postgres:16
TEST_DB_CONTAINER := sparks-effect-test-db
TEST_DB_PORT     := 5432
TEST_DB_PASSWORD := postgres
TEST_DB_NAME     := sparks_effect_test
# Container tool: docker by default, override with `make db-up DOCKER=podman`.
DOCKER           := docker
TEST_DATABASE_URL := postgres://postgres:$(TEST_DB_PASSWORD)@localhost:$(TEST_DB_PORT)/$(TEST_DB_NAME)?sslmode=disable

.PHONY: all deps build run test test-integration itest \
	db-up db-wait db-down vet lint tidy clean dev-workflow

all: build

# dev-workflow runs everything a change needs to pass before it's pushed:
# tests, vet, lint, and a build. Meant to be run end-to-end by humans or
# agents as a single verification step.
dev-workflow: test vet lint build

deps:
	go mod download

build: deps
	go build -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)

run: build
	$(BUILD_DIR)/$(BINARY_NAME)

# test runs the full suite. Postgres integration tests skip themselves unless
# TEST_DATABASE_URL (or DATABASE_URL) is set, so this stays green with no DB.
test: deps
	go test ./... -race -cover

# test-integration runs the full suite with a Postgres URL exported, so the
# integration tests actually execute. Point it at any Postgres via TEST_DATABASE_URL;
# defaults to the `make db-up` container. Used by both `make itest` and CI.
#
# -p 1 runs one package at a time. More than one package's integration tests
# reset the shared throwaway database to a known-empty state, and Go otherwise
# runs packages in parallel — so without this they wipe each other's schema
# mid-test and fail spuriously.
test-integration: deps
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test ./... -race -cover -p 1

# itest brings up a throwaway Postgres, runs the integration suite, and tears it
# down again — the one-command local equivalent of the CI job.
itest:
	$(MAKE) db-up
	$(MAKE) test-integration; status=$$?; $(MAKE) db-down; exit $$status

# db-up starts (or reuses) a throwaway Postgres and waits until it accepts
# connections. Idempotent: a running container is left in place.
db-up:
	@if [ -z "$$($(DOCKER) ps -q -f name=^/$(TEST_DB_CONTAINER)$$)" ]; then \
		if [ -n "$$($(DOCKER) ps -aq -f name=^/$(TEST_DB_CONTAINER)$$)" ]; then \
			$(DOCKER) rm -f $(TEST_DB_CONTAINER) >/dev/null; \
		fi; \
		echo "starting $(POSTGRES_IMAGE) as $(TEST_DB_CONTAINER)"; \
		$(DOCKER) run -d --name $(TEST_DB_CONTAINER) \
			-e POSTGRES_PASSWORD=$(TEST_DB_PASSWORD) \
			-e POSTGRES_DB=$(TEST_DB_NAME) \
			-p $(TEST_DB_PORT):5432 \
			$(POSTGRES_IMAGE) >/dev/null; \
	else \
		echo "$(TEST_DB_CONTAINER) already running"; \
	fi
	$(MAKE) db-wait

# db-wait blocks until Postgres is ready, using pg_isready from inside the
# container so no host tooling is required.
db-wait:
	@echo "waiting for Postgres to accept connections..."
	@for i in $$(seq 1 30); do \
		if $(DOCKER) exec $(TEST_DB_CONTAINER) pg_isready -U postgres -d $(TEST_DB_NAME) >/dev/null 2>&1; then \
			echo "Postgres is ready"; exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "Postgres did not become ready in time" >&2; exit 1

db-down:
	@$(DOCKER) rm -f $(TEST_DB_CONTAINER) >/dev/null 2>&1 || true

vet: deps
	go vet ./...

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

$(GOLANGCI_LINT):
	GOBIN=$(GOBIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

tidy:
	go mod tidy

clean:
	rm -rf $(BUILD_DIR)
