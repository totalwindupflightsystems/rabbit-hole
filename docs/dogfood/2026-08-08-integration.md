# Rabbit-Hole — Real-Use Integration Report (2026-08-08)

**Run type:** Full dogfood field test (cron) — a user trying to do real work, not the test suite.
**Environment:** Linux 7.0.0-28-generic, unprivileged (no CAP_SYS_RESOURCE), Go 1.26.5, Ollama on
127.0.0.1:11434 (gemma3:4b, gpt-oss:20b). Binary built fresh with `make build` (v1.0.0-dev).

**Verdict: 🔴 DOES-NOT-DELIVER (core promise) — the Express/Storage shell is real and valuable;
the Collect layer (attach real agent processes) is unreachable and the chat can't answer its
flagship question type.** Fixable without redesign — see board tasks DF-001..DF-004.

---

## What the product promises (README/specs)

> "A legibility layer between AI agents and humans — it watches what your agents do and explains
> it back to you in plain language." Zero SDK, one binary, three layers:
> COLLECT (eBPF attach to a real process) → CLASSIFY (pattern + ML) → EXPRESS (chat: "What did
> helios do at 3am?").

## The promised workflow, verbatim from README Quick Start

```bash
make build
./bin/rabbit-hole attach --pid <PID>      # attach to an agent process
./bin/rabbit-hole serve                   # collect + classify + serve
./bin/rabbit-hole chat "What did the agent do in the last hour?"
./bin/rabbit-hole search "sql error"
./bin/rabbit-hole status
./bin/rabbit-hole compact --before 720h
```

## What actually works (verified live, with evidence)

| Thing | Result |
|---|---|
| `make build` | ✅ clean build, binary runs |
| `serve --demo-stream --no-ebpf` | ✅ full pipeline: demo generator → pattern classifier → SQLite (FTS5) → HTTP/WS server, flow published every 3s with intents (`write_file`, `llm_api_call`, `patch_code`, `git_commit`, `run_tests`...) |
| `serve --demo-stream` alone (no `--no-ebpf`) | ✅ skips the eBPF preflight in dogfood mode (serve.go:87: `if !noEBPF && !demoStream`), as README promises |
| Unprivileged `attach --pid X` | ✅ hard-fails: `Error: eBPF unavailable — telemetry DISABLED (requires CAP_SYS_RESOURCE and kernel 5.11+...)`, exit 1 (GAP-001 fix confirmed) |
| `attach --pid X --no-ebpf` | ⚠️ prints success, persists nothing (see Failures #1) |
| `status` / `list` | ✅ work, honest "eBPF: DISABLED" note; reflect DB state |
| `demo --flows N --hours-back M` | ✅ seeds realistic classified sessions; ⚠️ stale :8080 hint (DF-004) |
| `search "write"` / `search "llm"` | ✅ FTS5 works, returns classified flows with confidence |
| `chat "write_file"` (CLI+API, real model) | ✅ 15 flows, real LLM summary, `stub` absent |
| `chat "What did the agent do in the last hour?"` (real model) | ❌ canned "couldn't find any matching activity" (Failure #2) |
| Stub mode (`RABBITHOLE_CHAT_ENABLED=false` or unset env) | ✅ API sets `"stub": true`; CLI prints stderr warning naming the 3 env vars (GAP-004 confirmed) |
| `GET /health` | ✅ 200, components incl. collector "eBPF degraded — telemetry DISABLED" |
| `GET /api/v1/sessions`, `/api/v1/flows/{id}`, `/api/v1/search`, `/api/v1/metrics`, `/api/v1/dashboard/summary` | ✅ all 200, sane payloads |
| `POST /api/v1/search` structured filters | ✅ accepts Query/Limit (wire format); envelope `{flows,total,cursor,has_more}` |
| WebSocket `/api/v1/ws/sessions/{id}` | ✅ real-time flows arrive (verified with a gorilla/websocket probe; ping/30s, drop-if-behind semantics) |
| `/dashboard/` SPA | ✅ served, embedded (dark New-Relic-ish SPA); `/dashboard` (no slash) → 301 (GAP-006, still open) |
| Error handling | ✅ bad JSON → 400 with message; empty chat message → 400; unknown flow → 404 JSON; exit codes correct |
| Restart persistence | ✅ killed server → restarted on same DB: 143→147 flows, 3 sessions, dashboard summary intact |
| `compact --before 1h` | ✅ deletes old traces/flows with counts logged, DB stays consistent |
| Rate limiting | ⚠️ 12 rapid `/health` calls all 200 (either burst-friendly or health exempt — not investigated further) |

**Time-to-first-success (demo-mode end-to-end): ~3 minutes** — build + `serve --demo-stream
--no-ebpf` + health check + first flow via API. Genuinely fast and pleasant.

## Failures (each is a board task)

### 1. 🔴 DF-001 (P0) — `attach` is a fire-and-forget no-op; real telemetry can never reach the DB
Reproduced exactly:
```bash
$ ./bin/rabbit-hole attach --pid $$ --no-ebpf
Attached to PID 1261966
Session ID: 019fe3a3-02f2-7844-a3a0-a4338fe71fbe
Agent: /usr/bin/bash (...)
Collection active. Use 'rabbit-hole status' to monitor or 'rabbit-hole chat' to query.
Detach with: rabbit-hole detach 019fe3a3-02f2-7844-a3a0-a4338fe71fbe
$ ./bin/rabbit-hole list            # → "No active sessions."
$ ./bin/rabbit-hole detach 019fe3a3-...   # → Error: session ... not found (exit 1)
```
The session ID exists in **no database** (searched `~/.rabbit-hole/rabbit-hole.db` and the serve
DB). Why: `cmd/rabbit-hole/attach.go` opens no storage, creates a fresh in-memory collector,
attaches, prints, and the process exits. `internal/collector` keeps sessions in a process-local
map. The real pipeline (collector→classifier→store) is wired only inside `serve`
(serve.go:121), and nothing can start a session on the serve daemon — no attach flag, no API
endpoint (`grep StartSession` in cmd/ and express/ = 0 hits). The demo stream is the **only**
flow source in the entire binary. Even as root, `attach` dies with its process.
→ Fix direction: daemon-side attach (HTTP/gRPC endpoint on `serve` + persistence), or make
`attach` the long-running collector.

### 2. 🟠 DF-002 (P1) — Chat drops the LLM's structured filters and has no time dimension
```bash
$ curl -s -X POST http://127.0.0.1:9734/api/v1/chat -d '{"Message":"What did the agent do in the last hour?"}' | jq .Answer
"I couldn't find any matching activity for your query."   # 140+ flows live, incl. last-3s
```
Root cause chain (read from source, then confirmed against the store API):
- `TranslateQuery` asks the LLM for JSON `{query, limit, phases, outcomes}` and parses
  phases/outcomes into `SearchRequest.Categories/Outcomes` (chatmodel.go:180-191).
- `handleChat` (chat.go:41) then calls `store.SearchFlows(ctx, searchReq.Query,
  searchReq.Limit)` — **Categories/Outcomes/SessionID silently discarded**.
- `SearchFlows` (sqlite.go:326) has **no time-range parameter at all**, so the flagship
  "what did the agent do at 3am / in the last hour" question class is structurally impossible.
- Small local models (gemma3:4b) translate the question into generic keywords ("agent
  activity") that FTS5 can't match.
Direct-keyword chat works (`write_file` → 15 flows + real summary), so the plumbing is fine.

### 3. 🟠 DF-003 (P2) — "Authoritative" OpenAPI spec contradicts the live API
- Spec `Flow` schema: required `id, session_id, trace_ids` (snake_case). Live API: `ID,
  SessionID, TraceIDs` (Go PascalCase). Spec `ChatResponse`: `answer, flows, suggestions`;
  live: `Answer, Flows, Suggestions`. `Session` requires `metadata` — live responses omit it.
  Demo session ids aren't UUIDv7 (spec declares UUIDv7).
- docs/integration.md response examples match the spec fiction: my first jq on the
  documented example returned `null` for every field. Request-side docs (Query/Limit) are correct.

### 4. 🟠 DF-004 (P2) — stale port in `demo` output
`rabbit-hole demo` ends with "open http://localhost:8080/dashboard" — real bind is
127.0.0.1:9734 and the SPA is at `/dashboard/`.

### Also reproduced (already tracked on the board)
- GAP-006: `GET /dashboard` → 301 (SPA only at `/dashboard/`).

## The working recipe (use this for demos/dev)

```bash
make build
# Degraded/dogfood mode (no root):
RABBITHOLE_DB_PATH=/tmp/rh.db ./bin/rabbit-hole serve --demo-stream --no-ebpf
curl -s http://127.0.0.1:9734/health | jq
./bin/rabbit-hole demo --flows 50 --hours-back 48        # batch-seed historical data
./bin/rabbit-hole search "patch"                          # FTS works
./bin/rabbit-hole chat "write_file"                       # keyword chat works (real LLM if configured)
# Real LLM answers (Ollama):
RABBITHOLE_CHAT_MODEL_ENDPOINT=http://127.0.0.1:11434/v1 \
RABBITHOLE_CHAT_MODEL_NAME=gemma3:4b RABBITHOLE_CHAT_MODEL_API_KEY=x \
./bin/rabbit-hole serve --demo-stream --no-ebpf
# Watch live flows:
go run /tmp/dogfood-rabbit-hole/wsprobe.go "ws://127.0.0.1:9734/api/v1/ws/sessions/<demo-session-id>" 10
# Dashboard: http://127.0.0.1:9734/dashboard/   (trailing slash required)
```

## What a maintainer should fix FIRST (1 hour)
1. DF-001: wire attach into the daemon (or make attach persist via SessionManager) — without
   this the product's reason for existing doesn't run end-to-end.
2. DF-002: pass the translated filters + add a time range to the store query — one handler
   rewrite + one SQL change, and the flagship chat example starts working.
3. DF-003: emit snake_case json tags on Flow/ChatResponse so the spec becomes true.

## What a NEW user would need that isn't documented
- That `attach` currently does not persist sessions (until DF-001 lands).
- That `--demo-stream` is the only way to see data without root.
- That the WS stream only carries flows published *while connected* (no replay).
- That chat answers are only as good as the LLM's keyword translation; time-filtered
  questions don't work yet (DF-002).
- Wire format: responses mix snake_case (sessions, search envelope) and PascalCase (flows, chat).
