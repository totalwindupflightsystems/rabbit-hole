# Rabbit-Hole Integration Guide

**Status:** Active
**Applies to:** `rabbit-hole serve` (the Express layer)
**Authoritative API contract:** [../specs/openapi.yaml](../specs/openapi.yaml)

This guide is for consumers who want to talk to Rabbit-Hole over HTTP —
dashboards, bots, alerting hooks, or custom tooling. Rabbit-Hole exposes a
REST API plus a WebSocket stream for real-time flow updates, all served by the
single `rabbit-hole` binary.

## Quick Start

```bash
# Build the binary
make build

# Start the daemon (collect + classify + serve). Degraded mode (no eBPF)
# works on any host: ./bin/rabbit-hole serve --no-ebpf
./bin/rabbit-hole serve

# The API is now live:
curl -s http://127.0.0.1:9734/health
```

## Base URL

The server binds to `127.0.0.1:9734` by default. Override the bind address
with `RABBITHOLE_LISTEN_ADDR` (host:port). All examples below assume the
default.

## Authentication

**None.** The server binds to loopback by default and is intended for
single-machine, self-hosted use. If you bind to a non-loopback interface
(e.g. `RABBITHOLE_LISTEN_ADDR=0.0.0.0:9734`), you are exposing the API to your
network — make sure that is what you want. There is no built-in token auth.

## Endpoints

| Method | Path | Description |
|---|---|---|
| GET | `/health` | Liveness + per-component health (storage, metrics, classifier, collector) |
| GET | `/api/v1/sessions` | List agent sessions (`offset`, `limit` query params; `limit` max 100) |
| GET | `/api/v1/sessions/{id}` | One session by UUID |
| GET | `/api/v1/flows/{id}` | One flow by UUID |
| GET | `/api/v1/flows/{id}/context-window` | Context window for a flow (if captured) |
| POST | `/api/v1/search` | Search flows (FTS5 or structured filters) |
| POST | `/api/v1/chat` | Ask a natural-language question about agent activity |
| GET | `/api/v1/metrics` | Prometheus metrics (also at `/metrics`) |
| GET | `/api/v1/dashboard/summary` | Aggregate metrics for the dashboard overview |
| GET | `/api/v1/ws/sessions/{id}` | WebSocket: real-time flow stream for a session |
| GET | `/dashboard` | Embedded web dashboard (served at `/dashboard/`) |

The complete request/response schema for every endpoint — including flow and
session field definitions — is the [OpenAPI 3.0 contract](../specs/openapi.yaml).
Treat the spec as the source of truth; the examples below are the common cases.

## Examples

### Health

```bash
curl -s http://127.0.0.1:9734/health | jq
```

```json
{
  "status": "ok",
  "version": "1.0.0",
  "uptime": "12m4.5s",
  "components": {
    "storage": { "status": "ok" },
    "metrics": { "status": "ok", "detail": "runtime gauges active" }
  }
}
```

### List sessions

```bash
curl -s "http://127.0.0.1:9734/api/v1/sessions?limit=10" | jq
```

```json
{
  "sessions": [
    {
      "id": "0191a000-0000-7000-8000-000000000001",
      "agent_pid": 12345,
      "agent_name": "hermes",
      "start_time": "2026-08-08T10:00:00Z",
      "status": "running"
    }
  ],
  "total": 1,
  "offset": 0,
  "limit": 10
}
```

### Search flows

`POST /api/v1/search` accepts a `SearchRequest` JSON body. Field names follow
the Go wire format (`Query`, `Limit`, `SessionID`, ...); the response is a
`SearchResponse` with `flows`, `total`, `cursor`, and `has_more`.

```bash
curl -s -X POST http://127.0.0.1:9734/api/v1/search \
  -H 'Content-Type: application/json' \
  -d '{"Query": "sql error", "Limit": 10}' | jq
```

```json
{
  "flows": [
    {
      "id": "0191b000-0000-7000-8000-000000000001",
      "session_id": "0191a000-0000-7000-8000-000000000001",
      "intent": "execute_code",
      "phase": "action",
      "description": "Ran sql query against billing database",
      "outcome": "success",
      "confidence": 0.97
    }
  ],
  "total": 1,
  "cursor": "",
  "has_more": false
}
```

Filter without a free-text query by omitting `Query` and using `SessionID`,
`Categories`, `Outcomes`, `TimeRange`, and `Cursor` instead.

### Chat

`POST /api/v1/chat` accepts a `ChatRequest` with `Message` (required) and an
optional `SessionID`. The server translates the question into a search, runs
it, and returns a natural-language `Answer` plus follow-up `Suggestions` and
the referenced `Flows`.

```bash
curl -s -X POST http://127.0.0.1:9734/api/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"Message": "What did the agent do in the last hour?"}' | jq
```

```json
{
  "Answer": "Found 3 actions:",
  "Flows": [
    {
      "id": "0191b000-0000-7000-8000-000000000001",
      "intent": "execute_code",
      "description": "Ran sql query against billing database",
      "outcome": "success"
    }
  ],
  "Suggestions": [
    "What happened in the last hour?",
    "Show me the slowest operations"
  ]
}
```

**Stub mode:** when no real chat model is configured (see
[Configuration](#configuration)), the server answers from a built-in keyword
stub and sets `"stub": true` in the response. The `rabbit-hole chat` CLI
prints a warning to stderr in this case. Consumers should check the `stub`
field and surface it to users — stub answers are canned, not model-generated.

### Metrics

Prometheus text format:

```bash
curl -s http://127.0.0.1:9734/api/v1/metrics | head -20
```

### Dashboard summary

```bash
curl -s http://127.0.0.1:9734/api/v1/dashboard/summary | jq
```

## WebSocket streaming

Real-time flow updates stream over a WebSocket per session:

```bash
wscat -c "ws://127.0.0.1:9734/api/v1/ws/sessions/<session-id>"
```

Each message is a JSON-encoded `Flow` object, pushed as the collector →
classifier pipeline produces it. The server sends pings every 30s (clients
should pong within 60s) and closes the socket with a normal closure when the
session ends. If a subscriber falls behind, the server drops flows
(non-blocking) rather than backpressuring the pipeline.

## Configuration

Rabbit-Hole is configured entirely through environment variables:

| Variable | Default | Purpose |
|---|---|---|
| `RABBITHOLE_LISTEN_ADDR` | `127.0.0.1:9734` | HTTP/WebSocket bind address |
| `RABBITHOLE_DB_PATH` | `~/.rabbit-hole/rabbit-hole.db` | SQLite database path |
| `RABBITHOLE_DATA_DIR` | `~/.rabbit-hole/` | Data directory (db, models) |
| `RABBITHOLE_CHAT_ENABLED` | `true` | Set `false` to force the stub chat model |
| `RABBITHOLE_CHAT_MODEL_ENDPOINT` | — | OpenAI-compatible chat completions base URL (e.g. `http://127.0.0.1:11434/v1` for Ollama) |
| `RABBITHOLE_CHAT_MODEL_NAME` | — | Model name (e.g. `llama3.1`) |
| `RABBITHOLE_CHAT_MODEL_API_KEY` | — | API key (any value for local endpoints) |
| `RABBITHOLE_CORS_ORIGINS` | `*` | Allowed CORS origins |
| `RABBITHOLE_RATE_LIMIT_ENABLED` | `true` | Per-endpoint rate limiting |
| `RABBITHOLE_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |

The chat model env vars (`RABBITHOLE_CHAT_MODEL_ENDPOINT`,
`RABBITHOLE_CHAT_MODEL_NAME`, `RABBITHOLE_CHAT_MODEL_API_KEY`) are all
**required** to enable the real model; if any is unset, the server logs a
warning and falls back to the built-in stub (responses carry `"stub": true`).

## API contract

The authoritative, machine-readable contract for this API is
[../specs/openapi.yaml](../specs/openapi.yaml) (OpenAPI 3.0). If the examples
above and the spec ever disagree, the spec wins.
