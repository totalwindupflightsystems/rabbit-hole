# Rabbit-Hole — Agent Legibility
BINARY = bin/rabbit-hole
GO = go

# Build metadata injected via -ldflags so `rabbit-hole version` and
# GET /health report the real version/commit/date of THIS build (DF-021).
# Overridable: make build VERSION=v1.2.3 COMMIT=abc1234 BUILD_DATE=...
VERSION ?= $(shell git describe --tags 2>/dev/null || printf 'v0.0.0-%s' "$$(git rev-parse --short HEAD 2>/dev/null)")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git log -1 --format=%cd --date=iso-strict 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
GOFLAGS = -ldflags="-s -w -X main.Version=$(VERSION) -X main.Commit=$(COMMIT) -X main.BuildTime=$(BUILD_DATE)"

.PHONY: build test test-all test-slo lint vet clean install

build:
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/rabbit-hole/

test:
	$(GO) test ./... -count=1 -short

# -race is mandatory: a racing test can still exit 0. Timing SLO budgets in the
# stress tests are per-host and assume a quiet host — see docs/testing.md.
test-all:
	$(GO) test ./... -count=1 -race

# The stress SLO budgets were calibrated WITHOUT -race; -race inflates the
# SQLite-heavy paths 20-55x, so they cannot hold inside test-all. Run them here.
test-slo:
	$(GO) test ./internal/storage/ -count=1 -run '^TestStress_' -timeout 15m

lint:
	$(GO) vet ./...

vet: lint

clean:
	rm -rf bin/

install:
	$(GO) install $(GOFLAGS) ./cmd/rabbit-hole/

bench:
	$(GO) test -bench=. -benchtime=1s -run='^$$' ./... -count=1

stress:
	$(GO) test ./... -count=1 -race -timeout 10m

integration:
	$(GO) test ./... -count=1 -tags=integration -timeout 5m

e2e:
	$(GO) test ./... -count=1 -timeout 10m
