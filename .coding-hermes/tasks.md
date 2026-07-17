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

## [x] S03-GAP-002 — Remote gRPC classifier backend (774e63b)
  - Spec S03 describes remote gRPC backend for centralized fleet deployment
  - Implemented: RemoteBackend struct, pb package, proto definition, --remote flag

## [x] S03-GAP-003 — ModelInfo struct completeness (774e63b)
  - Spec defines 9 fields (Name, Version, Kind, Ready, LoadedAt, MemoryMB, DeviceType, Endpoint, Latency)
  - Added missing Kind, Ready, Endpoint, Latency fields

## [x] S05-GAP-001 — Expose Storage interface as exported Go type (ec7ee7b)
  - All 14 methods exist on SQLiteStore
  - Storage interface only defined in classify/engine.go with just StoreFlows
  - ✅ Created internal/storage/storage.go with full Storage interface + compile-time check

## [x] CI-001 — Update Go version in CI workflow from 1.22 to 1.26
## [x] DOC-002 — Update AGENTS.md Go version from 1.22+ to 1.26
## [x] DOC-001 — Create README.md with build/run instructions (e6320e7)

## [x] CI — Check CI pipeline health, fix failing jobs
## [x] DOC — Verify documentation matches current code

## [ ] INFRA — Update Go toolchain from 1.26.0 to ≥1.26.5 ⚠️ BLOCKED — requires sudo
  - GO-2026-5856: crypto/tls ECH privacy leak (fixed in go1.26.5)
  - GO-2026-5039: net/textproto arbitrary input in errors (fixed in go1.26.4)
  - GO-2026-5037: crypto/x509 inefficient hostname parsing (fixed in go1.26.4)
  - All are standard library fixes — requires system Go update
  - ⚠️ Investigation 2026-07-17: System Go = 1.26.0 (apt), Snap Go = 1.26.4 (toolchain mismatch — compile 1.26.3 vs tool 1.26.4, can't build)
  - go install golang.org/dl/go1.26.5 blocked by security scanner
  - No apt upgrade available for golang-1.26-go
  - Resolution: Requires `sudo apt upgrade golang-1.26-go` to ≥1.26.5 — manual intervention needed

## [x] GAP — Track real server uptime in /health endpoint (e7ab85e)
  - internal/express/handlers.go:18: hardcoded "0s" with TODO
  - ✅ Added startTime field to Server struct, time.Since() in health handler
  - Commit: e7ab85e
