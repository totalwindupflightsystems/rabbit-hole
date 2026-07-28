package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	// Verify sensible defaults
	if cfg.ListenAddr != "127.0.0.1:9734" {
		t.Errorf("expected ListenAddr 127.0.0.1:9734, got %s", cfg.ListenAddr)
	}
	if cfg.BufferSize != 100000 {
		t.Errorf("expected BufferSize 100000, got %d", cfg.BufferSize)
	}
	if cfg.MaxSessions != 50 {
		t.Errorf("expected MaxSessions 50, got %d", cfg.MaxSessions)
	}
	if cfg.ModelName != "gemma-3-4b" {
		t.Errorf("expected ModelName gemma-3-4b, got %s", cfg.ModelName)
	}
	if cfg.TLSIntercept != true {
		t.Errorf("expected TLSIntercept true, got false")
	}
	if cfg.ContextWindows != false {
		t.Errorf("expected ContextWindows false, got true")
	}
	if cfg.ChatEnabled != true {
		t.Errorf("expected ChatEnabled true, got false")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected LogLevel info, got %s", cfg.LogLevel)
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("expected RetentionDays 30, got %d", cfg.RetentionDays)
	}
	if cfg.ModelThreads != 4 {
		t.Errorf("expected ModelThreads 4, got %d", cfg.ModelThreads)
	}
	if cfg.ModelGPULayers != 0 {
		t.Errorf("expected ModelGPULayers 0, got %d", cfg.ModelGPULayers)
	}
	if cfg.BatchSize != 100 {
		t.Errorf("expected BatchSize 100, got %d", cfg.BatchSize)
	}

	// Verify duration defaults
	if cfg.ReadTimeout != 30*time.Second {
		t.Errorf("expected ReadTimeout 30s, got %v", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 30*time.Second {
		t.Errorf("expected WriteTimeout 30s, got %v", cfg.WriteTimeout)
	}
	if cfg.BatchInterval != 500*time.Millisecond {
		t.Errorf("expected BatchInterval 500ms, got %v", cfg.BatchInterval)
	}
	if cfg.BatchTimeout != 500*time.Millisecond {
		t.Errorf("expected BatchTimeout 500ms, got %v", cfg.BatchTimeout)
	}
	if cfg.InferenceTimeout != 5*time.Second {
		t.Errorf("expected InferenceTimeout 5s, got %v", cfg.InferenceTimeout)
	}
	if cfg.CompactInterval != 1*time.Hour {
		t.Errorf("expected CompactInterval 1h, got %v", cfg.CompactInterval)
	}

	// Verify data dir uses home directory
	home, _ := os.UserHomeDir()
	expectedDataDir := filepath.Join(home, ".rabbit-hole")
	if cfg.DataDir != expectedDataDir {
		t.Errorf("expected DataDir %s, got %s", expectedDataDir, cfg.DataDir)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	// Set environment variables
	envVars := map[string]string{
		"RABBITHOLE_LISTEN_ADDR":       "0.0.0.0:8080",
		"RABBITHOLE_BUFFER_SIZE":       "50000",
		"RABBITHOLE_MAX_SESSIONS":      "25",
		"RABBITHOLE_TLS_INTERCEPT":     "false",
		"RABBITHOLE_CONTEXT_WINDOWS":   "true",
		"RABBITHOLE_CHAT_ENABLED":      "false",
		"RABBITHOLE_LOG_LEVEL":         "debug",
		"RABBITHOLE_RETENTION_DAYS":    "60",
		"RABBITHOLE_BATCH_SIZE":        "200",
		"RABBITHOLE_MODEL_THREADS":     "8",
		"RABBITHOLE_MODEL_GPU_LAYERS":  "16",
		"RABBITHOLE_BATCH_INTERVAL":    "1s",
		"RABBITHOLE_BATCH_TIMEOUT":     "2s",
		"RABBITHOLE_INFERENCE_TIMEOUT": "10s",
		"RABBITHOLE_READ_TIMEOUT":      "60s",
		"RABBITHOLE_WRITE_TIMEOUT":     "60s",
		"RABBITHOLE_COMPACT_INTERVAL":  "2h",
		"RABBITHOLE_WS_PING_INTERVAL":  "15s",
		"RABBITHOLE_WS_READ_TIMEOUT":   "30s",
		"RABBITHOLE_MODEL_NAME":        "gemma-3-1b",
		"RABBITHOLE_MODEL_PATH":        "/opt/models/gemma.gguf",
		"RABBITHOLE_DB_PATH":           "/data/rabbit-hole.db",
		"RABBITHOLE_CORS_ORIGINS":      "http://localhost:3000",
		"RABBITHOLE_BPF_DEBUG":         "true",
		"RABBITHOLE_DATA_DIR":          "/opt/rabbit-hole",
	}
	for k, v := range envVars {
		os.Setenv(k, v)
	}
	defer func() {
		for k := range envVars {
			os.Unsetenv(k)
		}
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.ListenAddr != "0.0.0.0:8080" {
		t.Errorf("ListenAddr: expected 0.0.0.0:8080, got %s", cfg.ListenAddr)
	}
	if cfg.BufferSize != 50000 {
		t.Errorf("BufferSize: expected 50000, got %d", cfg.BufferSize)
	}
	if cfg.MaxSessions != 25 {
		t.Errorf("MaxSessions: expected 25, got %d", cfg.MaxSessions)
	}
	if cfg.TLSIntercept != false {
		t.Errorf("TLSIntercept: expected false, got true")
	}
	if cfg.ContextWindows != true {
		t.Errorf("ContextWindows: expected true, got false")
	}
	if cfg.ChatEnabled != false {
		t.Errorf("ChatEnabled: expected false, got true")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel: expected debug, got %s", cfg.LogLevel)
	}
	if cfg.RetentionDays != 60 {
		t.Errorf("RetentionDays: expected 60, got %d", cfg.RetentionDays)
	}
	if cfg.BatchSize != 200 {
		t.Errorf("BatchSize: expected 200, got %d", cfg.BatchSize)
	}
	if cfg.ModelThreads != 8 {
		t.Errorf("ModelThreads: expected 8, got %d", cfg.ModelThreads)
	}
	if cfg.ModelGPULayers != 16 {
		t.Errorf("ModelGPULayers: expected 16, got %d", cfg.ModelGPULayers)
	}
	if cfg.ModelName != "gemma-3-1b" {
		t.Errorf("ModelName: expected gemma-3-1b, got %s", cfg.ModelName)
	}
	if cfg.ModelPath != "/opt/models/gemma.gguf" {
		t.Errorf("ModelPath: expected /opt/models/gemma.gguf, got %s", cfg.ModelPath)
	}
	if cfg.DBPath != "/data/rabbit-hole.db" {
		t.Errorf("DBPath: expected /data/rabbit-hole.db, got %s", cfg.DBPath)
	}
	if cfg.CORSOrigins != "http://localhost:3000" {
		t.Errorf("CORSOrigins: expected http://localhost:3000, got %s", cfg.CORSOrigins)
	}
	if cfg.BPFDebug != true {
		t.Errorf("BPFDebug: expected true, got false")
	}
	if cfg.DataDir != "/opt/rabbit-hole" {
		t.Errorf("DataDir: expected /opt/rabbit-hole, got %s", cfg.DataDir)
	}

	// Duration checks
	if cfg.BatchInterval != 1*time.Second {
		t.Errorf("BatchInterval: expected 1s, got %v", cfg.BatchInterval)
	}
	if cfg.BatchTimeout != 2*time.Second {
		t.Errorf("BatchTimeout: expected 2s, got %v", cfg.BatchTimeout)
	}
	if cfg.InferenceTimeout != 10*time.Second {
		t.Errorf("InferenceTimeout: expected 10s, got %v", cfg.InferenceTimeout)
	}
	if cfg.ReadTimeout != 60*time.Second {
		t.Errorf("ReadTimeout: expected 60s, got %v", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 60*time.Second {
		t.Errorf("WriteTimeout: expected 60s, got %v", cfg.WriteTimeout)
	}
	if cfg.CompactInterval != 2*time.Hour {
		t.Errorf("CompactInterval: expected 2h, got %v", cfg.CompactInterval)
	}
	if cfg.WSPingInterval != 15*time.Second {
		t.Errorf("WSPingInterval: expected 15s, got %v", cfg.WSPingInterval)
	}
	if cfg.WSReadTimeout != 30*time.Second {
		t.Errorf("WSReadTimeout: expected 30s, got %v", cfg.WSReadTimeout)
	}
}

func TestLoadDataDirDerivesPaths(t *testing.T) {
	os.Setenv("RABBITHOLE_DATA_DIR", "/custom/data")
	defer os.Unsetenv("RABBITHOLE_DATA_DIR")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.DataDir != "/custom/data" {
		t.Errorf("DataDir: expected /custom/data, got %s", cfg.DataDir)
	}
	if cfg.DBPath != "/custom/data/rabbit-hole.db" {
		t.Errorf("DBPath: expected /custom/data/rabbit-hole.db, got %s", cfg.DBPath)
	}
	if cfg.ModelPath != "/custom/data/models/gemma-3-4b.gguf" {
		t.Errorf("ModelPath: expected /custom/data/models/gemma-3-4b.gguf, got %s", cfg.ModelPath)
	}
}

func TestLoadExplicitPathsOverrideDataDir(t *testing.T) {
	os.Setenv("RABBITHOLE_DATA_DIR", "/custom/data")
	os.Setenv("RABBITHOLE_DB_PATH", "/explicit/db.sqlite")
	os.Setenv("RABBITHOLE_MODEL_PATH", "/explicit/model.gguf")
	defer func() {
		os.Unsetenv("RABBITHOLE_DATA_DIR")
		os.Unsetenv("RABBITHOLE_DB_PATH")
		os.Unsetenv("RABBITHOLE_MODEL_PATH")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.DataDir != "/custom/data" {
		t.Errorf("DataDir: expected /custom/data, got %s", cfg.DataDir)
	}
	// Explicit paths override derived paths
	if cfg.DBPath != "/explicit/db.sqlite" {
		t.Errorf("DBPath: expected /explicit/db.sqlite, got %s", cfg.DBPath)
	}
	if cfg.ModelPath != "/explicit/model.gguf" {
		t.Errorf("ModelPath: expected /explicit/model.gguf, got %s", cfg.ModelPath)
	}
}

func TestValidateDefaults(t *testing.T) {
	cfg := Defaults()
	if err := cfg.Validate(); err != nil {
		t.Errorf("Default config should validate: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name:    "empty listen addr",
			modify:  func(c *Config) { c.ListenAddr = "" },
			wantErr: "RABBITHOLE_LISTEN_ADDR must not be empty",
		},
		{
			name:    "buffer too small",
			modify:  func(c *Config) { c.BufferSize = 100 },
			wantErr: "RABBITHOLE_BUFFER_SIZE must be at least 1000",
		},
		{
			name:    "zero max sessions",
			modify:  func(c *Config) { c.MaxSessions = 0 },
			wantErr: "RABBITHOLE_MAX_SESSIONS must be at least 1",
		},
		{
			name:    "invalid log level",
			modify:  func(c *Config) { c.LogLevel = "verbose" },
			wantErr: "RABBITHOLE_LOG_LEVEL must be one of: debug, info, warn, error",
		},
		{
			name:    "negative retention",
			modify:  func(c *Config) { c.RetentionDays = 0 },
			wantErr: "RABBITHOLE_RETENTION_DAYS must be at least 1",
		},
		{
			name:    "zero model threads",
			modify:  func(c *Config) { c.ModelThreads = 0 },
			wantErr: "RABBITHOLE_MODEL_THREADS must be at least 1",
		},
		{
			name:    "invalid listen address format",
			modify:  func(c *Config) { c.ListenAddr = "bad-addr" },
			wantErr: "RABBITHOLE_LISTEN_ADDR must be in host:port format (e.g., 127.0.0.1:9734)",
		},
		{
			name:    "non-existent data dir",
			modify:  func(c *Config) { c.DataDir = "/this/path/should/not/exist/rabbit-hole-test-xyz" },
			wantErr: "RABBITHOLE_DATA_DIR does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Defaults()
			tt.modify(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() == "" {
				t.Fatal("expected non-empty error message")
			}
		})
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	os.Setenv("RABBITHOLE_BATCH_INTERVAL", "not-a-duration")
	defer os.Unsetenv("RABBITHOLE_BATCH_INTERVAL")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid duration, got nil")
	}
}

func TestLoadInvalidInt(t *testing.T) {
	os.Setenv("RABBITHOLE_BUFFER_SIZE", "not-a-number")
	defer os.Unsetenv("RABBITHOLE_BUFFER_SIZE")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid int, got nil")
	}
}

func TestLoadInvalidBool(t *testing.T) {
	os.Setenv("RABBITHOLE_TLS_INTERCEPT", "maybe")
	defer os.Unsetenv("RABBITHOLE_TLS_INTERCEPT")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid bool, got nil")
	}
}

// TestValidateValidAddr confirms that a well-formed host:port listen address
// passes the new SplitHostPort check without producing an error.
func TestValidateValidAddr(t *testing.T) {
	cfg := Defaults()
	cfg.ListenAddr = "127.0.0.1:9734"
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected validation to pass for 127.0.0.1:9734, got: %v", err)
	}
}

// TestValidateDataDirNotExist confirms that a non-existent DataDir triggers a
// validation error.
func TestValidateDataDirNotExist(t *testing.T) {
	cfg := Defaults()
	cfg.DataDir = "/this/path/should/not/exist/rabbit-hole-test-xyz-12345"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for non-existent DataDir, got nil")
	}
	if !strings.Contains(err.Error(), "RABBITHOLE_DATA_DIR does not exist") {
		t.Errorf("expected error mentioning RABBITHOLE_DATA_DIR does not exist, got: %v", err)
	}
}
