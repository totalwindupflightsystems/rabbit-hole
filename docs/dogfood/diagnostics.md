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

---

# Follow-up run 2026-08-27 — first real-LLM run; the time-window bug surfaces

## 9. What changed since 08-18 (all verified live this run)

- **DF-027 (daemon wedge) FIXED.** 15+ min soak with a heavier load than the
  08-18 wedge (demo-stream + real LLM + WS subscriber + search/chat loadgen
  + a second remote-backend daemon on :19734): RSS stayed ~27-29 MB, CPU
  ~0-1 %, `/health` instant, SIGQUIT clean exit (see §10 table).
- **DF-028 (session lifecycle) FIXED.** Attached `sleep 45` → `completed`
  ~1 s after exit; the "parent reaped first" race is handled by marking the
  session completed instead of leaving a ghost `running`.
- **DF-029 (compact 10m) FIXED.** `compact --before 10m` now works and
  deleted exactly the 15 seeded 24 h-old flows + 90 traces.
- **Real LLM path verified for keyword questions** (Ollama gpt-oss:20b):
  `chat "patch"` → 12 actions with success/failure marks and follow-up
  suggestions. This is the first run where the real model (not the stub)
  answered.

## 10. The time-window bug — how it's built, why it breaks, the right way

**How it's built:** the demo stream (and attach pipeline) write
`start_time` as Go `time.Now().Format(time.RFC3339Nano)` — i.e. **local
offset** (`-05:00` on this host). `QueryFlows` filters with
`AND start_time >= ?` binding `req.TimeRange.Start.Format(time.RFC3339Nano)`.
SQLite compares TEXT lexically. Two RFC3339Nano strings with *different
offsets do not compare correctly*: `'…T05:11:39.436-05:00'` (10:11 UTC)
sorts *before* `'…T08:11:26Z'` because '5' < '8'.

**The trap that hid it:** chat stub mode has a fallback — time-only stub
questions returning 0 flows fall back to "recent flows" (chat.go:61-70).
So the DF-002 acceptance probe (stub, "50 flows") passed while the filter
itself was never exercised. Real-model requests skip the fallback. This is
the premature-completion pattern from the research pack: the acceptance
probe tested the stub's fallback, not the store's filter.

**Repro ladder (user-level → store-level):**
1. `chat "What did the agent do in the last hour?"` (real model) → 0 flows.
2. `POST /api/v1/search` with `time_range.start` in `Z` → 0 flows.
3. Same window expressed with `-05:00` offset → flows returned.
4. `SELECT start_time FROM flows LIMIT 1` → shows the `-05:00` storage.

**The right way:** one of —
- write timestamps normalized to UTC (`time.Now().UTC().Format(RFC3339Nano)`)
  at every insert site (demo stream, attach pipeline, session manager); or
- compare numerically in SQL: `AND unixepoch(start_time) >= unixepoch(?)`
  (modernc sqlite supports `unixepoch()`); or
- store epoch seconds/nanos and format only at the API edge.
Plus a regression test that inserts mixed-offset rows and queries with a
UTC window. After the fix, the stub fallback can stay (it's honest UX) but
the acceptance probe must run against the real model AND the raw API, not
just the stub.

## 11. The LLM cold-start 500 — how it breaks, the right way

`RealChatModel.complete` runs with a 30 s HTTP client timeout. A cold local
model (first call after daemon start: process load + tokenization) exceeds
30 s → `translate: http call: … context deadline exceeded` → chat handler
500s → CLI prints `Error: chat: server returned 500`. Warm model: 200 in
~20-25 s per round-trip (translate + summarize = 18-50 s per question).

**The right way:** warm-up request at `serve` startup when a real model is
configured; make the timeout configurable (e.g. `RABBITHOLE_CHAT_TIMEOUT`)
or retry once on timeout; return a JSON error body that tells the user the
model timed out and to retry, instead of a bare 500.

## 12. Soak table (DF-027 acceptance probe, 2026-08-27)

Daemon: `serve --demo-stream --no-ebpf` + real chat model, plus loadgen
(WS subscriber + search/8s + chat/24s), plus second remote daemon :19734.
Baseline at 4:36 = 27,212 KB RSS. Sampled again at 5:51 = 28,096 KB.
No drift over the run; see integration report §5 for the end state.

## 13. History notes (updated)

- DF-027..DF-032 complete + judge-passed; this run re-verified all of them
  live. DF-033 (P3, double-store WARN) still open.
- New: DF-034 (P0 time-window UTC filter), DF-035 (P1 LLM cold-start 500),
  DF-036 (P3 flows-list doc drift), DF-037 (P3 chat latency/no-progress UX).
- The E2E battery still does not cover: real-model chat, mixed-offset
  timestamp queries, daemon soak — worth adding once DF-034 lands.
  (Update 09-05: DF-034 verified fixed in real use — UTC-window search returns
  flows. Real-model chat + soak still absent from the battery; the soak has now
  been done three times by dogfood runs, twice clean, once 08-18 wedge.)

# Follow-up run 2026-09-05 — first REAL-eBPF attempt; the Collect layer's
# kernel-level wall surfaces

## 14. How the Collect layer is built, why it cannot start on kernel 7.0, and the right way to diagnose it

**How it's built.** `internal/collector/` ships pre-compiled eBPF objects
(`bpf_x86_bpfel.o`, generated from `bpf/collector.bpf.c` via `go generate` with
clang). The C side defines, among others:

```c
struct {
    __uint(type, BPF_MAP_TYPE_STACK_TRACE);
    __uint(max_entries, 10240);
    __type(key, __u32);
} stack_map SEC(".maps");
```

`trace_enter_syscall` calls `bpf_get_stackid(ctx, &stack_map, BPF_F_USER_STACK)`
— every syscall event wants a user stack ID. The Go side loads all objects in
one `cilium/ebpf` Collection; any single map/program failure aborts the whole
load (all-or-nothing), which is why one bad map kills the entire collector.

**Why it breaks on kernel 7.0.** `load bpf objects: ... map stack_map:
map create: invalid argument` (EINVAL) — even as root with all 38 caps
(cap_bpf, cap_perfmon, cap_sys_admin, cap_sys_resource all present, verified
via `capsh --print`), with BTF present, clang 21 and matching headers
installed. The isolation probe settles the attribution: a standalone Go
program using the *same* `cilium/ebpf v0.17.3` cannot create **any**
`BPF_MAP_TYPE_STACK_TRACE` map on this kernel (tried the project's exact spec,
+ValueSize 4, and MaxEntries 256 — all EINVAL). `kernel.perf_event_paranoid=4`
is the host's most conspicuous suspect: stack-trace maps allocate perf events
internally, and paranoid=4 restricts exactly that layer. It may also be a
distributor patch or namespaced/perfserver restriction — but the probe proves
the failure belongs to the *kernel/map-type interaction*, not to the project's
objects and not to missing privileges.

**Why the user-facing message is wrong (DF-RABBIT-HOLE-6, P0).** `attach`
refuses with `requires CAP_SYS_RESOURCE and kernel 5.11+` — both demonstrably
satisfied — and the WARN hint sends the user to regenerate BPF objects, which
cannot help. The message is *assumed*, not *checked*: nothing inspects CapEff
or kernel version before printing it. A capable user follows the printed
advice, fails twice, and concludes the product is broken — the worst outcome
for the layer the README sells as the differentiator.

**The right way to diagnose this class** (worked here in ~10 minutes):
1. Read the verbatim error; note WHICH map failed and the errno.
2. Isolate: minimal program, same library, create ONLY that map type. If a
   bare spec fails, the cause is environmental (kernel), not the object file.
3. Vary one axis at a time: value_size, max_entries, then (if it passes)
   the program that references the map.
4. Check the knobs that govern that map type (for stack traces:
   perf_event_paranoid, unprivileged_bpf_disabled, CAP_PERFMON/CAP_BPF).
5. Only then consider `go generate` regeneration — and verify clang/kernel
   headers exist first.

**The right fix** (task text carries this): normalize/probe the stack_map spec
for modern kernels (consider making the user-stack capture optional at load
time so a stack_map failure degrades to flows-without-stacks instead of no
collector at all); gate the privilege/kernel message on an actual CapEff
check; surface the real errno; add a CI/e2e smoke that loads the collector on
a 7.x kernel.

## 15. The install leg (bunker) — where the documented path stops being a path

The README's Go toolchain section offers only root routes (`sudo apt-get`,
tarball→`/usr/local`). On a bare Debian user (the bunker agent): sudo demands a
password, /usr/local is root-owned → `make build` dies with `go: not found` at
the very first documented step. The degraded `--no-ebpf` mode the README
markets is *for* unprivileged users, yet unprivileged users cannot reach the
build. Right fix: document the user-space tarball install
(`mkdir -p ~/go && tar -C ~/go -xzf …; PATH=$HOME/go/bin:$PATH`), which was
verified working in the bunker.

Two further fresh-machine traps, both reproduced live:
- **GOTMPDIR:** Go compiles into `/tmp`; on a host whose /tmp is a small or
  shared tmpfs, `make build` fails with `no space left on device` while `df`
  shows gigabytes free. Document the GOTMPDIR/TMPDIR remedy.
- **DB ownership:** CLI maintenance commands (`compact`) against a
  daemon-owned data dir fail with raw SQLite `attempt to write a readonly
  database (8)` when run as a different user — the *normal* shape of a
  systemd self-host. Wrap SQLITE_READONLY into an ownership/permission
  message naming RABBITHOLE_DATA_DIR.

## 16. History notes (updated 09-05)

- 09-05: DF-001..037 all complete. This run re-verified DF-028 (session
  terminal state ≤60 s), DF-029 (`compact --before 10m`), DF-032
  (end_time non-null), and — first re-verification — DF-034 (UTC time-window
  search). Still open from 09-04: DF-RABBIT-HOLE-1 (test-all races/SLOs),
  -2 (memlock WARN noise), -3 (/health doc drift), -4 (non-window chat
  dead-end), -5 (pagination total) — -2/-4/-5 re-confirmed verbatim this run.
- New this run: DF-RABBIT-HOLE-6 (P0 eBPF stack-map EINVAL + wrong error),
  -7 (P1 root-only install docs), -8 (P1 compact SQLITE_READONLY UX),
  -9 (P3 GOTMPDIR docs).
- Soak: 18 min degraded daemon at 5.5 MB RSS / 0 % CPU — third clean soak
  since the 08-18 wedge.
