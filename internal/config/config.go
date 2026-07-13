// Package config parses and validates Rabbit-Hole configuration from
// environment variables. All config values have sensible defaults for
// self-hosted single-machine deployment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all Rabbit-Hole configuration, parsed from environment variables.
type Config struct {
	// Data
	DataDir         string        // RABBITHOLE_DATA_DIR — default: ~/.rabbit-hole/
	DBPath          string        // RABBITHOLE_DB_PATH — default: $DATA_DIR/rabbit-hole.db
	ModelPath       string        // RABBITHOLE_MODEL_PATH — default: $DATA_DIR/models/gemma-3-4b.gguf
	ModelName       string        // RABBITHOLE_MODEL_NAME — default: gemma-3-4b

	// Server
	ListenAddr      string        // RABBITHOLE_LISTEN_ADDR — default: 127.0.0.1:9734
	ReadTimeout     time.Duration // RABBITHOLE_READ_TIMEOUT — default: 30s
	WriteTimeout    time.Duration // RABBITHOLE_WRITE_TIMEOUT — default: 30s
	CORSOrigins     string        // RABBITHOLE_CORS_ORIGINS — default: *

	// WebSocket
	WSPingInterval  time.Duration // RABBITHOLE_WS_PING_INTERVAL — default: 30s
	WSReadTimeout   time.Duration // RABBITHOLE_WS_READ_TIMEOUT — default: 60s

	// Collection
	BufferSize      int           // RABBITHOLE_BUFFER_SIZE — default: 100000
	TLSIntercept    bool          // RABBITHOLE_TLS_INTERCEPT — default: true
	MaxSessions     int           // RABBITHOLE_MAX_SESSIONS — default: 50

	// Classification
	BatchInterval   time.Duration // RABBITHOLE_BATCH_INTERVAL — default: 500ms
	BatchSize       int           // RABBITHOLE_BATCH_SIZE — default: 100
	BatchTimeout    time.Duration // RABBITHOLE_BATCH_TIMEOUT — default: 500ms
	ModelThreads    int           // RABBITHOLE_MODEL_THREADS — default: 4
	ModelGPULayers  int           // RABBITHOLE_MODEL_GPU_LAYERS — default: 0
	InferenceTimeout time.Duration // RABBITHOLE_INFERENCE_TIMEOUT — default: 5s
	ContextWindows  bool          // RABBITHOLE_CONTEXT_WINDOWS — default: false

	// Chat
	ChatEnabled     bool          // RABBITHOLE_CHAT_ENABLED — default: true

	// Maintenance
	RetentionDays   int           // RABBITHOLE_RETENTION_DAYS — default: 30
	CompactInterval time.Duration // RABBITHOLE_COMPACT_INTERVAL — default: 1h

	// Logging
	LogLevel        string        // RABBITHOLE_LOG_LEVEL — default: info

	// Debug
	BPFDebug        bool          // RABBITHOLE_BPF_DEBUG — default: false
}

// Defaults returns a Config with all default values populated.
// Use this as the base before applying overrides.
func Defaults() Config {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".rabbit-hole")

	return Config{
		DataDir:          dataDir,
		DBPath:           filepath.Join(dataDir, "rabbit-hole.db"),
		ModelPath:        filepath.Join(dataDir, "models", "gemma-3-4b.gguf"),
		ModelName:        "gemma-3-4b",
		ListenAddr:       "127.0.0.1:9734",
		ReadTimeout:      30 * time.Second,
		WriteTimeout:     30 * time.Second,
		CORSOrigins:      "*",
		WSPingInterval:   30 * time.Second,
		WSReadTimeout:    60 * time.Second,
		BufferSize:       100000,
		TLSIntercept:     true,
		MaxSessions:      50,
		BatchInterval:    500 * time.Millisecond,
		BatchSize:        100,
		BatchTimeout:     500 * time.Millisecond,
		ModelThreads:     4,
		ModelGPULayers:   0,
		InferenceTimeout: 5 * time.Second,
		ContextWindows:   false,
		ChatEnabled:      true,
		RetentionDays:    30,
		CompactInterval:  1 * time.Hour,
		LogLevel:         "info",
		BPFDebug:         false,
	}
}

// Load reads configuration from environment variables, starting with
// Defaults() and overriding any value that has a corresponding env var set.
func Load() (Config, error) {
	cfg := Defaults()

	if v := os.Getenv("RABBITHOLE_DATA_DIR"); v != "" {
		cfg.DataDir = v
		// Re-derive paths that depend on DataDir, unless explicitly overridden.
		if os.Getenv("RABBITHOLE_DB_PATH") == "" {
			cfg.DBPath = filepath.Join(v, "rabbit-hole.db")
		}
		if os.Getenv("RABBITHOLE_MODEL_PATH") == "" {
			cfg.ModelPath = filepath.Join(v, "models", "gemma-3-4b.gguf")
		}
	}

	// Paths (after DataDir is resolved)
	if v := os.Getenv("RABBITHOLE_DB_PATH"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("RABBITHOLE_MODEL_PATH"); v != "" {
		cfg.ModelPath = v
	}
	if v := os.Getenv("RABBITHOLE_MODEL_NAME"); v != "" {
		cfg.ModelName = v
	}

	// Server
	if v := os.Getenv("RABBITHOLE_LISTEN_ADDR"); v != "" {
		cfg.ListenAddr = v
	}
	if v := os.Getenv("RABBITHOLE_CORS_ORIGINS"); v != "" {
		cfg.CORSOrigins = v
	}

	// Durations
	if v := os.Getenv("RABBITHOLE_READ_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_READ_TIMEOUT: %w", err)
		}
		cfg.ReadTimeout = d
	}
	if v := os.Getenv("RABBITHOLE_WRITE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_WRITE_TIMEOUT: %w", err)
		}
		cfg.WriteTimeout = d
	}
	if v := os.Getenv("RABBITHOLE_WS_PING_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_WS_PING_INTERVAL: %w", err)
		}
		cfg.WSPingInterval = d
	}
	if v := os.Getenv("RABBITHOLE_WS_READ_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_WS_READ_TIMEOUT: %w", err)
		}
		cfg.WSReadTimeout = d
	}
	if v := os.Getenv("RABBITHOLE_BATCH_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_BATCH_INTERVAL: %w", err)
		}
		cfg.BatchInterval = d
	}
	if v := os.Getenv("RABBITHOLE_BATCH_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_BATCH_TIMEOUT: %w", err)
		}
		cfg.BatchTimeout = d
	}
	if v := os.Getenv("RABBITHOLE_INFERENCE_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_INFERENCE_TIMEOUT: %w", err)
		}
		cfg.InferenceTimeout = d
	}
	if v := os.Getenv("RABBITHOLE_COMPACT_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_COMPACT_INTERVAL: %w", err)
		}
		cfg.CompactInterval = d
	}

	// Integers
	if v := os.Getenv("RABBITHOLE_BUFFER_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_BUFFER_SIZE: %w", err)
		}
		cfg.BufferSize = n
	}
	if v := os.Getenv("RABBITHOLE_MAX_SESSIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_MAX_SESSIONS: %w", err)
		}
		cfg.MaxSessions = n
	}
	if v := os.Getenv("RABBITHOLE_BATCH_SIZE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_BATCH_SIZE: %w", err)
		}
		cfg.BatchSize = n
	}
	if v := os.Getenv("RABBITHOLE_MODEL_THREADS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_MODEL_THREADS: %w", err)
		}
		cfg.ModelThreads = n
	}
	if v := os.Getenv("RABBITHOLE_MODEL_GPU_LAYERS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_MODEL_GPU_LAYERS: %w", err)
		}
		cfg.ModelGPULayers = n
	}
	if v := os.Getenv("RABBITHOLE_RETENTION_DAYS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_RETENTION_DAYS: %w", err)
		}
		cfg.RetentionDays = n
	}

	// Booleans
	if v := os.Getenv("RABBITHOLE_TLS_INTERCEPT"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_TLS_INTERCEPT: %w", err)
		}
		cfg.TLSIntercept = b
	}
	if v := os.Getenv("RABBITHOLE_CONTEXT_WINDOWS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_CONTEXT_WINDOWS: %w", err)
		}
		cfg.ContextWindows = b
	}
	if v := os.Getenv("RABBITHOLE_CHAT_ENABLED"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_CHAT_ENABLED: %w", err)
		}
		cfg.ChatEnabled = b
	}
	if v := os.Getenv("RABBITHOLE_BPF_DEBUG"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("RABBITHOLE_BPF_DEBUG: %w", err)
		}
		cfg.BPFDebug = b
	}

	// String
	if v := os.Getenv("RABBITHOLE_LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}

	return cfg, nil
}

// Validate checks the configuration for correctness.
// Returns nil if all values are within acceptable ranges.
func (c Config) Validate() error {
	var errs []string

	if c.ListenAddr == "" {
		errs = append(errs, "RABBITHOLE_LISTEN_ADDR must not be empty")
	}
	if c.DataDir == "" {
		errs = append(errs, "RABBITHOLE_DATA_DIR must not be empty")
	}
	if c.BufferSize < 1000 {
		errs = append(errs, "RABBITHOLE_BUFFER_SIZE must be at least 1000")
	}
	if c.MaxSessions < 1 {
		errs = append(errs, "RABBITHOLE_MAX_SESSIONS must be at least 1")
	}
	if c.BatchSize < 1 {
		errs = append(errs, "RABBITHOLE_BATCH_SIZE must be at least 1")
	}
	if c.BatchTimeout <= 0 {
		errs = append(errs, "RABBITHOLE_BATCH_TIMEOUT must be positive")
	}
	if c.BatchInterval <= 0 {
		errs = append(errs, "RABBITHOLE_BATCH_INTERVAL must be positive")
	}
	if c.InferenceTimeout <= 0 {
		errs = append(errs, "RABBITHOLE_INFERENCE_TIMEOUT must be positive")
	}
	if c.RetentionDays < 1 {
		errs = append(errs, "RABBITHOLE_RETENTION_DAYS must be at least 1")
	}
	if c.ModelThreads < 1 {
		errs = append(errs, "RABBITHOLE_MODEL_THREADS must be at least 1")
	}
	if !validLogLevel(c.LogLevel) {
		errs = append(errs, "RABBITHOLE_LOG_LEVEL must be one of: debug, info, warn, error")
	}

	if len(errs) > 0 {
		return fmt.Errorf("configuration validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validLogLevel(level string) bool {
	switch level {
	case "debug", "info", "warn", "error":
		return true
	default:
		return false
	}
}
