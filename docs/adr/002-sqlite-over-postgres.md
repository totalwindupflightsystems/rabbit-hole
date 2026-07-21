# ADR-002: SQLite over PostgreSQL for Self-Hosted Deployment

**Status:** Accepted  
**Date:** 2026-07-13  
**Deciders:** Bane (Alexis Okuwa)

## Context

Rabbit-Hole stores traces, flows, sessions, and context windows. The storage layer must support FTS5 full-text search, concurrent reads during writes, retention/compaction, and survive process crashes. The platform is self-hosted first — the user runs `rabbit-hole serve` on their own machine, not in a managed cloud.

## Decision

**SQLite with WAL mode.** Single-file database at `~/.rabbit-hole/data/rabbit-hole.db`. Zero configuration, zero external dependencies.

SQLite was chosen because:

1. **Zero operational burden.** No `CREATE DATABASE`, no `pg_hba.conf`, no connection strings, no `systemctl start postgresql`. The database file is created automatically on first run. Backups are `cp rabbit-hole.db rabbit-hole.db.bak`.

2. **WAL mode provides concurrent read/write.** Writers don't block readers. The classification pipeline writes flows while the expression server serves search queries — all in the same process, but WAL mode ensures readers never see partial writes.

3. **FTS5 is built in.** Full-text search over flow intents, descriptions, and trace arguments requires no external search engine. SQLite's FTS5 extension is compiled into `modernc.org/sqlite` (pure-Go SQLite), giving us `MATCH` queries with prefix search and ranking out of the box.

4. **Single binary, single file.** The entire platform is one Go binary + one SQLite file. Deploy by copying the binary. Migrate by copying the DB file. No Docker Compose with a separate database container.

5. **Sufficient for agent monitoring.** An agent session generates ~100-500K traces per hour, ~10-100 flows per minute. SQLite handles millions of rows comfortably. The 30-day retention policy keeps the database well under 1GB for typical workloads.

## Alternatives Considered

### PostgreSQL

- **Rejected for self-hosted MVP.** PostgreSQL is the right choice for multi-tenant SaaS, fleet-scale deployments, or when you need row-level security, connection pooling, and replication. But for self-hosted: it requires installation (`apt install postgresql`), configuration (users, databases, extensions, `pg_hba.conf`), and ongoing maintenance (vacuum, upgrades, backups). This violates the "one binary, one command" principle.

- **Future consideration:** If Rabbit-Hole adds a managed cloud offering, a PostgreSQL backend with `pgvector` for semantic search would be the natural choice. The `Storage` interface supports swapping backends without code changes.

### DuckDB

- **Rejected.** DuckDB is optimized for analytical queries (OLAP), not operational workloads (OLTP). Rabbit-Hole does frequent small writes (flow inserts) and point queries (get flow by ID). DuckDB's append-only model and lack of concurrent write support make it a poor fit.

### BoltDB / bbolt

- **Rejected.** BoltDB is a key-value store with no query language, no full-text search, and no schema enforcement. We'd need to build FTS5-equivalent search and relational queries on top of it — essentially reimplementing SQLite poorly.

## Consequences

**Positive:**
- One binary, one file. True zero-config deployment.
- WAL mode: concurrent reads during writes, crash-safe.
- FTS5: full-text search without Elasticsearch, Meilisearch, or any external service.
- The `Storage` interface is backend-agnostic — PostgreSQL support can be added later without changing any consumer code.

**Negative:**
- Single writer (SQLite limitation). The classification pipeline and express server share one process, so this is fine. But if Rabbit-Hole ever needs multiple writer processes, SQLite won't work.
- No built-in replication or horizontal scaling. If a fleet deployment needs centralized storage across nodes, PostgreSQL or a distributed store is needed.
- SQLite WAL grows unbounded under sustained write load. The compaction task checkpoints the WAL periodically, but a write spike between checkpoints can produce large WAL files. Mitigation: `PRAGMA wal_autocheckpoint=1000` and periodic `PRAGMA wal_checkpoint(TRUNCATE)`.
