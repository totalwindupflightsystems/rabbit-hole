# Rabbit-Hole Dogfood Integration Report — 2026-09-05

**Run type:** scheduled cron dogfood (5th run: 08-08, 08-18, 08-27, 09-04, 09-05)
**HEAD:** c1c9386 (`foreman tick [ci skip]`, board clean, DF-001..037 all `complete`)
**Verdict:** 🟡 **PROMISING-BUT-ROUGH** — Express + Storage are solid and every fix from
prior runs re-verified live, but this run's reason to exist is a new P0: **the headline
eBPF Collect layer cannot start even WITH full privileges on the fleet's kernel
generation, and the error misattributes the cause.**

---

## Promise (null hypothesis)

*"A user can attach to a real agent process with zero SDK, and Rabbit-Hole collects
kernel-level (eBPF) telemetry, classifies it, and answers 'what did the agent do?' —
self-hosted, one binary."*

## Why this run was different

Every previous dogfood run (08-08 → 09-04) tested in `--no-ebpf` degraded mode or with
the demo stream, because the control host was assumed unprivileged. Today the host has
passwordless sudo, kernel **7.0.0-29-generic**, BTF at `/sys/kernel/btf/vmlinux`, and a
full toolchain — so the **real Collect layer was exercised for the first time.** It failed,
and the failure mode had been invisible for a month.

## What actually happened (chronological)

### 1. Fresh build — works
`make build` at c1c9386: **10 s**, `bin/rabbit-hole` 20.5 MB. No friction.

### 2. First-ever full-privilege run → eBPF load fails (NEW P0, DF-RABBIT-HOLE-6)
```
sudo env RABBITHOLE_DATA_DIR=/tmp/dogfood-rh-e ./bin/rabbit-hole serve --demo-stream
WARN ... ebpf: kernel probes unavailable, running without eBPF
  err="load bpf objects: field TraceExitSyscall: program trace_exit_syscall:
       map stack_map: map create: invalid argument"
  hint="run 'go generate ./internal/collector/' with clang and kernel headers installed"
```
Then the daemon degrades to pattern-only (demo stream still flows).

`attach` **without** `--no-ebpf` refuses (as documented, exit 1) — but with the wrong diagnosis:
```
Error: eBPF unavailable — telemetry DISABLED (requires CAP_SYS_RESOURCE and kernel 5.11+; ...)
```
**Both stated prerequisites are satisfied on this host** (root, kernel 7.0), and the
regenerate-objects hint wouldn't help either: clang 21.1.8, kernel headers
(7.0.0-29), and prebuilt `bpf_x86_bpfel.o` are all present.

**Isolation probe** (standalone Go program using the same `cilium/ebpf v0.17.3`, run as root):

| MapSpec | Result |
|---|---|
| `BPF_MAP_TYPE_STACK_TRACE, key=4B, 10240 entries` (project's spec, no value_size) | `map create: invalid argument` |
| same + `ValueSize: 4` | `map create: invalid argument` |
| same, `MaxEntries: 256` | `map create: invalid argument` |

The kernel refuses **any** stack-trace map here (`kernel.perf_event_paranoid = 4`,
`unprivileged_bpf_disabled = 2`; stack-trace maps allocate perf events internally).
So: the project's BPF objects may be fine; the map *type itself* is what this kernel
generation rejects — and the product reports the wrong reason to the user.

**Impact:** AGENTS.md's competitive table sells "eBPF + classification + chat" against
AgentSight; on current kernels the differentiating layer is unreachable, and a capable
user debugging it is sent down two wrong paths (privileges, regeneration).

**Task:** DF-RABBIT-HOLE-6 (P0). FIX direction: normalize/probe the stack_map spec for
modern kernels; gate the privilege/kernel message on an actual CapEff check; print the
real `map create` errno with targeted remediation; CI smoke that loads the collector on a
7.x kernel.

### 3. Degraded daemon + real attach lifecycle → prior P1 verified FIXED
- `serve --no-ebpf --demo-stream` up in ~1 s; demo flows every 3 s.
- Attached `sleep 300` (`--no-ebpf`): session persisted via daemon, `list --all` sees it.
- Process exited 05:07:57 → session **`completed` with real `end_time` well inside the
  60 s bound** (DF-028 ✓, re-verified).
- Demo session now also completes with non-null `end_time` (DF-032 ✓).
- 18-min soak: **5.5 MB RSS, 0 % CPU**, `/health` instant — no DF-027 regression.

### 4. Express/API retests
- `POST /api/v1/search` with **UTC-window** `time_range.start` → flows returned
  (**DF-034 P0 verified FIXED live** — first re-verification since the fix).
- Stub chat answers time-window questions from recent activity (50 actions, suggestions).
- Dashboard SPA: 200, 26 KB.
- WS route is session-scoped: `/api/v1/ws/sessions/{id}` → **HTTP 101** upgrade OK.
  (Friction: nothing in `/health` or README points at the session-scoped path; probing
  `/api/v1/ws` 404s. Minor — noted, not filed.)
- Remote `classify-server --addr :50059` boots, advertises `classifier.v1.Classifier`,
  version banner matches daemon (v1.0.0-512-gc1c9386).

### 5. Still-open yesterday findings, re-confirmed verbatim
- **DF-RABBIT-HOLE-2** — `attach --no-ebpf` still prints the two scary memlock WARN
  lines before honoring the flag.
- **DF-RABBIT-HOLE-4** — `POST /api/v1/chat {"message":"hello"}` with 100+ live flows →
  "I couldn't find any matching activity", `flows:[]`.
- **DF-RABBIT-HOLE-5** — sessions pagination `total` still page-scoped
  (`limit=1` → `total:1`; `limit=1&offset=1` → `total:1`; 2 sessions exist).

### 6. Compact — works as root; cryptic as non-owner (NEW, DF-RABBIT-HOLE-8)
- `compact --before 10m` **as the daemon's user (root)**: `Compact complete.` (DF-029 ✓).
- Same command as a different user against the root-owned data dir:
  `Error: compact: delete old traces: attempt to write a readonly database (8)` —
  raw SQLite errno, zero guidance. The daemon-user/CLI-user mismatch is the *normal*
  deployment for a systemd self-host, so this message will confuse real operators.

### 7. Install leg — ephemeral bunker (las-bunker-03), agent 95ca8881 (destroyed post-run)
Per Bane's hard rule, no repo visibility/credential changes were made.

- `git clone` from the agent → **access denied** (private GitLab-over-SSH, agent has no
  credential). Recorded as a finding; **workaround used:** rsync the tree from the
  control host with existing access (repo unchanged).
- **Documented Go toolchain install is root-only** (NEW P1, DF-RABBIT-HOLE-7):
  `sudo apt-get install -y golang-go` → `sudo: a password is required`; tarball path says
  "extract to /usr/local" (root-owned). A bare non-root user — exactly the
  `--no-ebpf` degraded-mode audience — is dead at step one. `make build` → `go: not found`.
- User-space Go install (workaround, NOT in docs): Go 1.26.5 into `$HOME`.
- First `make build` died mid-compile: `no space left on device` while `df` showed 190 G
  free — `/tmp` on the bunker is a **shared tmpfs at 99 %**. Go builds compile into /tmp
  unless `GOTMPDIR` is set (NEW P3, DF-RABBIT-HOLE-9: docs troubleshooting bullet).
  *(Bunker-infra note, not a project bug: consider resizing/rotating the shared /tmp.)*
- Retry with `TMPDIR=$HOME/tmp`: **build OK in 11 s** (20 MB binary).
- Documented smoke on the fresh agent: `version` prints; `serve --no-ebpf` boots;
  `/health` answers honestly (`degraded`, "ollama unreachable → pattern matching only");
  `list --all` → "No sessions found."; `search "write"` → "No results found." on a clean
  DB — all correct for a fresh install. **Smoke: passed.**
- `INSTALL_SECONDS`: ~49 s of build+warmup after Go is present (11 s build, ~38 s first
  attempt before the tmpfs stop); the un-round number reflects the two documented-path
  blockers above. `agent=95ca8881` destroyed cleanly (`bunker destroy` rc=0).

## Judgement

| Question | Answer |
|---|---|
| Does it work? | Express + Storage: yes, verified live. Collect: **no** — eBPF cannot load even with full privileges on kernel 7.0 (DF-RABBIT-HOLE-6). |
| Is it useful? | The demo/degraded product is genuinely useful as an agent-activity explainer. The kernel-telemetry differentiator is the point of the project and is unreachable today. |
| Is it usable? | t2fs ~25 s (build+serve+health). Friction 6: misleading eBPF error, useless regen hint, compact errno, root-only install docs, GOTMPDIR trap, WS path discovery. |
| Is it trustworthy? | Yes in degraded mode: honest health/status, session states correct, data durable, no wedge in an 18-min soak. Distrust point: the eBPF error message lies about the cause. |

## Left behind (committed)

- `docs/dogfood/2026-09-05-integration.md` (this file)
- `docs/dogfood/diagnostics.md` §14 — how the Collect layer is built, the stack-trace-map
  failure mechanism, the isolation-probe method, the right way to diagnose
- `skills/rabbit-hole-usage/SKILL.md` v1.1.0 — state 09-05, DF-034/035 marked verified-fixed,
  new hazards (eBPF kernel block, install path, compact ownership, GOTMPDIR)
- Board: DF-RABBIT-HOLE-6 (P0), -7 (P1), -8 (P1), -9 (P3)
- `.coding-hermes/tasks.md` + `.coding-hermes/dogfood-log.md` entries

## Scratch evidence paths (control host, not committed)

`/tmp/dogfood-rh-e/serve.log`, `cs.log`, `compact.log`, `maptest/` (isolation probe),
`scratch-notes.txt` (bunker agent record).
