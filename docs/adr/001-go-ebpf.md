# ADR-001: Go + eBPF for Agent Legibility

**Status:** Accepted  
**Date:** 2026-07-13  
**Deciders:** Bane (Alexis Okuwa)

## Context

Rabbit-Hole needs to observe agent processes — closed-source, third-party binaries like `hermes`, `codex`, `claude-code` — and make their decision-making legible. The fundamental constraint: we cannot modify the agent. We cannot add an SDK. We must observe from the outside using what the kernel already sees.

## Decision

**Language: Go.** **Collection mechanism: eBPF.**

Go was chosen because:

1. **eBPF ecosystem lives in Go.** The `cilium/ebpf` library is the gold standard for userspace eBPF tooling — pure Go, no CGo, no libbpf dependency. It provides program loading, map management, ring buffer readers, and BTF support out of the box.

2. **Single binary deployment.** The entire platform compiles to one ~1.2MB stripped ELF binary. No runtime, no virtual environment, no package manager. Copy the binary to a server and run it. This is essential for the self-hosted deployment model.

3. **Concurrency model matches the problem.** The three layers (collect, classify, express) run as concurrent goroutines communicating over channels. Go's CSP model maps directly to the data flow: eBPF events → ring buffer → classification pipeline → storage → HTTP/WS.

4. **Compiled, fast, low-overhead.** Running alongside the agent process, Rabbit-Hole must not meaningfully impact agent performance. Go has a sub-millisecond GC pause and near-zero allocation overhead on hot paths.

eBPF was chosen because:

1. **The kernel sees everything.** Every syscall, file operation, network connection, and TLS handshake passes through the kernel. eBPF programs attach to tracepoints and uprobes, capturing events without modifying the target process.

2. **Zero SDK.** Unlike AgentOps or Arize Phoenix, Rabbit-Hole doesn't require the agent to import a library, wrap API calls, or emit spans. It works with closed-source agents, interpreted scripts, and any ELF binary.

3. **Production-grade.** eBPF is used by Cilium, Falco, and Pixie in production at scale. The verifier guarantees safety — eBPF programs can't crash the kernel.

## Alternatives Considered

### strace (ptrace)

- **Rejected:** `ptrace` is slow (20-50x overhead per syscall), requires `SYS_PTRACE` capability, and breaks when the agent is already being traced. eBPF has sub-1% overhead and works without stopping the target.

### Rust + Aya

- **Rejected:** Aya is an excellent eBPF library for Rust, but the broader ecosystem (HTTP servers, SQLite drivers, WebSocket libraries) is more mature and battle-tested in Go. The `cilium/ebpf` + `net/http` + `modernc.org/sqlite` stack in Go is fully self-contained.

### Python (bcc/bpftrace)

- **Rejected:** Python requires a runtime, has higher overhead, and can't compile to a single binary. bcc depends on LLVM/clang at runtime for BPF compilation — an unacceptable deployment burden.

## Consequences

**Positive:**
- One binary, one config file, one SQLite database. Deploy in seconds.
- eBPF programs are verified by the kernel before loading — safe by construction.
- Graceful degradation: when eBPF isn't available (kernel < 5.8, missing capabilities), the collector enters degraded mode with a clear error message.

**Negative:**
- eBPF requires kernel 5.8+ and `CAP_BPF`/`CAP_SYS_ADMIN`. Older kernels or restricted containers need fallback options (future: strace fallback).
- BPF CO-RE (Compile Once, Run Everywhere) requires BTF-enabled kernels. Without BTF, the eBPF programs need kernel-headers at build time.
- Go's GC can interfere with eBPF ring buffer latency at very high throughput (>500K events/sec). Acceptable for agent monitoring workloads (typically <10K events/sec).
