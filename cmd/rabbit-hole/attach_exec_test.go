package main

import (
	"testing"
)

func TestAttachCmd_NonExistentPid(t *testing.T) {
	// Running attach with a PID that doesn't exist should fail at processExists
	output := captureStdout(func() {
		cmd := newAttachCmd()
		cmd.SetArgs([]string{"--pid=999999"})
		err := cmd.Execute()
		if err != nil {
			t.Logf("attach error (expected): %v", err)
		} else {
			t.Error("expected error for non-existent PID")
		}
	})
	t.Logf("attach output: %s", output)
}

func TestAttachCmd_AlternativeFlags(t *testing.T) {
	// Test that all flags can be set and command attempts execution
	output := captureStdout(func() {
		cmd := newAttachCmd()
		cmd.SetArgs([]string{
			"--pid", "123",
			"--context-windows",
			"--categories", "syscall,network",
			"--no-tls-intercept",
		})
		err := cmd.Execute()
		if err != nil {
			t.Logf("attach error: %v", err)
		}
	})
	t.Logf("attach flags output: %s", output)
}
