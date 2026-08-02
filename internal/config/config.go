package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// CortexDir returns the centralized data directory (~/.cortex/).
// It uses $HOME, $USERPROFILE, or os.UserHomeDir() to resolve the home directory.
func CortexDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ".cortex"
	}
	return filepath.Join(home, ".cortex")
}

// DefaultDBPath returns the default database path (~/.cortex/cortex.db).
func DefaultDBPath() string {
	return filepath.Join(CortexDir(), "cortex.db")
}

// Config represents the main configuration structure
type Config struct {
	Server    ServerConfig    `yaml:"server" mapstructure:"server"`
	Database  DatabaseConfig  `yaml:"database" mapstructure:"database"`
	MCP       MCPConfig       `yaml:"mcp" mapstructure:"mcp"`
	HTTP      HTTPConfig      `yaml:"http" mapstructure:"http"`
	Logging   LoggingConfig   `yaml:"logging" mapstructure:"logging"`
	Search    SearchConfig    `yaml:"search" mapstructure:"search"`
	Memory    MemoryConfig    `yaml:"memory" mapstructure:"memory"`
	Lifecycle LifecycleConfig `yaml:"lifecycle" mapstructure:"lifecycle"`
	Vector    VectorConfig    `yaml:"vector" mapstructure:"vector"`

	// LoadedFrom is the path of the config file that was loaded.
	// Used by Save() and ReloadConfig() to always use the same file.
	// Not serialized to YAML.
	LoadedFrom string `yaml:"-" mapstructure:"-"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Name                    string               `yaml:"name" mapstructure:"name"`
	Version                 string               `yaml:"version" mapstructure:"version"`
	Storage                 ServerStorageConfig  `yaml:"storage" mapstructure:"storage"`
	Provider                ServerProviderConfig `yaml:"provider" mapstructure:"provider"`
	Secrets                 ServerSecretsConfig  `yaml:"secrets" mapstructure:"secrets"`
	TenantID                string               `yaml:"tenant_id" mapstructure:"tenant_id"`
	WorkspaceID             string               `yaml:"workspace_id" mapstructure:"workspace_id"`
	PrincipalSubject        string               `yaml:"principal_subject" mapstructure:"principal_subject"`
	GrantDigest             string               `yaml:"grant_digest" mapstructure:"grant_digest"`
	GrantVersion            int64                `yaml:"grant_version" mapstructure:"grant_version"`
	Roles                   []string             `yaml:"roles" mapstructure:"roles"`
	Scopes                  []string             `yaml:"scopes" mapstructure:"scopes"`
	ProjectIDs              []string             `yaml:"project_ids" mapstructure:"project_ids"`
	ClassificationClearance []string             `yaml:"classification_clearance" mapstructure:"classification_clearance"`
	BootstrapDevelopment    bool                 `yaml:"bootstrap_development" mapstructure:"bootstrap_development"`
}

// ServerStorageConfig contains server-only PostgreSQL connection settings.
// It is intentionally separate from the local SQLite database config.
type ServerStorageConfig struct {
	Driver       string `yaml:"driver" mapstructure:"driver"`
	DSN          string `yaml:"dsn" mapstructure:"dsn"`
	MigrationDSN string `yaml:"migration_dsn" mapstructure:"migration_dsn"`
	MaxConns     int32  `yaml:"max_conns" mapstructure:"max_conns"`
}

// ServerProviderConfig selects server-side providers without constructing them.
type ServerProviderConfig struct {
	Embedding string `yaml:"embedding" mapstructure:"embedding"`
	Vector    string `yaml:"vector" mapstructure:"vector"`
}

// ServerSecretsConfig contains credentials consumed by later identity waves.
// Secrets are never rendered by Config.String.
type ServerSecretsConfig struct {
	SigningKey       string `yaml:"signing_key" mapstructure:"signing_key"`
	OIDCClientSecret string `yaml:"oidc_client_secret" mapstructure:"oidc_client_secret"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Path     string       `yaml:"path" mapstructure:"path"`
	InMemory bool         `yaml:"in_memory" mapstructure:"in_memory"`
	Pragma   PragmaConfig `yaml:"pragma" mapstructure:"pragma"`
}

// PragmaConfig holds SQLite pragma settings
type PragmaConfig struct {
	JournalMode string `yaml:"journal_mode" mapstructure:"journal_mode"`
	Synchronous string `yaml:"synchronous" mapstructure:"synchronous"`
	CacheSize   int    `yaml:"cache_size" mapstructure:"cache_size"`
	ForeignKeys bool   `yaml:"foreign_keys" mapstructure:"foreign_keys"`
	TempStore   string `yaml:"temp_store" mapstructure:"temp_store"`
	MmapSize    int    `yaml:"mmap_size" mapstructure:"mmap_size"`
}

// MCPConfig holds MCP (Model Context Protocol) configuration
type MCPConfig struct {
	Enabled bool `yaml:"enabled" mapstructure:"enabled"`
}

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Enabled        bool     `yaml:"enabled" mapstructure:"enabled"`
	Port           int      `yaml:"port" mapstructure:"port"`
	Host           string   `yaml:"host" mapstructure:"host"`
	Token          string   `yaml:"token" mapstructure:"token"`
	AllowedOrigins []string `yaml:"allowed_origins" mapstructure:"allowed_origins"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level" mapstructure:"level"`
	Format string `yaml:"format" mapstructure:"format"`
}

// SearchConfig holds search-related configuration
type SearchConfig struct {
	DefaultLimit      int     `yaml:"default_limit" mapstructure:"default_limit"`
	MaxLimit          int     `yaml:"max_limit" mapstructure:"max_limit"`
	FTS5              bool    `yaml:"fts5" mapstructure:"fts5"`
	Vector            bool    `yaml:"vector" mapstructure:"vector"`
	FusionK           float64 `yaml:"fusion_k" mapstructure:"fusion_k"`
	EmbeddingProvider string  `yaml:"embedding_provider" mapstructure:"embedding_provider"` // "ollama", "openai", "none" (default)
	EmbeddingModel    string  `yaml:"embedding_model" mapstructure:"embedding_model"`       // Model name override (e.g. "qwen3-embedding:8b")
	EmbeddingBaseURL  string  `yaml:"embedding_base_url" mapstructure:"embedding_base_url"` // Ollama base URL override (default: http://localhost:11434)
	OllamaAutoStart   bool    `yaml:"ollama_auto_start" mapstructure:"ollama_auto_start"`   // Auto-start Ollama when configured as provider
}

// MemoryConfig holds memory management configuration
type MemoryConfig struct {
	MaxObservationLength int     `yaml:"max_observation_length" mapstructure:"max_observation_length"`
	DedupeWindow         string  `yaml:"dedupe_window" mapstructure:"dedupe_window"`
	AutoArchiveDays      int     `yaml:"auto_archive_days" mapstructure:"auto_archive_days"`
	DecayHalfLifeDays    float64 `yaml:"importance_decay_half_life" mapstructure:"importance_decay_half_life"`
	MinArchiveScore      float64 `yaml:"min_archive_score" mapstructure:"min_archive_score"`
}

// LifecycleConfig holds lifecycle management configuration
type LifecycleConfig struct {
	EnableAutoArchive    bool   `yaml:"enable_auto_archive" mapstructure:"enable_auto_archive"`
	ArchiveCheckInterval string `yaml:"archive_check_interval" mapstructure:"archive_check_interval"`
}

// VectorConfig holds external vector index adapter configuration.
//
// This is DATA-ONLY: no adapter client is imported here (config.go stays
// local-track, zero-CGO, no external vector dependency). The local
// composition path (internal/app) wires the sqlite_blob adapter (the
// zero-CGO default) regardless of Provider. Provider selection happens
// ONLY in the server/external composition path, which is permitted to
// import the external adapter packages (ADR-05, REQ-VEC-001/002). When
// Provider is empty or "sqlite_blob", no external adapter is constructed
// and the local zero-CGO default is preserved.
//
// Provider is an enum: "" | "sqlite_blob" (default) | "qdrant" | "pgvector"
// | "none". "pgvector" is a SCOPED provider (recognized by validation) but
// is NOT yet implemented — selecting it does not silently fall back to
// another provider; the server composition path is responsible for rejecting
// an unimplemented-but-scoped provider at wiring time.
type VectorConfig struct {
	Provider string         `yaml:"provider" mapstructure:"provider"` // "" | "sqlite_blob" (default) | "qdrant" | "pgvector" | "none"
	Qdrant   QdrantConfig   `yaml:"qdrant" mapstructure:"qdrant"`
	Pgvector PGVectorConfig `yaml:"pgvector" mapstructure:"pgvector"`
}

// QdrantConfig holds connection parameters for the Qdrant external vector
// adapter (internal/vector/qdrant). It is consumed ONLY by the server/
// external composition path. All fields are plain data types so config.go
// never imports the qdrant client (ADR-01 dependency direction, REQ-FOUND-001).
// The APIKey is passed to the gRPC client and MUST NEVER be logged or
// surfaced in error messages (REQ-CP-002 token storage / no plaintext).
type QdrantConfig struct {
	Host         string        `yaml:"host" mapstructure:"host"`                     // gRPC host (default localhost)
	Port         int           `yaml:"port" mapstructure:"port"`                     // gRPC port (default 6334)
	Collection   string        `yaml:"collection" mapstructure:"collection"`         // collection name (default cortex)
	Dimension    int           `yaml:"dimension" mapstructure:"dimension"`           // expected vector dimension (collection vector size)
	APIKey       string        `yaml:"api_key" mapstructure:"api_key"`               // optional API key (never logged)
	UseTLS       bool          `yaml:"use_tls" mapstructure:"use_tls"`               // TLS for gRPC (default false)
	MaxBatchSize int           `yaml:"max_batch_size" mapstructure:"max_batch_size"` // upsert batch ceiling (default 256)
	MaxRetries   uint          `yaml:"max_retries" mapstructure:"max_retries"`       // transient gRPC retries (default 3)
	Timeout      time.Duration `yaml:"timeout" mapstructure:"timeout"`               // per-operation gRPC timeout (default 30s)
}

// PGVectorConfig holds connection parameters for the pgvector external vector
// adapter (internal/vector/pgvector). It is consumed ONLY by the server/
// external composition path. All fields are plain data types so config.go
// never imports the pgx driver (ADR-01 dependency direction, REQ-FOUND-001).
// The DSN may contain a password; it MUST NEVER be logged or surfaced in
// error messages (REQ-CP-002 token storage / no plaintext).
type PGVectorConfig struct {
	DSN                string        `yaml:"dsn" mapstructure:"dsn"`                                   // PostgreSQL connection string
	Schema             string        `yaml:"schema" mapstructure:"schema"`                             // schema name (default cortex_vector)
	Table              string        `yaml:"table" mapstructure:"table"`                               // table name (default embeddings)
	Dimension          int           `yaml:"dimension" mapstructure:"dimension"`                       // expected vector dimension
	IndexType          string        `yaml:"index_type" mapstructure:"index_type"`                     // hnsw or ivfflat (default hnsw)
	HNSWM              int           `yaml:"hnsw_m" mapstructure:"hnsw_m"`                             // HNSW max connections per node (default 16, range 2-100)
	HNSWEfConstruction int           `yaml:"hnsw_ef_construction" mapstructure:"hnsw_ef_construction"` // HNSW dynamic candidate list size for build (default 64, range 1-1000)
	IVFFlatLists       int           `yaml:"ivfflat_lists" mapstructure:"ivfflat_lists"`               // IVFFlat number of inverted lists (default 100, range 1-50000)
	MaxBatchSize       int           `yaml:"max_batch_size" mapstructure:"max_batch_size"`             // upsert batch ceiling (default 256)
	Timeout            time.Duration `yaml:"timeout" mapstructure:"timeout"`                           // per-operation timeout (default 30s)
	MaxConns           int32         `yaml:"max_conns" mapstructure:"max_conns"`                       // pool max connections (default 10)
	StatementTimeoutMs int           `yaml:"statement_timeout_ms" mapstructure:"statement_timeout_ms"` // PostgreSQL statement_timeout in ms (default 5000)
}

// Default configuration values
var defaults = Config{
	Server: ServerConfig{
		Name:     "cortex",
		Version:  "2.0.0",
		Storage:  ServerStorageConfig{Driver: "postgres", MaxConns: 10},
		Provider: ServerProviderConfig{Embedding: "none", Vector: "none"},
	},
	Database: DatabaseConfig{
		Path:     "", // resolved dynamically by DefaultDBPath()
		InMemory: false,
		Pragma: PragmaConfig{
			JournalMode: "WAL",
			Synchronous: "NORMAL",
			CacheSize:   -64000, // 64MB
			ForeignKeys: true,
			TempStore:   "MEMORY",
			MmapSize:    268435456, // 256MB
		},
	},
	MCP: MCPConfig{
		Enabled: true,
	},
	HTTP: HTTPConfig{
		Enabled: true,
		Port:    7438,
		Host:    "localhost",
		Token:   "",
	},
	Logging: LoggingConfig{
		Level:  "info",
		Format: "json",
	},
	Search: SearchConfig{
		DefaultLimit: 20,
		MaxLimit:     100,
		FTS5:         true,
		Vector:       false,
		FusionK:      60,
	},
	Memory: MemoryConfig{
		MaxObservationLength: 50000,
		DedupeWindow:         "15m",
		AutoArchiveDays:      90,
		DecayHalfLifeDays:    30,
		MinArchiveScore:      0.1,
	},
	Lifecycle: LifecycleConfig{
		EnableAutoArchive:    true,
		ArchiveCheckInterval: "1h",
	},
	Vector: VectorConfig{
		Provider: "", // unset => sqlite_blob zero-CGO default (local path)
		Qdrant: QdrantConfig{
			Host:         "localhost",
			Port:         6334,
			Collection:   "cortex",
			Dimension:    0, // resolved from embedding model at adapter construction
			MaxBatchSize: 256,
			MaxRetries:   3,
			Timeout:      30 * time.Second,
		},
		Pgvector: PGVectorConfig{
			Schema:             "cortex_vector",
			Table:              "embeddings",
			Dimension:          0, // resolved from embedding model at adapter construction
			IndexType:          "hnsw",
			HNSWM:              16,
			HNSWEfConstruction: 64,
			IVFFlatLists:       100,
			MaxBatchSize:       256,
			Timeout:            30 * time.Second,
			MaxConns:           10,
			StatementTimeoutMs: 5000,
		},
	},
}

// Load reads configuration from file, environment variables, and applies defaults
// The configPath parameter is optional - if empty, it searches for cortex.yaml
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set default values
	setDefaults(v)

	// Configure viper
	v.SetConfigName("cortex")
	v.SetConfigType("yaml")

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		// Add search paths
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath("$HOME/.cortex")
		v.AddConfigPath("/etc/cortex")
	}

	// Enable environment variable override
	v.SetEnvPrefix("CORTEX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Read config file (optional - may not exist)
	configNotFound := false
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// Config file was found but another error was produced
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		configNotFound = true
	}

	// Unmarshal into config struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate configuration
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	// Track which file was loaded for Save() and ReloadConfig()
	if !configNotFound {
		cfg.LoadedFrom = v.ConfigFileUsed()
	} else if configPath != "" {
		cfg.LoadedFrom = configPath
	}

	// Create default config file if none was found and no explicit path was given
	if configNotFound && configPath == "" {
		defaultPath := filepath.Join(CortexDir(), "cortex.yaml")
		_ = ensureDefaultConfig(&cfg)
		cfg.LoadedFrom = defaultPath
	}

	return &cfg, nil
}

// setDefaults configures all default values in viper
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.name", defaults.Server.Name)
	v.SetDefault("server.version", defaults.Server.Version)
	v.SetDefault("server.storage.driver", defaults.Server.Storage.Driver)
	v.SetDefault("server.storage.dsn", defaults.Server.Storage.DSN)
	v.SetDefault("server.storage.migration_dsn", defaults.Server.Storage.MigrationDSN)
	v.SetDefault("server.storage.max_conns", defaults.Server.Storage.MaxConns)
	v.SetDefault("server.provider.embedding", defaults.Server.Provider.Embedding)
	v.SetDefault("server.provider.vector", defaults.Server.Provider.Vector)
	v.SetDefault("server.tenant_id", "")
	v.SetDefault("server.grant_digest", "")
	v.SetDefault("server.grant_version", int64(0))
	v.SetDefault("server.roles", []string{})
	v.SetDefault("server.scopes", []string{})
	v.SetDefault("server.project_ids", []string{})
	v.SetDefault("server.classification_clearance", []string{})
	v.SetDefault("server.bootstrap_development", false)
	v.SetDefault("server.workspace_id", "")
	v.SetDefault("server.principal_subject", "")
	v.SetDefault("server.secrets.signing_key", "")
	v.SetDefault("server.secrets.oidc_client_secret", "")

	v.SetDefault("database.path", DefaultDBPath())
	v.SetDefault("database.in_memory", defaults.Database.InMemory)
	v.SetDefault("database.pragma.journal_mode", defaults.Database.Pragma.JournalMode)
	v.SetDefault("database.pragma.synchronous", defaults.Database.Pragma.Synchronous)
	v.SetDefault("database.pragma.cache_size", defaults.Database.Pragma.CacheSize)
	v.SetDefault("database.pragma.foreign_keys", defaults.Database.Pragma.ForeignKeys)
	v.SetDefault("database.pragma.temp_store", defaults.Database.Pragma.TempStore)
	v.SetDefault("database.pragma.mmap_size", defaults.Database.Pragma.MmapSize)

	v.SetDefault("mcp.enabled", defaults.MCP.Enabled)

	v.SetDefault("http.enabled", defaults.HTTP.Enabled)
	v.SetDefault("http.port", defaults.HTTP.Port)
	v.SetDefault("http.host", defaults.HTTP.Host)
	v.SetDefault("http.token", defaults.HTTP.Token)
	v.SetDefault("http.allowed_origins", []string{})

	v.SetDefault("logging.level", defaults.Logging.Level)
	v.SetDefault("logging.format", defaults.Logging.Format)

	v.SetDefault("search.default_limit", defaults.Search.DefaultLimit)
	v.SetDefault("search.max_limit", defaults.Search.MaxLimit)
	v.SetDefault("search.fts5", defaults.Search.FTS5)
	v.SetDefault("search.vector", defaults.Search.Vector)
	v.SetDefault("search.fusion_k", defaults.Search.FusionK)

	v.SetDefault("memory.max_observation_length", defaults.Memory.MaxObservationLength)
	v.SetDefault("memory.dedupe_window", defaults.Memory.DedupeWindow)
	v.SetDefault("memory.auto_archive_days", defaults.Memory.AutoArchiveDays)
	v.SetDefault("memory.importance_decay_half_life", defaults.Memory.DecayHalfLifeDays)
	v.SetDefault("memory.min_archive_score", defaults.Memory.MinArchiveScore)

	v.SetDefault("lifecycle.enable_auto_archive", defaults.Lifecycle.EnableAutoArchive)
	v.SetDefault("lifecycle.archive_check_interval", defaults.Lifecycle.ArchiveCheckInterval)

	v.SetDefault("vector.provider", defaults.Vector.Provider)
	v.SetDefault("vector.qdrant.host", defaults.Vector.Qdrant.Host)
	v.SetDefault("vector.qdrant.port", defaults.Vector.Qdrant.Port)
	v.SetDefault("vector.qdrant.collection", defaults.Vector.Qdrant.Collection)
	v.SetDefault("vector.qdrant.dimension", defaults.Vector.Qdrant.Dimension)
	v.SetDefault("vector.qdrant.api_key", defaults.Vector.Qdrant.APIKey)
	v.SetDefault("vector.qdrant.use_tls", defaults.Vector.Qdrant.UseTLS)
	v.SetDefault("vector.qdrant.max_batch_size", defaults.Vector.Qdrant.MaxBatchSize)
	v.SetDefault("vector.qdrant.max_retries", defaults.Vector.Qdrant.MaxRetries)
	v.SetDefault("vector.qdrant.timeout", defaults.Vector.Qdrant.Timeout)

	v.SetDefault("vector.pgvector.dsn", defaults.Vector.Pgvector.DSN)
	v.SetDefault("vector.pgvector.schema", defaults.Vector.Pgvector.Schema)
	v.SetDefault("vector.pgvector.table", defaults.Vector.Pgvector.Table)
	v.SetDefault("vector.pgvector.dimension", defaults.Vector.Pgvector.Dimension)
	v.SetDefault("vector.pgvector.index_type", defaults.Vector.Pgvector.IndexType)
	v.SetDefault("vector.pgvector.hnsw_m", defaults.Vector.Pgvector.HNSWM)
	v.SetDefault("vector.pgvector.hnsw_ef_construction", defaults.Vector.Pgvector.HNSWEfConstruction)
	v.SetDefault("vector.pgvector.ivfflat_lists", defaults.Vector.Pgvector.IVFFlatLists)
	v.SetDefault("vector.pgvector.max_batch_size", defaults.Vector.Pgvector.MaxBatchSize)
	v.SetDefault("vector.pgvector.timeout", defaults.Vector.Pgvector.Timeout)
	v.SetDefault("vector.pgvector.max_conns", defaults.Vector.Pgvector.MaxConns)
	v.SetDefault("vector.pgvector.statement_timeout_ms", defaults.Vector.Pgvector.StatementTimeoutMs)
}

// validate checks if the configuration values are valid
func validate(cfg *Config) error {
	// Validate logging level
	validLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLevels[strings.ToLower(cfg.Logging.Level)] {
		return fmt.Errorf("invalid logging level: %s (valid: debug, info, warn, error)", cfg.Logging.Level)
	}

	// Validate logging format
	validFormats := map[string]bool{
		"json":  true,
		"text":  true,
		"plain": true,
	}
	if !validFormats[strings.ToLower(cfg.Logging.Format)] {
		return fmt.Errorf("invalid logging format: %s (valid: json, text, plain)", cfg.Logging.Format)
	}

	// Validate HTTP port
	if cfg.HTTP.Enabled && (cfg.HTTP.Port < 1 || cfg.HTTP.Port > 65535) {
		return fmt.Errorf("invalid HTTP port: %d (must be 1-65535)", cfg.HTTP.Port)
	}

	// Validate database path when not in memory
	if !cfg.Database.InMemory && cfg.Database.Path == "" {
		return fmt.Errorf("database path is required when not using in-memory mode")
	}

	// Validate pragma journal mode
	validJournalModes := map[string]bool{
		"DELETE":   true,
		"TRUNCATE": true,
		"PERSIST":  true,
		"MEMORY":   true,
		"WAL":      true,
		"OFF":      true,
	}
	if !validJournalModes[strings.ToUpper(cfg.Database.Pragma.JournalMode)] {
		return fmt.Errorf("invalid journal_mode: %s (valid: DELETE, TRUNCATE, PERSIST, MEMORY, WAL, OFF)",
			cfg.Database.Pragma.JournalMode)
	}

	// Validate pragma synchronous
	validSynchronous := map[string]bool{
		"OFF":    true,
		"NORMAL": true,
		"FULL":   true,
		"EXTRA":  true,
	}
	if !validSynchronous[strings.ToUpper(cfg.Database.Pragma.Synchronous)] {
		return fmt.Errorf("invalid synchronous: %s (valid: OFF, NORMAL, FULL, EXTRA)",
			cfg.Database.Pragma.Synchronous)
	}

	// Validate pragma temp_store
	validTempStore := map[string]bool{
		"FILE":   true,
		"MEMORY": true,
	}
	if !validTempStore[strings.ToUpper(cfg.Database.Pragma.TempStore)] {
		return fmt.Errorf("invalid temp_store: %s (valid: FILE, MEMORY)",
			cfg.Database.Pragma.TempStore)
	}

	// Validate search config
	if cfg.Search.DefaultLimit < 1 {
		return fmt.Errorf("invalid search.default_limit: %d (must be >= 1)", cfg.Search.DefaultLimit)
	}
	if cfg.Search.MaxLimit < 1 {
		return fmt.Errorf("invalid search.max_limit: %d (must be >= 1)", cfg.Search.MaxLimit)
	}
	if cfg.Search.FusionK < 1 {
		return fmt.Errorf("invalid search.fusion_k: %.1f (must be >= 1)", cfg.Search.FusionK)
	}
	if cfg.Search.MaxLimit < cfg.Search.DefaultLimit {
		return fmt.Errorf("invalid search.max_limit: %d (must be >= default_limit %d)", cfg.Search.MaxLimit, cfg.Search.DefaultLimit)
	}

	// Validate memory config
	if cfg.Memory.MaxObservationLength < 1 {
		return fmt.Errorf("invalid memory.max_observation_length: %d (must be >= 1)", cfg.Memory.MaxObservationLength)
	}
	if cfg.Memory.AutoArchiveDays < 1 {
		return fmt.Errorf("invalid memory.auto_archive_days: %d (must be >= 1)", cfg.Memory.AutoArchiveDays)
	}
	if cfg.Memory.DecayHalfLifeDays <= 0 {
		return fmt.Errorf("invalid memory.importance_decay_half_life: %.1f (must be > 0)", cfg.Memory.DecayHalfLifeDays)
	}
	if cfg.Memory.MinArchiveScore < 0 || cfg.Memory.MinArchiveScore > 5 {
		return fmt.Errorf("invalid memory.min_archive_score: %.2f (must be between 0.0 and 5.0)", cfg.Memory.MinArchiveScore)
	}

	// Validate vector provider + adapter config.
	if err := validateVector(&cfg.Vector); err != nil {
		return err
	}

	return nil
}

// validVectorProviders is the scoped enum of recognized vector providers. An
// empty string means "use the local sqlite_blob zero-CGO default" (no external
// adapter). "pgvector" is scoped (recognized) but NOT yet implemented — it
// passes config validation; the server composition path rejects wiring it.
var validVectorProviders = map[string]bool{
	"":            true,
	"sqlite_blob": true,
	"qdrant":      true,
	"pgvector":    true,
	"none":        true,
}

// validateVector validates the vector provider enum and, when qdrant is the
// selected provider, the full QdrantConfig. Validation errors NEVER echo the
// API key (REQ-CP-002). An unknown provider is rejected — there is NO silent
// fallback to the default.
func validateVector(v *VectorConfig) error {
	if !validVectorProviders[v.Provider] {
		return fmt.Errorf(
			"invalid vector.provider: %q (valid: \"\", sqlite_blob, qdrant, pgvector, none)",
			v.Provider,
		)
	}
	if v.Provider == "qdrant" {
		return validateQdrant(&v.Qdrant)
	}
	if v.Provider == "pgvector" {
		return validatePgvector(&v.Pgvector)
	}
	return nil
}

// validateQdrant validates the QdrantConfig connection parameters. All ranges
// are checked with clear errors. The API key is intentionally never referenced
// in any error string.
func validateQdrant(q *QdrantConfig) error {
	if q.Host == "" {
		return fmt.Errorf("invalid vector.qdrant.host: must not be empty")
	}
	if q.Port < 1 || q.Port > 65535 {
		return fmt.Errorf("invalid vector.qdrant.port: %d (must be 1-65535)", q.Port)
	}
	if q.Collection == "" {
		return fmt.Errorf("invalid vector.qdrant.collection: must not be empty")
	}
	if q.Dimension <= 0 {
		return fmt.Errorf("invalid vector.qdrant.dimension: %d (must be > 0)", q.Dimension)
	}
	if q.MaxBatchSize <= 0 {
		return fmt.Errorf("invalid vector.qdrant.max_batch_size: %d (must be >= 1)", q.MaxBatchSize)
	}
	if q.MaxRetries > 10 {
		return fmt.Errorf("invalid vector.qdrant.max_retries: %d (must be 0-10)", q.MaxRetries)
	}
	if q.Timeout <= 0 {
		return fmt.Errorf("invalid vector.qdrant.timeout: %s (must be > 0)", q.Timeout)
	}
	return nil
}

// validatePgvector validates the PGVectorConfig connection parameters. All
// ranges are checked with clear errors. The DSN password is intentionally never
// referenced in any error string (REQ-CP-002). Index tuning fields are typed
// integers (never raw SQL); zero values normalize to pgvector-recommended
// defaults and out-of-range explicit values are rejected.
func validatePgvector(p *PGVectorConfig) error {
	if p.DSN == "" {
		return fmt.Errorf("invalid vector.pgvector.dsn: must not be empty")
	}
	if p.Schema == "" {
		return fmt.Errorf("invalid vector.pgvector.schema: must not be empty")
	}
	if p.Table == "" {
		return fmt.Errorf("invalid vector.pgvector.table: must not be empty")
	}
	if p.Dimension <= 0 {
		return fmt.Errorf("invalid vector.pgvector.dimension: %d (must be > 0)", p.Dimension)
	}
	if p.IndexType != "hnsw" && p.IndexType != "ivfflat" {
		return fmt.Errorf("invalid vector.pgvector.index_type: %q (valid: hnsw, ivfflat)", p.IndexType)
	}
	// Index tuning: zero → default; explicit out-of-range rejected.
	if p.HNSWM == 0 {
		p.HNSWM = 16
	}
	if p.HNSWM < 2 || p.HNSWM > 100 {
		return fmt.Errorf("invalid vector.pgvector.hnsw_m: %d (must be 2-100)", p.HNSWM)
	}
	if p.HNSWEfConstruction == 0 {
		p.HNSWEfConstruction = 64
	}
	if p.HNSWEfConstruction < 1 || p.HNSWEfConstruction > 1000 {
		return fmt.Errorf("invalid vector.pgvector.hnsw_ef_construction: %d (must be 1-1000)", p.HNSWEfConstruction)
	}
	if p.IVFFlatLists == 0 {
		p.IVFFlatLists = 100
	}
	if p.IVFFlatLists < 1 || p.IVFFlatLists > 50000 {
		return fmt.Errorf("invalid vector.pgvector.ivfflat_lists: %d (must be 1-50000)", p.IVFFlatLists)
	}
	if p.MaxBatchSize <= 0 {
		return fmt.Errorf("invalid vector.pgvector.max_batch_size: %d (must be >= 1)", p.MaxBatchSize)
	}
	if p.Timeout <= 0 {
		return fmt.Errorf("invalid vector.pgvector.timeout: %s (must be > 0)", p.Timeout)
	}
	if p.MaxConns < 0 {
		return fmt.Errorf("invalid vector.pgvector.max_conns: %d (must be >= 0)", p.MaxConns)
	}
	if p.StatementTimeoutMs <= 0 {
		return fmt.Errorf("invalid vector.pgvector.statement_timeout_ms: %d (must be > 0)", p.StatementTimeoutMs)
	}
	return nil
}

// LoadFromEnv loads configuration exclusively from environment variables and defaults
// Useful for containerized deployments where config files are not used
func LoadFromEnv() (*Config, error) {
	v := viper.New()
	setDefaults(v)

	// Enable environment variable override
	v.SetEnvPrefix("CORTEX")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Unmarshal into config struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate configuration
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// GetEnvVarName returns the environment variable name for a given config key
// e.g., "database.path" -> "CORTEX_DATABASE_PATH"
func GetEnvVarName(key string) string {
	return "CORTEX_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// SetEnvVar sets an environment variable for configuration override
// Useful for testing
func SetEnvVar(key, value string) error {
	return os.Setenv(GetEnvVarName(key), value)
}

// ClearEnvVar clears an environment variable
// Useful for testing
func ClearEnvVar(key string) error {
	return os.Unsetenv(GetEnvVarName(key))
}

// String returns a string representation of the configuration (for debugging).
// The Vector.Qdrant.APIKey is NEVER included: secrets must not surface in logs
// or debug output (REQ-CP-002). Vector is rendered with the key blanked so the
// representation stays useful without leaking credentials.
func (c *Config) String() string {
	if c == nil {
		return "Config<nil>"
	}
	server := c.Server
	server.Storage.DSN = redactDSNPassword(server.Storage.DSN)
	server.Storage.MigrationDSN = redactDSNPassword(server.Storage.MigrationDSN)
	server.Secrets.SigningKey = ""
	server.Secrets.OIDCClientSecret = ""
	httpCfg := c.HTTP
	httpCfg.Token = ""
	vec := c.Vector
	vec.Qdrant.APIKey = ""                                 // hard redaction: no plaintext secret in String()
	vec.Pgvector.DSN = redactDSNPassword(vec.Pgvector.DSN) // redact password from DSN
	return fmt.Sprintf(
		"Config{Server: %+v, Database: %+v, MCP: %+v, HTTP: %+v, Logging: %+v, Search: %+v, Memory: %+v, Lifecycle: %+v, Vector: %+v}",
		server, c.Database, c.MCP, httpCfg, c.Logging, c.Search, c.Memory, c.Lifecycle, vec,
	)
}

// ensureDefaultConfig creates ~/.cortex/cortex.yaml with default values
// if it does not already exist. Errors are intentionally ignored (best-effort).
func ensureDefaultConfig(cfg *Config) error {
	path := filepath.Join(CortexDir(), "cortex.yaml")
	if _, err := os.Stat(path); err == nil {
		return nil // file already exists
	}
	return Save(cfg, path)
}

// redactDSNPassword replaces the password component of a PostgreSQL DSN with
// ***REDACTED***. Supports both URL format (postgres://user:pass@host/db) and
// key=value format (host=localhost password=pass). Returns the original string
// if no password is found. Used by Config.String() so DSNs never leak secrets
// in logs or debug output (REQ-CP-002).
func redactDSNPassword(dsn string) string {
	if dsn == "" {
		return ""
	}
	// URL format: postgres://user:password@host
	if strings.Contains(dsn, "://") {
		atIdx := strings.LastIndex(dsn, "@")
		schemeEnd := strings.Index(dsn, "://")
		if atIdx > schemeEnd {
			creds := dsn[schemeEnd+3 : atIdx]
			if colonIdx := strings.Index(creds, ":"); colonIdx >= 0 {
				return dsn[:schemeEnd+3+colonIdx+1] + "***REDACTED***" + dsn[atIdx:]
			}
		}
	}
	// Key=value format: password=value
	if idx := strings.Index(dsn, "password="); idx >= 0 {
		start := idx + len("password=")
		rest := dsn[start:]
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			return dsn[:start] + "***REDACTED***"
		}
		return dsn[:start] + "***REDACTED***" + rest[end:]
	}
	return dsn
}

// Save writes the configuration to the specified path.
// If path is empty, writes to ~/.cortex/cortex.yaml.
// The write is atomic: data goes to a .tmp file first, then renamed.
func Save(cfg *Config, path string) error {
	if path == "" {
		// Use the same file that was loaded, or default to ~/.cortex/cortex.yaml
		if cfg.LoadedFrom != "" {
			path = cfg.LoadedFrom
		} else {
			path = filepath.Join(CortexDir(), "cortex.yaml")
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}

	return nil
}
