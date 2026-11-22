package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	tests := []struct {
		name  string
		level string
		want  slog.Level
	}{
		{"debug level", "debug", slog.LevelDebug},
		{"info level", "info", slog.LevelInfo},
		{"warn level", "warn", slog.LevelWarn},
		{"error level", "error", slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := newLogger(tt.level)
			if logger == nil {
				t.Fatal("newLogger returned nil")
			}
			// Verify the logger has the expected handler
			if logger.Handler() == nil {
				t.Error("logger handler is nil")
			}
			// Enable debug for this test and verify text handler output
			logger.Info("test message", "key", "value")
		})
	}
}

func TestNewLoggerEnabledLevel(t *testing.T) {
	// Verify log level is respected by checking Enabled
	logger := newLogger("warn")
	if !logger.Enabled(nil, slog.LevelWarn) {
		t.Error("expected warn level to be enabled")
	}
	if !logger.Enabled(nil, slog.LevelError) {
		t.Error("expected error level to be enabled")
	}
	if logger.Enabled(nil, slog.LevelInfo) {
		t.Error("expected info level to be disabled at warn")
	}
	if logger.Enabled(nil, slog.LevelDebug) {
		t.Error("expected debug level to be disabled at warn")
	}
}

// TestNewLoggerWithFormat proves RABBITHOLE_LOG_FORMAT reaches the handler:
// "json" emits one JSON object per line, "text" stays key=value, and the level
// filter still applies in both.
func TestNewLoggerWithFormat(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		out := captureStderr(func() {
			newLoggerWithFormat("info", "json").Info("hello", "key", "value")
		})

		line := strings.TrimSpace(out)
		if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
			t.Fatalf("json format: got %q, want a JSON object", line)
		}
		if !strings.Contains(line, `"msg":"hello"`) || !strings.Contains(line, `"key":"value"`) {
			t.Errorf("json format: missing structured fields in %q", line)
		}
	})

	t.Run("text default", func(t *testing.T) {
		out := captureStderr(func() {
			newLoggerWithFormat("info", "text").Info("hello", "key", "value")
		})

		if strings.HasPrefix(strings.TrimSpace(out), "{") {
			t.Fatalf("text format: got JSON output %q", out)
		}
		if !strings.Contains(out, "msg=hello") || !strings.Contains(out, "key=value") {
			t.Errorf("text format: unexpected output %q", out)
		}
	})

	t.Run("format value does not change the level filter", func(t *testing.T) {
		logger := newLoggerWithFormat("warn", "json")
		if logger.Enabled(context.TODO(), slog.LevelInfo) {
			t.Error("expected info to stay disabled at warn with json format")
		}
	})
}

func TestNowFunc(t *testing.T) {
	// nowFunc is a package-level variable defaulting to time.Now
	// Verify it's callable and returns a non-zero time
	now := nowFunc()
	if now.IsZero() {
		t.Error("nowFunc() returned zero time")
	}
}

func TestFormatBytesNegative(t *testing.T) {
	// formatBytes handles negative values
	got := formatBytes(-1024)
	if !strings.Contains(got, "B") {
		t.Errorf("formatBytes(-1024) = %q, expected bytes format", got)
	}
}

func TestHumanizeBytesNegative(t *testing.T) {
	got := humanizeBytes(-100)
	if !strings.Contains(got, "B") {
		t.Errorf("humanizeBytes(-100) = %q, expected bytes format", got)
	}
}
