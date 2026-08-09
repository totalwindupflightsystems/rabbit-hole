---
name: rabbit-hole-usage
description: >-
  How to actually USE the rabbit-hole project (agent legibility: collect → classify →
  express). Entry points, run recipes, current state, and the working demo/API patterns.
  Load this before touching the repo.
version: 1.0.0
---

# Rabbit-Hole Usage Skill

Rabbit-Hole is a Go binary that watches AI agents and explains their activity: eBPF collect →
pattern/ML classify → HTTP/WebSocket + chat express. Self-hosted, one binary, zero SDK.

**Current state (2026-08-09):** Express/Storage layers work well. `attach` sessions persist
via the daemon-side SessionManager — the CLI hands the session to a running `serve` over
`POST /api/v1/sessions/attach` (DF-001 fixed). NL chat applies time/category filters
(DF-002 fixed). The wire format is snake_case and matches the OpenAPI spec (DF-003/DF-004
fixed). The `--demo-stream` daemon is still the easiest data source; see `docs/dogfood/`
for the full trail.

## Entry points

| Entry | What it is | Works? |
|---|---|---|
| `./bin/rabbit-hole serve --demo-stream --no-ebpf` | full pipeline daemon, demo flows every 3s, HTTP+WS on 127.0.0.1:9734 | ✅ (no root needed) |
| `./bin/rabbit-hole serve` | same, with eBPF (needs CAP_SYS_RESOURCE + kernel 5.11+) | ✅ hard-fails honestly without privileges |
| `./bin/rabbit-hole attach --pid X [--no-ebpf]` | attach to a real process; session handed to a running serve via POST /api/v1/sessions/attach | ✅ (DF-001 fixed, commit 7545df4) |
| `./bin/rabbit-hole demo --flows N --hours-back M` | batch-seed realistic data | ✅ |
| `./bin/rabbit-hole chat/search/status/list/detach/compact` | CLI over the daemon/DB | ✅ |
| HTTP API | `/health`, `/api/v1/sessions|flows|search|chat|metrics|dashboard/summary`, `/api/v1/ws/sessions/{id}` | ✅ |
| `/dashboard/` | embedded SPA (trailing slash required) | ✅ |

## Fast start (unprivileged host)

```bash
make build
RABBITHOLE_DB_PATH=/tmp/rh.db ./bin/rabbit-hole serve --demo-stream --no-ebpf &   # daemon
./bin/rabbit-hole demo --flows 50 --hours-back 48    # historical data (same DB)
curl -s http://127.0.0.1:9734/health | jq             # status ok, collector degraded note
curl -s -X POST http://127.0.0.1:9734/api/v1/search -H 'Content-Type: application/json' \
  -d '{"query":"write","limit":10}' | jq '.flows[0]'  # NOTE: wire keys are snake_case
./bin/rabbit-hole chat "write_file"                   # keyword chat works
```

Real LLM chat answers (Ollama on this host):
```bash
RABBITHOLE_CHAT_MODEL_ENDPOINT=http://127.0.0.1:11434/v1 RABBITHOLE_CHAT_MODEL_NAME=gemma3:4b \
RABBITHOLE_CHAT_MODEL_API_KEY=ollama ./bin/rabbit-hole serve --demo-stream --no-ebpf
```
Without all three env vars the server answers from a keyword stub — API responses carry
`"stub": true`, the CLI prints a stderr warning. That's by design (GAP-004).

## Common pitfalls (learned the hard way)

1. **`attach` sessions live in the daemon.** `attach` hands the session to a running `serve`
   via `POST /api/v1/sessions/attach` (DF-001 fixed) — the printed session id now shows up in
   `list`/`detach`/`status`. Use `demo`/`--demo-stream` for bulk data.
2. **Wire format is snake_case end-to-end.** Requests: `query`, `limit`, `session_id` (wire
   names). Flow responses: `id`, `session_id`, `intent`, `description`, `outcome`,
   `confidence`, `duration` (ns). Sessions + search envelope: `id`, `agent_name`, `total`,
   `cursor`, `has_more` (snake_case). The OpenAPI spec matches this (DF-003 fixed). Always
   jq against a live response first.
3. **NL chat questions** ("what did the agent do in the last hour?") apply time/category
   filters and return matching flows (DF-002 fixed). Keyword-style queries (`write_file`,
   `sql`, `patch`) are still the most reliable for precise hits.
4. **Chat with a local CPU model is slow:** 3–35s per question (two LLM round-trips:
   translate + summarize). Budget for it.
5. **Dashboard needs the trailing slash:** `/dashboard` → 301; `/dashboard/` → SPA (GAP-006).
6. **WS stream is live-only:** flows are pushed to subscribers connected at publish time; no
   replay. Ping every 30s; clients must pong or the server drops them.
7. **Compact is destructive:** `compact --before 1h` deletes flows/traces older than 1h
   (verified: 25 flows + 150 traces gone). Point it at a copy first if unsure.
8. **Data dir:** default `~/.rabbit-hole/rabbit-hole.db`; use `RABBITHOLE_DB_PATH` for
   hermetic runs. SQLite WAL — data survives restarts (verified).

## Useful internals map

- `cmd/rabbit-hole/serve.go` — daemon wiring (pipeline at serve.go:121, demo stream at :167)
- `internal/express/chat.go` — chat handler (LLM translate/summarize; time/category filters applied)
- `internal/express/chatmodel.go` — LLM translate/answer + stub fallback
- `internal/collector/collector.go` — process-local session map (DF-001 root cause)
- `internal/attach/` — the REAL pipeline + SessionManager (only wired into serve)
- `internal/storage/sqlite.go` — SQLite WAL + FTS5 (`SearchFlows` at :326)
- `specs/openapi.yaml` — declared authoritative; matches the live snake_case wire format (DF-003 fixed)

## Verifying a fix (acceptance probes)

- DF-001: `attach --pid X --no-ebpf` → session appears in `list`/`status`/`GET /api/v1/sessions`
  and `detach <id>` exits 0.
- DF-002: `POST /api/v1/chat {"message":"What did the agent do in the last hour?"}` → `flows>0`.
- DF-003: `jq '.flows[0].session_id'` on a live search returns a value.
- DF-004: `demo` output references the real listen addr.
- GAP-006: `curl -o /dev/null -w '%{http_code}' http://127.0.0.1:9734/dashboard` → 200.
