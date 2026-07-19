# Rabbit-Hole — Complete Task Board

> **Never done.** If the foreman reports idle, the board is too small.

## PHASE -1: Spec Completion ✅

| ID | Task | Status |
|---|---|---|
| S01 | Overview & Architecture | ✅ |
| S02 | Collection Layer | ✅ |
| S03 | Classification Layer (updated: pluggable backends) | ✅ |
| S04 | Expression Layer | ✅ |
| S05 | Storage & Data Model | ✅ |
| S06 | CLI & Self-Hosted Deployment | ✅ |

---

## PHASE 0: Stub Removal — NO STUBS IN PRODUCTION

> Bane's rule: "NO STUBS." Every DI component wired to real infrastructure.

| ID | Task | Files | Status |
|---|---|---|---|
| STUB-001 | Replace stubChatModel with real LLM-backed NL query translation | internal/express/server.go | ✅ d753e49 |
| STUB-002 | Wire Gemma model loading — Load() currently no-ops, inference returns confidence=0 | internal/classify/gemma.go | ✅ cac93c5 |
| STUB-003 | Generate real protobuf Go code from classifier.proto — replace hand-written stubs | internal/classify/pb/classifier.go, api/proto/classifier/v1/classifier.proto | pending |
| STUB-004 | Generate real eBPF Go bindings from collector.bpf.c via bpf2go — replace loadBpfObjects stub | internal/collector/ebpf.go | pending |
| STUB-005 | Replace stub ModelInfo ("none"/"stub") with real model metadata | internal/classify/classifier.go | ✅ complete |

**Gate:** All "not implemented" / "stub" / "no-op" code paths eliminated. `grep -r "stub\|not implemented\|placeholder" --include='*.go' .` returns zero results.

---

## PHASE 1: Test Coverage — 0% packages

| ID | Task | Target | Current | Status |
|---|---|---|---|---|
| COV-001 | Unit tests for cmd/rabbit-hole/ — all 9 cobra subcommands | 60%+ | 0% | pending |
|| COV-002 | Unit tests for pkg/types/ — error types, serialization | 80%+ | 100% | ✅ complete |
| COV-003 | Unit tests for internal/classify/pb/ — generated proto types | 80%+ | 0% | pending |
| COV-004 | Unit tests for internal/collector/ebpf.go — parseTraceEvent, readEvents, loadBpfObjects | 60%+ | 42.4% | pending |
| COV-005 | Unit tests for storage QueryFlows paths — all filter combinations | 80%+ | 68.8% | pending |
| COV-006 | Unit tests for searchFlowsLike, searchFlowsRecent — FTS5 fallback paths | 90%+ | 0% (sub-functions) | pending |
| COV-007 | Unit tests for internal/attach/e2e_test.go — full pipeline wiring | 60%+ | 69.4% | pending |

**Gate:** All packages ≥60% coverage. Zero packages at 0%.

---

## PHASE 2: Integration Tests — Real Infrastructure

| ID | Task | Description | Status |
|---|---|---|---|
| INT-001 | Integration test: attach eBPF to real process (not test binary) | Spawn `sleep 5`, attach, verify syscall traces captured | pending |
| INT-002 | Integration test: attach → detach lifecycle, session cleanup | Attach, verify session created, detach, verify cleanup | pending |
| INT-003 | Integration test: TLS interception (attach to HTTPS-making process) | Attach to `curl https://example.com`, verify TLS probes fire | pending |
| INT-004 | Integration test: Gemma model loaded + classify real trace batch | Load Gemma 3 4B, feed recorded traces, verify classification output | pending |
| INT-005 | Integration test: Remote gRPC backend against real classifier server | Start classifier-only rabbit-hole, connect remote backend, classify | pending |
| INT-006 | Integration test: WebSocket — connect, receive real-time flows, disconnect | Start server, WebSocket subscribe, push flows, verify received | pending |
| INT-007 | Integration test: Full server lifecycle — start → health → attach → search → chat → shutdown | Every endpoint exercised against live server | pending |
| INT-008 | Integration test: FTS5 search with real data — insert flows, search, verify results | 100 flows inserted, FTS5 queries return correct matches | pending |
| INT-009 | Integration test: Retention/compaction — insert old data, compact, verify deleted | Insert data 31 days old, run compact, verify 0 rows remain | pending |

**Gate:** All integration tests pass. Real eBPF attachment. Real model inference. Real gRPC.

---

## PHASE 3: Stress & Benchmark Tests

| ID | Task | Description | Status |
|---|---|---|---|
| STR-001 | Ring buffer overflow test — push 2x capacity, verify oldest dropped | 200K traces into 100K buffer, verify drop count = 100K | pending |
| STR-002 | 100K trace insert performance test — StoreTraces batch of 100K | Must complete in <30s, verify all stored | pending |
| STR-003 | 100 concurrent FTS5 searches — latency <50ms per search | Concurrent goroutines, p99 latency measurement | pending |
| STR-004 | 10 concurrent sessions — filter map isolation, no cross-talk | 10 agents, verify each session only sees own traces | pending |
| STR-005 | Classification throughput benchmark — traces/sec through engine | Measure with/without model, with/without pattern matching | pending |
| STR-006 | 100 concurrent WebSocket connections — flow delivery, no dropped messages | Stress test gorilla/websocket under load | pending |
| STR-007 | Memory leak detection — 1000 attach/detach cycles | Verify RSS returns to baseline | pending |
| STR-008 | SQLite WAL pressure — 1M trace inserts, verify WAL doesn't grow unbounded | WAL checkpoint behavior under sustained write load | pending |

**Gate:** All stress tests pass. No memory leaks. P99 latency within bounds.

---

## PHASE 4: Deployment & Packaging

| ID | Task | Description | Status |
|---|---|---|---|
| DEP-001 | systemd unit file — rabbit-hole.service with eBPF capabilities | CAP_BPF, CAP_SYS_ADMIN, MemoryMax, Restart=always | pending |
| DEP-002 | install.sh — one-command install with model download | curl | bash install, optional systemd enable | pending |
| DEP-003 | Dockerfile — `docker run rabbit-hole serve` | Multi-stage build, eBPF capabilities, volume for data | pending |
| DEP-004 | Makefile completion — add bench, stress, integration, e2e targets | make bench, make stress, make integration, make e2e | pending |
| DEP-005 | Shell completion — bash, zsh, fish | cobra completion generation in install script | pending |
| DEP-006 | CHANGELOG.md — track versions and changes | Semantic versioning, keep-a-changelog format | pending |

**Gate:** External dev goes zero → `rabbit-hole attach --pid <PID>` → `rabbit-hole chat` < 5 min.

---

## PHASE 5: Production Hardening

| ID | Task | Description | Status |
|---|---|---|---|
| PROD-001 | Rate limiting — per-endpoint limits on search/chat/websocket | Token bucket, configurable limits, 429 responses | pending |
| PROD-002 | Authentication — API key or token for expression server | RABBITHOLE_API_KEY env var, 401 on all endpoints if set | pending |
| PROD-003 | Structured logging — JSON log format, request IDs, trace sampling | slog JSON handler with consistent field names | pending |
| PROD-004 | Metrics endpoint — Prometheus /metrics with collector/classifier/express stats | goroutine count, trace rate, classify latency, buffer drops, session count | pending |
| PROD-005 | Health endpoint depth — add classifier health, storage health, model loaded status | /health returns component-level status | pending |
| PROD-006 | Graceful shutdown — SIGTERM drains ring buffer, closes DB, stops server | Signal handler with 30s drain timeout | pending |
| PROD-007 | Crash recovery — WAL replay on restart, session state reconciliation | On start, mark running sessions as crashed, allow reattach | pending |
| PROD-008 | Config validation at startup — fail fast on invalid config | Validate all env vars, model path existence, port availability | pending |

**Gate:** Production deployment checklist complete. Metrics scrapeable. Graceful shutdown verified.

---

## PHASE 6: Documentation

| ID | Task | Description | Status |
|---|---|---|---|
| DOC-003 | Architecture Decision Records — key design decisions in docs/adr/ | ADR-001: Go+eBPF, ADR-002: SQLite over Postgres, ADR-003: Pluggable classifiers | pending |
| DOC-004 | API documentation — OpenAPI spec for expression server endpoints | openapi.yaml for all 9 endpoints | pending |
| DOC-005 | gRPC proto documentation — classifier.proto with full comments | Proto file with service/ message documentation | pending |
| DOC-006 | go doc comments — all exported types, interfaces, functions | godoc.org-quality comments everywhere | pending |
| DOC-007 | CONTRIBUTING.md — development setup, testing, PR process | Standard OSS contributing guide | pending |

---

## PHASE 7: E2E — Full Agent Session

| ID | Task | Description | Status |
|---|---|---|---|
| E2E-001 | E2E: serve → attach to test-agent → verify flows in search → detach | Smoke test the full pipeline end-to-end | pending |
| E2E-002 | E2E: chat query returns correct flows from real agent session | NL question → structured search → correct flows returned | pending |
| E2E-003 | E2E: WebSocket receives real-time flows during agent execution | Connect WS, agent runs, flows stream within 2s of classification | pending |
| E2E-004 | E2E: Remote classifier — edge node → central classifier → flows stored | Two rabbit-hole instances, one in classifier-only mode | pending |
| E2E-005 | E2E: Context window capture — enable → attach → verify windows stored | Full context window retrieval for LLM call flows | pending |
| E2E-006 | E2E: Agent crash detection — agent killed → session marked crashed | Verify session status transitions to 'crashed' within 5s | pending |

**Gate:** `rabbit-hole chat "what happened?"` after a real agent session returns accurate, complete answer with context windows.

---

## PHASE 8: DuckBrain — Project Memory

| ID | Task | Description | Status |
|---|---|---|---|
| DB-001 | Seed /project/rabbit-hole/architecture — three-layer design, component map | All 6 specs summarized into DuckBrain entries | pending |
| DB-002 | Seed /spec/rabbit-hole/collector — eBPF design decisions, pitfalls | eBPF-specific learnings for future foreman ticks | pending |
| DB-003 | Seed /spec/rabbit-hole/classifier — pluggable backend rationale | Why local + remote, when to use each | pending |
| DB-004 | Seed /spec/rabbit-hole/express — API design, chat model strategy | Endpoint rationale, stub→real migration path | pending |
| DB-005 | Seed /spec/rabbit-hole/storage — SQLite rationale, FTS5 design | Why SQLite over Postgres for self-hosted | pending |
| DB-006 | Seed /project/rabbit-hole/status — current phase, coverage, blockers | Living status entry for foreman context | pending |

---

## PHASE 9: Release

| ID | Task | Description | Status |
|---|---|---|---|
| REL-001 | GitHub/GitLab release with built binaries (linux/amd64, linux/arm64) | Tag v1.0.0, attach binaries to release | pending |
| REL-002 | All quality gates green: build, vet, test, lint, vulncheck, coverage | CI pipeline passes on all platforms | pending |
| REL-003 | External dev verification — zero → working harness < 5 min | Time a new developer through install → attach → chat | pending |

---

## Phase Gates Summary

| Phase | Tasks | Gate |
|---|---|---|
| P-1 | 6 specs | All specs at axiom level |
| P0 | 5 stubs | Zero "not implemented" in codebase |
| P1 | 7 coverage | All packages ≥60%, zero at 0% |
| P2 | 9 integration | Real eBPF, real Gemma, real gRPC |
| P3 | 8 stress | No leaks, P99 within bounds |
| P4 | 6 deployment | One-command install works |
| P5 | 8 hardening | Metrics scrapeable, graceful shutdown |
| P6 | 5 docs | godoc, OpenAPI, ADRs complete |
| P7 | 6 E2E | Full agent session capture verified |
| P8 | 6 DuckBrain | Foreman has context across ticks |
| P9 | 3 release | v1.0.0 shipped |

**Total: 63 tasks across 9 phases (plus 6 specs).** If the foreman reports idle, there's something wrong with the board.
