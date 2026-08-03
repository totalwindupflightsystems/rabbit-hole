# S07 — Web Dashboard & Dogfooding

**Status:** ✅ Implemented (2026-08-02)
**Spec type:** Axiom-level (interface + behavior complete)

## 1. Goal

Give Rabbit-Hole a first-class, self-hosted web dashboard (New Relic-inspired
trace explorer) and a dogfooding path that works **without root/eBPF**, so the
project can be demonstrated and exercised end-to-end on any machine.

## 2. Dogfooding (no-root path)

Two mechanisms, both seeded from the `demo` generator:

### 2.1 `rabbit-hole demo` — batch seed

| Flag | Default | Meaning |
|------|---------|---------|
| `--flows N` | 48 | number of flows to generate |
| `--hours-back H` | 3 | session start offset |

Creates one session (`AgentName=hermes`, plausible command line) and writes:

- `N` flows with realistic intents: `read_file`, `search_web`, `llm_api_call`,
  `patch_code`, `run_tests`, `git_commit`, `write_file`, `network_call`,
  `classify_traces`, `memory_write`
- Flow phases distributed across observation/deliberation/action/verification
- Outcomes weighted toward success with a realistic failure/timeout tail
- Per-flow step metadata (`steps[]`) so the dashboard waterfall renders
- ~4 synthetic syscall traces per flow (category `syscall`)
- Context windows on `llm_api_call` and `patch_code` flows

Flow IDs and session IDs are unique per run (seed-tagged) so re-seeding into
the same DB never collides.

### 2.2 `rabbit-hole serve --demo-stream` — live feed

| Flag | Default | Meaning |
|------|---------|---------|
| `--demo-stream` | off | publish one demo flow every N seconds |
| `--demo-every N` | 3 | seconds between flows |

Stores the flow, then calls `Server.PublishFlow` so WebSocket subscribers
(the dashboard Live tab) receive it in real time. This is the dogfood path:
open `/dashboard#live`, connect to the demo session, watch the agent work.

## 3. Dashboard

### 3.1 Delivery

- Single-file SPA: `web/dashboard/index.html` (inline CSS/JS, zero build step)
- Embedded into the binary via `go:embed` (package `web`)
- Served at `GET /dashboard` (redirects to `/dashboard/`) by the express server
- No new runtime dependencies; reads existing REST + WebSocket endpoints

### 3.2 Views

| View | Route | Contents |
|------|-------|----------|
| Overview | `/dashboard` | stat cards (flows/traces/sessions/avg duration + error rate), hourly activity bar chart (last 24h), phase breakdown, outcome breakdown, top intents, recent flows table |
| Traces & Flows | `#flows` | filter bar (FTS search, session, phase, outcome) + sortable table (time, intent, phase, outcome, duration, confidence, description) |
| Live Stream | `#live` | session picker + WebSocket table; new flows prepend in real time with a toast notification |

### 3.3 Flow detail drawer

Clicking any flow row (any view) opens a right-hand drawer with:

- Key/value block: id, session, intent, phase, outcome, confidence, start,
  duration, trace count, description
- Waterfall: step bars rendered from `metadata.steps` with start offsets and
  durations
- Context window: fetched from `/api/v1/flows/{id}/context-window`, JSON-formatted

### 3.4 API

`GET /api/v1/dashboard/summary` — aggregates for the overview page:

```json
{
  "total_flows": 141, "total_sessions": 4, "total_traces": 360,
  "error_count": 8, "avg_duration_ns": 929400000,
  "by_phase": {"observation": 47, "deliberation": 29, "action": 51, "verification": 14},
  "by_outcome": {"success": 133, "failure": 4, "timeout": 4},
  "top_intents": [{"intent": "classify_traces", "count": 18}],
  "hourly": [{"hour": "2026-08-02T21:00:00Z", "count": 60}],
  "recent_flows": [12 most recent flows]
}
```

Implementation: `storage.DashboardSummary(ctx)` — one aggregate pass over the
flows table (totals, error count, AVG duration), plus phase/outcome/intent
GROUP BYs, a 24h hourly GROUP BY on `start_time`, and a recent-flows query.

### 3.5 Styling

New Relic-inspired dark theme: near-black background (`#0b0e14`), panel
surfaces, teal accent (`#00c8b3`), per-phase colors (observation=blue,
deliberation=purple, action=teal, verification=pink), per-outcome pills
(green/red/amber/gray). Monospace numerals. Sticky table headers.

## 4. Acceptance Criteria

- [x] `rabbit-hole demo` seeds a session with flows + traces + context windows, idempotent across runs
- [x] `rabbit-hole serve --demo-stream` publishes live flows over WebSocket
- [x] `GET /dashboard/` serves the SPA (200, non-empty)
- [x] `GET /api/v1/dashboard/summary` returns aggregates (200)
- [x] Overview renders stat cards, hourly chart, phase/outcome breakdowns, top intents, recent flows
- [x] Traces & Flows view filters by search/session/phase/outcome
- [x] Flow detail drawer shows KV + waterfall + context window
- [x] Live view receives WebSocket flows in real time
- [x] `go build ./...`, `go vet ./...`, full test suite pass
- [x] Single binary: dashboard embedded, no external assets

## 5. Files

| File | Change |
|------|--------|
| `web/dashboard/index.html` | new — SPA (26KB, inline CSS/JS) |
| `web/web.go` | new — go:embed host package |
| `internal/express/dashboard.go` | new — routes + summary handler |
| `internal/storage/dashboard.go` | new — DashboardSummary aggregates |
| `internal/demo/demo.go` | new — scenario generator |
| `cmd/rabbit-hole/demo.go` | new — `rabbit-hole demo` command |
| `cmd/rabbit-hole/serve.go` | + — `--demo-stream` / `--demo-every` flags |
| `cmd/rabbit-hole/main.go` | + — register demo command |
| `internal/express/server.go` | + — wire dashboardRoutes() |
