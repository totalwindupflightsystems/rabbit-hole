// SQLite write errors translated into operator guidance.
//
// The daemon owns the SQLite store. When a maintenance command (compact, and
// any future out-of-daemon writer) runs as a different user than the daemon,
// SQLite opens the file read-only and every write fails with a bare
// "attempt to write a readonly database (8)". That message names neither the
// cause nor the fix, so it is translated here into ownership guidance (DF-8).

package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
)

// sqliteReadOnly is SQLITE_READONLY, the primary result code SQLite reports
// when a write is refused because the database file cannot be modified by this
// process. Extended result codes share the same low byte.
const sqliteReadOnly = 8

// ReadOnlyError reports a write that SQLite refused because the store is not
// writable by the current user — in practice because the daemon, started as
// another user (commonly root), owns the database file and its directory.
type ReadOnlyError struct {
	// Path is the database file path the store was opened with.
	Path string
	// Owner is the owning user of the database file or its directory, when it
	// can be resolved; empty when unknown.
	Owner string
	// Op is the storage operation that was refused, e.g. "delete old traces".
	Op string
	// Err is the underlying SQLite failure, kept for debugging and unwrapping.
	Err error
}

// Error implements error.
func (e *ReadOnlyError) Error() string {
	var b strings.Builder
	b.WriteString("database not writable by current user — the daemon (or another user) owns ")
	if e.Path == "" {
		b.WriteString("the data directory")
	} else {
		fmt.Fprintf(&b, "the data directory %s", filepath.Dir(e.Path))
	}
	// Name the owning user (the daemon user, in practice) both as the cause
	// and inside the remedy: "run as the daemon" is useless without saying
	// which user that is.
	if e.Owner != "" {
		fmt.Fprintf(&b, " (owner: %s); run maintenance commands as the daemon user (%s)", e.Owner, e.Owner)
	} else {
		b.WriteString("; run maintenance commands as the daemon user")
	}
	b.WriteString(", or check RABBITHOLE_DATA_DIR ownership/permissions")
	if e.Err != nil {
		if e.Op != "" {
			fmt.Fprintf(&b, " (sqlite: %s: %v)", e.Op, e.Err)
		} else {
			fmt.Fprintf(&b, " (sqlite: %v)", e.Err)
		}
	}
	return b.String()
}

// Unwrap exposes the underlying SQLite error.
func (e *ReadOnlyError) Unwrap() error { return e.Err }

// wrapWriteError adds operation context to a failed write and translates
// SQLITE_READONLY into *ReadOnlyError so callers print ownership guidance
// instead of a bare errno. Every other error passes through unchanged.
func wrapWriteError(dbPath, op string, err error) error {
	if err == nil {
		return nil
	}
	if !isSQLiteReadOnly(err) {
		return fmt.Errorf("%s: %w", op, err)
	}
	return &ReadOnlyError{Path: dbPath, Owner: storeOwner(dbPath), Op: op, Err: err}
}

// isSQLiteReadOnly reports whether err is SQLITE_READONLY, either as a typed
// driver error or (for drivers that only expose text) in its message.
func isSQLiteReadOnly(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xff == sqliteReadOnly
	}
	return strings.Contains(err.Error(), "attempt to write a readonly database")
}

// storeOwner resolves the user owning the store — the daemon user, for a store
// the daemon created. It prefers the database file and falls back to the data
// directory, since WAL sidecars and directory ownership both decide
// writability.
func storeOwner(dbPath string) string {
	if dbPath == "" || strings.HasPrefix(dbPath, ":") {
		return ""
	}
	if owner := fileOwnerName(dbPath); owner != "" {
		return owner
	}
	return fileOwnerName(filepath.Dir(dbPath))
}
