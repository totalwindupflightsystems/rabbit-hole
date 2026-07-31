# Changelog

All notable changes to Rabbit-Hole will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-07-31

### Added

- Three-layer agent legibility platform: COLLECT (eBPF) → CLASSIFY (pluggable) → EXPRESS (chat)
- eBPF-based process attachment for zero-SDK kernel-level telemetry collection
- Pluggable classification backends: local Gemma 3 4B via Ollama REST API and remote gRPC
- Pattern-matching fast path for 80% of classifications without model inference
- Natural-language chat interface with NL-to-structured-search query translation
- SQLite storage with FTS5 full-text search for flow queries
- Expression server with WebSocket real-time flow streaming
- Context window capture for LLM call inspection
- Ring buffer overflow protection with oldest-drop semantics
- TLS interception for HTTPS-making process attachment
- Remote gRPC classifier backend support (classifier-only mode)
- Full server lifecycle integration (health → attach → search → chat → shutdown)
- Stress testing: ring buffer overflow, 100K batch insert, 100 concurrent FTS5 searches, 10 concurrent sessions, classification throughput benchmarks, 100 concurrent WebSocket connections, 1000-cycle memory leak detection, 1M-insert WAL pressure test
- Deployment: systemd unit file with eBPF capabilities, install.sh one-command installer, multi-stage Dockerfile
- Makefile targets: bench, stress, integration, e2e
- Shell completion for bash, zsh, and fish
- Release binaries for linux/amd64 and linux/arm64 (arm64 eBPF bindings via bpf2go -target arm64)
- CONTRIBUTING.md with development setup and PR guide
- Package doc comments on all Go source files
- Benchmark suite: parseTraceEvent, ring buffer ops, pattern matching, classification pipeline, FTS5 search, batch inserts

### Changed

- Upgraded cobra from v1.9.1 to v1.10.2
- Upgraded modernc.org/sqlite from v1.35.0 to v1.54.0

### Fixed

- Replaced hand-written protobuf stubs with generated code from classifier.proto
- Replaced stub eBPF bindings with bpf2go-generated Go bindings
- Replaced stub chat model with real LLM-backed NL query translation
- Wired Gemma model loading via Ollama REST API (was no-op returning confidence=0)
- Replaced stub ModelInfo with real model metadata

### Infrastructure

- CI/CD: GitLab pipeline configured (runners currently offline — INFRA issue)
- Host thread exhaustion on parallel test runs (pids.max=512; individual package tests pass)
