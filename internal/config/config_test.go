package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/transportpolicy"
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
				"CORTEX_SERVER_NAME":          "env-server",
				"CORTEX_HTTP_PORT":            "3000",
				"CORTEX_HTTP_TOKEN":           "env-token",
				"CORTEX_HTTP_ALLOWED_ORIGINS": "http://localhost:5173",
				"CORTEX_LOGGING_LEVEL":        "error",
				"CORTEX_DATABASE_PATH":        "/custom/path.db",
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
				if len(cfg.HTTP.AllowedOrigins) != 1 || cfg.HTTP.AllowedOrigins[0] != "http://localhost:5173" {
					t.Errorf("expected browser origin, got %v", cfg.HTTP.AllowedOrigins)
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

func TestSavePreservesCommentsAndUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	original := "# operator notes\ndatabase:\n  # keep this path note\n  path: old.db\n  custom_driver_option: keep-me\ncustom_section:\n  enabled: true\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaults
	cfg.LoadedFrom = path
	cfg.Database.Path = "new.db"
	if err := Save(&cfg, ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"# operator notes", "# keep this path note", "path: new.db", "custom_driver_option: keep-me", "custom_section:"} {
		if !strings.Contains(text, want) {
			t.Errorf("saved config missing %q:\n%s", want, text)
		}
	}
	if err := Save(&cfg, ""); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "custom_driver_option:") != 1 {
		t.Fatalf("save duplicated unknown key:\n%s", data)
	}
}

func TestSaveRejectsInvalidExistingYAMLWithoutMutation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	original := []byte("database: [unterminated\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaults
	if err := Save(&cfg, path); err == nil {
		t.Fatal("Save accepted invalid existing YAML")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatal("Save mutated invalid config")
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary file remains: %v", err)
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

func TestMCPRemoteConfiguration(t *testing.T) {
	clearEnvVars(t)
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	yaml := `
mcp:
  enabled: true
  remote:
    enabled: true
    url: https://cortex.example/mcp
    token_env: CORTEX_TEST_REMOTE_TOKEN
    timeout: 45s
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MCP.Remote.Enabled || cfg.MCP.Remote.URL != "https://cortex.example/mcp" || cfg.MCP.Remote.TokenEnv != "CORTEX_TEST_REMOTE_TOKEN" || cfg.MCP.Remote.Timeout != 45*time.Second {
		t.Fatalf("remote MCP config = %+v", cfg.MCP.Remote)
	}
}

func TestLoadFindsGlobalConfigThroughUserProfile(t *testing.T) {
	clearEnvVars(t)
	home := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())
	configDir := filepath.Join(home, ".cortex")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "mcp:\n  remote:\n    enabled: true\n    url: https://global.example/mcp\n    token_env: GLOBAL_TOKEN\n    timeout: 30s\n"
	if err := os.WriteFile(filepath.Join(configDir, "cortex.yaml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LoadedFrom != filepath.Join(configDir, "cortex.yaml") || cfg.MCP.Remote.URL != "https://global.example/mcp" {
		t.Fatalf("global config loaded from %q with remote %+v", cfg.LoadedFrom, cfg.MCP.Remote)
	}
}

func TestMCPRemoteConfigurationValidation(t *testing.T) {
	for name, remote := range map[string]string{
		"relative URL":      "url: /mcp\n    token_env: TOKEN\n    timeout: 30s",
		"missing token env": "url: https://example.test/mcp\n    token_env: ''\n    timeout: 30s",
		"invalid timeout":   "url: https://example.test/mcp\n    token_env: TOKEN\n    timeout: 0s",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cortex.yaml")
			content := "mcp:\n  remote:\n    enabled: true\n    " + remote + "\n"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("expected remote MCP validation error")
			}
		})
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
		// pgvector requires full PGVectorConfig to be valid; set a valid one.
		if p == "pgvector" {
			cfg.Vector.Pgvector = validPgvectorConfig()
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

// validPgvectorConfig returns a PGVectorConfig that passes validation on its own.
func validPgvectorConfig() PGVectorConfig {
	return PGVectorConfig{
		DSN:                "postgres://user:pass@localhost:5432/cortex",
		Schema:             "cortex_vector",
		Table:              "embeddings",
		Dimension:          384,
		IndexType:          "hnsw",
		HNSWM:              16,
		HNSWEfConstruction: 64,
		IVFFlatLists:       100,
		MaxBatchSize:       256,
		Timeout:            30 * time.Second,
		MaxConns:           10,
		StatementTimeoutMs: 5000,
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
	cfg.Vector.Provider = ""                                        // default local path
	cfg.Vector.Qdrant = QdrantConfig{Dimension: 0, MaxBatchSize: 0} // would fail qdrant checks
	if err := validate(cfg); err != nil {
		t.Errorf("default provider should not trigger qdrant field checks: %v", err)
	}
}

// TestValidate_PgvectorFields verifies all PGVectorConfig fields are validated
// with clear errors when provider is "pgvector".
func TestValidate_PgvectorFields(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*PGVectorConfig)
		wantErr string
	}{
		{
			name:    "dsn empty",
			modify:  func(p *PGVectorConfig) { p.DSN = "" },
			wantErr: "dsn",
		},
		{
			name:    "schema empty",
			modify:  func(p *PGVectorConfig) { p.Schema = "" },
			wantErr: "schema",
		},
		{
			name:    "table empty",
			modify:  func(p *PGVectorConfig) { p.Table = "" },
			wantErr: "table",
		},
		{
			name:    "dimension zero",
			modify:  func(p *PGVectorConfig) { p.Dimension = 0 },
			wantErr: "dimension",
		},
		{
			name:    "dimension negative",
			modify:  func(p *PGVectorConfig) { p.Dimension = -8 },
			wantErr: "dimension",
		},
		{
			name:    "invalid index type",
			modify:  func(p *PGVectorConfig) { p.IndexType = "bogus" },
			wantErr: "index_type",
		},
		{
			name:    "batch size zero",
			modify:  func(p *PGVectorConfig) { p.MaxBatchSize = 0 },
			wantErr: "max_batch_size",
		},
		{
			name:    "timeout zero",
			modify:  func(p *PGVectorConfig) { p.Timeout = 0 },
			wantErr: "timeout",
		},
		{
			name:    "timeout negative",
			modify:  func(p *PGVectorConfig) { p.Timeout = -1 * time.Second },
			wantErr: "timeout",
		},
		{
			name:    "max_conns negative",
			modify:  func(p *PGVectorConfig) { p.MaxConns = -1 },
			wantErr: "max_conns",
		},
		{
			name:    "statement_timeout_ms zero",
			modify:  func(p *PGVectorConfig) { p.StatementTimeoutMs = 0 },
			wantErr: "statement_timeout_ms",
		},
		{
			name:    "hnsw_m below min",
			modify:  func(p *PGVectorConfig) { p.HNSWM = 1 },
			wantErr: "hnsw_m",
		},
		{
			name:    "hnsw_m above max",
			modify:  func(p *PGVectorConfig) { p.HNSWM = 200 },
			wantErr: "hnsw_m",
		},
		{
			name:    "hnsw_ef_construction below min",
			modify:  func(p *PGVectorConfig) { p.HNSWEfConstruction = -1 },
			wantErr: "hnsw_ef_construction",
		},
		{
			name:    "hnsw_ef_construction above max",
			modify:  func(p *PGVectorConfig) { p.HNSWEfConstruction = 5000 },
			wantErr: "hnsw_ef_construction",
		},
		{
			name:    "ivfflat_lists below min",
			modify:  func(p *PGVectorConfig) { p.IVFFlatLists = -1 },
			wantErr: "ivfflat_lists",
		},
		{
			name:    "ivfflat_lists above max",
			modify:  func(p *PGVectorConfig) { p.IVFFlatLists = 100000 },
			wantErr: "ivfflat_lists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseline()
			cfg.Vector.Provider = "pgvector"
			p := validPgvectorConfig()
			tt.modify(&p)
			cfg.Vector.Pgvector = p
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

// TestValidate_PgvectorNotCheckedForOtherProviders verifies pgvector-specific
// field validation is NOT enforced when provider is not "pgvector".
func TestValidate_PgvectorNotCheckedForOtherProviders(t *testing.T) {
	cfg := validBaseline()
	cfg.Vector.Provider = ""                                    // default local path
	cfg.Vector.Pgvector = PGVectorConfig{DSN: "", Dimension: 0} // would fail pgvector checks
	if err := validate(cfg); err != nil {
		t.Errorf("default provider should not trigger pgvector field checks: %v", err)
	}
}

// TestValidate_PgvectorValidIVFFlat verifies ivfflat is a valid index type.
func TestValidate_PgvectorValidIVFFlat(t *testing.T) {
	cfg := validBaseline()
	cfg.Vector.Provider = "pgvector"
	p := validPgvectorConfig()
	p.IndexType = "ivfflat"
	cfg.Vector.Pgvector = p
	if err := validate(cfg); err != nil {
		t.Errorf("ivfflat index type should be valid: %v", err)
	}
}

// TestValidate_PgvectorIndexTuningDefaults verifies that zero/zero/zero index
// tuning values are normalized to sensible pgvector defaults (not rejected).
func TestValidate_PgvectorIndexTuningDefaults(t *testing.T) {
	cfg := validBaseline()
	cfg.Vector.Provider = "pgvector"
	p := validPgvectorConfig()
	p.HNSWM = 0
	p.HNSWEfConstruction = 0
	p.IVFFlatLists = 0
	cfg.Vector.Pgvector = p
	if err := validate(cfg); err != nil {
		t.Fatalf("zero index tuning should default, got error: %v", err)
	}
	got := cfg.Vector.Pgvector
	if got.HNSWM != 16 {
		t.Errorf("default HNSWM = %d, want 16", got.HNSWM)
	}
	if got.HNSWEfConstruction != 64 {
		t.Errorf("default HNSWEfConstruction = %d, want 64", got.HNSWEfConstruction)
	}
	if got.IVFFlatLists != 100 {
		t.Errorf("default IVFFlatLists = %d, want 100", got.IVFFlatLists)
	}
}

// TestConfig_String_NeverLeaksPgvectorDSN verifies the DSN password NEVER
// appears in Config.String() (REQ-CP-002).
func TestConfig_String_NeverLeaksPgvectorDSN(t *testing.T) {
	const secret = "pg-super-secret-do-not-leak"
	cfg := validBaseline()
	cfg.Vector.Pgvector = PGVectorConfig{
		DSN: "postgres://user:" + secret + "@localhost:5432/cortex",
	}
	s := cfg.String()
	if strings.Contains(s, secret) {
		t.Errorf("DSN password LEAKED into Config.String(): %s", s)
	}
	if !strings.Contains(s, "***REDACTED***") {
		t.Errorf("expected redaction placeholder in String(): %s", s)
	}
}

// TestRedactDSNPassword verifies password redaction for both DSN formats.
func TestRedactDSNPassword(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "URL format",
			dsn:  "postgres://user:secretpass@localhost:5432/db",
			want: "***REDACTED***",
		},
		{
			name: "key=value format",
			dsn:  "host=localhost password=kvpsecret dbname=test",
			want: "***REDACTED***",
		},
		{
			name: "no password URL",
			dsn:  "postgres://localhost:5432/db",
			want: "",
		},
		{
			name: "empty DSN",
			dsn:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := redactDSNPassword(tt.dsn)
			if tt.want == "" {
				// No redaction expected; verify no password leaked if any was present.
				if strings.Contains(out, "secretpass") || strings.Contains(out, "kvpsecret") {
					t.Errorf("password leaked: %s", out)
				}
			} else {
				if !strings.Contains(out, tt.want) {
					t.Errorf("expected %q in redacted DSN, got: %s", tt.want, out)
				}
			}
			// Original password must never appear in output.
			if strings.Contains(out, "secretpass") || strings.Contains(out, "kvpsecret") {
				t.Errorf("password still present after redaction: %s", out)
			}
		})
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

func TestConfigStringRedactsAllCredentialFields(t *testing.T) {
	cfg := validBaseline()
	cfg.HTTP.Token = "http-secret"
	cfg.Server.Storage.DSN = "postgres://u:storage-secret@db/cortex"
	cfg.Server.Storage.MigrationDSN = "postgres://u:migration-secret@db/cortex"
	cfg.Server.Secrets.SigningKey = "signing-secret"
	cfg.Server.Secrets.OIDCClientSecret = "oidc-secret"
	cfg.Vector.Qdrant.APIKey = "qdrant-secret"
	cfg.Vector.Pgvector.DSN = "postgres://u:vector-secret@db/cortex"
	s := cfg.String()
	for _, secret := range []string{"http-secret", "storage-secret", "migration-secret", "signing-secret", "oidc-secret", "qdrant-secret", "vector-secret"} {
		if strings.Contains(s, secret) {
			t.Fatalf("secret %q leaked: %s", secret, s)
		}
	}
}

// TestConfigStringNeverRendersBootstrapBearer pins the server-composition
// contract (IDP-T03B): the configured bootstrap bearer is a secret and must
// never appear in the debug representation, because Runtime.Config retains
// the original configuration while provenance stays transient.
func TestConfigStringNeverRendersBootstrapBearer(t *testing.T) {
	const bearer = "configured-bootstrap-bearer-sentinel"
	cfg := validBaseline()
	cfg.HTTP.Token = bearer
	if s := cfg.String(); strings.Contains(s, bearer) {
		t.Fatalf("bootstrap bearer leaked into Config.String(): %s", s)
	}
}

// TestConfigStringNeverRendersNonCanonicalBearer extends the bearer redaction
// pin to non-canonical (whitespace/control padded) bearers: the loader keeps
// the configured value byte-exact for server-side canonicality rejection and
// String() never renders any form of it.
func TestConfigStringNeverRendersNonCanonicalBearer(t *testing.T) {
	for _, bearer := range []string{
		"   configured-bootstrap-bearer",
		"configured-bootstrap-bearer\t\n",
		"configured\x7f-bootstrap-bearer",
	} {
		cfg := validBaseline()
		cfg.HTTP.Token = bearer
		if s := cfg.String(); strings.Contains(s, bearer) {
			t.Fatalf("non-canonical bearer leaked into Config.String(): %s", s)
		}
	}
}

// TestValidate_ErrorNeverContainsAPIKey verifies validation errors NEVER echo
// the API key, regardless of which field triggers the error (REQ-CP-002).
func TestValidate_ErrorNeverContainsAPIKey(t *testing.T) {
	const secret = "sk-validation-guard-xyz"
	tests := []func(*Config){
		func(c *Config) {
			c.Vector.Provider = "qdrant"
			c.Vector.Qdrant = validQdrantConfig()
			c.Vector.Qdrant.Port = 99999
		},
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
		"CORTEX_MCP_REMOTE_ENABLED",
		"CORTEX_MCP_REMOTE_URL",
		"CORTEX_MCP_REMOTE_TOKEN_ENV",
		"CORTEX_MCP_REMOTE_TIMEOUT",
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

// transportPolicyTestConfig returns a minimal configuration that passes every
// other validation rule, so transport policy is the only variable under test.
func transportPolicyTestConfig() *Config {
	return &Config{
		Logging: LoggingConfig{Level: "info", Format: "json"},
		Database: DatabaseConfig{
			InMemory: true,
			Pragma: PragmaConfig{
				JournalMode: "WAL",
				Synchronous: "NORMAL",
				TempStore:   "MEMORY",
			},
		},
		Search: SearchConfig{DefaultLimit: 20, MaxLimit: 100, FusionK: 60},
		Memory: MemoryConfig{MaxObservationLength: 1, AutoArchiveDays: 1, DecayHalfLifeDays: 1, MinArchiveScore: 1},
	}
}

func TestValidateBearerTransportPolicy(t *testing.T) {
	tests := []struct {
		name     string
		mcp      bool
		sync     bool
		url      string
		wantErr  bool
		wantCode string
	}{
		{name: "mcp remote https accepted", mcp: true, url: "https://cortex.example/mcp"},
		{name: "mcp remote http loopback accepted", mcp: true, url: "http://127.0.0.1:7438/mcp"},
		{name: "mcp remote http IPv6 loopback accepted", mcp: true, url: "http://[::1]:7438/mcp"},
		{name: "mcp remote http localhost accepted", mcp: true, url: "http://localhost:7438/mcp"},
		{name: "mcp remote http remote rejected", mcp: true, url: "http://cortex.example/mcp", wantErr: true, wantCode: "insecure_scheme"},
		{name: "mcp remote http private network rejected", mcp: true, url: "http://10.0.0.5:7438/mcp", wantErr: true, wantCode: "insecure_scheme"},
		{name: "mcp remote non-HTTP scheme rejected", mcp: true, url: "ftp://cortex.example/mcp", wantErr: true, wantCode: "unsupported_scheme"},
		{name: "sync https accepted", sync: true, url: "https://cortex.example.com"},
		{name: "sync http loopback accepted", sync: true, url: "http://127.0.0.1:7438"},
		{name: "sync http remote rejected", sync: true, url: "http://cortex.example.com", wantErr: true, wantCode: "insecure_scheme"},
		{name: "sync relative URL rejected", sync: true, url: "cortex.example.com", wantErr: true, wantCode: "invalid_url"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := transportPolicyTestConfig()
			if tt.mcp {
				cfg.MCP.Remote = MCPRemoteConfig{Enabled: true, URL: tt.url, TokenEnv: "CORTEX_TEST_REMOTE_TOKEN", Timeout: 30 * time.Second}
			}
			if tt.sync {
				cfg.Sync = SyncConfig{Enabled: true, URL: tt.url, TokenEnv: "CORTEX_TEST_REMOTE_TOKEN", Interval: 30 * time.Second, Timeout: 30 * time.Second}
			}
			err := Validate(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate(url=%q) = nil, want rejection %q", tt.url, tt.wantCode)
				}
				var policyErr *transportpolicy.Error
				if !errors.As(err, &policyErr) {
					t.Fatalf("error %v does not wrap *transportpolicy.Error", err)
				}
				if policyErr.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", policyErr.Code, tt.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate(url=%q) = %v, want accepted", tt.url, err)
			}
		})
	}
}

func TestLoadMultiFormat(t *testing.T) {
	t.Run("load from JSON", func(t *testing.T) {
		tmpDir := t.TempDir()
		jsonPath := filepath.Join(tmpDir, "cortex.json")
		jsonContent := `{
			"database": { "path": "/custom/from-json.db" },
			"http": { "port": 8888, "token": "json-secret" },
			"logging": { "level": "warn" }
		}`
		if err := os.WriteFile(jsonPath, []byte(jsonContent), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(jsonPath)
		if err != nil {
			t.Fatalf("Load(json) failed: %v", err)
		}
		if cfg.Database.Path != "/custom/from-json.db" {
			t.Errorf("expected db path '/custom/from-json.db', got %q", cfg.Database.Path)
		}
		if cfg.HTTP.Port != 8888 {
			t.Errorf("expected port 8888, got %d", cfg.HTTP.Port)
		}
		if cfg.HTTP.Token != "json-secret" {
			t.Errorf("expected token 'json-secret', got %q", cfg.HTTP.Token)
		}
		if cfg.Logging.Level != "warn" {
			t.Errorf("expected level 'warn', got %q", cfg.Logging.Level)
		}
	})

	t.Run("load from TOML", func(t *testing.T) {
		tmpDir := t.TempDir()
		tomlPath := filepath.Join(tmpDir, "cortex.toml")
		tomlContent := `
[database]
path = "/custom/from-toml.db"

[http]
port = 7777
token = "toml-token"

[logging]
level = "debug"
`
		if err := os.WriteFile(tomlPath, []byte(tomlContent), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(tomlPath)
		if err != nil {
			t.Fatalf("Load(toml) failed: %v", err)
		}
		if cfg.Database.Path != "/custom/from-toml.db" {
			t.Errorf("expected db path '/custom/from-toml.db', got %q", cfg.Database.Path)
		}
		if cfg.HTTP.Port != 7777 {
			t.Errorf("expected port 7777, got %d", cfg.HTTP.Port)
		}
		if cfg.HTTP.Token != "toml-token" {
			t.Errorf("expected token 'toml-token', got %q", cfg.HTTP.Token)
		}
	})
}

func TestInitConfigAndSaveMultiFormat(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("init yaml", func(t *testing.T) {
		p := filepath.Join(tmpDir, "cortex.yaml")
		out, err := InitConfig(p, "yaml", false)
		if err != nil {
			t.Fatalf("InitConfig(yaml) failed: %v", err)
		}
		if out != p {
			t.Errorf("expected %s, got %s", p, out)
		}
		// Second init without force should fail
		if _, err := InitConfig(p, "yaml", false); err == nil {
			t.Error("expected error when file already exists")
		}
		// Second init with force should succeed
		if _, err := InitConfig(p, "yaml", true); err != nil {
			t.Errorf("expected success with force: %v", err)
		}
	})

	t.Run("init json", func(t *testing.T) {
		p := filepath.Join(tmpDir, "cortex.json")
		if _, err := InitConfig(p, "json", false); err != nil {
			t.Fatalf("InitConfig(json) failed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load after InitConfig(json) failed: %v", err)
		}
		if cfg.HTTP.Port != 7438 {
			t.Errorf("expected default port 7438, got %d", cfg.HTTP.Port)
		}
	})

	t.Run("init toml", func(t *testing.T) {
		p := filepath.Join(tmpDir, "cortex.toml")
		if _, err := InitConfig(p, "toml", false); err != nil {
			t.Fatalf("InitConfig(toml) failed: %v", err)
		}
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("Load after InitConfig(toml) failed: %v", err)
		}
		if cfg.HTTP.Port != 7438 {
			t.Errorf("expected default port 7438, got %d", cfg.HTTP.Port)
		}
	})

	t.Run("save json and toml", func(t *testing.T) {
		cfg := defaults
		cfg.Database.Path = "/saved/test.db"

		jsonTarget := filepath.Join(tmpDir, "saved.json")
		if err := Save(&cfg, jsonTarget); err != nil {
			t.Fatalf("Save(json) failed: %v", err)
		}
		loadedJSON, err := Load(jsonTarget)
		if err != nil {
			t.Fatalf("Load(saved.json) failed: %v", err)
		}
		if loadedJSON.Database.Path != "/saved/test.db" {
			t.Errorf("expected db path '/saved/test.db', got %s", loadedJSON.Database.Path)
		}

		tomlTarget := filepath.Join(tmpDir, "saved.toml")
		if err := Save(&cfg, tomlTarget); err != nil {
			t.Fatalf("Save(toml) failed: %v", err)
		}
		loadedTOML, err := Load(tomlTarget)
		if err != nil {
			t.Fatalf("Load(saved.toml) failed: %v", err)
		}
		if loadedTOML.Database.Path != "/saved/test.db" {
			t.Errorf("expected db path '/saved/test.db', got %s", loadedTOML.Database.Path)
		}
	})
}

