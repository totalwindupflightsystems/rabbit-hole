// Tests for the SQLITE_READONLY → ownership guidance translation (DF-8).

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modernc.org/sqlite"
)

// TestWrapWriteErrorReadOnlyGuidance proves a read-only write failure is
// translated into guidance that names the cause, the store path, and the fix
// instead of leaving the bare errno.
func TestWrapWriteErrorReadOnlyGuidance(t *testing.T) {
	err := wrapWriteError("/tmp/rh-owner/rabbit-hole.db", "delete old traces",
		errors.New("attempt to write a readonly database (8)"))

	roErr := assertReadOnlyGuidance(t, err, "/tmp/rh-owner")

	msg := err.Error()
	if !strings.Contains(msg, "delete old traces") {
		t.Errorf("guidance %q lost the operation context", msg)
	}
	// The raw driver text may appear, but never as the only thing printed.
	if strings.HasPrefix(msg, "attempt to write a readonly database") {
		t.Errorf("guidance %q still leads with the raw errno", msg)
	}
	if errors.Is(err, os.ErrPermission) {
		t.Errorf("read-only write must not be mistaken for a permission error")
	}
	if len(roErr.Op) == 0 {
		t.Errorf("ReadOnlyError.Op is empty, want the refused operation")
	}
}

// TestWrapWriteErrorPassesOtherErrorsThrough keeps non-read-only failures on
// their existing path: operation prefix, wrapped error, no guidance.
func TestWrapWriteErrorPassesOtherErrorsThrough(t *testing.T) {
	cause := errors.New("no such table: traces")
	err := wrapWriteError("/tmp/rh-owner/rabbit-hole.db", "delete old traces", cause)

	if err == nil {
		t.Fatal("wrapWriteError(non-read-only) = nil, want the error back")
	}
	var roErr *ReadOnlyError
	if errors.As(err, &roErr) {
		t.Fatalf("wrapWriteError translated a non-read-only error: %v", err)
	}
	if got, want := err.Error(), "delete old traces: no such table: traces"; got != want {
		t.Errorf("wrapWriteError = %q, want %q", got, want)
	}
	if !errors.Is(err, cause) {
		t.Errorf("wrapWriteError dropped the cause from the chain")
	}
	if got := wrapWriteError("", "op", nil); got != nil {
		t.Errorf("wrapWriteError(nil) = %v, want nil", got)
	}
}

// TestCompactReadOnlyStoreGuidance reproduces the DF-8 scenario: a store owned
// (and unwritable) by another user — the daemon — compacted as the normal user.
// The failure must name the data directory and the owning user, never the bare
// errno alone.
func TestCompactReadOnlyStoreGuidance(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits cannot make the store read-only")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rabbit-hole.db")

	store, err := NewSQLiteStore(dbPath, nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Reproduce another user's ownership with permission bits: the database
	// file loses our write access, as a root-owned store does. The directory
	// stays writable, so SQLite can still create its WAL sidecars — that is
	// the state a daemon run under `sudo` leaves behind, and it is the state
	// in which Compact (not the open) is the call that fails.
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		_ = os.Chmod(dbPath, 0o644)
	})
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatalf("chmod db: %v", err)
	}

	roStore, err := NewSQLiteStore(dbPath, nil)
	if err != nil {
		// Opening can fail before compaction if SQLite refuses to lay down
		// the WAL sidecars; the operator still gets the same guidance.
		t.Logf("store refused at open, asserting guidance there: %v", err)
		_ = assertReadOnlyGuidance(t, err, dir)
		return
	}
	defer func() { _ = roStore.Close() }()

	err = roStore.Compact(context.Background(), time.Now())
	if err == nil {
		t.Fatalf("compact against an unwritable store succeeded, want an ownership error")
	}
	t.Logf("compact stderr: %v", err)
	roErr := assertReadOnlyGuidance(t, err, dir)

	if want := "delete old traces"; roErr.Op != want {
		t.Errorf("ReadOnlyError.Op = %q, want %q", roErr.Op, want)
	}
	if roErr.Owner == "" {
		t.Errorf("ReadOnlyError.Owner is empty, want the owning user of the store")
	}
}

// TestCompactReadOnlyDataDirGuidance covers the other half of the DF-8 state:
// the whole data directory belongs to another user (a daemon started as root),
// so the store cannot even be opened for writing. Opening must still fail with
// ownership guidance rather than a bare errno.
func TestCompactReadOnlyDataDirGuidance(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits cannot make the store read-only")
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rabbit-hole.db")

	store, err := NewSQLiteStore(dbPath, nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	t.Cleanup(func() {
		_ = os.Chmod(dbPath, 0o644)
		_ = os.Chmod(dir, 0o755)
	})
	if err := os.Chmod(dbPath, 0o444); err != nil {
		t.Fatalf("chmod db: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}

	roStore, err := NewSQLiteStore(dbPath, nil)
	if err == nil {
		defer func() { _ = roStore.Close() }()
		err = roStore.Compact(context.Background(), time.Now())
	}
	if err == nil {
		t.Fatalf("opening/compacting an unwritable data dir succeeded, want an ownership error")
	}
	t.Logf("unwritable data dir stderr: %v", err)
	_ = assertReadOnlyGuidance(t, err, dir)
}

// assertReadOnlyGuidance asserts err is a *ReadOnlyError whose message names
// the data directory and points at the ownership fix.
func assertReadOnlyGuidance(t *testing.T, err error, dir string) *ReadOnlyError {
	t.Helper()

	var roErr *ReadOnlyError
	if !errors.As(err, &roErr) {
		t.Fatalf("error is %T, want *ReadOnlyError: %v", err, err)
	}

	msg := err.Error()
	for _, want := range []string{
		"database not writable by current user",
		dir,
		"RABBITHOLE_DATA_DIR",
		"daemon user",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("guidance %q does not mention %q", msg, want)
		}
	}
	// The remedy has to name the user to run as; "run as the daemon user"
	// alone leaves the operator guessing after a root-run daemon.
	if roErr.Owner != "" && !strings.Contains(msg, "daemon user ("+roErr.Owner+")") {
		t.Errorf("guidance %q does not name the daemon user %q", msg, roErr.Owner)
	}

	// When the driver error reaches us, its primary code must be the one we
	// claim to translate; anything else means the detection is guessing.
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff != sqliteReadOnly {
		t.Errorf("translated sqlite code %d, want %d", sqliteErr.Code(), sqliteReadOnly)
	}
	return roErr
}
