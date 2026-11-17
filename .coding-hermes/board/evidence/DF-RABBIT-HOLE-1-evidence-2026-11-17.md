# DF-RABBIT-HOLE-1 — evidence pack (tick rabbit-hole-foreman-2026-11-17-08-25-38)

Tree: `24f4dc5` (merged main, includes the DF-RABBIT-HOLE-010 OR-gate landing `eed2aae`)
Host: 16 cores, go1.26.5, load average **18.15 / 25.40 / 30.36** (1/5/15 min) during the run.
Raw logs in this directory:

| File | What it is |
| --- | --- |
| `DF-RABBIT-HOLE-1-race-targeted-2026-11-17.log` | the AC's exact targeted `-race` command, rc=0 |
| `DF-RABBIT-HOLE-1-make-test-all-2026-11-17.log` | full `make test-all` (`go test ./... -count=1 -race`) transcript |
| `DF-RABBIT-HOLE-1-judge-fail-7782a7d9-2026-11-17.log` | the first judge run (FAIL, for the record: it could not find this pack) |

## 1. Race half — PASS

```
$ go test -race -count=1 ./internal/attach/ \
    -run 'TestStress_MemoryLeakParallel|TestSessionMonitor_NaturalExit_Completes|TestSessionMonitor_SignalDeath_Crashed'
ok  gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/attach  3.802s   # rc=0
```

`grep -c 'WARNING: DATA RACE'` over that log = **0**. The whole package under `-race`
(`go test ./internal/attach/ -count=1 -race`) is also green: 6.222s, rc=0.

The three races the QA rig reported are fixed by `cc4c4d1` (merged `c35edc2`):
`processMonitor.finalize` serializes `pidfdWatch.close()`/`exitInfo()` on `w.mu`, and the
attach/monitor fixtures clone under the lock.

## 2. Timing half — per-host budget recorded in the docs

`docs/testing.md` (sections "Timing SLOs and the per-host budget" and "Measured on this
host") records every `TestStress_*` budget, the fact that the wall-clock budgets were
calibrated **without** `-race`, the measured `-race` inflation (20–55x), and the
`make test-slo` target that runs the storage stress set in the mode it was written for.
`Makefile:23` is `test-all: go test ./... -count=1 -race`; `Makefile:28` is
`test-slo: go test ./internal/storage/ -count=1 -run '^TestStress_' -timeout 15m`.

## 3. Full `make test-all` table (this host, this load)

| Package | Result | Duration |
| --- | --- | --- |
| api/proto/classifier/v1 | ok | 1.086s |
| cmd/rabbit-hole | ok | 70.789s |
| internal/attach | **ok** | 6.733s |
| internal/classify | ok | 1.115s |
| internal/classify/pb | no test files | — |
| internal/collector | ok | 1.866s |
| internal/config | ok | 1.127s |
| internal/demo | ok | 1.119s |
| internal/express | ok | 23.004s |
| internal/storage | **FAIL** | 600.040s (package timeout) |
| pkg/types | ok | 1.028s |
| web | no test files | — |

`WARNING: DATA RACE` lines in the whole run: **0**.

The only red is `internal/storage`, and only on the three stress tests whose budgets are
wall-clock and were calibrated without `-race`:

| Test | Measured under `-race` on this host | Budget (no `-race`) |
| --- | --- | --- |
| `TestStress_100KTraceInsert` | 86.41s | < 30s |
| `TestStress_ConcurrentFTS5Search` | 1.16s wall (p99 assertion) | p99 < 50ms |
| `TestStress_WALPressure` | still running at 8m29s → `panic: test timed out after 10m0s` | must finish inside the 10m package timeout |

These numbers are this host's (load 18–30), not the quieter host/load the table in
`docs/testing.md:53-59` was sampled at (that row: 59.9s insert / 542ms FTS5 p99 / >10m
WALPressure at load 8–20). Both show the same shape: an order-of-magnitude `-race` cost
that the no-`race` budgets cannot absorb, which is exactly the mode mismatch
`docs/testing.md:35-49` documents.

**Consequence, stated plainly:** `make test-all` still exits non-zero — it always will
while those three tests keep their no-`race` wall-clock budgets inside the `-race`
target. No test was deleted, skipped or time-boxed to change that. Whether the gate
itself should change shape (race-mode skip guard, or moving the stress set out of
`go test ./...` and documenting `test-all` + `test-slo` as the gate pair) is filed as
DF-RABBIT-HOLE-013 — it is a gate-design decision, not a race or SLO regression.
