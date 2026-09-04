
## Dogfood Findings (2026-09-04)
Verdict: PROMISING-BUT-ROUGH
Promise: {"entry_point":"Single CLI binary ./bin/rabbit-hole (Cobra, cmd/rabbit-hole/): 'serve' runs the daemon (eBPF collect + classify + HTTP/WS API on 127.0.0.1:9734, embedded dashboard, optional classify-server subcommand for remote gRPC classification); 'attach/--pid' attaches to agent processes; 'chat'

- [P1] make test-all exits red twice over: timing-SLO breaches AND 3 real DATA RACE failures — Re-ran `env -u RABBITHOLE_DB_PATH -u RABBITHOLE_LISTEN_ADDR make test-all` at HEAD (92f827d, exit 2, ~10 min): TestStress_100KTraceInsert 55.8s vs 30s SLO, TestStress_ConcurrentFTS5Search p99 425ms vs
- [P1] attach --no-ebpf prints two scary eBPF memlock WARN lines before honoring the flag — Verified verbatim: `WARN ebpf: remove memlock rlimit failed ... operation not permitted ... may need CAP_SYS_RESOURCE or kernel 5.11+` then `WARN ebpf: kernel probes unavailable ... run 'go generate .
- [P2] docs/integration.md /health example contradicts every no-root run — integration.md:82-95 shows `{"status":"ok", ... components: {storage, metrics}}` while every unprivileged run returns `{"status":"degraded", components:{classifier:'degraded — pattern-only', collector
- [P2] REST POST /api/v1/chat dead-ends on non-time-window questions despite live data — With 50+ flows in DB: `curl -X POST /api/v1/chat -d '{"message":"hello"}'` → `"I couldn't find any matching activity for your query."` with `flows:[]`, while `/api/v1/chat` with a time-window phrase r
- [P2] sessions pagination `total` is page count, not match count — real total unknowable — GET /api/v1/sessions?limit=1 → total:1, ?limit=1&offset=1 → total:0, with 3 sessions in DB (verified live). openapi.yaml:798 documents total as 'Number of summaries returned in this response' so it is
