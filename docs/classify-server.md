# Rabbit-Hole classify-server — Reference gRPC Classifier Deployment

**Status:** Active
**Applies to:** `rabbit-hole classify-server` (the remote gRPC classifier)
**Protocol:** [classifier.v1](../api/proto/classifier/v1/classifier.proto)

This is the server side of the remote classification backend. A fleet or
centralized deployment runs `classify-server` on one or more hosts, and
`rabbit-hole serve` instances point at it with `--remote`. The server wraps
the **same pattern-matching catalog** as the local backend, so it works out
of the box — no model files, no custom code.

## Quick Start

```bash
# 1. Build the binary
make build

# 2. Run the classifier server (fleet/centralized side)
./bin/rabbit-hole classify-server --addr :50051
#    →  time=... msg="classify-server listening" addr=[::]:50051 service=classifier.v1.Classifier

# 3. Point a serve instance at it (edge side). --no-ebpf runs anywhere;
#    drop it on a host with eBPF for full telemetry.
./bin/rabbit-hole serve --no-ebpf --remote localhost:50051

# 4. Verify: /health shows the remote backend as healthy
curl -s http://127.0.0.1:9734/health | jq '.components.classifier'
#    →  { "status": "ok", "detail": "remote gRPC backend" }
```

`classify-server` blocks until SIGINT/SIGTERM, then drains in-flight RPCs
(`GracefulStop`) and exits.

## Token auth

Optional bearer-token auth on both sides — the token is passed as an
`authorization: Bearer <token>` metadata header on every RPC.

```bash
# Server requires the token:
./bin/rabbit-hole classify-server --addr :50051 --token s3cret

# Client presents it with the @<token> slot of --remote:
./bin/rabbit-hole serve --no-ebpf --remote localhost:50051@s3cret

# A client without the token is rejected with UNAUTHENTICATED.
```

## What the server implements

The `classifier.v1.Classifier` service, three RPCs:

| RPC | Behaviour |
|---|---|
| `Classify` | Runs each `TraceGroup` through the built-in pattern catalog (`file_read`, `file_write`, `git_operation`, `test_run`, …). Matched groups yield a high-confidence flow; unmatched groups yield a low-confidence `unknown` flow, mirroring the local pipeline's fallback. One flow per input group; empty batches return an empty response. |
| `Ping` | Empty response — used by serve's health-check loop. |
| `Info` | `name=rabbit-hole-pattern-classifier`, `kind=pattern`, `ready=true`, `device_type=cpu`, version from the binary. |

## Wire-format caveats

- **Args are space-joined on the wire.** `serve` serializes trace args as a
  single space-separated string; the server splits on whitespace. Filenames
  or arguments containing spaces lose fidelity.
- **Trace IDs are not on the wire.** The `Trace` message has no ID field, so
  flows returned by the reference server carry empty `trace_ids`.
- The reference server is **pattern-based only** (no Gemma). It is a
  working reference deployment, not a model-serving replacement.

## Deeper verification (optional)

```bash
# Direct gRPC ping via grpcurl:
grpcurl -plaintext localhost:50051 classifier.v1.Classifier/Ping
```

## Reference

- Proto definition: `api/proto/classifier/v1/classifier.proto`
- Server implementation: `internal/classify/server.go`
- Client: `internal/classify/remote.go` (`RemoteBackend`)
- Integration test (runs in the default `-short` suite):
  `internal/classify/server_integration_test.go`
