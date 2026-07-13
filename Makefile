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
