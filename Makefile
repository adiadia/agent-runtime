SHELL := /bin/bash

GOCACHE ?= $(CURDIR)/.cache/go-build
GOMODCACHE ?= $(CURDIR)/.cache/go-mod
GOTMPDIR ?= $(CURDIR)/.cache/go-tmp
DATABASE_URL ?= postgres://durable:durable@localhost:5432/durable?sslmode=disable
GO_ENV = GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) GOTMPDIR=$(GOTMPDIR)

VERSION ?= dev
GIT_SHA ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
LDFLAGS_API := -X main.Version=$(VERSION) -X main.Commit=$(GIT_SHA) -X main.BuildDate=$(DATE)
LDFLAGS_WORKER := -X main.Version=$(VERSION) -X main.Commit=$(GIT_SHA) -X main.BuildDate=$(DATE)

INTEGRATION_PACKAGES := ./internal/persistence/postgres ./internal/repository ./internal/worker

.PHONY: cache-dirs test-setup fmt fmt-check vet lint test test-unit test-integration test-integration-db validate \
	docker-build docker-up docker-down wait-db migrate build-api build-worker build-cli build

cache-dirs:
	@mkdir -p $(GOCACHE) $(GOMODCACHE) $(GOTMPDIR)

test-setup: cache-dirs
	@$(GO_ENV) go mod download

fmt:
	@files="$$(find . -type f -name '*.go' -not -path './.git/*' -not -path './.cache/*' -not -path './.gocache/*' -not -path './.gomodcache/*' -not -path './vendor/*')"; \
	if [ -z "$$files" ]; then \
		echo "no go files found"; \
		exit 0; \
	fi; \
	gofmt -w $$files

fmt-check:
	@files="$$(find . -type f -name '*.go' -not -path './.git/*' -not -path './.cache/*' -not -path './.gocache/*' -not -path './.gomodcache/*' -not -path './vendor/*')"; \
	if [ -z "$$files" ]; then \
		echo "no go files found"; \
		exit 0; \
	fi; \
	unformatted="$$(gofmt -l $$files)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt would change the following files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet: cache-dirs
	@$(GO_ENV) go vet ./...

lint: vet

test: cache-dirs
	@$(GO_ENV) go test ./...

test-unit: cache-dirs
	@$(GO_ENV) go test ./...

test-integration-db: cache-dirs
	@DATABASE_URL=$(DATABASE_URL) $(GO_ENV) go test -count=1 -tags=integration $(INTEGRATION_PACKAGES)

test-integration:
	@set -euo pipefail; \
	$(MAKE) docker-up; \
	trap '$(MAKE) docker-down >/dev/null' EXIT; \
	$(MAKE) wait-db; \
	$(MAKE) migrate; \
	$(MAKE) test-integration-db

validate: fmt-check vet test test-integration

docker-build:
	docker build -f Dockerfile.api \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(GIT_SHA) \
		--build-arg BUILD_DATE=$(DATE) \
		-t agent-runtime-api:$(VERSION) \
		-t agent-runtime-api:latest .
	docker build -f Dockerfile.worker \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(GIT_SHA) \
		--build-arg BUILD_DATE=$(DATE) \
		-t agent-runtime-worker:$(VERSION) \
		-t agent-runtime-worker:latest .

docker-up:
	docker compose up -d postgres

docker-down:
	docker compose down

wait-db:
	@for i in $$(seq 1 30); do \
		if docker compose exec -T postgres pg_isready -U durable -d durable >/dev/null 2>&1; then \
			exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo "postgres did not become ready in time"; \
	exit 1

migrate:
	for f in $$(ls migrations/*.sql | sort); do \
		echo "applying $$f"; \
		cat $$f | docker compose exec -T postgres psql -v ON_ERROR_STOP=1 -U durable -d durable; \
	done

build-api: cache-dirs
	@mkdir -p bin
	@$(GO_ENV) go build -ldflags "$(LDFLAGS_API)" -o bin/api ./cmd/api

build-worker: cache-dirs
	@mkdir -p bin
	@$(GO_ENV) go build -ldflags "$(LDFLAGS_WORKER)" -o bin/worker ./cmd/worker

build-cli: cache-dirs
	@mkdir -p bin
	@$(GO_ENV) go build -o bin/cli ./cmd/cli

build: build-api build-worker build-cli
