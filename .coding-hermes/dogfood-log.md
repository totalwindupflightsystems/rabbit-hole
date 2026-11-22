# Dogfood Log

## 2026-08-08 — rabbit-hole — 🔴 DOES-NOT-DELIVER (core Collect promise) / shell is real

**Promise:** *"A user can attach to a real agent process (zero SDK, eBPF), have Rabbit-Hole
collect kernel-level telemetry, classify it, and ask 'what did the agent do at 3am?' in plain
language — self-hosted, one binary."*

**Reality:** Express/Storage layers genuinely work (demo stream, FTS search, dashboard SPA,
metrics, WebSocket live flows, stub+real chat, persistence across restarts, honest eBPF
preflight — all verified live). But the Collect layer is unreachable: `attach` prints success
and exits, persisting nothing (session in no DB; `detach` → "session not found"); `serve` has
no way to start a session; the demo stream is the only flow source in the binary. Chat cannot
answer time-based NL questions (filters dropped + no time range in store). OpenAPI spec
contradicts the live wire format for Flow/ChatResponse.

**Time-to-first-success (demo mode):** ~3 min. **Friction count:** 8 (attach theater, detach
failure, NL chat failure x3, jq nulls from docs, stale :8080 hint, /dashboard 301).

**Top 3 findings (board tasks):**
1. DF-001 (P0) — attach/list/detach session lifecycle is per-process ephemeral; real telemetry
   can never reach the DB.
2. DF-002 (P1) — chat drops translated filters, no time range → "what did the agent do in the
   last hour?" returns canned no-match while 140+ flows are live.
3. DF-003 (P2) — OpenAPI "authoritative" spec + integration.md show lowercase flow fields;
   API returns PascalCase → spec-following consumers get nulls.

**Also:** DF-004 (stale :8080 in demo output); GAP-006 (/dashboard 301) re-confirmed open.

**Left behind:** docs/dogfood/2026-08-08-integration.md, docs/dogfood/diagnostics.md,
skills/rabbit-hole-usage/SKILL.md, 4 board tasks (DF-001..004). Foreman not paused (cooldown
7200s) — will pick up tasks next tick.

## 2026-08-18 — rabbit-hole — 🟡 PROMISING-BUT-ROUGH (was 🔴 on 08-08)

**Promise:** *"A user can attach to a real agent process (zero SDK, eBPF), have Rabbit-Hole
collect kernel-level telemetry, classify it, and ask 'what did the agent do at 3am?' in plain
language — self-hosted, one binary."*

**Reality:** The promise now HOLDS in degraded mode (unprivileged host). Every DF-001..DF-026
fix re-verified live: attach→daemon persistence + list/status/detach, NL chat with time
window (50 flows, honest stub), snake_case wire format, remote gRPC classify-server with
token auth, truthful /health + real version identity, opt-in API-key auth, WS live stream
(5 msgs/10s), dashboard 200, compact, demo --spread, persistence + SIGKILL survival (WAL,
integrity ok), tests 6s. **But:** the serve daemon wedged after ~15 min of documented use
(681% CPU / 19.7 GB RSS, HTTP dead, SIGQUIT useless → SIGKILL) — a P0 for a 24/7 watcher.

**Time-to-first-success:** ~2 min. **Friction count:** 6.

**Top 3 findings (board tasks):**
1. DF-027 (P0) — daemon wedge under demo-stream+remote+WS+attach use; silent collection
   stop; needs soak test + bounded RSS.
2. DF-028 (P1) — session lifecycle unreliable in --no-ebpf mode (ghost "running" after
   process exit; one false "crashed").
3. DF-029 (P2) — compact rejects `10m` ("use h or d") — natural unit missing.

**Also:** DF-030/031/032 (P3) — stale demo hint, attach message names wrong DB path,
completed sessions with end_time=null.

**Left behind:** docs/dogfood/2026-08-18-integration.md, diagnostics.md §5-8 updated,
skills/rabbit-hole-usage/SKILL.md refreshed (state 08-18 + pitfalls 9-11 + new probes),
6 board tasks (DF-027..032). Foreman cooldown 21600s ≥ 14400 → woken to 900s to work DF-027.

## 2026-08-27 — rabbit-hole — 🟡 PROMISING-BUT-ROUGH (first real-LLM run; DF-027..032 all fixed)

**Promise:** *"A user can attach to a real agent process (zero SDK, eBPF), have Rabbit-Hole
collect kernel-level telemetry, classify it, and ask 'what did the agent do at 3am?' in plain
language — self-hosted, one binary."*

**Reality:** Every finding from the 08-18 run re-verified FIXED in real use: DF-027 daemon
wedge (15+ min soak with heavier load: 28 MB RSS, 0-1% CPU, /health instant — vs 19.7 GB
before), DF-028 session lifecycle (sleep 45 → completed ~1 s after exit), DF-029
`compact --before 10m`, DF-030/031/032. Remote classify-server + token auth, WS live
stream, real-LLM keyword chat (12 actions w/ success marks), search "write", dashboard,
metrics, exit codes — all verified. **BUT the first run with a REAL LLM (Ollama
gpt-oss:20b) exposed the core-promise bug the stub had been masking:** time-window
questions return 0 flows because flows are stored with local-offset timestamps while
filters compare TEXT lexically against UTC ("Z") timestamps (which the translate prompt
instructs the LLM to emit). `chat "What did the agent did in the last hour?"` → "no
matching activity" with 70+ live flows. Also: first LLM chat after daemon start 500s
(30 s client timeout < cold-model latency).

**Time-to-first-success:** ~2 min (daemon+search). **Friction count:** 4 (NL time chat
broken, cold-start 500, 18-50 s/questions with no progress UI, /api/v1/flows doc drift).

**Top 3 findings (board tasks):**
1. DF-034 (P0) — time-window search/chat broken for UTC timestamps; core "at 3am" promise
   fails with a real model (stub fallback masked it; repro at raw API level).
2. DF-035 (P1) — first real-LLM chat after daemon start → 30 s timeout → HTTP 500, no
   guidance; retry after warm-up works.
3. DF-036 (P3) — docs list a /api/v1/flows list endpoint that doesn't exist (404).

**Also:** DF-037 (P3) — chat CLI silent for 18-50 s per question.

**Left behind:** docs/dogfood/2026-08-27-integration.md (full journey + repros + soak
table), docs/dogfood/diagnostics.md §9-13 (how it's built, why it breaks, right way),
skills/rabbit-hole-usage/SKILL.md refreshed (state 08-27, new hazards + probes),
4 board tasks (DF-034..037). Foreman cooldown 21600s ≥ 14400 → woken to 900s to work DF-034/035.
2026-09-04 | PROMISING-BUT-ROUGH | 7s t2fs | friction 5 | 5 findings

## 2026-09-05 — rabbit-hole — 🟡 PROMISING-BUT-ROUGH (first REAL-eBPF attempt; Collect layer hits a kernel wall)

**Promise:** *"A user can attach to a real agent process with zero SDK, and Rabbit-Hole
collects kernel-level (eBPF) telemetry, classifies it, and answers 'what did the agent
do?' — self-hosted, one binary."*

**Reality:** Every prior run (08-08→09-04) tested only `--no-ebpf`/demo degraded mode.
Today the host had root + all caps + kernel 7.0.0-29 + BTF, so the headline Collect layer
was exercised for the first time — and it cannot start: `map stack_map: map create:
invalid argument` even as root. An isolation probe (standalone cilium/ebpf program)
proved THIS KERNEL rejects ANY BPF_MAP_TYPE_STACK_TRACE map (suspect
perf_event_paranoid=4), and `attach` misattributes the failure ("requires CAP_SYS_RESOURCE
and kernel 5.11+" — both satisfied; hint to regen objects can't help). Meanwhile Express/
Storage remain solid: DF-034's UTC-window search verified FIXED in real use (first
re-verification), DF-028/029/032 re-verified, 18-min soak clean (5.5 MB RSS). Bunker
install leg: documented path is root-only for Go install (new P1), clone impossible
without credentials (recorded, access NOT widened), smoke passed after workarounds;
agent destroyed cleanly.

**Time-to-first-success:** ~25 s (build+serve+health). **Friction count:** 6 (misleading
eBPF error, useless regen hint, compact errno, root-only install docs, GOTMPDIR trap,
undocumented session-scoped WS path).

**Top 3 findings (board tasks):**
1. DF-RABBIT-HOLE-6 (P0) — eBPF Collect cannot start even WITH full privileges on
   kernel 7.0 (stack_map EINVAL at the map layer); error message misattributes cause.
2. DF-RABBIT-HOLE-7 (P1) — README's Go toolchain install is root-only; unprivileged
   users (the degraded-mode audience) dead at step one of the documented install.
3. DF-RABBIT-HOLE-8 (P1) — compact vs daemon-owned DB prints raw SQLite
   `readonly database (8)` with no ownership guidance (normal systemd self-host shape).

**Also:** DF-RABBIT-HOLE-9 (P3, GOTMPDIR/`no space left` docs); partial
SKIPPED-install-bunker row (fresh-clone untestable without credentials — policy for Bane).

**Left behind:** docs/dogfood/2026-09-05-integration.md, diagnostics.md §14-16 (how the
collector is built, the stack-map failure mechanism, the isolation-probe method, install
traps), skills/rabbit-hole-usage/SKILL.md v1.1.0, 4 board tasks, this log.
2026-09-05 | PROMISING-BUT-ROUGH | 25s t2fs | friction 6 | 4 findings

2026-09-07 | SHIPPABLE | 4s t2fs | friction 6 | 5 findings

## 2026-11-20 no-op provenance — delivery 2

Tick id in body (never in the heading): `rabbit-hole-dogfood-2026-11-20-14-19-33`.

**This tick already ran the loop.** `~/.hermes/logs/gateway.log` carries two inbound
dispatch lines for this exact tick id on the same chat (f310c481): 2026-11-20 09:19:39
and 10:30:07 -05. Delivery 1 did the work and left its record in three places: board rows
created 15:24:18Z, commit `ffbbbb9` at 10:24:36 -05 ("board: dogfood 2026-11-20 — file
DF-039/DF-040 (correctness) + PERF-001/PERF-002 (perf)"), and DuckBrain event
`/dogfood/2026-11-20-tick` (domain `event`, timestamp 15:27:54Z, attributes tick=rabbit-hole-dogfood-2026-11-20-14-19-33,
head_before e18af6e, head_after ffbbbb9, rows_filed DF-039 DF-040 PERF-001 PERF-002).

**Parity proof (product unchanged since the last delivery):** `git fetch origin`;
`HEAD` = `origin/main` = `ffbbbb9`; `git rev-list --left-right --count HEAD...origin/main`
= `0 0`; `git diff --name-only ffbbbb9..origin/main` = empty. Zero product delta is what
licenses this no-op.

**Board untouched and healthy:** `git show origin/main:.coding-hermes/board/tasks.jsonl`
= 158 rows (106 complete, 51 pending, 1 blocked), zero duplicate ids; DF-039, DF-040,
PERF-001, PERF-002 all present and `pending` exactly as filed.

**Evidence spot-check of delivery 1's filed numbers (not re-measured):** the PERF rows'
text matches `scratch/rh-2026-11-20/evidence/timings-summary.txt` — stats warm mean
1.2764s / cold 0.5449s / 300k 0.2375s and dashsum warm mean 2.0552s / cold 2.1722s /
300k 0.7866s, i.e. the rows' quoted 1.28 / 0.54 / 0.24 and 2.06 / 2.17 / 0.79.
`evidence/plans1m.txt` does show `SCAN traces USING COVERING INDEX idx_traces_date` and
`SCAN flows_fts VIRTUAL TABLE INDEX 0:` as the rows claim. The filed numbers are
artifact-backed, so they stand.

**Perf leg deliberately NOT re-run:** re-driving an unchanged tree re-measures the host
(delivery 1's 1M-row runs happened at load ~32.7), not the product. The earlier warm/cold
numbers are the honest ones.

**Residue scan:** `/proc/*/cmdline` sweep for `rabbit-hole` / `rabbit_hole` (own shell
filtered out of the match) found no serve/attach process; no lane listener on the usual
port range. The only listener found in that range is `chimera` :8765 (pid 2580, a
systemd-user managed service) — another project's deployed service, named here and left
running, not this lane's residue.

**Disclosures.** (a) Delivery 1 wrote no entry to this file — its record lives in
DuckBrain and the board commit. The newest entry before this one is 2026-09-07, so every
dogfood delivery between 09-08 and 11-20 is unlogged and this file cannot serve as the
duplicate-delivery record until that gap is repaired; repair is a lane-discipline job,
not a product defect. (b) A tick label re-dispatched 71 minutes after its first delivery,
at zero product delta, is a scheduler/gateway dispatch defect — reported here and in the
tick report; it is not filed on this product's board. (c) No code, no board row and no
repo visibility/credential state was touched by this delivery.

## 2026-11-20 no-op provenance — delivery 3

Tick id (body only, never the heading): `rabbit-hole-dogfood-2026-11-20-14-19-33`.

**Third dispatch of one tick.** `~/.hermes/logs/agent.log` carries three inbound
`[Scheduler tick]` lines for this exact tick id on chat f310c481: 09:19:39, 10:30:07 and
10:36:02 -05. Delivery 1 did the loop; delivery 2 recorded its own no-op; this delivery
verified and stood down again. Nothing was re-walked and nothing was re-filed.

**Parity proof (zero product delta):** `git fetch origin main`; `HEAD` = `origin/main` =
`458e1da`; `git rev-list --left-right --count HEAD...origin/main` = `0 0`; the only commit
between delivery 1's `ffbbbb9` and `458e1da` is delivery 2's `docs(dogfood-log)` entry
(48 lines, one file). No product code, no board row changed since the loop ran.

**Rows stand as filed on the remote tree** (`git show origin/main:.coding-hermes/board/tasks.jsonl`):
DF-039 (P2 compact `--before` under-deletes), DF-040 (P3 clean-machine `go test` red,
TestValidateDefaults), PERF-001 (P2 dashboard summary 2.06s warm/2.17s cold at 1M rows),
PERF-002 (P3 `/api/v1/stats` 1.28s warm). All four `pending`. Board census 158 rows =
106 complete / 51 pending / 1 blocked, zero duplicate ids; local board md5 == remote md5
(`d79056f2…`).

**Evidence artifacts still on disk** under `scratch/rh-2026-11-20/evidence/`
(timings-summary.txt, plans1m.txt, cold1m.txt, warm1m/, warm300k/, dashboard.html,
postcompact-orphans.txt, board-*.txt).

**One anomaly in the perf artifact, triaged and NOT filed:** `timings-summary.txt` shows
`chat_stub … code 429 x5, code 200 x5`. That is the documented chat limiter (default
`RABBITHOLE_RATE_LIMIT_CHAT_RPS=5`, internal/config/config.go:62) answering ten rapid stub
requests — expected behaviour, not a defect. Its documentation gap is already open as
DOC-16 (pending). No row filed.

**Residue:** none. `/proc/*/cmdline` sweep for `rabbit-hole`/`rabbit_hole` matched only this
shell (self-filtered); no lane listener in the 9xxx range belongs to this project.

**Disclosure:** a tick label dispatched three times inside 77 minutes at zero product delta
is a scheduler/gateway dispatch defect, not a product finding; it is reported in the tick
report and in DuckBrain (`/dogfood/2026-11-20-tick-delivery3`), never filed on this board.
No code, board row, repo visibility or credential was touched by this delivery.

## 2026-11-20 no-op provenance — delivery 4

Tick id (body only, never the heading): `rabbit-hole-dogfood-2026-11-20-14-19-33`.

**Fourth dispatch of one tick.** `~/.hermes/logs/gateway.log` carries four inbound
`[Scheduler tick]` lines for this exact tick id on chat f310c481: 09:19:39, 10:30:07,
10:36:02 and 10:38:26 -05. Delivery 1 ran the loop; deliveries 2 and 3 recorded their own
no-ops; this delivery verified again and stood down. Nothing was re-walked, nothing was
re-measured, nothing was re-filed.

**Parity proof (zero product delta):** `git fetch --quiet origin main` at 10:39:02 -05;
`HEAD` = `origin/main` = `7540226`; `git rev-list --left-right --count HEAD...origin/main`
= `0 0`. The whole diff since delivery 1's `ffbbbb9` is `.coding-hermes/dogfood-log.md`
alone (delivery 2's `458e1da` and delivery 3's `7540226` entries) — `git diff --name-only
ffbbbb9..origin/main` names no other path. No product code, no board row changed since the
loop ran.

**Rows stand as filed on the remote tree** (`git show origin/main:.coding-hermes/board/tasks.jsonl`):
DF-039 `pending`/P2, DF-040 `pending`/P3, PERF-001 `pending`/P2, PERF-002 `pending`/P3.
Board census 158 rows = 106 complete / 51 pending / 1 blocked, zero duplicate ids; local
board md5 == remote md5 (`d79056f2…`). Board file untouched by this delivery.

**Perf leg deliberately NOT re-run:** the tree is byte-identical to the one delivery 1
measured, so a re-run measures the host's load, not the product. The filed warm/cold
numbers (stats 1.28s warm / 0.54s cold; dashboard summary 2.06s warm / 2.17s cold at 1M
flows) stand on `scratch/rh-2026-11-20/evidence/timings-summary.txt` and were already
spot-checked against that artifact by delivery 2.

**Residue scan:** `/proc/*/cmdline` sweep for `rabbit-hole` / `rabbit_hole` matched no
process (own shell filtered; the only matches were short-lived `/proc` entries that
vanished mid-scan). No listener in the 8xxx/9xxx range belongs to this project — the
listeners present are other projects' services (task-router :9092/:9391, router_web :9093,
temple-runner-web :8123, aipp console :8899, game bridge, cf-inference-proxy :8775,
dashboard :9095, chimera :8765, off-by-one :8766), named here and left running.

**Disclosure:** a tick label dispatched four times inside 79 minutes at zero product delta
is a scheduler/gateway dispatch defect, not a product finding. It is reported in the tick
report and belongs on the scheduler board; it is never filed on this product's board. No
code, no board row, no repo visibility or credential state was touched by this delivery.

### Correction to the delivery-4 attribution

Appended minutes after the entry above, because the scheduler's own state contradicts the
attribution I wrote before checking it. The log entry stays as written; this is the
correction of record.

**There was one dispatch, not four.** `sqlite3 ~/.hermes/scheduler.db` (symlink to
`.hermes/coding-hermes/scheduler.db`) holds exactly one `ticks` row for
`rabbit-hole-dogfood-2026-11-20-14-19-33`: `status=failed`, `outcome=failed`,
`spawned_at=2026-11-20T09:19:33-05:00`, `completed_at=2026-11-20T09:59:41-05:00`,
`files_changed=0`, `failure_reason=gateway_5xx`. `tick_dispatch` has zero rows for lane
`rabbit-hole-dogfood` on 2026-11-20. The project row is enabled with `cooldown_s=259200`
pinned (3 days), so a second scheduler dispatch inside the window would have been refused
outright — the four `gateway.log` inbound lines at 09:19:39, 10:30:07, 10:36:02 and 10:38:26
are the same tick prompt re-delivered into the same chat `f310c481`, i.e. a client-side
retry of one request, not four ticks. Calling this a scheduler dispatch defect was wrong.

**The product work landed after the tick was already marked failed.** Tick closed failed at
09:59:41; delivery 1's board commit `ffbbbb9` is timestamped 10:24:36 — 25 minutes later.
So `gateway_5xx` / `files_changed=0` describes an HTTP turn that outlived its tick budget and
returned 5xx to the caller, not a product that failed to work. The loop ran, measured and
filed; the scheduler gave up on it first.

**Scope of the zero-delta claim.** It holds for deliveries 2, 3 and 4 against `ffbbbb9`
(10:24:36) — `.coding-hermes/dogfood-log.md` is the only path changed since. Delivery 1's
own delta is the four rows it filed (`DF-039`, `DF-040`, `PERF-001`, `PERF-002`), which is
not zero and was never claimed to be.

**Where the real finding points.** A long dogfood turn outlives the tick's HTTP budget, gets
recorded failed while it is still productive, and each retry re-enters the same session as a
fresh turn because the prompt carries no idempotency key beyond the tick id — three retries
burned three full agent turns to re-derive "already delivered". That is a scheduler/gateway
concern. It is reported in the tick report and is not a row for this project's board: the
rabbit-hole foreman cannot fix the scheduler. No row filed, no code touched.
