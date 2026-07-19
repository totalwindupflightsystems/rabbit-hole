package main

import (
	"testing"
)

func TestDetachCmd_WithNonExistentSession(t *testing.T) {
	// Detach with a session that doesn't exist — will fail at collector layer
	output := captureStdout(func() {
		cmd := newDetachCmd()
		cmd.SetArgs([]string{"nonexistent-session-id"})
		err := cmd.Execute()
		if err != nil {
			t.Logf("detach error: %v", err)
		}
	})
	t.Logf("detach output: %s", output)
}

func TestDetachCmd_WithAllFlag(t *testing.T) {
	output := captureStdout(func() {
		cmd := newDetachCmd()
		cmd.SetArgs([]string{"--all"})
		err := cmd.Execute()
		if err != nil {
			t.Logf("detach --all error: %v", err)
		}
	})
	t.Logf("detach --all output: %s", output)
}
