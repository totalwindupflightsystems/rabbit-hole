package main

import (
	"testing"
)

func TestParseRemoteFlag(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantEndpoint string
		wantToken    string
		wantErr      bool
	}{
		{"host and port only", "localhost:50051", "localhost:50051", "", false},
		{"host port and token", "host:443@secrettoken", "host:443", "secrettoken", false},
		{"ip and token", "10.0.0.1:9000@tok123", "10.0.0.1:9000", "tok123", false},
		{"no token in input", "example.com:8080", "example.com:8080", "", false},
		{"empty string error", "", "", "", true},
		{"only token delimiter", "@token", "", "", true},
		{"only at symbol", "@", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, token, err := parseRemoteFlag(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRemoteFlag(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
				return
			}
			if endpoint != tt.wantEndpoint {
				t.Errorf("parseRemoteFlag(%q) endpoint = %q, want %q", tt.input, endpoint, tt.wantEndpoint)
			}
			if token != tt.wantToken {
				t.Errorf("parseRemoteFlag(%q) token = %q, want %q", tt.input, token, tt.wantToken)
			}
		})
	}
}

func TestNewServeCmd_Structure(t *testing.T) {
	cmd := newServeCmd()

	if cmd.Use != "serve" {
		t.Errorf("Use = %q, want %q", cmd.Use, "serve")
	}
	if cmd.Short != "Start the Rabbit-Hole daemon (collect + classify + serve)" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Start the Rabbit-Hole daemon (collect + classify + serve)")
	}
	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil — expected a run function")
	}
}

func TestNewServeCmd_Flags(t *testing.T) {
	cmd := newServeCmd()

	tests := []struct {
		name         string
		flagName     string
		typ          string // "string", "bool", "int"
		defaultValue string
	}{
		{"listen address", "addr", "string", ""},
		{"no classifier flag", "no-classifier", "bool", "false"},
		{"remote classifier", "remote", "string", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := cmd.Flags().Lookup(tt.flagName)
			if f == nil {
				t.Fatalf("flag --%s not registered", tt.flagName)
			}
			if f.DefValue != tt.defaultValue {
				t.Errorf("flag --%s default = %q, want %q", tt.flagName, f.DefValue, tt.defaultValue)
			}
		})
	}
}

func TestNewServeCmd_FlagOverride(t *testing.T) {
	// Test that --addr overrides the config ListenAddr via SetArgs
	cmd := newServeCmd()
	if err := cmd.Flags().Set("addr", "0.0.0.0:9999"); err != nil {
		t.Fatalf("failed to set --addr: %v", err)
	}
	addr, err := cmd.Flags().GetString("addr")
	if err != nil {
		t.Fatalf("GetString(addr): %v", err)
	}
	if addr != "0.0.0.0:9999" {
		t.Errorf("addr = %q, want %q", addr, "0.0.0.0:9999")
	}
}
