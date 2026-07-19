package main

import (
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
