BINARY_NAME := sparks-effect-api
BUILD_DIR := ./bin
CMD_DIR := ./cmd/api

GOLANGCI_LINT_VERSION := v2.12.2
GOBIN := $(shell go env GOPATH)/bin
GOLANGCI_LINT := $(GOBIN)/golangci-lint

.PHONY: all deps build run test vet lint tidy clean dev-workflow

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

test: deps
	go test ./... -race -cover

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
