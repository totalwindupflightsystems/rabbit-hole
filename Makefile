# Rabbit-Hole — Agent Legibility
BINARY = bin/rabbit-hole
GO = go
GOFLAGS = -ldflags="-s -w"

.PHONY: build test lint vet clean install

build:
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/rabbit-hole/

test:
	$(GO) test ./... -count=1 -short

test-all:
	$(GO) test ./... -count=1 -race

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
