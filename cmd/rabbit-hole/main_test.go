package main

import (
	"os"
	"testing"
)

// TestMain isolates cmd/rabbit-hole tests from a leaked RABBITHOLE_DB_PATH
// in the outer environment (e.g. a daemon started from another terminal or
// a dogfood session). Tests that need a specific DB path set one explicitly
// with t.Setenv (DF-012). RABBITHOLE_DATA_DIR and RABBITHOLE_LISTEN_ADDR are
// intentionally left untouched — several tests set them deliberately.
func TestMain(m *testing.M) {
	os.Unsetenv("RABBITHOLE_DB_PATH")
	os.Exit(m.Run())
}
