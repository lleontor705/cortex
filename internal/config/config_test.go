package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name       string
		configYAML string
		envVars    map[string]string
		wantErr    bool
		checkFunc  func(*testing.T, *Config)
	}{
		{
			name:       "default configuration",
			configYAML: "",
			envVars:    nil,
			wantErr:    false,
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.Server.Name != "cortex" {
					t.Errorf("expected server name 'cortex', got '%s'", cfg.Server.Name)
				}
				if cfg.Server.Version != "2.0.0" {
					t.Errorf("expected default server version '2.0.0' (v2 line, REQ-DB-001), got '%s'", cfg.Server.Version)
				}
				if cfg.HTTP.Port != 7438 {
					t.Errorf("expected HTTP port 7438, got %d", cfg.HTTP.Port)
				}
				if cfg.Database.Pragma.ForeignKeys != true {
					t.Errorf("expected foreign_keys to be true")
				}
			},
		},
		{
			name: "custom YAML configuration",
			configYAML: `
server:
  name: test-server
  version: 1.0.0
database:
  path: /tmp/test.db
  in_memory: true
  pragma:
    journal_mode: MEMORY
    cache_size: -32000
http:
  port: 9090
  host: 0.0.0.0
  token: secret
logging:
  level: debug
  format: text
`,
			envVars: nil,
			wantErr: false,
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.Server.Name != "test-server" {
					t.Errorf("expected server name 'test-server', got '%s'", cfg.Server.Name)
				}
				if cfg.Database.InMemory != true {
					t.Errorf("expected in_memory to be true")
				}
				if cfg.HTTP.Port != 9090 {
					t.Errorf("expected HTTP port 9090, got %d", cfg.HTTP.Port)
				}
				if cfg.HTTP.Token != "secret" {
					t.Errorf("expected HTTP token 'secret', got '%s'", cfg.HTTP.Token)
				}
				if cfg.Logging.Level != "debug" {
					t.Errorf("expected logging level 'debug', got '%s'", cfg.Logging.Level)
				}
			},
		},
		{
			name:       "environment variable override",
			configYAML: "",
			envVars: map[string]string{
				"CORTEX_SERVER_NAME":   "env-server",
				"CORTEX_HTTP_PORT":     "3000",
				"CORTEX_HTTP_TOKEN":    "env-token",
				"CORTEX_LOGGING_LEVEL": "error",
				"CORTEX_DATABASE_PATH": "/custom/path.db",
			},
			wantErr: false,
			checkFunc: func(t *testing.T, cfg *Config) {
				if cfg.Server.Name != "env-server" {
					t.Errorf("expected server name 'env-server', got '%s'", cfg.Server.Name)
				}
				if cfg.HTTP.Port != 3000 {
					t.Errorf("expected HTTP port 3000, got %d", cfg.HTTP.Port)
				}
				if cfg.HTTP.Token != "env-token" {
					t.Errorf("expected HTTP token 'env-token', got '%s'", cfg.HTTP.Token)
				}
				if cfg.Logging.Level != "error" {
					t.Errorf("expected logging level 'error', got '%s'", cfg.Logging.Level)
				}
				if cfg.Database.Path != "/custom/path.db" {
					t.Errorf("expected database path '/custom/path.db', got '%s'", cfg.Database.Path)
				}
			},
		},
		{
			name: "invalid logging level",
			configYAML: `
logging:
  level: invalid
`,
			envVars: nil,
			wantErr: true,
		},
		{
			name: "invalid HTTP port",
			configYAML: `
http:
  port: 99999
`,
			envVars: nil,
			wantErr: true,
		},
		{
			name: "invalid journal mode",
			configYAML: `
database:
  pragma:
    journal_mode: INVALID
`,
			envVars: nil,
			wantErr: true,
		},
		{
			name: "empty database path without in_memory",
			configYAML: `
database:
  path: ""
  in_memory: false
`,
			envVars: nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment variables before each test
			clearEnvVars(t)

			// Set test environment variables
			for k, v := range tt.envVars {
				if err := os.Setenv(k, v); err != nil {
					t.Fatalf("failed to set env var %s: %v", k, err)
				}
			}

			// Create temp config file if YAML is provided
			var configPath string
			if tt.configYAML != "" {
				tmpDir := t.TempDir()
				configPath = filepath.Join(tmpDir, "cortex.yaml")
				if err := os.WriteFile(configPath, []byte(tt.configYAML), 0644); err != nil {
					t.Fatalf("failed to write config file: %v", err)
				}
			}

			// Load configuration
			cfg, err := Load(configPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && tt.checkFunc != nil {
				tt.checkFunc(t, cfg)
			}
		})
	}
}

func TestLoadFromEnv(t *testing.T) {
	// Clear environment variables
	clearEnvVars(t)

	// Set environment variables
	envVars := map[string]string{
		"CORTEX_SERVER_NAME":   "env-only-server",
		"CORTEX_HTTP_PORT":     "7000",
		"CORTEX_MCP_ENABLED":   "false",
		"CORTEX_LOGGING_LEVEL": "warn",
	}

	for k, v := range envVars {
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("failed to set env var %s: %v", k, err)
		}
	}
	defer clearEnvVars(t)

	cfg, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.Server.Name != "env-only-server" {
		t.Errorf("expected server name 'env-only-server', got '%s'", cfg.Server.Name)
	}
	if cfg.HTTP.Port != 7000 {
		t.Errorf("expected HTTP port 7000, got %d", cfg.HTTP.Port)
	}
	if cfg.MCP.Enabled != false {
		t.Errorf("expected MCP enabled to be false")
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("expected logging level 'warn', got '%s'", cfg.Logging.Level)
	}
}

func TestGetEnvVarName(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{"server.name", "CORTEX_SERVER_NAME"},
		{"database.path", "CORTEX_DATABASE_PATH"},
		{"http.port", "CORTEX_HTTP_PORT"},
		{"logging.level", "CORTEX_LOGGING_LEVEL"},
		{"database.pragma.journal_mode", "CORTEX_DATABASE_PRAGMA_JOURNAL_MODE"},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := GetEnvVarName(tt.key)
			if result != tt.expected {
				t.Errorf("GetEnvVarName(%s) = %s, want %s", tt.key, result, tt.expected)
			}
		})
	}
}

func TestSetAndClearEnvVar(t *testing.T) {
	key := "test.key"
	value := "test-value"

	// Set env var
	if err := SetEnvVar(key, value); err != nil {
		t.Fatalf("SetEnvVar() error = %v", err)
	}

	// Verify it was set
	envName := GetEnvVarName(key)
	if os.Getenv(envName) != value {
		t.Errorf("env var %s not set correctly", envName)
	}

	// Clear env var
	if err := ClearEnvVar(key); err != nil {
		t.Fatalf("ClearEnvVar() error = %v", err)
	}

	// Verify it was cleared
	if os.Getenv(envName) != "" {
		t.Errorf("env var %s not cleared", envName)
	}
}

func TestConfigString(t *testing.T) {
	clearEnvVars(t)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	str := cfg.String()
	if str == "" {
		t.Error("String() returned empty string")
	}

	// Verify it contains expected fields
	if !containsAll(str, "Server", "Database", "MCP", "HTTP", "Logging", "Search", "Memory", "Lifecycle") {
		t.Errorf("String() missing expected fields: %s", str)
	}
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid config",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name: "invalid logging level",
			modify: func(c *Config) {
				c.Logging.Level = "invalid"
			},
			wantErr: true,
		},
		{
			name: "invalid logging format",
			modify: func(c *Config) {
				c.Logging.Format = "invalid"
			},
			wantErr: true,
		},
		{
			name: "port too low",
			modify: func(c *Config) {
				c.HTTP.Port = 0
			},
			wantErr: true,
		},
		{
			name: "port too high",
			modify: func(c *Config) {
				c.HTTP.Port = 70000
			},
			wantErr: true,
		},
		{
			name: "invalid synchronous pragma",
			modify: func(c *Config) {
				c.Database.Pragma.Synchronous = "INVALID"
			},
			wantErr: true,
		},
		{
			name: "invalid temp_store pragma",
			modify: func(c *Config) {
				c.Database.Pragma.TempStore = "INVALID"
			},
			wantErr: true,
		},
		{
			name: "max_limit less than default_limit",
			modify: func(c *Config) {
				c.Search.DefaultLimit = 50
				c.Search.MaxLimit = 20
			},
			wantErr: true,
		},
		{
			name: "min_archive_score negative",
			modify: func(c *Config) {
				c.Memory.MinArchiveScore = -0.5
			},
			wantErr: true,
		},
		{
			name: "min_archive_score too high",
			modify: func(c *Config) {
				c.Memory.MinArchiveScore = 6.0
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server: ServerConfig{Name: "test", Version: "1.0"},
				Database: DatabaseConfig{
					Path:     "test.db",
					InMemory: false,
					Pragma: PragmaConfig{
						JournalMode: "WAL",
						Synchronous: "NORMAL",
						TempStore:   "MEMORY",
					},
				},
				HTTP:      HTTPConfig{Enabled: true, Port: 7438},
				Logging:   LoggingConfig{Level: "info", Format: "json"},
				Search:    SearchConfig{DefaultLimit: 20, MaxLimit: 100, FTS5: true, FusionK: 60},
				Memory:    MemoryConfig{MaxObservationLength: 50000, DedupeWindow: "15m", AutoArchiveDays: 90, DecayHalfLifeDays: 30, MinArchiveScore: 0.1},
				Lifecycle: LifecycleConfig{EnableAutoArchive: true, ArchiveCheckInterval: "1h"},
			}

			tt.modify(cfg)
			err := validate(cfg)

			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Helper function to clear all CORTEX environment variables
func clearEnvVars(t *testing.T) {
	t.Helper()
	envVars := []string{
		"CORTEX_SERVER_NAME",
		"CORTEX_SERVER_VERSION",
		"CORTEX_DATABASE_PATH",
		"CORTEX_DATABASE_IN_MEMORY",
		"CORTEX_DATABASE_PRAGMA_JOURNAL_MODE",
		"CORTEX_DATABASE_PRAGMA_SYNCHRONOUS",
		"CORTEX_DATABASE_PRAGMA_CACHE_SIZE",
		"CORTEX_DATABASE_PRAGMA_FOREIGN_KEYS",
		"CORTEX_DATABASE_PRAGMA_TEMP_STORE",
		"CORTEX_DATABASE_PRAGMA_MMAP_SIZE",
		"CORTEX_MCP_ENABLED",
		"CORTEX_HTTP_ENABLED",
		"CORTEX_HTTP_PORT",
		"CORTEX_HTTP_HOST",
		"CORTEX_HTTP_TOKEN",
		"CORTEX_LOGGING_LEVEL",
		"CORTEX_LOGGING_FORMAT",
	}

	for _, env := range envVars {
		if err := os.Unsetenv(env); err != nil {
			t.Logf("warning: failed to unset %s: %v", env, err)
		}
	}
}

// Helper function to check if string contains all substrings
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			return false
		}
	}
	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr ||
		len(s) > len(substr) && contains(s[1:], substr)
}
