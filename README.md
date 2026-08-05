# Rabbit-Hole 🐇🕳️

**Go down the rabbit hole into your agent's decisions.** Rabbit-Hole is a legibility layer between AI agents and humans — it watches what your agents do and explains it back to you in plain language.

Three layers, one binary:
- **Collect** — eBPF kernel-level telemetry. Zero SDK. Works with closed-source agents.
- **Classify** — Two-tier pipeline: fast deterministic pattern matching + pluggable ML backend (local Gemma or remote gRPC).
- **Express** — Chat interface. "What did helios do at 3am?" Context window retrieval.

Self-hosted. One binary. Zero SDK requirements on the agent.

## Quick Start

```bash
# Build
make build

# Attach to an agent process
./bin/rabbit-hole attach --pid <PID>

# Start the daemon (collect + classify + serve)
./bin/rabbit-hole serve --db rabbit-hole.db

# Ask questions
./bin/rabbit-hole chat "What did the agent do in the last hour?"

# Search flows
./bin/rabbit-hole search "sql error"

# Check status
./bin/rabbit-hole status --db rabbit-hole.db

# Compact old data
./bin/rabbit-hole compact --db rabbit-hole.db --before 720h
```

## Requirements

- **Go 1.26+** (uses `go tool` and `testing/synctest`)
- **Linux** with eBPF support (kernel 5.11+) and **CAP_SYS_RESOURCE** for full telemetry collection
- **SQLite** (embedded via `modernc.org/sqlite` — no CGO required)

### eBPF privileges

Full kernel telemetry (the Collect layer) requires:

- Linux kernel **5.11+**
- **CAP_SYS_RESOURCE** (typically root — run `sudo ./bin/rabbit-hole attach --pid <PID>`)

Without these privileges, `attach` and `serve` **refuse to start** and exit
non-zero with:

```
eBPF unavailable — telemetry DISABLED
```

This is deliberate: silently running without kernel probes would make you
believe telemetry is being collected when nothing is.

**Degraded mode** (explicit opt-in, no kernel telemetry) is available via:

```bash
./bin/rabbit-hole attach --pid <PID> --no-ebpf
./bin/rabbit-hole serve --db rabbit-hole.db --no-ebpf
./bin/rabbit-hole serve --db rabbit-hole.db --demo-stream   # dogfood mode: generates demo flows, no root/eBPF needed
```

In degraded mode Rabbit-Hole logs a clear warning that telemetry is
**DISABLED** and the `/health` collector component reports
`eBPF degraded — telemetry DISABLED`. Session-management commands
(`status`, `list`, `detach`) keep working on unprivileged hosts and print a
"telemetry DISABLED" note so the limitation stays visible.

## Building

```bash
# Build the binary
make build          # → bin/rabbit-hole

# Run tests
make test           # short mode, no race detector
make test-all       # full suite with race detector

# Lint
make lint           # go vet
```

## Architecture

```
┌──────────┐     ┌──────────┐     ┌──────────┐
│  COLLECT  │ ──→ │ CLASSIFY │ ──→ │ EXPRESS  │
│  (eBPF)   │     │ (pattern  │     │ (chat)   │
│           │     │  + ML)    │     │          │
└──────────┘     └──────────┘     └──────────┘
       │               │               │
       └───────────────┴───────────────┘
                       │
                 ┌──────────┐
                 │ STORAGE  │
                 │ (SQLite) │
                 └──────────┘
```

## Project Structure

```
cmd/rabbit-hole/          # CLI entrypoint (Cobra)
internal/
  attach/                 # Pipeline orchestration
  classify/               # Classification engine + pattern matching
  classify/pb/            # gRPC protobuf definitions for remote backend
  collector/              # eBPF trace collection (cilium/ebpf)
  config/                 # Configuration loading (env vars + dotenv)
  express/                # HTTP + WebSocket chat server
  storage/                # SQLite persistence (WAL, FTS5)
pkg/types/                # Shared domain types
specs/                    # Architecture and design specifications
```

## Specifications

| # | File | Description |
|---|---|---|
| S01 | specs/01-Overview-Architecture.md | Project overview and architecture |
| S02 | specs/02-Collection-Layer.md | eBPF trace collection |
| S03 | specs/03-Classification-Layer.md | Two-tier classification engine |
| S04 | specs/04-Expression-Layer.md | HTTP/WebSocket chat interface |
| S05 | specs/05-Storage-Data-Model.md | SQLite storage and data model |
| S06 | specs/06-CLI-Self-Hosted.md | CLI and self-hosted deployment |

## License

MIT
