---
name: rabbit-hole-usage
description: >-
  How to actually USE the rabbit-hole project (agent legibility: collect → classify →
  express). Entry points, run recipes, current known-broken paths (attach persistence,
  NL chat filters), and the working demo/API patterns. Load this before touching the repo.
version: 1.0.0
---

# Rabbit-Hole Usage Skill

Rabbit-Hole is a Go binary that watches AI agents and explains their activity: eBPF collect →
pattern/ML classify → HTTP/WebSocket + chat express. Self-hosted, one binary, zero SDK.

**Current state (2026-08-08 dogfood):** Express/Storage layers work well. The `attach` CLI
workflow does NOT persist sessions (DF-001 — session dies with the process; the demo stream is
the only flow source). NL chat drops time/category filters (DF-002). OpenAPI spec is fiction
for Flow/ChatResponse wire format (DF-003). See `docs/dogfood/` for the full trail.

## Entry points

| Entry | What it is | Works? |
|---|---|---|
| `./bin/rabbit-hole serve --demo-stream --no-ebpf` | full pipeline daemon, demo flows every 3s, HTTP+WS on 127.0.0.1:9734 | ✅ (no root needed) |
| `./bin/rabbit-hole serve` | same, with eBPF (needs CAP_SYS_RESOURCE + kernel 5.11+) | ✅ hard-fails honestly without privileges |
| `./bin/rabbit-hole attach --pid X [--no-ebpf]` | attach to a real process | ⚠️ prints success, persists nothing (DF-001) |
| `./bin/rabbit-hole demo --flows N --hours-back M` | batch-seed realistic data | ✅ |
| `./bin/rabbit-hole chat/search/status/list/detach/compact` | CLI over the daemon/DB | ✅ except detach of attach-sessions (DF-001) |
| HTTP API | `/health`, `/api/v1/sessions|flows|search|chat|metrics|dashboard/summary`, `/api/v1/ws/sessions/{id}` | ✅ |
| `/dashboard/` | embedded SPA (trailing slash required) | ✅ |

## Fast start (unprivileged host)

```bash
make build
RABBITHOLE_DB_PATH=/tmp/rh.db ./bin/rabbit-hole serve --demo-stream --no-ebpf &   # daemon
./bin/rabbit-hole demo --flows 50 --hours-back 48    # historical data (same DB)
curl -s http://127.0.0.1:9734/health | jq             # status ok, collector degraded note
curl -s -X POST http://127.0.0.1:9734/api/v1/search -H 'Content-Type: application/json' \
  -d '{"Query":"write","Limit":10}' | jq '.flows[0]'  # NOTE: keys are PascalCase!
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

1. **Never trust `attach`'s output for state.** Session IDs printed by `attach` exist nowhere;
   `list`/`detach`/`status` read other state. Use `demo`/`--demo-stream` for data.
2. **Wire format is mixed.** Requests: `Query`, `Limit`, `SessionID` (wire names). Flow
   responses: `ID`, `SessionID`, `Intent`, `Description`, `Outcome`, `Confidence`, `Duration`
   (ns). Sessions + search envelope: `id`, `agent_name`, `total`, `cursor`, `has_more`
   (snake_case). The OpenAPI spec and docs/integration.md examples show lowercase for flows —
   wrong (DF-003). Always jq against a live response first.
3. **NL chat questions** ("what did the agent do in the last hour?") return canned
   "couldn't find any matching activity" — the LLM's filters are dropped and there is no time
   filter (DF-002). Use keyword-style queries (`write_file`, `sql`, `patch`) for reliable hits.
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
- `internal/express/chat.go` — chat handler (drops filters at :41 — DF-002)
- `internal/express/chatmodel.go` — LLM translate/answer + stub fallback
- `internal/collector/collector.go` — process-local session map (DF-001 root cause)
- `internal/attach/` — the REAL pipeline + SessionManager (only wired into serve)
- `internal/storage/sqlite.go` — SQLite WAL + FTS5 (`SearchFlows` at :326, no time filter)
- `specs/openapi.yaml` — declared authoritative; Flow/ChatResponse schemas currently wrong (DF-003)

## Verifying a fix (acceptance probes)

- DF-001: `attach --pid X --no-ebpf` → session appears in `list`/`status`/`GET /api/v1/sessions`
  and `detach <id>` exits 0.
- DF-002: `POST /api/v1/chat {"Message":"What did the agent do in the last hour?"}` → `Flows>0`.
- DF-003: `jq '.flows[0].session_id'` on a live search returns a value.
- DF-004: `demo` output references the real listen addr.
- GAP-006: `curl -o /dev/null -w '%{http_code}' http://127.0.0.1:9734/dashboard` → 200.
