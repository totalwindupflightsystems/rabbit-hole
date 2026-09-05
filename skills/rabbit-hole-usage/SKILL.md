---
name: rabbit-hole-usage
description: >-
  How to actually USE the rabbit-hole project (agent legibility: collect → classify →
  express). Entry points, run recipes, current state, and the working demo/API patterns.
  Load this before touching the repo.
version: 1.1.0
---

# Rabbit-Hole Usage Skill

Rabbit-Hole is a Go binary that watches AI agents and explains their activity: eBPF collect →
pattern/ML classify → HTTP/WebSocket + chat express. Self-hosted, one binary, zero SDK.

**Current state (2026-09-05):** DF-001..DF-037 all complete; 09-04 findings
DF-RABBIT-HOLE-1..5 pending on the board. Verified fixed live: DF-028
(session terminal ≤60 s), DF-029 (`compact --before 10m`), DF-032
(end_time non-null), and DF-034's raw-API leg (UTC-window search returns
flows — first re-verification). 18-min soak: 5.5 MB RSS, 0 % CPU.
**NEW hazard (dogfood 2026-09-05 — first REAL-eBPF attempt, DF-RABBIT-HOLE-6
P0): the Collect layer cannot start even WITH full privileges on kernel
7.0.0-29.** `map stack_map: map create: invalid argument` as root (all caps
present, BTF present); an isolation probe shows THIS KERNEL rejects ANY
`BPF_MAP_TYPE_STACK_TRACE` map (suspect: `kernel.perf_event_paranoid=4`).
Worse, `attach` misattributes the failure: "requires CAP_SYS_RESOURCE and
kernel 5.11+" — both satisfied on the test host — and the regen-objects hint
cannot help. **Do not burn time on privileges or `go generate` for this;
see diagnostics.md §14 for the probe method.** Full report:
`docs/dogfood/2026-09-05-integration.md`.

## Entry points

| Entry | What it is | Works? |
|---|---|---|
| `./bin/rabbit-hole serve --demo-stream --no-ebpf` | full pipeline daemon, demo flows every 3s, HTTP+WS on 127.0.0.1:9734 | ✅ (no root needed) |
| `./bin/rabbit-hole serve` | same, with eBPF (needs CAP_SYS_RESOURCE + kernel 5.11+) | ❌ on kernel 7.0: stack_map EINVAL even as root (DF-RABBIT-HOLE-6); degrades to pattern-only |
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
   (verified). Point it at a copy first if unsure. `--before 10m` now works (DF-029 fixed).
   **Run compact as the daemon's user:** against a daemon-owned data dir it fails with raw
   SQLite `attempt to write a readonly database (8)` when run as a different user — the
   normal systemd self-host shape (DF-RABBIT-HOLE-8).
8. **Data dir:** default `~/.rabbit-hole/rabbit-hole.db`; use `RABBITHOLE_DB_PATH` for
   hermetic runs. SQLite WAL — data survives restarts and even SIGKILL (verified).
9. **Daemon wedge (DF-027): FIXED + re-verified (08-27, 09-05: 18 min, 5.5 MB RSS,
   0 % CPU).** Still health-check long runs (`curl -m 2 /health`) — the 08-18 wedge was
   silent.
10. **Session lifecycle in degraded mode (DF-028): FIXED + re-verified 09-05** (natural
    exit → `completed` with real end_time well under 60 s). Demo sessions now complete
    with non-null end_time (DF-032).
11. **Remote backend:** `classify-server --addr :50051 [--token T]` + client side
     `serve --remote host:port@T`; `/health` then shows `ok — remote gRPC <endpoint>`.
12. **eBPF on kernel 7.x is blocked at the map layer (DF-RABBIT-HOLE-6, P0):** stack-trace
    map creation EINVALs even as root with all caps. Ignore the printed privilege/regen
    advice; confirm with the isolation probe in diagnostics.md §14 before debugging deeper.
13. **Fresh-machine install:** README's Go install steps are root-only (sudo apt / tarball
    → /usr/local). Unprivileged: user-space tarball into $HOME works (verified in bunker).
    If `make build` dies with `no space left on device` while df shows space, /tmp is a
    small tmpfs — set `GOTMPDIR`/`TMPDIR` to a disk-backed dir (DF-RABBIT-HOLE-7/-9).
14. **WS route is session-scoped:** `/api/v1/ws/sessions/{id}` (HTTP 101). There is no
    global `/api/v1/ws` — probing it 404s and nothing advertises the real path.

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
- **DF-034 (fixed; raw-API leg re-verified 09-05):** `POST /api/v1/search` with
  `time_range.start` in UTC (`Z`) now returns the flows. Real-model chat leg (LLM emits
  UTC windows) still unproven end-to-end since the fix — probe it if a model is handy.
- **DF-035 (P1, open):** fresh `serve` with real chat model → first `chat` must not 500
  (warm-up/retry/timeout).
