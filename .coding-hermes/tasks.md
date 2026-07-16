# Task Board — rabbit-hole

## [x] INIT — Set up DuckBrain namespace, GitReins tasks, Hilo .vfs/
## [x] SPEC — Audit specs vs implementation, queue gap tasks
### ✓ S01: Architecture overview — matches codebase
### ✓ S02: Collection layer (eBPF/cilium/ebpf) — implemented
### ✓ S03: Classification Engine + pattern matching — implemented; pluggable backend interface missing
### ✓ S04: Expression layer (HTTP+WS+Chat) — fully implemented
### ✓ S05: Storage (SQLite+WAL+FTS5) — all 14 methods on SQLiteStore; interface not exposed
### ✓ S06: CLI (9 cobra subcommands) — fully implemented
### ⬜ Gaps queued below

## [x] S03-GAP-001 — ClassificationBackend pluggable interface (6dc605e)
  - Spec S03 defines ClassificationBackend interface (Classify/Health/Info/Close)
  - Current code has hardcoded `*GemmaModel` field — no backend abstraction
  - Need: interface definition, LocalBackend wrapper, move GemmaModel behind it

## [ ] S03-GAP-002 — Remote gRPC classifier backend
  - Spec S03 describes remote gRPC backend for centralized fleet deployment
  - Not implemented at all

## [ ] S03-GAP-003 — ModelInfo struct completeness
  - Spec defines 9 fields (Name, Version, Kind, Ready, LoadedAt, MemoryMB, DeviceType, Endpoint, Latency)
  - Code has 5: missing Kind, Ready, Endpoint, Latency

## [ ] S05-GAP-001 — Expose Storage interface as exported Go type
  - All 14 methods exist on SQLiteStore
  - Storage interface only defined in classify/engine.go with just StoreFlows

## [x] CI-001 — Update Go version in CI workflow from 1.22 to 1.26
## [x] DOC-002 — Update AGENTS.md Go version from 1.22+ to 1.26
## [ ] DOC-001 — Create README.md with build/run instructions

## [x] CI — Check CI pipeline health, fix failing jobs
## [x] DOC — Verify documentation matches current code
