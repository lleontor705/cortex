package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// validBaseline is a Config that passes validate() unchanged. Vector-specific
// tests clone it and mutate only the field under test.
func validBaseline() *Config {
	return &Config{
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
}

// TestValidate_VectorProviderEnum verifies the vector provider is restricted to
// the scoped enum set, and that an unknown provider is rejected with a clear
// error (no silent fallback to the default).
func TestValidate_VectorProviderEnum(t *testing.T) {
	validProviders := []string{"", "sqlite_blob", "qdrant", "pgvector", "none"}
	for _, p := range validProviders {
		cfg := validBaseline()
		cfg.Vector.Provider = p
		// qdrant requires full QdrantConfig to be valid; set a valid one.
		if p == "qdrant" {
			cfg.Vector.Qdrant = validQdrantConfig()
		}
		if err := validate(cfg); err != nil {
			t.Errorf("provider %q should be valid, got error: %v", p, err)
		}
	}

	unknownProviders := []string{"redis", "pinecone", "weaviate", "milvus", "QDRANT", "SQLite_Blob"}
	for _, p := range unknownProviders {
		cfg := validBaseline()
		cfg.Vector.Provider = p
		err := validate(cfg)
		if err == nil {
			t.Errorf("provider %q should be REJECTED (unknown provider, no silent fallback)", p)
			continue
		}
		if !strings.Contains(err.Error(), "provider") {
			t.Errorf("provider %q error should mention 'provider', got: %v", p, err)
		}
	}
}

// validQdrantConfig returns a QdrantConfig that passes validation on its own.
func validQdrantConfig() QdrantConfig {
	return QdrantConfig{
		Host:         "localhost",
		Port:         6334,
		Collection:   "cortex",
		Dimension:    384,
		MaxBatchSize: 256,
		MaxRetries:   3,
		Timeout:      30 * time.Second,
	}
}

// TestValidate_QdrantFields verifies all QdrantConfig fields are validated with
// clear errors when provider is "qdrant".
func TestValidate_QdrantFields(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*QdrantConfig)
		wantErr string // substring expected in error
	}{
		{
			name:    "port too low",
			modify:  func(q *QdrantConfig) { q.Port = 0 },
			wantErr: "port",
		},
		{
			name:    "port too high",
			modify:  func(q *QdrantConfig) { q.Port = 70000 },
			wantErr: "port",
		},
		{
			name:    "dimension zero",
			modify:  func(q *QdrantConfig) { q.Dimension = 0 },
			wantErr: "dimension",
		},
		{
			name:    "dimension negative",
			modify:  func(q *QdrantConfig) { q.Dimension = -8 },
			wantErr: "dimension",
		},
		{
			name:    "batch size zero",
			modify:  func(q *QdrantConfig) { q.MaxBatchSize = 0 },
			wantErr: "max_batch_size",
		},
		{
			name:    "batch size negative",
			modify:  func(q *QdrantConfig) { q.MaxBatchSize = -5 },
			wantErr: "max_batch_size",
		},
		{
			name:    "retries exceed max",
			modify:  func(q *QdrantConfig) { q.MaxRetries = 11 },
			wantErr: "max_retries",
		},
		{
			name:    "timeout zero",
			modify:  func(q *QdrantConfig) { q.Timeout = 0 },
			wantErr: "timeout",
		},
		{
			name:    "timeout negative",
			modify:  func(q *QdrantConfig) { q.Timeout = -1 * time.Second },
			wantErr: "timeout",
		},
		{
			name:    "collection empty",
			modify:  func(q *QdrantConfig) { q.Collection = "" },
			wantErr: "collection",
		},
		{
			name:    "host empty",
			modify:  func(q *QdrantConfig) { q.Host = "" },
			wantErr: "host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseline()
			cfg.Vector.Provider = "qdrant"
			q := validQdrantConfig()
			tt.modify(&q)
			cfg.Vector.Qdrant = q
			err := validate(cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error should contain %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestValidate_QdrantNotCheckedForOtherProviders verifies qdrant-specific field
// validation is NOT enforced when provider is not "qdrant" (e.g. empty default
// with dimension 0 must still pass — dimension is resolved at adapter build).
func TestValidate_QdrantNotCheckedForOtherProviders(t *testing.T) {
	cfg := validBaseline()
	cfg.Vector.Provider = "" // default local path
	cfg.Vector.Qdrant = QdrantConfig{Dimension: 0, MaxBatchSize: 0} // would fail qdrant checks
	if err := validate(cfg); err != nil {
		t.Errorf("default provider should not trigger qdrant field checks: %v", err)
	}
}

// TestConfig_String_NeverLeaksAPIKey verifies the API key NEVER appears in the
// Config.String() representation, even when a non-empty key is configured
// (REQ-CP-002 token storage / no plaintext secrets).
func TestConfig_String_NeverLeaksAPIKey(t *testing.T) {
	const secret = "sk-leak-guard-do-not-print"
	cfg := validBaseline()
	cfg.Vector.Qdrant.APIKey = secret
	s := cfg.String()
	if strings.Contains(s, secret) {
		t.Errorf("API key LEAKED into Config.String(): %s", s)
	}
	// String() must still be non-empty and useful (other sections present).
	if s == "" {
		t.Error("String() returned empty")
	}
}

// TestValidate_ErrorNeverContainsAPIKey verifies validation errors NEVER echo
// the API key, regardless of which field triggers the error (REQ-CP-002).
func TestValidate_ErrorNeverContainsAPIKey(t *testing.T) {
	const secret = "sk-validation-guard-xyz"
	tests := []func(*Config){
		func(c *Config) { c.Vector.Provider = "qdrant"; c.Vector.Qdrant = validQdrantConfig(); c.Vector.Qdrant.Port = 99999 },
		func(c *Config) { c.Vector.Provider = "bogus" },
		func(c *Config) { c.Logging.Level = "nope" },
	}
	for i, mutate := range tests {
		cfg := validBaseline()
		cfg.Vector.Qdrant.APIKey = secret
		mutate(cfg)
		err := validate(cfg)
		if err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("case %d: API key LEAKED into validation error: %s", i, err.Error())
		}
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
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
