# Contributing to Rabbit-Hole

Thanks for wanting to contribute! Rabbit-Hole is a legibility layer for AI agents — it makes agent decisions visible to humans.

## Development Setup

```bash
# Clone
git clone git@gitlab.readydedis.com:rabbit-hole/rabbit-hole.git
cd rabbit-hole

# Build
make build

# Run all checks
make vet
make test
make lint
```

### Requirements

- **Go 1.26+** (uses `go tool` and `testing/synctest`)
- **Linux kernel 5.8+** (eBPF ring buffer support)
- **libbpf** for eBPF compilation
- **CAP_BPF** and **CAP_SYS_ADMIN** for collector attachment
- **Ollama** (optional, for local Gemma classification)

## Architecture

Three layers, one binary:

```
COLLECT (eBPF) → CLASSIFY (pluggable) → EXPRESS (chat/API)
```

See `specs/01-Overview-Architecture.md` for the full design.

## Testing

```bash
# Unit tests
go test ./... -count=1 -short

# Benchmarks
go test -bench=. -benchmem -run='^$' ./...

# Integration tests (requires root/eBPF capabilities)
sudo go test -tags=integration ./internal/collector/...

# Full suite
make test-all
```

## Code Conventions

- All exported functions must have Go doc comments
- Use `internal/` for private packages, `pkg/` for public API
- Tests live alongside code (`*_test.go` in same package)
- No stubs in production code — every interface wired to real infrastructure

## CI

GitLab CI runs on every push to `main`:
- Build (`go build ./...`)
- Vet (`go vet ./...`)
- Test (`go test ./... -count=1 -short`)
- Vulncheck (`govulncheck ./...`)

## Spec-Driven Development

All features start as specs in `specs/`. Each spec follows a 10-section axiom-level template with exact Go interfaces, DDL, error paths, wiring, and test scenarios.

## Questions?

Open an issue on GitLab or start a chat with the foreman.
