-- Rabbit-Hole Database Schema v1
-- Migration 001_initial

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;

-- SESSIONS
CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    agent_pid   INTEGER NOT NULL,
    agent_name  TEXT NOT NULL,
    start_time  TEXT NOT NULL,
    end_time    TEXT,
    status      TEXT NOT NULL DEFAULT 'running',
    command_line TEXT NOT NULL DEFAULT '',
    work_dir    TEXT NOT NULL DEFAULT '',
    binary_path TEXT NOT NULL DEFAULT '',
    env_vars    TEXT NOT NULL DEFAULT '{}',
    version     TEXT NOT NULL DEFAULT '',
    trace_count INTEGER NOT NULL DEFAULT 0,
    flow_count  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_sessions_status ON sessions(status);
CREATE INDEX idx_sessions_agent_pid ON sessions(agent_pid);
CREATE INDEX idx_sessions_start_time ON sessions(start_time);

-- TRACES
CREATE TABLE traces (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    timestamp   TEXT NOT NULL,
    category    TEXT NOT NULL,
    syscall     TEXT NOT NULL DEFAULT '',
    args        TEXT NOT NULL DEFAULT '[]',
    return_value INTEGER NOT NULL DEFAULT 0,
    duration_ns INTEGER NOT NULL DEFAULT 0,
    errno       INTEGER NOT NULL DEFAULT 0,
    stack       TEXT NOT NULL DEFAULT '[]',
    filename    TEXT NOT NULL DEFAULT '',
    saddr       TEXT NOT NULL DEFAULT '',
    daddr       TEXT NOT NULL DEFAULT '',
    sport       INTEGER NOT NULL DEFAULT 0,
    dport       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_traces_session_id ON traces(session_id);
CREATE INDEX idx_traces_session_time ON traces(session_id, timestamp);
CREATE INDEX idx_traces_category ON traces(category);
CREATE INDEX idx_traces_syscall ON traces(syscall);
CREATE INDEX idx_traces_timestamp ON traces(timestamp);
CREATE INDEX idx_traces_date ON traces(date(timestamp));

-- FLOWS
CREATE TABLE flows (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    trace_ids   TEXT NOT NULL DEFAULT '[]',
    intent      TEXT NOT NULL DEFAULT 'unknown',
    phase       TEXT NOT NULL DEFAULT 'action',
    description TEXT NOT NULL DEFAULT '',
    outcome     TEXT NOT NULL DEFAULT 'unknown',
    confidence  REAL NOT NULL DEFAULT 0.0,
    start_time  TEXT NOT NULL,
    end_time    TEXT NOT NULL,
    duration_ns INTEGER NOT NULL DEFAULT 0,
    metadata    TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_flows_session_id ON flows(session_id);
CREATE INDEX idx_flows_session_time ON flows(session_id, start_time);
CREATE INDEX idx_flows_intent ON flows(intent);
CREATE INDEX idx_flows_phase ON flows(phase);
CREATE INDEX idx_flows_outcome ON flows(outcome);
CREATE INDEX idx_flows_confidence ON flows(confidence);
CREATE INDEX idx_flows_start_time ON flows(start_time);
CREATE INDEX idx_flows_date ON flows(date(start_time));

-- FTS5 full-text search on flows
CREATE VIRTUAL TABLE flows_fts USING fts5(
    intent, description, outcome,
    content='flows', content_rowid='rowid'
);

CREATE TRIGGER flows_ai AFTER INSERT ON flows BEGIN
    INSERT INTO flows_fts(rowid, intent, description, outcome)
    VALUES (new.rowid, new.intent, new.description, new.outcome);
END;
CREATE TRIGGER flows_ad AFTER DELETE ON flows BEGIN
    INSERT INTO flows_fts(flows_fts, rowid, intent, description, outcome)
    VALUES ('delete', old.rowid, old.intent, old.description, old.outcome);
END;
CREATE TRIGGER flows_au AFTER UPDATE ON flows BEGIN
    INSERT INTO flows_fts(flows_fts, rowid, intent, description, outcome)
    VALUES ('delete', old.rowid, old.intent, old.description, old.outcome);
    INSERT INTO flows_fts(rowid, intent, description, outcome)
    VALUES (new.rowid, new.intent, new.description, new.outcome);
END;

-- CONTEXT WINDOWS
CREATE TABLE context_windows (
    flow_id      TEXT PRIMARY KEY REFERENCES flows(id) ON DELETE CASCADE,
    timestamp    TEXT NOT NULL,
    model_name   TEXT NOT NULL DEFAULT '',
    prompt_text  TEXT NOT NULL DEFAULT '',
    messages_in  TEXT NOT NULL DEFAULT '',
    messages_out TEXT NOT NULL DEFAULT '',
    token_count  INTEGER NOT NULL DEFAULT 0,
    duration_ns  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_context_windows_timestamp ON context_windows(timestamp);

-- METADATA — table created by migrate(), just insert initial version
INSERT OR REPLACE INTO metadata (key, value) VALUES ('schema_version', '1');
