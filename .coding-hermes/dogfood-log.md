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
