# AGENTS.md — Rabbit-Hole

Rabbit-Hole — go down the rabbit hole into your agent's decisions. A legibility layer between agents and humans.

**Org:** rabbit-hole/rabbit-hole ([gitlab.readydedis.com/rabbit-hole/rabbit-hole](https://gitlab.readydedis.com/rabbit-hole/rabbit-hole))

## Architecture

Three layers, one binary:

```
COLLECT (eBPF/strace) → CLASSIFY (pluggable: local Gemma or remote gRPC) → EXPRESS (chat)
```

- **Collect:** Kernel-level telemetry. Zero SDK. Works with closed-source agents.
- **Classify:** Pluggable backends — local Gemma (edge/air-gapped) or remote gRPC (fleet/centralized). Pattern matching catches 80% fast. Nobody else does this.
- **Express:** Chat interface. "What did helios do at 3am?" Context window retrieval.

## Competitive Gap

| Competitor | Does | Gap |
|---|---|---|
| AgentSight | eBPF (layer 1) | No classification, no chat, no context windows |
| AgentOps | SDK dashboards | SDK-dependent, no chat, no system-level telemetry |
| Arize Phoenix | OTel spans | No classification, no chat |
| groundcover | eBPF for K8s | K8s-only, enterprise |

**Rabbit-Hole gap:** Classification layer. Chat interface. Context windows. Agent-supervising-agent.

## Development

- **Language:** Go 1.26+
- **Build:** `make build` (Go binary)
- **Test:** `go test ./... -count=1 -short`
- **Lint:** `go vet ./...`
- **Foreman:** coding-hermes foreman (every 30m, deepseek-foreman PAYG)

## Specs

| # | File | Status |
|---|---|---|
| S01 | specs/01-Overview-Architecture.md | ✅ |
| S02 | specs/02-Collection-Layer.md | ✅ |
| S03 | specs/03-Classification-Layer.md | ✅ |
| S04 | specs/04-Expression-Layer.md | ✅ |
| S05 | specs/05-Storage-Data-Model.md | ✅ |
| S06 | specs/06-CLI-Self-Hosted.md | ✅ |

**Total: 6 specs, axiom-level.**

## Task Board

`.coding-hermes/board/tasks.jsonl` — spec-driven phased implementation.

## Foreman

- Model: `deepseek-v4-pro` on `deepseek-foreman` (PAYG)
- Skills: `coding-hermes-foreman`, `coding-hermes-cron`, `hilo-usage`, `gitreins`
- Toolsets: `["terminal","file","web","search","skills","memory"]`
- Worker spawn: `hermes chat -q` with prepaid bucket
- DuckBrain namespace: `rabbit-hole`
