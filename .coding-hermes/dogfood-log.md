# Dogfood Log

## 2026-08-08 — rabbit-hole — 🔴 DOES-NOT-DELIVER (core Collect promise) / shell is real

**Promise:** *"A user can attach to a real agent process (zero SDK, eBPF), have Rabbit-Hole
collect kernel-level telemetry, classify it, and ask 'what did the agent do at 3am?' in plain
language — self-hosted, one binary."*

**Reality:** Express/Storage layers genuinely work (demo stream, FTS search, dashboard SPA,
metrics, WebSocket live flows, stub+real chat, persistence across restarts, honest eBPF
preflight — all verified live). But the Collect layer is unreachable: `attach` prints success
and exits, persisting nothing (session in no DB; `detach` → "session not found"); `serve` has
no way to start a session; the demo stream is the only flow source in the binary. Chat cannot
answer time-based NL questions (filters dropped + no time range in store). OpenAPI spec
contradicts the live wire format for Flow/ChatResponse.

**Time-to-first-success (demo mode):** ~3 min. **Friction count:** 8 (attach theater, detach
failure, NL chat failure x3, jq nulls from docs, stale :8080 hint, /dashboard 301).

**Top 3 findings (board tasks):**
1. DF-001 (P0) — attach/list/detach session lifecycle is per-process ephemeral; real telemetry
   can never reach the DB.
2. DF-002 (P1) — chat drops translated filters, no time range → "what did the agent do in the
   last hour?" returns canned no-match while 140+ flows are live.
3. DF-003 (P2) — OpenAPI "authoritative" spec + integration.md show lowercase flow fields;
   API returns PascalCase → spec-following consumers get nulls.

**Also:** DF-004 (stale :8080 in demo output); GAP-006 (/dashboard 301) re-confirmed open.

**Left behind:** docs/dogfood/2026-08-08-integration.md, docs/dogfood/diagnostics.md,
skills/rabbit-hole-usage/SKILL.md, 4 board tasks (DF-001..004). Foreman not paused (cooldown
7200s) — will pick up tasks next tick.
