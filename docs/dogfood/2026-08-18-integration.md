# Rabbit-Hole Dogfood Integration Report — 2026-08-18

**Verdict: 🟡 PROMISING-BUT-ROUGH** (up from 🔴 on 2026-08-08)
**Run:** ~55 min of real use, unprivileged host (no eBPF — degraded mode, the
README's primary no-root path), Go 1.26.5, scratch DB in `/tmp/dogfood-rabbit-hole/`.
**What was verified:** every previously-filed finding (DF-001..DF-026) was
re-checked live, plus the full user journey below. **One new P0 found:** the
daemon wedges under sustained documented use (details in §4).

---

## 1. The journey that now works end-to-end

Everything below was executed live against a scratch DB
(`RABBITHOLE_DB_PATH=/tmp/dogfood-rabbit-hole/rh.db`), daemon on a custom port
(`--addr 127.0.0.1:19734`).

| Step | Command | Result |
|---|---|---|
| Build | `make build` | ✅ 20MB binary, version injected (`v1.0.0-343-g059d830`, commit, build time) |
| Daemon | `serve --no-ebpf --demo-stream --addr 127.0.0.1:19734` | ✅ starts, honest degraded warnings |
| Health | `curl /health` | ✅ 4 components; classifier truthfully `degraded — pattern-only (model not loaded)`; collector `eBPF degraded — telemetry DISABLED` |
| Status | `rabbit-hole status --addr …` | ✅ reads via daemon (DF-014) |
| Attach | `attach --pid <pid> --no-ebpf --addr …` | ✅ hands session to daemon; visible in `list --all`, `status`, `GET /api/v1/sessions` across invocations (DF-001) |
| Dup attach | same PID again | ✅ `Error: already attached to process <pid>`, exit 1, existing session untouched |
| Detach | `detach <id> --addr …` | ✅ exit 0, session completed with end_time |
| Search | `search "patch"` | ✅ 6 results; `search "sql error"` → "No results found." (README note explains demo vocabulary — DF-026) |
| NL chat | `chat "What did the agent do in the last hour?"` | ✅ **50 flows returned** with stub warning on stderr (DF-002 + GAP-004) |
| Chat API | `POST /api/v1/chat {"message": …}` | ✅ `"stub": true` (honest), 50 flows |
| Sessions API | `GET /api/v1/sessions` | ✅ snake_case (`id`, `agent_name`, `agent_pid`, `status`, `start_time`, `end_time`, `metadata`) |
| Search API | `POST /api/v1/search {"query":"write","limit":3}` | ✅ snake_case flows; `session_id` non-null (DF-003) |
| Stats | `GET /api/v1/stats` | ✅ db_path/listen_addr/session+flow counts |
| Dashboard | `GET /dashboard` → **200**; `GET /dashboard/` → SPA; `GET /api/v1/dashboard/summary` | ✅ (GAP-006) |
| Metrics | `GET /metrics` | ✅ Prometheus text |
| Remote classify | `classify-server --addr 127.0.0.1:50051 --token dogfood-secret` + `serve --remote 127.0.0.1:50051@dogfood-secret` | ✅ `/health` → `classifier: ok — remote gRPC 127.0.0.1:50051` (DF-023) |
| WebSocket | Go client → `ws://…/api/v1/ws/sessions/<id>` | ✅ 5 flow messages in 10s, snake_case JSON, correct session_id |
| Auth | `RABBITHOLE_API_KEY` + `X-API-Key` | ✅ `/health`+`/metrics` public; API 401 without key, 200 with (DF-018/DF-020) |
| Seeding | `demo --flows 20 --hours-back 24 --spread` while daemon live on same DB | ✅ 20 flows across 24h, no migration race (DF-009/DF-019) |
| Compact | `compact --before 1h` on a DB copy | ✅ "Compact complete." (destructive — use a copy) |
| Restart | SIGKILL daemon, restart on same DB | ✅ 13 sessions / 283 flows intact, `PRAGMA integrity_check = ok` (WAL) |
| Tests | `go test ./... -count=1 -short` | ✅ ~6s total (DF-013) |

**Time-to-first-success: ~2 minutes** (build + serve + first `/health`).
**Friction count: 6** (listed in §3).

## 2. The working reference setup (unprivileged host)

```bash
make build
mkdir -p /tmp/rh && export RABBITHOLE_DB_PATH=/tmp/rh/rh.db
./bin/rabbit-hole serve --no-ebpf --demo-stream --addr 127.0.0.1:19734 &   # daemon
./bin/rabbit-hole status --addr 127.0.0.1:19734
./bin/rabbit-hole attach --pid $SOME_AGENT_PID --no-ebpf --addr 127.0.0.1:19734
./bin/rabbit-hole list --all --addr 127.0.0.1:19734
./bin/rabbit-hole chat "What did the agent do in the last hour?" --addr 127.0.0.1:19734
./bin/rabbit-hole search "patch" --addr 127.0.0.1:19734
# remote classifier on a backend host:
./bin/rabbit-hole classify-server --addr :50051 --token s3cret
./bin/rabbit-hole serve --no-ebpf --demo-stream --addr 127.0.0.1:19734 --remote localhost:50051@s3cret
# WS live stream (any language — this is the gorilla/websocket pattern):
#   ws://127.0.0.1:19734/api/v1/ws/sessions/<session-id>  → JSON Flow objects as published
```

Real LLM chat: set `RABBITHOLE_CHAT_MODEL_ENDPOINT` (+`_NAME`, `_API_KEY`); without
them the stub answers honestly (`"stub": true`, CLI stderr warning).

## 3. Errors hit & friction (all now filed on the board)

1. **DF-027 (P0) — daemon wedge.** After ~15 min of the documented usage
   (demo-stream + remote gRPC + attach/detach cycles + one WS client + one
   `demo --spread` seed), the daemon went to 681% CPU / **19.7 GB RSS**, all HTTP
   timed out, the demo stream went silent, and `kill -QUIT` (goroutine dump) did
   NOT terminate it — only SIGKILL worked. Data survived (WAL). The identical
   second daemon without demo-stream/remote stayed healthy. **Watch your daemon:
   `curl -m 2 /health` in a cron.** Board task with full evidence.
2. **DF-028 (P1) — session lifecycle.** Attached `sleep 30` exited normally; its
   session stayed `running` 3+ minutes later (no transition). Another session was
   labeled `crashed` 14s after attach while the process was alive (trigger never
   reproduced; all controlled retries clean). Status is the product for a
   legibility tool — treat lifecycle as a first-class bug.
3. **DF-029 (P2) — `compact --before 10m` rejected**: `unsupported duration
   suffix: m (use h or d)`. Minutes are the natural unit; only h/d documented.
4. **DF-030 (P3)** — `demo`'s "Next: open http://localhost:9734/dashboard" hint
   is stale when the daemon runs on a custom port (demo has no `--addr`).
5. **DF-031 (P3)** — `attach` prints "Session persisted to ~/.rabbit-hole/…"
   (the CLI's own config) even when the daemon's DB is elsewhere — misleading
   path in mixed-env setups.
6. **DF-032 (P3)** — demo sessions have `status=completed` with `end_time=null`.

## 4. The wedge, in detail (for the foreman)

- **Timeline:** daemon started 05:14:28 (`--no-ebpf --demo-stream --remote`).
  Normal operation 05:14:28→05:24:56 (last log line: `demo stream: flow
  published intent=search_web`). First timeouts observed 05:25:21. SIGQUIT at
  ~05:25:50 produced a goroutine dump whose head shows `goroutine 0 … [idle]`
  in `runtime.retake` (sysmon) and `goroutine 1 … [chan receive, 12 minutes]`
  (main goroutine parked on a channel). Process never exited; SIGKILL at
  ~05:26:30.
- **Evidence captured:** `VmRSS: 19766380 kB`, `Threads: 22` (681% CPU per ps).
  `/health`, `/api/v1/sessions`, `/api/v1/stats` all `000` (5s timeouts).
- **Environment at the time:** same-host `classify-server` (0% CPU — idle),
  second daemon on `:19735` with `RABBITHOLE_API_KEY` (no demo-stream, no
  remote) sharing the same SQLite DB — healthy, only starved by CPU saturation.
- **Suspects to bisect (not confirmed):** remote gRPC client retry loop;
  WS hub buffering for a disconnected subscriber; SQLite WAL busy-spin under
  concurrent writers. A 30-min soak test with RSS/CPU assertions is the fix
  vehicle (see DF-027 PASS criteria).

## 5. What to tell the maintainer first (1 hour of their time)

1. Soak-test the demo-stream daemon (with remote + WS + attach) for 30+ min and
   kill the wedge — it silently stops the collection your product promises.
2. Make session lifecycle honest (exit detection + correct labels) — DF-028.
3. Accept `m` in compact durations — DF-029. Everything else is polish.

## 6. Notes for future dogfooders

- Use a **fresh scratch DB per run**; `demo` + `serve` + `attach` on one DB is
  fine (migration is idempotent now), but `compact` is destructive — copy first.
- The **demo vocabulary** covers llm/patch/memory/network intents only; search
  for those, not "sql error".
- **WS is live-only** (no replay); connect while a demo stream is publishing.
- The stack dump on SIGQUIT is your best wedge diagnostic — but a wedged daemon
  may not even die on SIGQUIT; capture `/proc/<pid>/status` before SIGKILL.
