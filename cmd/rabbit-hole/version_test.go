package main

import (
	"bytes"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
)

// captureStdout runs fn and returns everything written to os.Stdout.
func captureStdout(fn func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func TestNewVersionCmd_Structure(t *testing.T) {
	cmd := newVersionCmd()

	if cmd.Use != "version" {
		t.Errorf("Use = %q, want %q", cmd.Use, "version")
	}
	if cmd.Short != "Print version information" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Print version information")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewVersionCmd_Output(t *testing.T) {
	oldVersion := Version
	oldCommit := Commit
	oldBuildTime := BuildTime
	defer func() {
		Version = oldVersion
		Commit = oldCommit
		BuildTime = oldBuildTime
	}()

	Version = "v9.9.9-rc1"
	Commit = "deadbeef12345678"
	BuildTime = "2026-07-19T12:00:00Z"

	output := captureStdout(func() {
		cmd := newVersionCmd()
		if err := cmd.RunE(cmd, nil); err != nil {
			t.Errorf("RunE returned error: %v", err)
		}
	})

	t.Logf("version output:\n%s", output)

	if !strings.Contains(output, "v9.9.9-rc1") {
		t.Errorf("output missing version: %s", output)
	}
	if !strings.Contains(output, runtime.GOOS) {
		t.Errorf("output missing GOOS: %s", output)
	}
	if !strings.Contains(output, runtime.GOARCH) {
		t.Errorf("output missing GOARCH: %s", output)
	}
	if !strings.Contains(output, "deadbeef") {
		t.Errorf("output missing commit: %s", output)
	}
	if !strings.Contains(output, "2026-07-19T12:00:00Z") {
		t.Errorf("output missing build time: %s", output)
	}
}

func TestNewVersionCmd_Execute(t *testing.T) {
	oldVersion := Version
	defer func() { Version = oldVersion }()
	Version = "v1.0.0-test"

	output := captureStdout(func() {
		cmd := newVersionCmd()
		if err := cmd.Execute(); err != nil {
			t.Errorf("Execute returned error: %v", err)
		}
	})

	if !strings.Contains(output, "v1.0.0-test") {
		t.Errorf("output missing version, got: %s", output)
	}
}
