# ADR-003: Pluggable Classification Backends

**Status:** Accepted  
**Date:** 2026-07-21  
**Deciders:** Bane (Alexis Okuwa)

## Context

Rabbit-Hole classifies raw eBPF trace events into structured, human-readable flows ("process opened file X", "LLM API call to provider Y took Zms"). The classification layer sits between raw kernel events and the expression server. It must work across two fundamentally different deployment topologies:

1. **Edge / single-machine:** The user runs Rabbit-Hole on their own workstation or a dev server. They want zero external dependencies — no cloud API keys, no network egress. Everything runs locally.

2. **Fleet / centralized:** An organization runs Rabbit-Hole collectors on many agent hosts, forwarding classified flows to a central analysis server. Classification runs on the central server for consistency and because edge nodes may not have GPU access.

## Decision

**Pluggable backends with a shared `Classifier` interface.** Two backends shipped:

1. **Local Gemma (`gemma` backend):** Runs Google Gemma 3 4B locally via Ollama's REST API. Selected because Gemma is open-weight, runs on CPU (no GPU required), and is small enough (4B parameters) to run alongside the agent without starving it of resources. The `classify/` package manages model lifecycle (pull, load, health check) and inference (natural language translation of trace events into flow descriptions). Pattern matching serves as a fast pre-filter — known patterns (syscall sequences, TLS handshake markers, HTTP method/URL pairs) catch ~80% of events without hitting the model.

2. **Remote gRPC (`remote` backend):** Connects to a separate `rabbit-hole serve --classifier-only` instance via gRPC. Sends raw trace batches, receives classified flows. Uses the same protobuf schema (`classifier.proto`) for both local and remote paths — the `classify/pb/` package is shared. The remote backend is stateless: it streams trace events, receives flow classifications, and stores them locally. If the remote classifier is unreachable, traces buffer in the ring buffer until connectivity resumes.

**Why two backends, not one:** The local backend covers the "one binary, zero config" self-hosted promise. The remote backend covers the fleet use case where a central GPU node classifies for many edge collectors. A single backend (local-only) breaks the fleet case. A single backend (remote-only) breaks the self-hosted case.

**Why not more backends:** Two is the right number for v1. Every additional backend adds a combinatorial test matrix (local × remote × model versions). A third backend (cloud-hosted LLM API, e.g. OpenAI) is a natural v2 addition but adds an API key dependency that contradicts the self-hosted principle.

## Alternatives Considered

### Remote-only (gRPC required)

- **Rejected.** Violates the "one binary, one command" self-hosted promise. A user running `rabbit-hole serve` on their laptop should not need to also run a separate classifier server. The most common deployment is a single developer observing their own agent — forcing a two-process architecture for that case is user-hostile.

### Local-only (no gRPC)

- **Rejected.** Blocks the fleet/centralized use case. Organizations running dozens of agent hosts want centralized classification for consistency and cost (one GPU, not dozens). The remote backend is gated behind a CLI flag (`--classifier-remote`) — it adds no complexity for the local-only user.

### Model-agnostic provider interface (OpenAI, Anthropic, etc.)

- **Rejected for v1.** A generic LLM provider interface (OpenAI-compatible API) would let users bring any model, but it adds: API key management, rate limit handling, cost tracking, and provider-specific error semantics. This is scope creep for v1. The remote gRPC backend is the escape hatch — if someone wants OpenAI classification, they run a `rabbit-hole serve --classifier-only` behind a proxy that calls OpenAI.

### No classification — raw trace search only

- **Rejected.** Raw trace events ("pid 12345 called read(3, 0x7fff, 4096)") are illegible to humans. The entire value proposition of Rabbit-Hole is classification — turning syscall traces into natural language flows. Without classification, the product is a fancy `strace` with a web UI.

## Consequences

**Positive:**
- Self-hosted users get classification with zero config (`gemma` backend, default)
- Fleet users get centralized classification with one extra command (`--classifier-remote grpc://central:9090`)
- Both backends share the same protobuf schema — swapping backends requires no data migration
- Pattern matching provides fallback classification when the model is loading or unreachable

**Negative:**
- Two backends means two code paths to test and maintain
- Gemma model download (~2.5GB) adds to first-run time
- gRPC adds a protocol dependency (though protobuf codegen is committed, not runtime-generated)
- Ollama must be installed separately for local classification (not bundled)

## Implementation Notes

- `internal/classify/classifier.go` defines the `Classifier` interface: `Classify(ctx, traces) ([]Flow, error)`, `Health() Status`, `Close() error`
- `internal/classify/gemma.go` implements local Ollama REST backend
- `internal/classify/remote.go` implements gRPC client backend
- Backend selection in `config.go`: `classifier.backend` (default `gemma`, also accepts `remote`)
- Pattern matching lives in `internal/classify/patterns.go` — runs before model inference, shared by both backends
- Protobuf schema at `api/proto/classifier/v1/classifier.proto`
