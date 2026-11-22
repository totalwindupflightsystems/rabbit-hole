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

One documented exception (DF-RABBIT-HOLE-013): the no-race stress SLO tests —
`TestStress_100KTraceInsert`, `TestStress_ConcurrentFTS5Search` and
`TestStress_WALPressure` in `internal/storage` — skip themselves when the
binary is built with `-race` (the `raceEnabled` build-tag constant in
`internal/storage/race_on.go` / `race_off.go`). Their wall-clock budgets are
no-race SLOs whose enforcement home is `make test-slo`; skipping them in the
`-race` gate is the documented gate contract, not gaming — the budgets stay
live and still fail a latency regression in `make test-slo`.
`TestStress_ConcurrentSessionIsolation` asserts per-session isolation, not
latency, so it runs in both modes.

## Timing SLOs and the per-host budget

The stress tests in the module, and what each actually asserts:

| Test | Package | Budget / assertion | Written for |
| --- | --- | --- | --- |
| `TestStress_100KTraceInsert` | `internal/storage` | 100k traces < 30 s | no `-race` |
| `TestStress_ConcurrentFTS5Search` | `internal/storage` | p99 < 50 ms | no `-race` |
| `TestStress_ConcurrentSessionIsolation` | `internal/storage` | none (asserts per-session isolation, not latency) | any mode |
| `TestStress_WALPressure` | `internal/storage` | none, but it must finish inside the 10 m package timeout | no `-race` |
| `TestStress_MemoryLeak` | `internal/attach` | RSS growth after 1000 attach/detach cycles — fails on **either** ceiling: >10% of baseline or >50 MB (DF-RABBIT-HOLE-010) | any mode |
| `TestStress_MemoryLeakParallel` | `internal/attach` | heap ceiling plus the same RSS gate (not time) | any mode |
| `TestStress_ConcurrentWebSocket` | `internal/express` | none (100 concurrent connections, no dropped messages or cross-talk) | any mode |

**The wall-clock budgets are per-host budgets measured without `-race`.** They
are latency *regression* tripwires for a single package on a quiet host, not
promises that the same workload stays inside them while `-race` instrumentation
and the rest of the module are running.

`-race` is not a small tax on these tests: it turns the SQLite-heavy insert and
search paths over almost every operation, and the measured cost is 20–55x.
They therefore cannot hold their budgets inside `make test-all`, on any host.
`test-all` does not pretend otherwise: the three budgeted stress tests skip
themselves under `-race` (the exception above), so the SKIP lines for them are
expected gate output — not lost coverage — and a `test-all` red that consists
only of those tests cannot happen anymore; if one does, the skip guard was
bypassed. The budgets are enforced in the mode they were written for:

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
`make test-all` (≈6 s), because its stress tests assert heap and RSS ceilings
instead of wall-clock time.

### The RSS ceiling in `internal/attach` (DF-RABBIT-HOLE-010)

`TestStress_MemoryLeak` and `TestStress_MemoryLeakParallel` fail when RSS growth
exceeds **either** ceiling:

- **10% of the measured baseline** (`maxRSSGrowthPct`) — the binding ceiling
  below a 500 MB baseline, which is every configuration these tests run in
  (measured baselines: ~18 MB plain, ~143–158 MB under `-race`, so 1.8–16 MB of
  headroom). A per-session or per-cycle leak grows with the footprint and lands
  here.
- **50 MB absolute** (`maxRSSGrowthBytes`) — the binding ceiling above a 500 MB
  baseline, where 10% would be the looser arm.

Both arms are checked and each reports itself, so the effective allowance is the
*smaller* of the two. The gate used to AND them, i.e. require growth > 50 MB and
> 10% simultaneously. At an 18–40 MB baseline the 50 MB arm alone is already
> > 125% growth, so the pair was unreachable and the tripwire never fired.

Two measurement details are part of the rule, not decoration:

- **The fixture's ledger is dropped before each reading.** The mock collector and
  mock store record every session they see (~17 KB per cycle, ~17 MB of heap per
  1000 cycles). That is the test's own bookkeeping, not memory the attach/detach
  path holds, and it would swamp a 10% ceiling.
- **`debug.FreeOSMemory` runs before each reading.** RSS above `HeapAlloc` is
  memory the runtime has freed but not yet returned; read naively, a clean
  1000-cycle run "grows" 25–37% and the 10% arm would fire on a clean tree. What
  is measured is memory the process cannot release — memory held by a real leak
  is live and still counted.

A **3000-cycle warm-up precedes the measured 1000 cycles** and asserts nothing.
The allocator's arenas/stacks and, above all, the race detector's shadow memory
are one-time costs that would otherwise be charged to the measured phase: the
shadow is mapped lazily per touched address, it added ~34 MB over the first 1000
cycles in an isolated `-race` run and ~115 MB over 3000 cycles with the rest of
the package ahead of it, and it is never returned to the OS. With the shorter
warm-up the 10% arm fires on a clean tree.

Red-proof for the gate, recorded on the DF-RABBIT-HOLE-010 completion event:
holding 40 MB of touched anonymous memory across the measured phase — 40 MB is
above 10% of the baseline and below the 50 MB arm — fails the percentage arm at
+40.1 MB / 210.8% (plain, heap delta 5.3 KB) and +40.9 MB / 28.6% (`-race`, heap
delta 0), while the clean run stays at 0.0–0.1% inside the package and at
1.2–2.3% when the test runs alone.

### Triaging an SLO failure

1. A wall-clock budget breach can only come from the calibrated mode: under
   `-race` the budgeted stress tests skip themselves (the exception above),
   so `make test-all` no longer reports one. Re-run the stress set without
   `-race`:

   ```bash
   make test-slo
   ```

   On revisions older than DF-RABBIT-HOLE-013 — or in a manual `-race` run
   with the skips bypassed — a budget breach there still means the same
   thing it always did: mode mismatch until proven otherwise.

2. If it passes there, the failure was mode/contention (see the
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
