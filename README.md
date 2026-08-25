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

# Start the daemon first (collect + classify + serve)
./bin/rabbit-hole serve
# (no root/eBPF? use: ./bin/rabbit-hole serve --no-ebpf)

# Remote classification backend (optional — fleet/centralized setup):
# run classify-server on a backend host, point serve at it with --remote.
./bin/rabbit-hole classify-server --addr :50051
./bin/rabbit-hole serve --no-ebpf --remote localhost:50051

# In a second terminal: attach to an agent process.
# The session is started on the daemon and persisted to the database.
./bin/rabbit-hole attach --pid <PID> --no-ebpf

# List sessions (persisted, survives separate invocations)
./bin/rabbit-hole list --all

# Check status
./bin/rabbit-hole status

# Detach — completes the session in the daemon and the database
./bin/rabbit-hole detach <session-id>

# Ask questions
./bin/rabbit-hole chat "What did the agent do in the last hour?"

# Search flows
# Note: search queries recorded flows. On a fresh unprivileged install the
# only data is the --demo-stream demo session, which covers llm/patch/memory/
# network intents — so `search "write"` matches right away, while
# `search "sql error"` returns "No results found." until you record real flows.
# Warm-up window: each demo intent's first flow lands within ~30-60s of serve
# start, so searching for a term from a just-published intent (e.g. "patch")
# can return "No results found." until its first flow lands — re-run the
# search after ~30s if that happens.
./bin/rabbit-hole search "write"

# Compact old data
./bin/rabbit-hole compact --before 720h
# Or use minutes/seconds: --before 10m, --before 90s
```

Sessions are **persistent**: `attach` hands the session to the running daemon,
which writes it to the database. The session stays visible to `list --all`,
`status`, and `GET /api/v1/sessions` across separate CLI invocations, and
`detach <session-id>` completes it. Collection happens in the daemon — keep
`serve` running (attach to the same PID twice is rejected, and without a
daemon `attach` explains that `serve` must be started first).

### Configuration

Rabbit-Hole is configured entirely through environment variables — there are
no database-path flags. The SQLite database defaults to
`~/.rabbit-hole/rabbit-hole.db`; override it with `RABBITHOLE_DB_PATH` (or move
the whole data directory with `RABBITHOLE_DATA_DIR`). The HTTP server binds to
`127.0.0.1:9734` by default (`RABBITHOLE_LISTEN_ADDR`).

### Chat model

The chat CLI gets natural-language answers from an LLM via the server. Without
a real model configured, the server falls back to a built-in keyword stub and
the chat CLI prints a `stub` warning so you know the answers are canned. In
stub mode, time-window questions (e.g. "What did the agent do in the last
hour?") are answered from the most recent recorded activity, so the Quick
Start flow works out of the box; full natural-language answers need the model
env vars below.

- `RABBITHOLE_CHAT_MODEL_ENDPOINT` — base URL of an OpenAI-compatible chat
  completions API (e.g. `http://127.0.0.1:11434/v1` for Ollama,
  `https://api.openai.com/v1` for OpenAI). `/chat/completions` is appended
  automatically.
- `RABBITHOLE_CHAT_MODEL_NAME` — the model name to use (e.g. `llama3.1`).
- `RABBITHOLE_CHAT_MODEL_API_KEY` — API key for the endpoint (any value works
  for local endpoints like Ollama).

All three are required to enable the real model; if any is unset, the server
logs a warning and uses built-in stub answers. Set `RABBITHOLE_CHAT_ENABLED=false`
to force the stub even when the env vars are present.

### Remote classification backend

Classification can also run on a remote host: start `classify-server` (a gRPC
server that wraps the same pattern-matching catalog as the local backend) and
point `serve` at it with `--remote`. This is the fleet/centralized setup —
edge hosts send traces over gRPC instead of classifying locally.

```bash
# Backend host: run the reference classifier (blocks until SIGINT/SIGTERM)
./bin/rabbit-hole classify-server --addr :50051
# (optional auth: --token s3cret — the client passes it as localhost:50051@s3cret)

# Edge host: point the daemon at the remote backend
./bin/rabbit-hole serve --no-ebpf --remote localhost:50051
```

With a remote backend configured, `/health` reports the classifier as
`"detail": "remote gRPC backend"`. See
[docs/classify-server.md](docs/classify-server.md) for the full reference
(tokens, wire-format caveats, RPC list, optional grpcurl verification).

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
./bin/rabbit-hole serve --no-ebpf
RABBITHOLE_DB_PATH=/tmp/rabbit-hole-demo.db ./bin/rabbit-hole serve --demo-stream   # dogfood mode: generates demo flows, no root/eBPF needed
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
| S07 | specs/openapi.yaml | OpenAPI 3.0 API contract (authoritative) |

For a consumer-oriented walkthrough of the HTTP API (endpoints, curl examples,
WebSocket streaming, configuration), see [docs/integration.md](docs/integration.md).
The machine-readable contract lives in [specs/openapi.yaml](specs/openapi.yaml).

## License

MIT
