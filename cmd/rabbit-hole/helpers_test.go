package main

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLogLevel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{"debug level", "debug", slog.LevelDebug},
		{"warn level", "warn", slog.LevelWarn},
		{"error level", "error", slog.LevelError},
		{"info default when missing", "info", slog.LevelInfo},
		{"unknown defaults to info", "bogus", slog.LevelInfo},
		{"empty defaults to info", "", slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logLevel(tt.input); got != tt.want {
				t.Errorf("logLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{"zero bytes", 0, "0 B"},
		{"just under 1 KB", 512, "512 B"},
		{"exactly 1 KB", 1024, "1.0 KB"},
		{"1.5 KB", 1536, "1.5 KB"},
		{"2 KB", 2048, "2.0 KB"},
		{"exactly 1 MB", 1048576, "1.0 MB"},
		{"1.5 MB", 1572864, "1.5 MB"},
		{"exactly 1 GB", 1073741824, "1.0 GB"},
		{"1.5 GB", 1610612736, "1.5 GB"},
		{"exactly 1 TB", 1099511627776, "1.0 TB"},
		{"1 PB", 1125899906842624, "1.0 PB"},
		{"1 EB", 1152921504606846976, "1.0 EB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatBytes(tt.input); got != tt.want {
				t.Errorf("formatBytes(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
		errMsg  string // substring expected in the error when wantErr
	}{
		{"5 hours", "5h", 5 * time.Hour, false, ""},
		{"1 hour", "1h", time.Hour, false, ""},
		{"2 days", "2d", 48 * time.Hour, false, ""},
		{"negative 3 hours", "-3h", -3 * time.Hour, false, ""},
		{"zero hours", "0h", 0, false, ""},
		{"1 day is 24 hours", "1d", 24 * time.Hour, false, ""},
		{"negative days", "-7d", -7 * 24 * time.Hour, false, ""},
		{"10 minutes", "10m", 10 * time.Minute, false, ""},
		{"5 minutes", "5m", 5 * time.Minute, false, ""},
		{"90 seconds", "90s", 90 * time.Second, false, ""},
		{"30 seconds", "30s", 30 * time.Second, false, ""},
		{"negative 5 minutes", "-5m", -5 * time.Minute, false, ""},
		{"empty string returns error", "", 0, true, ""},
		{"single char returns error", "h", 0, true, ""},
		{"single char returns error (digit)", "5", 0, true, ""},
		{"unsupported suffix x", "5x", 0, true, "use s, m, h or d"},
		{"unsupported suffix w", "-5w", 0, true, "use s, m, h or d"},
		{"non-numeric prefix", "abch", 0, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDuration(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDuration(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("parseDuration(%q) error = %q, want it to contain %q", tt.input, err, tt.errMsg)
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
