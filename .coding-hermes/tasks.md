# Rabbit-Hole — Task Board

> SPECS FIRST (done). Now build.

## PHASE -1: Spec Completion ✅

| ID | Task | Status |
|---|---|---|
| S01 | Overview & Architecture — interfaces, entities, data flow | ✅ Done |
| S02 | Collection Layer — eBPF, ring buffer, TLS, attach/detach | ✅ Done |
| S03 | Classification Layer — Gemma, pattern catalog, pipeline | ✅ Done |
| S04 | Expression Layer — chat, search, WebSocket, middleware | ✅ Done |
| S05 | Storage & Data Model — SQLite DDL, FTS5, migrations, retention | ✅ Done |
| S06 | CLI & Self-Hosted Deployment — commands, systemd, install script | ✅ Done |

**Gate: 6/6 axiom-level specs written. ~65 pages. FOREMAN: proceed to Phase 0.**

---

## PHASE 0: Scaffold

| ID | Task | Status |
|---|---|---|
| P0-01 | go.mod, go.sum — module `github.com/totalwindupflightsystems/rabbit-hole` | ✅ Done (pre-existing in init commit) |
| P0-02 | Makefile — build, test, lint, install targets | ✅ Done (pre-existing in init commit) |
| P0-03 | .gitignore — Go binary, data dir, IDE files | ✅ Done (pre-existing in init commit) |
| P0-04 | Directory structure — cmd/, internal/{collector,classify,express,storage,attach,config}, pkg/types | ✅ Done |
| P0-05 | Git init + GitHub Actions CI workflow | ✅ Done |

**Gate:** `make build` produces `bin/rabbit-hole` ✅ (1.2MB ELF, stripped). CI workflow ready (will run on push).

---

## PHASE 1: Types + Config

| ID | Task | Spec Ref | Status |
|---|---|---|
| P1-01 | `pkg/types/trace.go` — Trace, TraceCategory, Frame types | S01 §3.1, S02 §1 | ✅ Done (7da0d78) |
| P1-02 | `pkg/types/flow.go` — Flow, FlowPhase, FlowOutcome types | S01 §3.2 | ✅ Done (7da0d78) |
| P1-03 | `pkg/types/session.go` — Session, SessionStatus, SessionMetadata types | S01 §3.4 | ✅ Done (7da0d78) |
| P1-04 | `pkg/types/context_window.go` — ContextWindow type | S01 §3.3 | ✅ Done (7da0d78) |
| P1-05 | `pkg/types/api.go` — SearchRequest, ChatRequest, ChatResponse, etc. | S01 §4.3, S04 §2 | ✅ Done (7da0d78) |
| P1-06 | `pkg/types/errors.go` — typed errors (all layers) | S02 §3, S03 §3, S04 §4, S05 §4 | ✅ Done (7da0d78) |
| P1-07 | `internal/config/config.go` — env var parsing, validation, defaults | S01 §7, S06 §4 | ✅ Done (d38a27e) |
| P1-08 | `internal/config/config_test.go` — env var tests, validation tests | — | ✅ Done (d38a27e) |

**Gate:** `go build ./...` passes. All types compilable. Config tests pass.

---

## PHASE 2: Storage

| ID | Task | Spec Ref | Status |
|---|---|---|---|
| P2-01 | `internal/storage/sqlite.go` — SQLiteStore, NewSQLiteStore, Close | S05 §3.1 | ✅ Done |
| P2-02 | `internal/storage/migrations/001_initial.up.sql` — full DDL | S05 §2 | ✅ Done |
| P2-03 | `internal/storage/migrate.go` — migration runner (folded into sqlite.go) | S05 §8 | ✅ Done |
| P2-04 | StoreTraces — batch insert | S05 §3 | ✅ Done |
| P2-05 | StoreFlows + FTS5 triggers — batch insert with FTS5 sync | S05 §3.2 | ✅ Done |
| P2-06 | GetFlow, QueryFlows, SearchFlows (FTS5 + LIKE fallback) | S05 §3.3 | ✅ Done |
| P2-07 | Session CRUD: StoreSession, GetSession, ListSessions, UpdateSession | S05 §3 | ✅ Done |
| P2-08 | ContextWindow CRUD: StoreContextWindow, GetContextWindow | S05 §3 | ✅ Done |
| P2-09 | Compact, Stats, Health | S05 §3.4 | ✅ Done |
| P2-10 | `internal/storage/storage_test.go` — all CRUD, FTS5, compaction, concurrent | S05 §9 | ✅ Done |

**Gate:** All storage operations tested. `go test ./internal/storage/... -count=1` passes. ✅ 14 tests, 0 failures.

---

## PHASE 3: Collection (eBPF)

| ID | Task | Spec Ref | Status |
|---|---|---|
| P3-01 | `internal/collector/ebpf.go` — eBPF program loading (collector.bpf.c via bpf2go) | ✅ Done (39b07c6) |
| P3-02 | `internal/collector/buffer.go` — ring buffer with Pop, Push, PopBatch, Stats | ✅ Done (39b07c6) |
| P3-03 | `internal/collector/collector.go` — Collector interface impl, NewEBPFCollector | ✅ Done (39b07c6) |
| P3-04 | `internal/collector/collector.go` — Attach (PID filter, TLS probes) | ✅ Done (39b07c6) |
| P3-05 | `internal/collector/collector.go` — Detach (cleanup, status update) | ✅ Done (39b07c6) |
| P3-06 | `internal/collector/collector.go` — Health, List | ✅ Done (39b07c6) |
| P3-07 | `internal/collector/collector_test.go` — ring buffer, session lifecycle, PID filter | ✅ Done (39b07c6) |
| P3-08 | `internal/collector/collector_test.go` — integration tests (attach to real process, verify traces) | ✅ Done (39b07c6) |

**Gate:** Attach to `sleep 5` → traces captured. Tests pass. Ring buffer overflow handled. ✅ 24 collector tests. Build+vet+test green.

---

## PHASE 4: Classification

| ID | Task | Spec Ref | Status |
|---|---|---|
| P4-01 | `internal/classify/patterns.go` — PatternCatalog with file_read, file_write, network_connect, etc. | S03 §2.2 | pending |
| P4-02 | `internal/classify/gemma.go` — GemmaModel, Load, Unload, ClassifyBatch | S03 §2.3 | pending |
| P4-03 | `internal/classify/engine.go` — ClassificationEngine, groupTracesByTime, Classify pipeline | S03 §2.1 | pending |
| P4-04 | `internal/classify/classifier.go` — Classifier interface impl, ClassifyStream, Health, ModelInfo | S03 §1 | pending |
| P4-05 | `internal/classify/classifier_test.go` — pattern matching, grouping, prompt building, parse | S03 §8 | pending |
| P4-06 | `internal/classify/gemma_test.go` — model load (skip if no model), batch classify with recorded traces | S03 §8 | pending |

**Gate:** Pattern matching works on recorded traces. Model optional — graceful degradation when unavailable.

---

## PHASE 5: Expression (HTTP + Chat)

| ID | Task | Spec Ref | Status |
|---|---|---|
| P5-01 | `internal/express/server.go` — Server, NewServer, Start, Shutdown, route registration | S04 §2.1 | pending |
| P5-02 | `internal/express/middleware.go` — request ID, CORS, logging, panic recovery | S04 §2.5 | pending |
| P5-03 | `internal/express/handlers.go` — health, list sessions, get session, get flow, get context window | S04 §3 | pending |
| P5-04 | `internal/express/search.go` — handleSearch with FTS5 query, filtering, pagination | S04 §2.3 | pending |
| P5-05 | `internal/express/chat.go` — handleChat: NL → search → NL answer | S04 §2.2 | pending |
| P5-06 | `internal/express/websocket.go` — handleWebSocket: upgrade, subscribe, flow push, ping/pong | S04 §2.4 | pending |
| P5-07 | `internal/express/server_test.go` — all handlers, middleware, chat flow | S04 §8 | pending |

**Gate:** `curl localhost:9734/health` returns 200. Chat endpoint works. WebSocket streams flows.

---

## PHASE 6: CLI

| ID | Task | Spec Ref | Status |
|---|---|---|
| P6-01 | `cmd/rabbit-hole/main.go` — root command, subcommand wiring | S06 §1 | pending |
| P6-02 | `cmd/rabbit-hole/attach.go` — attach subcommand | S06 §1.1 | pending |
| P6-03 | `cmd/rabbit-hole/serve.go` — serve subcommand (full daemon startup) | S06 §1.4 | pending |
| P6-04 | `cmd/rabbit-hole/chat.go` — chat subcommand | S06 §1.5 | pending |
| P6-05 | `cmd/rabbit-hole/search.go` — search subcommand | S06 §1.6 | pending |
| P6-06 | `cmd/rabbit-hole/status.go` — status subcommand | S06 §1.8 | pending |
| P6-07 | `cmd/rabbit-hole/detach.go` — detach subcommand | S06 §1.2 | pending |
| P6-08 | `cmd/rabbit-hole/compact.go` — compact subcommand | S06 §1.7 | pending |
| P6-09 | `cmd/rabbit-hole/version.go` — version subcommand | S06 §1.9 | pending |

**Gate:** All commands work. `rabbit-hole serve` → `rabbit-hole chat "hello"` → response.

---

## PHASE 7: Integration + E2E

| ID | Task | Spec Ref | Status |
|---|---|---|
| P7-01 | `internal/attach/attach.go` — orchestration: collector → classifier → storage pipeline | S01 §4 | pending |
| P7-02 | E2E test: serve → attach to test-agent → verify flows in search → detach | — | pending |
| P7-03 | E2E test: chat query returns correct flows | — | pending |
| P7-04 | E2E test: WebSocket receives real-time flows | — | pending |
| P7-05 | Stress test: 100K traces → classification → search → all within latency bounds | — | pending |
| P7-06 | `scripts/install.sh` — one-command install with model download, systemd | S06 §3 | pending |

**Gate:** Full agent session captured end-to-end. `rabbit-hole chat "what happened?"` works.

---

## PHASE 8: Release

| ID | Task | Status |
|---|---|---|
| P8-01 | GitHub release with built binary (linux/amd64, linux/arm64) | pending |
| P8-02 | README.md with Quickstart + architecture diagram | pending |
| P8-03 | Dockerfile — `docker run rabbit-hole serve` | pending |
| P8-04 | Tag v1.0.0 | pending |

**Gate:** External dev goes zero → `rabbit-hole attach --pid <PID>` → `rabbit-hole chat` working < 5 min.

---

## Phase Gates Summary

| Phase | Gate | Blocks |
|---|---|---|
| P-1 | 6/6 axiom-level specs | Everything |
| P0 | Scaffold builds + CI green | P1–P8 |
| P1 | All types compilable | P2–P7 |
| P2 | Storage CRUD tested | P3–P7 |
| P3 | eBPF attaches to process | P4–P7 |
| P4 | Pattern matching works | P5–P7 |
| P5 | HTTP server running | P6–P7 |
| P6 | All CLI commands work | P7 |
| P7 | Full E2E agent session | P8 |
| P8 | v1.0.0 released | Launch |
