package main

import (
	"os"
	"strings"
	"testing"
)

// TestMain isolates cmd/rabbit-hole tests from ambient RABBITHOLE_* env vars
// leaked from the outer environment (e.g. a daemon started from another
// terminal, a dogfood session, or a foreman/CI shell with leftover exports).
// Every variable with the RABBITHOLE_ prefix is scrubbed before the suite
// runs, so default-config assertions (RH-GAP-014) cannot be polluted by
// ambient values such as RABBITHOLE_LISTEN_ADDR or RABBITHOLE_DB_PATH.
// Tests that deliberately set RABBITHOLE_* variables do so inside their own
// bodies with t.Setenv/os.Setenv, which run after TestMain and are therefore
// unaffected. RH_SOAK_* variables (soak_test.go) use a different prefix and
// are intentionally left alone.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(key, "RABBITHOLE_") {
			os.Unsetenv(key)
		}
	}
	os.Exit(m.Run())
}
