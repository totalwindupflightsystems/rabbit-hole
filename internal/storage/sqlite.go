// Package storage provides the SQLite-backed persistence layer for Rabbit-Hole.
package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/pkg/types"
	_ "modernc.org/sqlite"
)

//go:embed migrations/001_initial.up.sql
var migration001 string

// SQLiteStore implements the storage layer backed by SQLite in WAL mode.
type SQLiteStore struct {
	db     *sql.DB
	logger *slog.Logger
	path   string // database file path, for reporting (GET /api/v1/stats)
}

// NewSQLiteStore opens a SQLite database at dbPath and runs migrations.
func NewSQLiteStore(dbPath string, logger *slog.Logger) (*SQLiteStore, error) {
	// Ensure the parent directory exists so a DB path in a fresh directory
	// works on first run without a manual mkdir (DF-011).
	if dir := filepath.Dir(dbPath); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir %s: %w", dir, err)
		}
	}
	dsn := dbPath + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if logger == nil {
		logger = slog.Default()
	}

	store := &SQLiteStore{db: db, logger: logger, path: dbPath}
	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// DB exposes the underlying *sql.DB for testing.
func (s *SQLiteStore) DB() *sql.DB { return s.db }

// Path returns the database file path the store was opened with.
func (s *SQLiteStore) Path() string { return s.path }

// ---------- Migration ----------

func (s *SQLiteStore) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	if err != nil {
		return fmt.Errorf("create metadata: %w", err)
	}

	// PRAGMAs cannot run inside a transaction, and concurrent migrators
	// must serialize — so hoist the pragmas out of the migration script
	// and run the version-read + apply loop under BEGIN IMMEDIATE
	// (DF-009). The DSN already sets the same pragmas (_journal_mode=WAL,
	// _foreign_keys=on, _busy_timeout=5000); executing them here again is
	// harmless and keeps the migration scripts self-contained.
	pragmas, body := splitMigrationSQL(migration001)
	for _, stmt := range pragmas {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("pragma %s: %w", stmt, err)
		}
	}
	// BEGIN IMMEDIATE takes the write lock, so a second migrator on the
	// same DB file blocks here (busy_timeout 5000ms) until the first
	// commits — it then reads schema_version=1 and skips. Without the
	// lock, both migrators can read version=0 and both apply migration
	// 001, failing with "table sessions already exists" (DF-009).
	if _, err := s.db.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = s.db.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	var version int
	err = s.db.QueryRowContext(ctx,
		`SELECT COALESCE(CAST(value AS INTEGER), 0) FROM metadata WHERE key = 'schema_version'`,
	).Scan(&version)
	if err != nil {
		version = 0
	}

	migrations := []struct {
		version int
		sql     string
	}{
		{1, body},
	}

	for _, m := range migrations {
		if m.version > version {
			s.logger.Info("applying migration", "version", m.version)
			if _, err := s.db.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("migration %d: %w", m.version, err)
			}
			_, err = s.db.ExecContext(ctx,
				`INSERT OR REPLACE INTO metadata (key, value) VALUES ('schema_version', ?)`, m.version)
			if err != nil {
				return fmt.Errorf("update schema_version: %w", err)
			}
		}
	}

	if _, err := s.db.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	committed = true
	return nil
}

// splitMigrationSQL separates leading PRAGMA statements from the rest of a
// migration script. SQLite forbids PRAGMA journal_mode/foreign_keys/
// busy_timeout inside a transaction, so they must run before the
// BEGIN IMMEDIATE block that applies the schema (DF-009). Pragmas must be
// single-line and appear before any other statement — true for every
// migration in this repo.
func splitMigrationSQL(script string) (pragmas []string, body string) {
	lines := strings.Split(script, "\n")
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			i++
			continue
		}
		if strings.HasPrefix(strings.ToUpper(trimmed), "PRAGMA") {
			pragmas = append(pragmas, lines[i])
			i++
			continue
		}
		break
	}
	return pragmas, strings.Join(lines[i:], "\n")
}

// ---------- Metadata ----------

// SetMetadata upserts a key/value pair in the metadata table.
func (s *SQLiteStore) SetMetadata(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO metadata (key, value) VALUES (?, ?)`, key, value)
	if err != nil {
		return fmt.Errorf("set metadata %s: %w", key, err)
	}
	return nil
}

// GetMetadata reads a key from the metadata table. ok is false when the
// key is absent.
func (s *SQLiteStore) GetMetadata(ctx context.Context, key string) (value string, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT value FROM metadata WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get metadata %s: %w", key, err)
	}
	return value, true, nil
}

// ---------- Trace Operations ----------

// StoreTraces inserts a batch of traces in a single transaction.
func (s *SQLiteStore) StoreTraces(ctx context.Context, traces []types.Trace) error {
	// Resolve session IDs from PIDs before opening the transaction
	sidCache := make(map[int32]string)
	for _, t := range traces {
		if _, ok := sidCache[t.PID]; !ok {
			sidCache[t.PID] = s.resolveSessionID(ctx, t.PID)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO traces (id, session_id, timestamp, category, syscall, args,
		                    return_value, duration_ns, errno, stack)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, t := range traces {
		argsJSON, _ := json.Marshal(t.Args)
		stackJSON, _ := json.Marshal(t.Stack)
		sid := sidCache[t.PID]
		_, err := stmt.ExecContext(ctx,
			t.ID, sid, t.Timestamp.Format(time.RFC3339Nano),
			string(t.Category), t.Syscall, string(argsJSON),
			t.ReturnValue, t.Duration.Nanoseconds(), t.Errno, string(stackJSON),
		)
		if err != nil {
			return fmt.Errorf("insert trace %s: %w", t.ID, err)
		}
	}
	return tx.Commit()
}

// QueryTraces retrieves traces matching the query filters.
func (s *SQLiteStore) QueryTraces(ctx context.Context, req types.TraceQuery) ([]types.Trace, error) {
	q := `SELECT id, session_id, timestamp, category, syscall, args,
	             return_value, duration_ns, errno, stack FROM traces WHERE 1=1`
	var args []any

	if req.SessionID != "" {
		q += " AND session_id = ?"
		args = append(args, req.SessionID)
	}
	if req.Category != "" {
		q += " AND category = ?"
		args = append(args, string(req.Category))
	}
	if req.Syscall != "" {
		q += " AND syscall = ?"
		args = append(args, req.Syscall)
	}
	if !req.TimeRange.Start.IsZero() {
		q += " AND timestamp >= ?"
		args = append(args, req.TimeRange.Start.Format(time.RFC3339Nano))
	}
	if !req.TimeRange.End.IsZero() {
		q += " AND timestamp <= ?"
		args = append(args, req.TimeRange.End.Format(time.RFC3339Nano))
	}
	q += " ORDER BY timestamp DESC LIMIT ?"
	if req.Limit <= 0 {
		req.Limit = 100
	}
	args = append(args, req.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query traces: %w", err)
	}
	defer rows.Close()

	var out []types.Trace
	for rows.Next() {
		var tr types.Trace
		var ts, cat, argsJSON, stackJSON, sessionID string
		var durNs int64
		if err := rows.Scan(&tr.ID, &sessionID, &ts, &cat, &tr.Syscall,
			&argsJSON, &tr.ReturnValue, &durNs, &tr.Errno, &stackJSON); err != nil {
			return nil, fmt.Errorf("scan trace: %w", err)
		}
		tr.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		tr.Category = types.TraceCategory(cat)
		tr.Duration = time.Duration(durNs) * time.Nanosecond
		json.Unmarshal([]byte(argsJSON), &tr.Args)
		json.Unmarshal([]byte(stackJSON), &tr.Stack)
		out = append(out, tr)
	}
	return out, rows.Err()
}

// ---------- Flow Operations ----------

// StoreFlows inserts a batch of flows. FTS5 sync is handled by SQLite triggers.
func (s *SQLiteStore) StoreFlows(ctx context.Context, flows []types.Flow) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO flows (id, session_id, trace_ids, intent, phase, description,
		                   outcome, confidence, start_time, end_time, duration_ns, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, flow := range flows {
		traceIDsJSON, _ := json.Marshal(flow.TraceIDs)
		metadataJSON := []byte("{}")
		if len(flow.Metadata) > 0 {
			metadataJSON = flow.Metadata
		}
		_, err := stmt.ExecContext(ctx,
			flow.ID, flow.SessionID, string(traceIDsJSON),
			flow.Intent, string(flow.Phase), flow.Description,
			string(flow.Outcome), flow.Confidence,
			flow.StartTime.Format(time.RFC3339Nano),
			flow.EndTime.Format(time.RFC3339Nano),
			flow.Duration.Nanoseconds(), string(metadataJSON),
		)
		if err != nil {
			return fmt.Errorf("insert flow %s: %w", flow.ID, err)
		}
	}
	return tx.Commit()
}

// GetFlow retrieves a single flow by ID.
func (s *SQLiteStore) GetFlow(ctx context.Context, flowID string) (*types.Flow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, trace_ids, intent, phase, description,
		       outcome, confidence, start_time, end_time, duration_ns, metadata
		FROM flows WHERE id = ?`, flowID)
	var f types.Flow
	var traceIDsJSON, metadataJSON, phase, outcome string
	var durNs int64
	var ts, te string
	err := row.Scan(&f.ID, &f.SessionID, &traceIDsJSON,
		&f.Intent, &phase, &f.Description,
		&outcome, &f.Confidence, &ts, &te, &durNs, &metadataJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("flow %s not found", flowID)
		}
		return nil, err
	}
	f.Phase = types.FlowPhase(phase)
	f.Outcome = types.FlowOutcome(outcome)
	f.StartTime, _ = time.Parse(time.RFC3339Nano, ts)
	f.EndTime, _ = time.Parse(time.RFC3339Nano, te)
	f.Duration = time.Duration(durNs) * time.Nanosecond
	json.Unmarshal([]byte(traceIDsJSON), &f.TraceIDs)
	if metadataJSON != "" && metadataJSON != "{}" {
		f.Metadata = json.RawMessage(metadataJSON)
	}
	return &f, nil
}

// QueryFlows retrieves flows matching structured filters. Returns a cursor for pagination.
func (s *SQLiteStore) QueryFlows(ctx context.Context, req types.FlowQuery) ([]types.Flow, string, error) {
	q := `SELECT id, session_id, trace_ids, intent, phase, description,
	             outcome, confidence, start_time, end_time, duration_ns, metadata
	      FROM flows WHERE 1=1`
	var args []any

	if req.SessionID != "" {
		q += " AND session_id = ?"
		args = append(args, req.SessionID)
	}
	if len(req.Phases) > 0 {
		q += " AND phase IN (?" + strings.Repeat(",?", len(req.Phases)-1) + ")"
		for _, p := range req.Phases {
			args = append(args, string(p))
		}
	}
	if len(req.Outcomes) > 0 {
		q += " AND outcome IN (?" + strings.Repeat(",?", len(req.Outcomes)-1) + ")"
		for _, o := range req.Outcomes {
			args = append(args, string(o))
		}
	}
	if req.Query != "" {
		q += " AND (description LIKE ? OR intent LIKE ?)"
		args = append(args, "%"+req.Query+"%", "%"+req.Query+"%")
	}
	if !req.TimeRange.Start.IsZero() {
		// Compare via unixepoch() so stored LOCAL-offset timestamps and
		// UTC "Z" query timestamps normalize to the same seconds (DF-034).
		// Raw TEXT comparison would sort 'Z' before '-05:00' for the same instant.
		q += " AND unixepoch(start_time) >= unixepoch(?)"
		args = append(args, req.TimeRange.Start.Format(time.RFC3339Nano))
	}
	if !req.TimeRange.End.IsZero() {
		q += " AND unixepoch(start_time) <= unixepoch(?)"
		args = append(args, req.TimeRange.End.Format(time.RFC3339Nano))
	}
	if req.MinConfidence > 0 {
		q += " AND confidence >= ?"
		args = append(args, req.MinConfidence)
	}
	q += " ORDER BY start_time DESC"
	if req.Limit <= 0 {
		req.Limit = 50
	}
	q += fmt.Sprintf(" LIMIT %d", req.Limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("query flows: %w", err)
	}
	defer rows.Close()

	flows, err := scanFlows(rows)
	if err != nil {
		return nil, "", err
	}
	var cursor string
	if len(flows) > 0 {
		cursor = flows[len(flows)-1].ID
	}
	return flows, cursor, nil
}

// SearchFlows performs FTS5 full-text search, falling back to LIKE on parse errors.
func (s *SQLiteStore) SearchFlows(ctx context.Context, query string, limit int) ([]types.Flow, error) {
	if limit <= 0 {
		limit = 50
	}
	if query != "" {
		rows, err := s.db.QueryContext(ctx, `
			SELECT f.id, f.session_id, f.trace_ids, f.intent, f.phase, f.description,
			       f.outcome, f.confidence, f.start_time, f.end_time, f.duration_ns, f.metadata
			FROM flows f JOIN flows_fts ft ON f.rowid = ft.rowid
			WHERE flows_fts MATCH ? ORDER BY rank LIMIT ?`, query, limit)
		if err != nil {
			return s.searchFlowsLike(ctx, query, limit)
		}
		defer rows.Close()
		return scanFlows(rows)
	}
	return s.searchFlowsRecent(ctx, limit)
}

func (s *SQLiteStore) searchFlowsLike(ctx context.Context, query string, limit int) ([]types.Flow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, trace_ids, intent, phase, description,
		       outcome, confidence, start_time, end_time, duration_ns, metadata
		FROM flows WHERE description LIKE ? OR intent LIKE ?
		ORDER BY start_time DESC LIMIT ?`,
		"%"+query+"%", "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFlows(rows)
}

func (s *SQLiteStore) searchFlowsRecent(ctx context.Context, limit int) ([]types.Flow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, trace_ids, intent, phase, description,
		       outcome, confidence, start_time, end_time, duration_ns, metadata
		FROM flows ORDER BY start_time DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFlows(rows)
}

func scanFlows(rows *sql.Rows) ([]types.Flow, error) {
	var out []types.Flow
	for rows.Next() {
		var f types.Flow
		var traceIDsJSON, metadataJSON, phase, outcome string
		var durNs int64
		var ts, te string
		err := rows.Scan(&f.ID, &f.SessionID, &traceIDsJSON,
			&f.Intent, &phase, &f.Description,
			&outcome, &f.Confidence, &ts, &te, &durNs, &metadataJSON)
		if err != nil {
			return nil, fmt.Errorf("scan flow: %w", err)
		}
		f.Phase = types.FlowPhase(phase)
		f.Outcome = types.FlowOutcome(outcome)
		f.StartTime, _ = time.Parse(time.RFC3339Nano, ts)
		f.EndTime, _ = time.Parse(time.RFC3339Nano, te)
		f.Duration = time.Duration(durNs) * time.Nanosecond
		json.Unmarshal([]byte(traceIDsJSON), &f.TraceIDs)
		if metadataJSON != "" && metadataJSON != "{}" {
			f.Metadata = json.RawMessage(metadataJSON)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ---------- Session Operations ----------

// StoreSession inserts a new session.
func (s *SQLiteStore) StoreSession(ctx context.Context, session *types.Session) error {
	envJSON, _ := json.Marshal(session.Metadata.Environment)
	var endTs any
	if session.EndTime != nil {
		endTs = session.EndTime.Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, agent_pid, agent_name, start_time, end_time, status,
		                      command_line, work_dir, binary_path, env_vars, version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.AgentPID, session.AgentName,
		session.StartTime.Format(time.RFC3339Nano), endTs,
		string(session.Status), session.Metadata.CommandLine,
		session.Metadata.WorkDir, session.Metadata.BinaryPath,
		string(envJSON), session.Metadata.Version,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetSession retrieves a session by ID.
func (s *SQLiteStore) GetSession(ctx context.Context, sessionID string) (*types.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent_pid, agent_name, start_time, end_time, status,
		       command_line, work_dir, binary_path, env_vars, version
		FROM sessions WHERE id = ?`, sessionID)
	return scanSession(row)
}

// ListSessions returns sessions with offset/limit pagination.
func (s *SQLiteStore) ListSessions(ctx context.Context, offset, limit int) ([]types.Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_pid, agent_name, start_time, end_time, status,
		       command_line, work_dir, binary_path, env_vars, version
		FROM sessions ORDER BY start_time DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var out []types.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

// UpdateSession updates an existing session.
func (s *SQLiteStore) UpdateSession(ctx context.Context, session *types.Session) error {
	envJSON, _ := json.Marshal(session.Metadata.Environment)
	var endTs any
	if session.EndTime != nil {
		endTs = session.EndTime.Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET end_time = ?, status = ?, command_line = ?, work_dir = ?,
		    binary_path = ?, env_vars = ?, version = ?, updated_at = datetime('now')
		WHERE id = ?`,
		endTs, string(session.Status), session.Metadata.CommandLine,
		session.Metadata.WorkDir, session.Metadata.BinaryPath,
		string(envJSON), session.Metadata.Version, session.ID,
	)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

// ReconcileCrashedSessions marks all sessions with status 'running' as
// 'crashed' with an end_time of now. Returns the number of sessions
// reconciled. This is called on daemon startup to recover from an
// unclean shutdown where the eBPF collector lost its ring buffer but
// sessions were still marked running in the database.
func (s *SQLiteStore) ReconcileCrashedSessions(ctx context.Context) (int, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = 'crashed', end_time = ?, updated_at = datetime('now') WHERE status = 'running'`,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("reconcile crashed sessions: %w", err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	s.logger.Info("reconciled crashed sessions", "count", count)
	return int(count), nil
}

func scanSession(scanner interface{ Scan(...any) error }) (*types.Session, error) {
	var s types.Session
	var startTs, envJSON string
	var endTs sql.NullString
	err := scanner.Scan(&s.ID, &s.AgentPID, &s.AgentName, &startTs, &endTs,
		&s.Status, &s.Metadata.CommandLine, &s.Metadata.WorkDir,
		&s.Metadata.BinaryPath, &envJSON, &s.Metadata.Version)
	if err != nil {
		return nil, err
	}
	s.StartTime, _ = time.Parse(time.RFC3339Nano, startTs)
	if endTs.Valid {
		t, _ := time.Parse(time.RFC3339Nano, endTs.String)
		s.EndTime = &t
	}
	s.Metadata.Environment = make(map[string]string)
	json.Unmarshal([]byte(envJSON), &s.Metadata.Environment)
	return &s, nil
}

// ---------- Context Window Operations ----------

// StoreContextWindow inserts a context window snapshot.
func (s *SQLiteStore) StoreContextWindow(ctx context.Context, cw *types.ContextWindow) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO context_windows (flow_id, timestamp, model_name, prompt_text,
		                             messages_in, messages_out, token_count, duration_ns)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		cw.FlowID, cw.Timestamp.Format(time.RFC3339Nano),
		cw.ModelName, cw.PromptText, cw.MessagesIn, cw.MessagesOut,
		cw.TokenCount, cw.Duration.Nanoseconds(),
	)
	if err != nil {
		return fmt.Errorf("insert context window: %w", err)
	}
	return nil
}

// GetContextWindow retrieves a context window by flow ID.
func (s *SQLiteStore) GetContextWindow(ctx context.Context, flowID string) (*types.ContextWindow, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT flow_id, timestamp, model_name, prompt_text,
		       messages_in, messages_out, token_count, duration_ns
		FROM context_windows WHERE flow_id = ?`, flowID)
	var cw types.ContextWindow
	var ts string
	var durNs int64
	err := row.Scan(&cw.FlowID, &ts, &cw.ModelName, &cw.PromptText,
		&cw.MessagesIn, &cw.MessagesOut, &cw.TokenCount, &durNs)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("context window for flow %s not found", flowID)
		}
		return nil, fmt.Errorf("scan context window: %w", err)
	}
	cw.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
	cw.Duration = time.Duration(durNs) * time.Nanosecond
	return &cw, nil
}

// ---------- Maintenance ----------

// Compact deletes data older than before, then vacuums.
func (s *SQLiteStore) Compact(ctx context.Context, before time.Time) error {
	s.logger.Info("starting compaction", "before", before)
	cutoff := before.Format(time.RFC3339Nano)

	result, err := s.db.ExecContext(ctx, `DELETE FROM traces WHERE timestamp < ?`, cutoff)
	if err != nil {
		return fmt.Errorf("delete old traces: %w", err)
	}
	if n, _ := result.RowsAffected(); n > 0 {
		s.logger.Info("deleted old traces", "count", n)
	}

	result, err = s.db.ExecContext(ctx, `DELETE FROM flows WHERE unixepoch(start_time) < unixepoch(?)`, cutoff)
	if err != nil {
		return fmt.Errorf("delete old flows: %w", err)
	}
	if n, _ := result.RowsAffected(); n > 0 {
		s.logger.Info("deleted old flows", "count", n)
	}

	_, err = s.db.ExecContext(ctx, `
		DELETE FROM sessions WHERE end_time < ? AND end_time IS NOT NULL
		AND id NOT IN (SELECT DISTINCT session_id FROM flows)`, cutoff)
	if err != nil {
		return fmt.Errorf("delete orphan sessions: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `VACUUM`)
	if err != nil {
		s.logger.Warn("vacuum failed", "err", err)
	}
	return nil
}

// Stats returns aggregate storage statistics.
func (s *SQLiteStore) Stats(ctx context.Context) (types.StorageStats, error) {
	var stats types.StorageStats
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traces`).Scan(&stats.TotalTraces)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM flows`).Scan(&stats.TotalFlows)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&stats.TotalSessions)

	var newest, oldest sql.NullString
	_ = s.db.QueryRowContext(ctx, `SELECT MIN(timestamp), MAX(timestamp) FROM traces`).Scan(&oldest, &newest)
	if oldest.Valid {
		stats.OldestTrace, _ = time.Parse(time.RFC3339Nano, oldest.String)
	}
	if newest.Valid {
		stats.NewestTrace, _ = time.Parse(time.RFC3339Nano, newest.String)
	}

	var pageCount, pageSize int64
	_ = s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pageCount)
	_ = s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize)
	stats.DBSizeBytes = pageCount * pageSize

	var walRows int64
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM flows_fts`).Scan(&walRows)
	stats.WALSizeBytes = walRows * 1024

	return stats, nil
}

// resolveSessionID finds the active session ID for a given PID.
func (s *SQLiteStore) resolveSessionID(ctx context.Context, pid int32) string {
	var sessionID string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM sessions WHERE agent_pid = ? AND status = 'running' ORDER BY start_time DESC LIMIT 1`,
		pid,
	).Scan(&sessionID)
	if err != nil {
		return ""
	}
	return sessionID
}

// Health checks database accessibility.
func (s *SQLiteStore) Health(ctx context.Context) error {
	var one int
	if err := s.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		return fmt.Errorf("storage health: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("storage health: unexpected result %d", one)
	}
	var version int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(CAST(value AS INTEGER), 0) FROM metadata WHERE key = 'schema_version'`,
	).Scan(&version); err != nil || version < 1 {
		return fmt.Errorf("storage health: schema version not set")
	}
	return nil
}
