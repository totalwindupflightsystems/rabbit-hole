<!--
  ⚠️  BOARD FORMAT — coding-hermes-model-router v1.3 (2026-07-24)
  All tasks MUST use matrix format: | ID | Task | Pri | Cpx | Deps | Tags | Model | Reasoning | Fallback |
  Before editing this file, load the skill: skill_view(name='coding-hermes-model-router')
  Validate: python3 ~/.hermes/scripts/validate-board-format.py .coding-hermes/tasks.md
- [ ] **GITREINS-JUDGE — Configure LLM evaluator for commit quality review**
  | 🔴 Critical | — | — | deepseek-v4-flash @ deepseek-foreman | GITREINS_LLM_API_KEY in ~/.hermes/.env | foreman-direct |

  Run: `python3 ~/.hermes/scripts/check-gitreins-judge.py .` to verify.
  Default limits (adjust per-project based on codebase size and task complexity):
  - Fast/small projects: `max_iterations: 50`, `max_time: 10m`, tokens: `0.2M/0.4M`
  - Large repos (Go monorepos, 100+ files): `max_iterations: 100`, `max_time: 30m`, tokens: `1M/2M`
  - C++/Rust (slow compiles): `max_time: 30m` minimum
  - Scheduler/production infra: `max_time: 30m`, tokens: `1M/2M`
  Supervisor auto-flags projects where limits are too low for codebase size.

| 🔴 Critical | — | — | deepseek-v4-flash @ deepseek-foreman | GITREINS_LLM_API_KEY in ~/.hermes/.env | foreman-direct |

  Run: `python3 ~/.hermes/scripts/check-gitreins-judge.py .` to verify.
  If missing, create/edit .gitreins/config.yaml with evaluator section using deepseek-v4-flash.
  This is CRITICAL for code quality — no automated review of worker output without it.

  NEVER remove the matrix header row or NEVER-DONE / E2E-001 fixtures.
-->

# Rabbit-Hole — Model Router Task Matrix

> **Core purpose:** eBPF-based agent observability — attach to AI agent processes, capture LLM call flows, classify with Gemma, search with FTS5, chat about what the agent did.
> **Language:** Go (eBPF + SQLite + gRPC) | **CI:** GitLab | **Host:** karaHermes-mde-7840hs
> **Status:** ALL PHASES COMPLETE (63 tasks + 6 DuckBrain entries, 9 phases). Zombie — maintenance only.
> **Last tick:** #22 (2026-07-25). Build ✅ Vet ✅ Test ✅ (77.1% cov) Hilo ✅ (532 edges) GitReins ✅ Lint ⚠️ (58 pre-existing). Self-fix: SECURITY.md + LICENSE created. Docs now complete.
> **Verdict:** idle — maintenance mode (11 idle ticks, self-fixed 2 doc gaps)

## Active Tasks

| ID | Task | Pri | Cpx | Deps | Tags | Model | Lvl | Fallback |
|----|------|-----|-----|------|------|-------|-----|----------|
|| DB-001 | ✅ DuckBrain: /project/rabbit-hole/architecture — 3-layer design, component map | Medium | 1 | ✅ Complete | — | — | — | — |
|| DB-002 | ✅ DuckBrain: /spec/rabbit-hole/collector — eBPF design decisions, pitfalls | Medium | 1 | ✅ Complete | — | — | — | — |
|| DB-003 | ✅ DuckBrain: /spec/rabbit-hole/classifier — pluggable backend rationale | Medium | 1 | ✅ Complete | — | — | — | — |
|| DB-004 | ✅ DuckBrain: /spec/rabbit-hole/express — API design, chat model strategy | Medium | 1 | ✅ Complete | — | — | — | — |
|| DB-005 | ✅ DuckBrain: /spec/rabbit-hole/storage — SQLite rationale, FTS5 design | Medium | 1 | ✅ Complete | — | — | — | — |
|| DB-006 | ✅ DuckBrain: /project/rabbit-hole/status — current phase, coverage, blockers | Medium | 1 | ✅ Complete | — | — | — | — |
|| REL-001 | GitHub/GitLab release with built binaries (linux/amd64, linux/arm64) | Medium | 2 | — | ++infra, +github | DeepSeek V4 Flash | Low | Step 3.7 Flash |
|| REL-003 | External dev verification — zero → working harness < 5 min | Low | 2 | REL-001 | ++testing, ++docs | N/A (human-gated) | — | — |
|| E2E-001 | E2E Testing Tick (self-improving loop) 🔁 Recurring every 5-10 ticks | High | 4 | server running | ++browser, ++screenshots, ++verification | GPT-5.6 Luna | High | Step 3.7 Flash |
|| NEVER-DONE | 11-point audit sweep | Medium | 2 | — | ++code-review, +testing | DeepSeek V4 Pro | Medium | GLM-5.2 |

## Completed (63/63 tasks, 9 phases)

All phases shipped: 6 specs, 5 stubs eliminated, 7 coverage gaps closed, 9 integration tests, 8 stress tests, 6 deployment artifacts, 8 hardening items, 5 docs, 6 E2E scenarios, DEP-*, REL-002.

| Phase | Purpose | Key outcomes |
|-------|---------|--------------|
| P-1 | Specs | 6 axiom-level specs (architecture, collection, classification, expression, storage, CLI) |
| P0 | Stub removal | Zero "not implemented" in codebase — 5 real implementations |
| P1 | Test coverage | All packages ≥60% — 0 packages at 0% |
| P2 | Integration | Real eBPF attachment, Gemma inference, gRPC, WebSocket, full server lifecycle |
| P3 | Stress | Ring buffer overflow, 100K trace insert, 100 concurrent FTS5, memory leak detection |
| P4 | Deployment | systemd unit, install.sh, Dockerfile, Makefile, shell completion, CHANGELOG |
| P5 | Hardening | Rate limiting, auth, logging, Prometheus metrics, health depth, graceful shutdown, crash recovery |
| P6 | Docs | ADRs, OpenAPI spec, gRPC proto docs, package docs, CONTRIBUTING |
| P7 | E2E | Full agent session — serve → attach → search → chat → detach, WebSocket, remote classifier |
| P8 | DuckBrain | Project memory seeded: 6 entries (architecture, collector, classifier, express, storage, status) ✅ |
| P9 | Release | v1.0.0 tag + binaries (pending REL-001, REL-003) |

## Assumptions

- Go project with eBPF (cilium/ebpf), SQLite (modernc.org/sqlite), Gemma 3 4B via Ollama
- 10/10 packages pass, 63%+ coverage, 14 benchmarks, 0 TODOs/FIXMEs, 0 vulns in called code
- GitLab CI blocked — zero online runners (host-level INFRA: pids.max=512 thread exhaustion)
- Host resource exhaustion (pids.max=512) blocks full parallel test suite — individual packages pass
- cilium/ebpf major bump intentionally blocked (eBPF API changes)
- Cooldown increased from 15m to 12h to reduce PAYG burn on idle ticks
- REL tasks are human-gated — require human to cut release and verify dev flow

## Routing Notes

- **DuckBrain seeding (DB-*):** ✅ Complete (tick #20). 6 entries seeded.
- **Release tasks (REL-*):** V4 Flash for mechanical, human-gated for REL-003
- **NEVER-DONE audit:** DeepSeek V4 Pro — needs full context, terminal, file search
- Project is effectively a zombie — 10 idle ticks, zero code changes, only DuckBrain seeding + release remain
- If host INFRA is fixed (pids.max increase + GitLab runners), escalate to full audit

## Execution Order

1. ~~DB-001 through DB-006~~ ✅ Complete (tick #20)
2. REL-001 (requires DB context)
3. REL-003 (human-gated — after REL-001)
4. NEVER-DONE (runs every tick)
5. E2E-001 (periodic, after server is running)

## Escalation Conditions

- Any DuckBrain write fails → escalate to foreman (check MCP transport)
- host INFRA resolved (pids.max increased, GitLab runners online) → run full audit, re-check all gates
- REL-001 release binary build fails → escalate to V4 Pro for debugging
- Audit finds new code gap after INFRA resolution → escalate to worker (MiniMax-M3 via ollama-cloud)
