package main

import (
	"os"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	// Without any env vars, loadConfig should return defaults
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() returned error: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9734" {
		t.Errorf("default ListenAddr = %q, want %q", cfg.ListenAddr, "127.0.0.1:9734")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("default LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.BufferSize != 100000 {
		t.Errorf("default BufferSize = %d, want %d", cfg.BufferSize, 100000)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("default RetentionDays = %d, want %d", cfg.RetentionDays, 30)
	}
	if !cfg.TLSIntercept {
		t.Error("default TLSIntercept should be true")
	}
	if cfg.MaxSessions != 50 {
		t.Errorf("default MaxSessions = %d, want %d", cfg.MaxSessions, 50)
	}
}

func TestLoadConfigWithEnv(t *testing.T) {
	// Set env vars and verify they're picked up
	os.Setenv("RABBITHOLE_LISTEN_ADDR", "0.0.0.0:9999")
	os.Setenv("RABBITHOLE_LOG_LEVEL", "debug")
	os.Setenv("RABBITHOLE_BUFFER_SIZE", "50000")
	os.Setenv("RABBITHOLE_MAX_SESSIONS", "10")
	os.Setenv("RABBITHOLE_RETENTION_DAYS", "90")
	os.Setenv("RABBITHOLE_TLS_INTERCEPT", "false")
	defer func() {
		os.Unsetenv("RABBITHOLE_LISTEN_ADDR")
		os.Unsetenv("RABBITHOLE_LOG_LEVEL")
		os.Unsetenv("RABBITHOLE_BUFFER_SIZE")
		os.Unsetenv("RABBITHOLE_MAX_SESSIONS")
		os.Unsetenv("RABBITHOLE_RETENTION_DAYS")
		os.Unsetenv("RABBITHOLE_TLS_INTERCEPT")
	}()

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() returned error: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:9999" {
		t.Errorf("ListenAddr = %q, want %q", cfg.ListenAddr, "0.0.0.0:9999")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.BufferSize != 50000 {
		t.Errorf("BufferSize = %d, want %d", cfg.BufferSize, 50000)
	}
	if cfg.MaxSessions != 10 {
		t.Errorf("MaxSessions = %d, want %d", cfg.MaxSessions, 10)
	}
	if cfg.RetentionDays != 90 {
		t.Errorf("RetentionDays = %d, want %d", cfg.RetentionDays, 90)
	}
	if cfg.TLSIntercept {
		t.Error("TLSIntercept should be false")
	}
}

func TestLoadConfigDataDirOverride(t *testing.T) {
	// Test that setting RABBITHOLE_DATA_DIR impacts dependent paths
	// Create the dir so config.Validate() (wired into loadConfig via PROD-008)
	// doesn't reject it for not existing.
	dir := t.TempDir()
	os.Setenv("RABBITHOLE_DATA_DIR", dir)
	defer os.Unsetenv("RABBITHOLE_DATA_DIR")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() returned error: %v", err)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
	// DBPath and ModelPath should be re-derived from DataDir
	if cfg.DBPath != dir+"/rabbit-hole.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, dir+"/rabbit-hole.db")
	}
	if cfg.ModelPath != dir+"/models/gemma-3-4b.gguf" {
		t.Errorf("ModelPath = %q, want %q", cfg.ModelPath, dir+"/models/gemma-3-4b.gguf")
	}
}

func TestLoadConfigBadTimeout(t *testing.T) {
	os.Setenv("RABBITHOLE_READ_TIMEOUT", "not-a-duration")
	defer os.Unsetenv("RABBITHOLE_READ_TIMEOUT")

	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for bad RABBITHOLE_READ_TIMEOUT, got nil")
	}
}

func TestLoadConfigBadBufferSize(t *testing.T) {
	os.Setenv("RABBITHOLE_BUFFER_SIZE", "not-a-number")
	defer os.Unsetenv("RABBITHOLE_BUFFER_SIZE")

	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for bad RABBITHOLE_BUFFER_SIZE, got nil")
	}
}

func TestLoadConfigBadBool(t *testing.T) {
	os.Setenv("RABBITHOLE_TLS_INTERCEPT", "maybe")
	defer os.Unsetenv("RABBITHOLE_TLS_INTERCEPT")

	_, err := loadConfig()
	if err == nil {
		t.Error("expected error for bad RABBITHOLE_TLS_INTERCEPT, got nil")
	}
}
