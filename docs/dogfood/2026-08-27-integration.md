# Rabbit-Hole Dogfood Integration Report — 2026-08-27

**Verdict: 🟡 PROMISING-BUT-ROUGH** (held from 08-18)
**Run:** ~75 min of real use, unprivileged host (degraded `--no-ebpf` mode —
the README's primary no-root path), Go 1.26.5, scratch DB
`RABBITHOLE_DB_PATH=/tmp/dogfood-rh/rh.db`, daemon on the default
`127.0.0.1:9734`.
**New since 08-18:** this run is the first with a **real LLM** (Ollama
`gpt-oss:20b`, `RABBITHOLE_CHAT_MODEL_*` env vars) instead of the built-in
stub. That exposed the time-window search bug the stub had been masking
(§2, DF-034) and the cold-start 500 (§3, DF-035). All previously-filed
findings DF-027..DF-032 were re-verified fixed (§4).

---

## 1. The journey that works end-to-end (re-verified live today)

Everything below executed against a scratch DB, unprivileged, no root.

| Step | Command | Result |
|---|---|---|
| Build | `make build` | ✅ 20 MB binary, real version identity (`v1.0.0-465-g8551f3a`, commit, build time) |
| Daemon | `serve --demo-stream --no-ebpf` (+ `RABBITHOLE_CHAT_MODEL_*`) | ✅ honest degraded warnings; `real chat model enabled` logged |
| Health | `GET /health` | ✅ 4 components truthful; `degraded — pattern-only` classifier, `eBPF degraded — telemetry DISABLED` |
| Attach | `attach --pid <sleep 45> --no-ebpf` | ✅ session handed to daemon, persisted (`POST /api/v1/sessions/attach` → 201) |
| Attach | `attach --pid <serve daemon>` | ✅ second concurrent session fine; duplicate-PID guard documented |
| List/status | `list --all`, `status` | ✅ 3 sessions (2 active + demo stream), flows count, DB size |
| Lifecycle | sleep exits → session status | ✅ **DF-028 FIXED**: `completed` ~1s after process exit ("parent reaped first" WARN handled gracefully) |
| Search | `search "write"` | ✅ 4 results (GAP-011 quickstart promise holds) |
| Chat (keyword) | `chat "patch"` (real LLM, warm) | ✅ 12 actions, success/failure marks, follow-up suggestions |
| Chat (NL time) | `chat "What did the agent do in the last hour?"` | ❌ **"I couldn't find any matching activity"** — see §2 (DF-034) |
| Chat API | `POST /api/v1/chat` | ✅ snake_case, `flows` array, no `stub` flag when real model |
| Sessions API | `GET /api/v1/sessions` | ✅ `{limit, offset, sessions, total}` snake_case |
| Search API | `POST /api/v1/search` | ⚠️ works for keyword; time_range broken for UTC windows (§2) |
| WS stream | `/api/v1/ws/sessions/{id}` | ✅ 5 messages / 12 s (3 s demo cadence), ping/pong fine |
| Demo seed | `demo --flows 15 --hours-back 24` | ✅ 15 flows across 24 h |
| Compact | `compact --before 10m` | ✅ **DF-029 FIXED**: `m` suffix accepted; deleted exactly the 15 seeded old flows + 90 traces, kept recent |
| Remote classify | `classify-server --addr :50051 --token s3cret` + `serve --remote localhost:50051@s3cret` | ✅ **DF-023 holds**: `/health` → `ok — remote gRPC localhost:50051` |
| Dashboard | `GET /dashboard` | ✅ 200 (GAP-006 holds) |
| Metrics/stats | `GET /api/v1/metrics`, `/api/v1/stats` | ✅ 200 |
| CLI errors | daemon down, all commands | ✅ exit 1 + `cannot reach Rabbit-Hole daemon (is 'rabbit-hole serve' running?)` (GAP-010 holds) |

## 2. P0 — time-window search is broken for UTC timestamps (the core promise)

**User-visible:** `rabbit-hole chat "What did the agent do in the last hour?"`
with a real LLM → *"I couldn't find any matching activity for your query."*,
`flows: 0`, while 70+ flows from the demo stream are live in the last hour.

**Root cause (verified, not guessed):**
1. Flows are stored with **local-offset** timestamps:
   `2026-08-27T05:11:39.436655825-05:00` (demo stream inserts `time.Now()`
   formatted RFC3339Nano).
2. `internal/storage/sqlite.go QueryFlows` filters with
   `AND start_time >= ?` where the bound arg is
   `req.TimeRange.Start.Format(time.RFC3339Nano)` — **lexical TEXT
   comparison** in SQLite.
3. The chat translate system prompt (`chatmodel.go`) tells the LLM
   *"compute concrete RFC3339 UTC timestamps"* and injects
   `Current time (UTC)`. So the real model emits `2026-08-27T08:11:26Z`
   — and `'…T05:11:39.436-05:00' >= '…T08:11:26Z'` is **false** by string
   order, even though 05:11-05:00 = 10:11 UTC is *after* 08:11 UTC.

**Repro (direct API, no LLM involved):**
```
POST /api/v1/search {"query":"","limit":5,"time_range":{"start":"2026-08-27T08:11:26Z"}}
→ total: 0        # UTC window: WRONG (10+ flows exist in window)

POST /api/v1/search {"query":"","limit":5,"time_range":{"start":"2026-08-27T03:00:00-05:00"}}
→ total: 5        # same instant, local offset: works
```

**Why 08-18 looked fine:** the stub path has a fallback
(`chat.go:61-70`: stub + time-only + 0 flows → return recent flows), so
stub-mode time questions "worked" without ever exercising the filter.
Real-model requests skip the fallback — the bug surfaced the first time a
real LLM was used. Classic premature-completion trap: the acceptance probe
only ever ran in stub mode.

**Fix direction (for the foreman):** normalize on write — store all
timestamps as UTC RFC3339Nano (`Z`) or epoch, OR compare in SQL with
`julianday()`/`unixepoch()` conversions; add a regression test that seeds
mixed-offset timestamps and queries with a UTC window. This is P0 because
"what did the agent do at 3am?" is the project's headline promise
(README, S04).

## 3. P1 — real-LLM cold start → 30 s timeout → 500

**User-visible:** first `chat "patch"` after daemon start:
```
Error: chat: server returned 500
```
and the daemon log:
```
level=ERROR msg="query translation failed" err="translate: http call: Post
"http://127.0.0.1:11434/v1/chat/completions": context deadline exceeded
(Client.Timeout exceeded while awaiting headers)" message=patch
```
The HTTP client timeout is 30 s; a cold local CPU model (gpt-oss:20b first
load + tokenization) exceeds it. Retry after warm-up: 200, 12 actions, 50 s
for two round-trips (translate + summarize ≈ 20-25 s each). So the feature
works, but the first query after startup reliably 500s on local models, with
no guidance to the user ("try again" vs "model not responding").

**Fix direction:** longer/configurable translate timeout; retry-once on
timeout; warm-up call at daemon start when a model is configured; friendlier
500 body. Consider caching the translate step for repeated queries.

## 4. Previously-filed findings — re-verification results

| ID | Claim | Today |
|---|---|---|
| DF-027 (P0, daemon wedge 19.7 GB/681 %) | soak fix | ✅ 15+ min demo-stream + WS subscriber + chat/search load + remote daemon: **28 MB RSS, ~0-1 % CPU, /health instant** (§5). SIGQUIT exit test: see §5 |
| DF-028 (P1, session lifecycle) | exit monitor | ✅ `completed` ≤1 s after process exit; "parent reaped first" handled |
| DF-029 (P2, compact 10m) | m/s suffixes | ✅ `compact --before 10m` accepted, correct deletion set |
| DF-030 (P3, demo hint addr) | fixed | ✅ (not re-spotted) |
| DF-031 (P3, attach wrong DB path msg) | fixed | ✅ attach message now says "persisted via daemon" |
| DF-032 (P3, end_time NULL) | fixed | ✅ demo sessions carry end_time |

## 5. Soak detail (DF-027 acceptance probe)

Daemon `serve --demo-stream --no-ebpf` + real chat model + a second
remote-backend daemon (`:19734`, `--remote localhost:50051@s3cret`) +
sustained load generator (WS subscriber + search every 8 s + chat every
24 s) + 2 attach sessions. Sampled RSS: 27 MB @ 4:36 → 28 MB @ 5:51 → see
the 08-27 diagnostics §10 for the full table to 15+ min. The 08-18 wedge
(681 % CPU / 19.7 GB RSS, HTTP dead, SIGQUIT useless) did **not** reproduce.

## 6. Friction notes (new-user experience)

- `chat` with a local CPU model is slow (18-50 s/question) — the CLI gives
  no progress indication; users may think it hung. Suggest a spinner or a
  "thinking…" line.
- `/api/v1/flows?limit=2` → 404; only `/api/v1/flows/{id}` exists. The
  usage skill's endpoint table lists `flows` ambiguously — doc drift, no
  data-loss issue (P3).
- `chat` failure (500) prints only `Error: chat: server returned 500` —
  no hint to check the daemon log or retry (P3, part of DF-035).

## 7. Verdict

🟡 **PROMISING-BUT-ROUGH.** The shell is real and getting more solid:
every 08-18 finding is fixed, the daemon no longer wedges, lifecycle is
honest, compact/remote/WS all verified. But the **headline interaction —
natural-language time questions — is broken the moment a real LLM is used**
(DF-034), and the first LLM query after startup 500s on local models
(DF-035). The project cannot claim its core promise until DF-034 lands;
both fixes are small and well-understood.
