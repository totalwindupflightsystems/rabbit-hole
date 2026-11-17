# Testing Guide

**Status:** Active
**Applies to:** every package (`go test ./...`)

## Running the suite

```bash
make test-all   # go test ./... -count=1 -race     <- the gate
make test-slo   # stress SLO budgets, NO -race      <- see below
make lint       # go vet ./...  (note: this is a vet pass, not golangci-lint)
```

`-race` is mandatory for `test-all`. A green run means zero `WARNING: DATA
RACE` lines; exit status alone is not enough, because a racing test can still
pass.

Do not delete, skip (`t.Skip`) or time-box a test to make the suite green.
Fix the code or the test fixture instead.

## Timing SLOs and the per-host budget

The stress tests in the module, and what each actually asserts:

| Test | Package | Budget / assertion | Written for |
| --- | --- | --- | --- |
| `TestStress_100KTraceInsert` | `internal/storage` | 100k traces < 30 s | no `-race` |
| `TestStress_ConcurrentFTS5Search` | `internal/storage` | p99 < 50 ms | no `-race` |
| `TestStress_ConcurrentSessionIsolation` | `internal/storage` | none (asserts per-session isolation, not latency) | any mode |
| `TestStress_WALPressure` | `internal/storage` | none, but it must finish inside the 10 m package timeout | no `-race` |
| `TestStress_MemoryLeak` | `internal/attach` | intended: RSS growth ceiling after 1000 attach/detach cycles — **not currently enforced**, see DF-RABBIT-HOLE-10 | any mode |
| `TestStress_MemoryLeakParallel` | `internal/attach` | heap ceiling (not time) | any mode |
| `TestStress_ConcurrentWebSocket` | `internal/express` | none (100 concurrent connections, no dropped messages or cross-talk) | any mode |

**The wall-clock budgets are per-host budgets measured without `-race`.** They
are latency *regression* tripwires for a single package on a quiet host, not
promises that the same workload stays inside them while `-race` instrumentation
and the rest of the module are running.

`-race` is not a small tax on these tests: it turns the SQLite-heavy insert and
search paths over almost every operation, and the measured cost is 20–55x.
They therefore cannot hold their budgets inside `make test-all`, on any host,
and a `test-all` red that consists only of these three tests is a mode
mismatch — not a latency regression. Check them in the mode they were written
for:

```bash
make test-slo
```

### Measured on this host (16 cores, `go1.26.5`)

| Scenario | Mode | Host load | 100k insert | FTS5 p99 | WALPressure | Verdict |
| --- | --- | --- | --- | --- | --- | --- |
| `internal/storage` stress set alone | no `-race` | 6 | 1.09 s | pass (< 50 ms) | 15.3 s | PASS |
| `TestStress_ConcurrentFTS5Search` alone, QA rig (clean clone) | no `-race` | 6–9 | — | 15.9 ms | — | PASS |
| `TestStress_ConcurrentFTS5Search` alone | no `-race` | ~32 | — | 23.6 ms | — | PASS |
| `internal/storage` alone | `-race` | 8–20 | 59.9 s | 542 ms | > 10 m (package timeout) | FAIL |
| `internal/storage` alone on a busy bunker | no `-race` | busy | — | 55.8 ms | — | FAIL |

`internal/attach` (the package that holds the real data races fixed under
DF-RABBIT-HOLE-1) is green under `-race` both alone (≈6 s) and inside
`make test-all` (≈6 s), because its stress tests assert a heap ceiling instead
of wall-clock time. Note that only the *heap* ceiling is enforced:
`TestStress_MemoryLeak`'s RSS check currently passes when just one of its two
thresholds is exceeded (DF-RABBIT-HOLE-10), so do not read the RSS number it
logs as a gate.

### Triaging an SLO failure

1. If `make test-all` reports a budget breach, re-run the stress set in its
   calibrated mode:

   ```bash
   make test-slo
   ```

2. If it passes there, the `test-all` failure was mode/contention (see the
   table above), not a latency regression. Record the load average and the
   measured numbers in the report instead of editing the budget.
3. Only treat it as a regression if it also breaches in `make test-slo` on an
   idle host, or if p50/p90 move up with the tail (a real slowdown shifts the
   whole distribution; instrumentation and contention inflate the tail first).

Raising a budget, or gating it on `-race`, is a last resort and needs the same
evidence attached.

## Race discipline

Shared fixtures in `_test.go` files must obey the same rules as production
code, because `-race` instruments them too:

- A store mock must not hand out the pointer it keeps. Clone on write and clone
  on read, otherwise a caller that dereferences the returned struct after the
  mock's mutex is released is racing the background goroutine that updates the
  record. A real SQLite store serialises through the database; a map-based mock
  has to do it explicitly.
- Derive generated IDs inside the lock, not from `len(map)` outside it, when a
  test drives the API from several goroutines.
- One record must never be reachable through two different mutexes. In
  `internal/attach` the collector mock and the storage mock each guard their own
  map; sharing one `*types.Session` between them is a race no single mutex can
  fix.
