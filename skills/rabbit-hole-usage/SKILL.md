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

**Current state (2026-08-27):** every prior finding DF-001..DF-032 is
complete and re-verified live; DF-027 (daemon wedge), DF-028 (session
lifecycle), DF-029 (compact m/s) fixes confirmed in real use. Real LLM chat
works for keyword questions (Ollama gpt-oss:20b, 18-50 s/question).
**Known hazards (dogfood 2026-08-27 — first real-LLM run):**
- **DF-034 (P0, open): time-window search is broken for UTC timestamps.**
  Flows are stored with LOCAL offset (`…T05:11:39.436-05:00`) but filters
  compare TEXT lexically, so any UTC (`Z`) window — exactly what the real
  LLM emits, per the translate prompt — matches 0 flows.
  `chat "What did the agent do in the last hour?"` with a real model says
  "no matching activity" while 70+ flows are live. Stub mode masked this
  via a fallback (chat.go:61-70). **Workaround until fixed:** pass windows
  in the same offset as storage, or use keyword queries (`chat "write"`).
- **DF-035 (P1, open): first LLM chat after daemon start → 500.** Cold local
  model exceeds the 30 s HTTP client timeout → `translate: context deadline
  exceeded` → `Error: chat: server returned 500`. Retry after warm-up works.
See `docs/dogfood/2026-08-27-integration.md` for the full report.

## Entry points

| Entry | What it is | Works? |
|---|---|---|
| `./bin/rabbit-hole serve --demo-stream --no-ebpf` | full pipeline daemon, demo flows every 3s, HTTP+WS on 127.0.0.1:9734 | ✅ (no root needed) |
| `./bin/rabbit-hole serve` | same, with eBPF (needs CAP_SYS_RESOURCE + kernel 5.11+) | ✅ hard-fails honestly without privileges |
| `./bin/rabbit-hole attach --pid X [--no-ebpf]` | attach to a real process; session handed to a running serve via POST /api/v1/sessions/attach | ✅ (DF-001 fixed, commit 7545df4) |
| `./bin/rabbit-hole demo --flows N --hours-back M` | batch-seed realistic data (--spread works) | ✅ |
| `./bin/rabbit-hole classify-server --addr :50051 [--token T]` | remote gRPC classifier backend (fleet setup) | ✅ (DF-023) |
| `./bin/rabbit-hole chat/search/status/list/detach/compact` | CLI over the daemon/DB | ✅ |
| HTTP API | `/health`, `/api/v1/sessions|search|chat|metrics|dashboard/summary`, `/api/v1/flows/{id}`, `/api/v1/flows/{id}/context-window`, `/api/v1/ws/sessions/{id}` | ✅ |
| `/dashboard/` | embedded SPA (trailing slash required) | ✅ |

Note: there is no `GET /api/v1/flows` list endpoint — use `POST /api/v1/search` instead (it returns flows).

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
   `list`/`detach`/`status`. Use `demo`/`--demo-stream` for bulk data. The attach success
   message names the CLI's local DB path (DF-031) — trust `list`/`status`, not the message.
2. **Wire format is snake_case end-to-end.** Requests: `query`, `limit`, `session_id` (wire
   names). Flow responses: `id`, `session_id`, `intent`, `description`, `outcome`,
   `confidence`, `duration` (ns). Sessions + search envelope: `id`, `agent_name`, `total`,
   `cursor`, `has_more` (snake_case). The OpenAPI spec matches this (DF-003 fixed). Always
   jq against a live response first.
3. **NL chat questions** ("what did the agent do in the last hour?") apply time/category
   filters and return matching flows (DF-002 fixed). Keyword-style queries (`write_file`,
   `sql`, `patch`) are still the most reliable for precise hits. In stub mode the API
   answers are canned but honest (`"stub": true`).
4. **Chat with a local CPU model is slow:** 3–35s per question (two LLM round-trips:
   translate + summarize). Budget for it.
5. **Dashboard needs the trailing slash:** `/dashboard` → 200 SPA (GAP-006); `/dashboard/`
   is the canonical path.
6. **WS stream is live-only:** flows are pushed to subscribers connected at publish time; no
   replay. Ping every 30s; clients must pong or the server drops them.
7. **Compact is destructive:** `compact --before 1h` deletes flows/traces older than 1h
   (verified). Point it at a copy first if unsure. **Duration suffixes are h/d only** —
   `compact --before 10m` fails with "unsupported duration suffix: m" (DF-029).
8. **Data dir:** default `~/.rabbit-hole/rabbit-hole.db`; use `RABBITHOLE_DB_PATH` for
   hermetic runs. SQLite WAL — data survives restarts and even SIGKILL (verified).
9. **Daemon wedge (DF-027, P0):** the `--demo-stream` daemon (esp. with `--remote` + WS +
   attach cycles) can burn CPU/RAM until unresponsive and may not die on SIGQUIT. Health-
   check it (`curl -m 2 /health`) if you run it long; capture `/proc/<pid>/status` before
   SIGKILL. Soak-test before trusting in production.
10. **Session lifecycle in degraded mode (DF-028):** sessions may stay `running` after the
    attached process exits, or (rarely) flip to `crashed` early. Verify with `list --all`
    rather than assuming status accuracy.
11. **Remote backend:** `classify-server --addr :50051 [--token T]` + client side
    `serve --remote host:port@T`; `/health` then shows `ok — remote gRPC <endpoint>`.

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
- DF-021: `rabbit-hole version` shows a real commit hash; `/health` version == CLI version.
- DF-022: zero-config `serve` → `/health` classifier says `degraded — pattern-only (model not loaded)`.
- DF-023: `classify-server :50051` + `serve --remote` → `/health` classifier `ok — remote gRPC …`.
- DF-026: `search "patch"` against `--demo-stream` data returns ≥1 result.
- DF-027 (fixed, re-verified 08-27): 15+ min soak demo-stream + remote + WS + attach → RSS
  bounded (~28 MB), `/health` fast, SIGQUIT exits.
- DF-028 (fixed, re-verified 08-27): attach to `sleep 30` → session terminal ≤60s after exit.
- DF-029 (fixed, re-verified 08-27): `compact --before 10m` accepted and deletes only old rows.
- **DF-034 (P0, open):** `POST /api/v1/search` with `time_range.start` in UTC (`Z`) over
  local-offset stored flows → must return the flows (today: 0). Also `chat` with a REAL model
  (not stub): "What did the agent do in the last hour?" → `flows>0`. Probe both offsets —
  the stub fallback does not exercise the filter.
- **DF-035 (P1, open):** fresh `serve` with real chat model → first `chat` must not 500
  (warm-up/retry/timeout).
