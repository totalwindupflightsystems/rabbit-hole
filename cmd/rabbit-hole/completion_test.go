package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdoutAsync runs fn and returns everything written to os.Stdout.
// Uses a goroutine to drain the pipe so large outputs don't block.
func captureStdoutAsync(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, r)
		close(done)
	}()

	fn()
	w.Close()
	os.Stdout = old
	<-done
	return buf.String()
}

func TestNewCompletionCmd_Structure(t *testing.T) {
	cmd := newCompletionCmd()

	if cmd.Use != "completion [bash|zsh|fish|powershell]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "completion [bash|zsh|fish|powershell]")
	}
	if cmd.Short != "Generate shell completion scripts" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Generate shell completion scripts")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}

	expectedArgs := []string{"bash", "zsh", "fish", "powershell"}
	for i, want := range expectedArgs {
		if cmd.ValidArgs[i] != want {
			t.Errorf("ValidArgs[%d] = %q, want %q", i, cmd.ValidArgs[i], want)
		}
	}
}

func TestCompletionCommands_AllShells(t *testing.T) {
	shells := []string{"bash", "zsh", "fish", "powershell"}

	for _, shell := range shells {
		t.Run(shell, func(t *testing.T) {
			output := captureStdoutAsync(func() {
				cmd := newCompletionCmd()
				cmd.SetArgs([]string{shell})
				if err := cmd.Execute(); err != nil {
					t.Errorf("Execute for %s returned error: %v", shell, err)
				}
			})

			if output == "" {
				t.Errorf("completion %s output is empty", shell)
			}

			t.Logf("completion %s output length: %d", shell, len(output))
		})
	}
}

func TestCompletionCommands_BashContainsCompletionKeyword(t *testing.T) {
	output := captureStdoutAsync(func() {
		cmd := newCompletionCmd()
		cmd.SetArgs([]string{"bash"})
		if err := cmd.Execute(); err != nil {
			t.Errorf("Execute for bash returned error: %v", err)
		}
	})

	// Bash completion output should contain completion-related keywords
	bashKeywords := []string{"completion", "bash", "compgen"}
	found := false
	for _, kw := range bashKeywords {
		if strings.Contains(strings.ToLower(output), kw) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("bash completion output does not contain completion keywords. Output starts with: %.200s", output)
	}

	if strings.Contains(output, "func _") || strings.Contains(output, "#compdef") {
		t.Error("bash completion output looks like zsh completion")
	}

	t.Logf("bash completion output length: %d", len(output))
	t.Logf("first 200 chars: %.200s", output)
}
