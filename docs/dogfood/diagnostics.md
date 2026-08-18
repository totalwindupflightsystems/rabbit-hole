# Rabbit-Hole — Diagnostics Trail (how it's built, what breaks, the right way)

*Dogfood run 2026-08-08. This is the "explain the machine" record: architecture as observed,
the failure modes found by real use, and the correct mental model for anyone (agent or human)
who lands here. Companion to `2026-08-08-integration.md`.*

## 1. How the binary is actually wired

```
cmd/rabbit-hole/
  attach.go   → collector.NewEBPFCollector → coll.Attach(pid) → print → EXIT   (no storage!)
  serve.go    → collector + classifier + store + attach.Pipeline + express.Server
                (pipeline goroutine runs forever; demo-stream is the only flow source)
  detach.go   → NEW collector → coll.Detach(id)   (fresh empty process-local session map!)
  list.go     → NEW collector → coll.List()       (same problem)
  demo.go     → demo.Generate → store (direct DB write, standalone)
  chat.go     → HTTP to running serve (POST /api/v1/chat)
```

Key facts that explain almost every failure:

- **Sessions live in a process-local map inside the collector.** `internal/collector` keeps
  `c.sessions` in memory only. Nothing in the CLI writes sessions to SQLite. The storage
  abstraction (`internal/attach/session.go` — `SessionManager`, `StoreSession`) is wired
  **only** inside `serve`'s pipeline, and nothing ever calls `StartSession` on it.
- **The attach→serve split is fictional.** The README workflow (`attach` then `serve`) implies
  attach hands data to the daemon. There is no handoff: no socket, no API, no shared DB
  writes. Each CLI invocation is a hermetically sealed process.
- **The chat path drops the search filters.** `handleChat` → `TranslateQuery` (LLM emits
  `{query, limit, phases, outcomes}` JSON) → parses phases/outcomes into the request →
  calls `SearchFlows(query, limit)` which ignores everything but text. The parse is dead code
  (chatmodel.go:180-191 → chat.go:41). `SearchFlows` (sqlite.go:326) is the only store search
  and has no time/category parameters; the richer structured filters exist only in the raw
  search HTTP handler (search.go passes them into a different path).
- **Wire format is inconsistent by construction.** Flow/ChatResponse use Go struct field
  names (no json tags → PascalCase). Sessions/search-envelope use explicit json tags
  (snake_case). The OpenAPI spec documents snake_case for everything → fiction for flows/chat.
- **eBPF preflight is honest (GAP-001, verified).** Unprivileged `attach`/`serve` hard-fail
  with `eBPF unavailable — telemetry DISABLED` and exit 1; `--no-ebpf`/`--demo-stream` are the
  explicit degraded opt-ins; `/health` reports `collector: eBPF degraded — telemetry DISABLED`.

## 2. Errors hit during the run (and their causes)

| Error | Cause | Resolution / correct behavior |
|---|---|---|
| `attach` WARN "remove memlock rlimit failed" then `Error: eBPF unavailable — telemetry DISABLED`, exit 1 | unprivileged host, preflight hard-fail (by design) | use `--no-ebpf` or `serve --demo-stream` |
| `detach <id>` → `Error: session ... not found`, exit 1, usage printed | session only existed in the attach process's memory; detach got a fresh collector | DF-001; no user workaround exists |
| `list` → "No active sessions." while DB has sessions | list reads collector memory, not the DB | same root cause |
| Chat NL question → canned "couldn't find any matching activity" | filters dropped + no time range + weak keyword translation | DF-002; use direct keywords (`write_file`, `sql`) until fixed |
| jq on integration.md example → null fields | docs show lowercase keys; API returns PascalCase | DF-003; query `Intent`, `Description`, `Outcome` |
| `search "sql"` → "No results found." | legitimately no matching text in demo flows (not a bug) | search `write`, `llm`, `patch` on demo data |
| `/dashboard` → 301 | SPA only mounted at `/dashboard/` | GAP-006 (open); use trailing slash |
| WS probe "repeated read on failed websocket connection" | client-side: gorilla/websocket forbids reads after a read-deadline timeout | server is fine; client must use ping/pong, not short read deadlines |
| demo output says `http://localhost:8080/dashboard` | stale hardcoded hint in demo.go | DF-004 |

## 3. The right way to use it today (until DF-001..004 land)

1. **Demo/dev (no root):** `serve --demo-stream --no-ebpf` + `demo --flows N` + API/CLI.
2. **Real telemetry (root, eBPF):** not possible end-to-end yet — attach doesn't persist
   (DF-001). Watch the board; the fix is in flight.
3. **Chat:** keyword queries work (`chat "write_file"`, `chat "sql"`); NL questions with
   time/category intent do not (DF-002). Configure `RABBITHOLE_CHAT_MODEL_ENDPOINT/NAME/API_KEY`
   (Ollama: `http://127.0.0.1:11434/v1`, any key) for real answers; without them the API sets
   `"stub": true` and the CLI prints a warning (by design, GAP-004).
4. **API consumers:** request bodies use wire names (`Query`, `Limit`, `SessionID`); flow
   responses are PascalCase (`ID`, `Intent`, `Description`...), sessions are snake_case.
   Validate against the live server, not the OpenAPI spec (DF-003).
5. **Data hygiene:** everything is env-configured (`RABBITHOLE_DB_PATH`, `RABBITHOLE_DATA_DIR`,
   `RABBITHOLE_LISTEN_ADDR`); SQLite WAL — data survives restarts; `compact --before <dur>`
   prunes old traces/flows.

## 4. History notes (from board/git)

- GAP-001..005 (eBPF honesty, README `--db` fiction, OpenAPI port + dashboard routes,
  chat stub surfacing, integration guide) were found by earlier gap-hunts and are **fixed and
  judge-passed**; this run re-verified them live.
- GAP-006 (`/dashboard` 301) still open.
- E2E-001 battery (tick #173) verified serve/API/CLI in hermetic degraded mode — but it never
  exercised the attach→persist→detach lifecycle, which is exactly where DF-001 lives.
- `specs/AGENTS.md` ("DexDat" spec-repo content) appears in some tooling's subdirectory
  context but does **not exist on disk and is not tracked** — matches the NEVER-DONE audit's
  "phantom injection" observation. Not a repo file; ignore it.

---

# Follow-up run 2026-08-18 — what changed, what broke, the right way now

*Second dogfood run, same unprivileged host, scratch DB. All DF-001..DF-026 are
now FIXED and were re-verified live; this section updates the mental model and
records the new failure modes (DF-027..DF-032). Companion: `2026-08-18-integration.md`.*

## 5. How the wiring changed since 08-08 (all verified live)

- **attach → daemon handoff is real now (DF-001).** `attach` POSTs to
  `POST /api/v1/sessions/attach` on the running `serve`; the daemon's
  SessionManager persists the session to SQLite. `list`/`status`/`search` read
  via the daemon HTTP API (DF-014), so CLI/server env differences no longer
  cause phantom data loss. Duplicate attach → `409 already attached` (verified;
  the existing session is untouched — 3 controlled retries).
- **Chat is a real two-stage pipeline now (DF-002).** `handleChat` → LLM
  translate → `QueryFlows` threading session/query/**time_range**/phases/
  outcomes → summarize. Verified: `chat "What did the agent do in the last
  hour?"` returns 50 flows in stub mode; `"stub": true` on the wire.
- **snake_case end-to-end (DF-003).** OpenAPI, docs, and the live API agree;
  `session_id` non-null on flows; demo session ids are UUIDv7.
- **Remote gRPC classify ships as a real subcommand (DF-023).**
  `classify-server --addr :50051 [--token T]` serves the classifier proto;
  `serve --remote host:port@T` uses it; `/health` reports
  `ok — remote gRPC <endpoint>`. Token auth verified.
- **Health and version tell the truth (DF-021/DF-022).** `/health` classifier =
  `degraded — pattern-only (model not loaded)` on zero-config runs; CLI
  `version` and `/health` version match (`v1.0.0-343-g059d830`).
- **Auth is opt-in and documented (DF-018/DF-020).** `RABBITHOLE_API_KEY` →
  `X-API-Key` on all routes except `/health` + `/metrics` (verified 401/200).

## 6. New failure modes (the 08-18 run)

| # | Symptom | Evidence | Cause hypothesis | Right way now |
|---|---|---|---|---|
| DF-027 P0 | Daemon wedge: HTTP dead, demo stream silent, 681% CPU, **19.7 GB RSS** in ~15 min; SIGQUIT didn't exit it (SIGKILL required) | `/proc/<pid>/status`: `VmRSS 19766380 kB`; all curls `000`; last log line 05:24:56 then silence; goroutine dump shows main goroutine `[chan receive, 12 minutes]` | Not root-caused (soak test needed). Suspects: remote-gRPC client retry loop, WS hub buffering for gone subscribers, SQLite WAL busy-spin. Second daemon (no demo-stream/remote) on same DB stayed healthy | Health-check the daemon (`curl -m 2 /health`) in any supervisor; capture `/proc/<pid>/status` + SIGQUIT dump before SIGKILL; don't trust a long-running demo-stream daemon yet |
| DF-028 P1 | Session lifecycle wrong in degraded mode: `sleep 30` exit → session stayed `running` 3+ min; another session labeled `crashed` 14s after attach while process alive (trigger unreproduced) | DB ground truth via sqlite3 + API polls | No process-exit monitor in the no-ebpf attach path (eBPF events are the only exit signal?), plus an unidentified early-crash path | Verify session states via `list --all`; treat `running`/`crashed` as approximate until DF-028 lands |
| DF-029 P2 | `compact --before 10m` → `unsupported duration suffix: m (use h or d)` | direct repro, exit 1; `1h` works | parser whitelist h/d only; docs never show m | use h/d; filed |
| DF-030..032 P3 | `demo` Next-hint stale port; attach message names CLI's DB path not daemon's; demo sessions `completed` with `end_time=null` | live output + sqlite3 | cosmetic | ignore or track via board |

## 7. The right way to use it today (2026-08-18)

1. **Demo/dev (no root):** `make build` → `serve --no-ebpf --demo-stream --addr :19734`
   → `attach/status/list/chat/search --addr :19734` → API/WS probes. Keep an eye
   on daemon RSS (DF-027).
2. **Real telemetry (root, eBPF):** the plumbing now exists (attach → daemon →
   DB) but was only exercised here in `--no-ebpf` degraded mode; the eBPF path
   needs a privileged host to confirm. Preflight honesty verified (GAP-001).
3. **Fleet setup:** `classify-server --addr :50051 --token T` on the backend,
   `serve --remote host:port@T` on edges; `/health` confirms the link.
4. **API consumers:** snake_case everywhere; spec == live wire format (DF-003);
   auth via `X-API-Key` when `RABBITHOLE_API_KEY` is set; WS is live-only.
5. **Data hygiene:** SQLite WAL survived even SIGKILL (integrity_check ok);
   `compact` is destructive and h/d-only; `demo` + `serve` on one DB is safe
   (idempotent migrations, DF-009).

## 8. History notes (updated)

- DF-001..DF-026 all complete + judge-passed; this run re-verified 20+ of them
  live (see integration report §1 table).
- Open board: E2E-001 (recurring battery), NEVER-DONE (audit sweep),
  REL-003 (external dev verification — human-gated), plus DF-027..DF-032 from
  this run. The E2E battery has never covered session-lifecycle transitions or
  daemon soak — both are now board tasks (DF-027/DF-028).
