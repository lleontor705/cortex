package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/internal/transportpolicy"
	"github.com/pelletier/go-toml/v2"
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
	AI        AIConfig        `yaml:"ai,omitempty" json:"ai,omitempty" toml:"ai,omitempty" mapstructure:"ai"`
	Database  DatabaseConfig  `yaml:"database,omitempty" json:"database,omitempty" toml:"database,omitempty" mapstructure:"database"`
	HTTP      HTTPConfig      `yaml:"http,omitempty" json:"http,omitempty" toml:"http,omitempty" mapstructure:"http"`
	Server    ServerConfig    `yaml:"server,omitempty" json:"server,omitempty" toml:"server,omitempty" mapstructure:"server"`
	MCP       MCPConfig       `yaml:"mcp,omitempty" json:"mcp,omitempty" toml:"mcp,omitempty" mapstructure:"mcp"`
	Logging   LoggingConfig   `yaml:"logging,omitempty" json:"logging,omitempty" toml:"logging,omitempty" mapstructure:"logging"`
	Search    SearchConfig    `yaml:"search,omitempty" json:"search,omitempty" toml:"search,omitempty" mapstructure:"search"`
	Memory    MemoryConfig    `yaml:"memory,omitempty" json:"memory,omitempty" toml:"memory,omitempty" mapstructure:"memory"`
	Lifecycle LifecycleConfig `yaml:"lifecycle,omitempty" json:"lifecycle,omitempty" toml:"lifecycle,omitempty" mapstructure:"lifecycle"`
	Vector    VectorConfig    `yaml:"vector,omitempty" json:"vector,omitempty" toml:"vector,omitempty" mapstructure:"vector"`
	Sync      SyncConfig      `yaml:"sync,omitempty" json:"sync,omitempty" toml:"sync,omitempty" mapstructure:"sync"`

	// LoadedFrom is the path of the config file that was loaded.
	// Used by Save() and ReloadConfig() to always use the same file.
	// Not serialized to YAML, JSON, or TOML.
	LoadedFrom string `yaml:"-" json:"-" toml:"-" mapstructure:"-"`
}

// AIConfig holds unified AI and embedding model settings
type AIConfig struct {
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty" toml:"provider,omitempty" mapstructure:"provider"`
	Model    string `yaml:"model,omitempty" json:"model,omitempty" toml:"model,omitempty" mapstructure:"model"`
	BaseURL  string `yaml:"base_url,omitempty" json:"base_url,omitempty" toml:"base_url,omitempty" mapstructure:"base_url"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Name                    string               `yaml:"name,omitempty" json:"name,omitempty" toml:"name,omitempty" mapstructure:"name"`
	Version                 string               `yaml:"version,omitempty" json:"version,omitempty" toml:"version,omitempty" mapstructure:"version"`
	Storage                 ServerStorageConfig  `yaml:"storage,omitempty" json:"storage,omitempty" toml:"storage,omitempty" mapstructure:"storage"`
	Provider                ServerProviderConfig `yaml:"provider,omitempty" json:"provider,omitempty" toml:"provider,omitempty" mapstructure:"provider"`
	Secrets                 ServerSecretsConfig  `yaml:"secrets,omitempty" json:"secrets,omitempty" toml:"secrets,omitempty" mapstructure:"secrets"`
	TenantID                string               `yaml:"tenant_id,omitempty" json:"tenant_id,omitempty" toml:"tenant_id,omitempty" mapstructure:"tenant_id"`
	WorkspaceID             string               `yaml:"workspace_id,omitempty" json:"workspace_id,omitempty" toml:"workspace_id,omitempty" mapstructure:"workspace_id"`
	PrincipalSubject        string               `yaml:"principal_subject,omitempty" json:"principal_subject,omitempty" toml:"principal_subject,omitempty" mapstructure:"principal_subject"`
	GrantDigest             string               `yaml:"grant_digest,omitempty" json:"grant_digest,omitempty" toml:"grant_digest,omitempty" mapstructure:"grant_digest"`
	GrantVersion            int64                `yaml:"grant_version,omitempty" json:"grant_version,omitempty" toml:"grant_version,omitempty" mapstructure:"grant_version"`
	Roles                   []string             `yaml:"roles,omitempty" json:"roles,omitempty" toml:"roles,omitempty" mapstructure:"roles"`
	Scopes                  []string             `yaml:"scopes,omitempty" json:"scopes,omitempty" toml:"scopes,omitempty" mapstructure:"scopes"`
	ProjectIDs              []string             `yaml:"project_ids,omitempty" json:"project_ids,omitempty" toml:"project_ids,omitempty" mapstructure:"project_ids"`
	ClassificationClearance []string             `yaml:"classification_clearance,omitempty" json:"classification_clearance,omitempty" toml:"classification_clearance,omitempty" mapstructure:"classification_clearance"`
	BootstrapDevelopment    bool                 `yaml:"bootstrap_development,omitempty" json:"bootstrap_development,omitempty" toml:"bootstrap_development,omitempty" mapstructure:"bootstrap_development"`
}

// ServerStorageConfig contains server-only PostgreSQL connection settings.
// It is intentionally separate from the local SQLite database config.
type ServerStorageConfig struct {
	Driver       string `yaml:"driver,omitempty" json:"driver,omitempty" toml:"driver,omitempty" mapstructure:"driver"`
	DSN          string `yaml:"dsn,omitempty" json:"dsn,omitempty" toml:"dsn,omitempty" mapstructure:"dsn"`
	MigrationDSN string `yaml:"migration_dsn,omitempty" json:"migration_dsn,omitempty" toml:"migration_dsn,omitempty" mapstructure:"migration_dsn"`
	MaxConns     int32  `yaml:"max_conns,omitempty" json:"max_conns,omitempty" toml:"max_conns,omitempty" mapstructure:"max_conns"`
}

// ServerProviderConfig selects server-side providers without constructing them.
type ServerProviderConfig struct {
	Embedding string `yaml:"embedding,omitempty" json:"embedding,omitempty" toml:"embedding,omitempty" mapstructure:"embedding"`
	Vector    string `yaml:"vector,omitempty" json:"vector,omitempty" toml:"vector,omitempty" mapstructure:"vector"`
}

// ServerSecretsConfig contains credentials consumed by later identity waves.
// Secrets are never rendered by Config.String.
type ServerSecretsConfig struct {
	SigningKey       string `yaml:"signing_key,omitempty" json:"signing_key,omitempty" toml:"signing_key,omitempty" mapstructure:"signing_key"`
	OIDCClientSecret string `yaml:"oidc_client_secret,omitempty" json:"oidc_client_secret,omitempty" toml:"oidc_client_secret,omitempty" mapstructure:"oidc_client_secret"`
}

// DatabaseConfig holds database configuration
type DatabaseConfig struct {
	Path     string       `yaml:"path,omitempty" json:"path,omitempty" toml:"path,omitempty" mapstructure:"path"`
	InMemory bool         `yaml:"in_memory,omitempty" json:"in_memory,omitempty" toml:"in_memory,omitempty" mapstructure:"in_memory"`
	Pragma   PragmaConfig `yaml:"pragma,omitempty" json:"pragma,omitempty" toml:"pragma,omitempty" mapstructure:"pragma"`
}

// PragmaConfig holds SQLite pragma settings
type PragmaConfig struct {
	JournalMode string `yaml:"journal_mode,omitempty" json:"journal_mode,omitempty" toml:"journal_mode,omitempty" mapstructure:"journal_mode"`
	Synchronous string `yaml:"synchronous,omitempty" json:"synchronous,omitempty" toml:"synchronous,omitempty" mapstructure:"synchronous"`
	CacheSize   int    `yaml:"cache_size,omitempty" json:"cache_size,omitempty" toml:"cache_size,omitempty" mapstructure:"cache_size"`
	ForeignKeys bool   `yaml:"foreign_keys,omitempty" json:"foreign_keys,omitempty" toml:"foreign_keys,omitempty" mapstructure:"foreign_keys"`
	TempStore   string `yaml:"temp_store,omitempty" json:"temp_store,omitempty" toml:"temp_store,omitempty" mapstructure:"temp_store"`
	MmapSize    int    `yaml:"mmap_size,omitempty" json:"mmap_size,omitempty" toml:"mmap_size,omitempty" mapstructure:"mmap_size"`
}

// MCPConfig holds MCP (Model Context Protocol) configuration
type MCPConfig struct {
	Enabled bool            `yaml:"enabled,omitempty" json:"enabled,omitempty" toml:"enabled,omitempty" mapstructure:"enabled"`
	Remote  MCPRemoteConfig `yaml:"remote,omitempty" json:"remote,omitempty" toml:"remote,omitempty" mapstructure:"remote"`
}

// MCPRemoteConfig makes the local stdio MCP process proxy an authenticated
// Streamable HTTP MCP server instead of opening the local SQLite composition.
type MCPRemoteConfig struct {
	Enabled  bool          `yaml:"enabled,omitempty" json:"enabled,omitempty" toml:"enabled,omitempty" mapstructure:"enabled"`
	URL      string        `yaml:"url,omitempty" json:"url,omitempty" toml:"url,omitempty" mapstructure:"url"`
	TokenEnv string        `yaml:"token_env,omitempty" json:"token_env,omitempty" toml:"token_env,omitempty" mapstructure:"token_env"`
	Timeout  time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty" toml:"timeout,omitempty" mapstructure:"timeout"`
}

// SyncConfig controls optional bidirectional SQLite/server replication.
type SyncConfig struct {
	Enabled  bool          `yaml:"enabled,omitempty" json:"enabled,omitempty" toml:"enabled,omitempty" mapstructure:"enabled"`
	URL      string        `yaml:"url,omitempty" json:"url,omitempty" toml:"url,omitempty" mapstructure:"url"`
	TokenEnv string        `yaml:"token_env,omitempty" json:"token_env,omitempty" toml:"token_env,omitempty" mapstructure:"token_env"`
	Interval time.Duration `yaml:"interval,omitempty" json:"interval,omitempty" toml:"interval,omitempty" mapstructure:"interval"`
	Timeout  time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty" toml:"timeout,omitempty" mapstructure:"timeout"`
}

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Enabled        bool     `yaml:"enabled,omitempty" json:"enabled,omitempty" toml:"enabled,omitempty" mapstructure:"enabled"`
	Port           int      `yaml:"port,omitempty" json:"port,omitempty" toml:"port,omitempty" mapstructure:"port"`
	Host           string   `yaml:"host,omitempty" json:"host,omitempty" toml:"host,omitempty" mapstructure:"host"`
	Token          string   `yaml:"token,omitempty" json:"token,omitempty" toml:"token,omitempty" mapstructure:"token"`
	AllowedOrigins []string `yaml:"allowed_origins,omitempty" json:"allowed_origins,omitempty" toml:"allowed_origins,omitempty" mapstructure:"allowed_origins"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level,omitempty" json:"level,omitempty" toml:"level,omitempty" mapstructure:"level"`
	Format string `yaml:"format,omitempty" json:"format,omitempty" toml:"format,omitempty" mapstructure:"format"`
}

// SearchConfig holds search-related configuration
type SearchConfig struct {
	DefaultLimit      int     `yaml:"default_limit,omitempty" json:"default_limit,omitempty" toml:"default_limit,omitempty" mapstructure:"default_limit"`
	MaxLimit          int     `yaml:"max_limit,omitempty" json:"max_limit,omitempty" toml:"max_limit,omitempty" mapstructure:"max_limit"`
	FTS5              bool    `yaml:"fts5,omitempty" json:"fts5,omitempty" toml:"fts5,omitempty" mapstructure:"fts5"`
	Vector            bool    `yaml:"vector,omitempty" json:"vector,omitempty" toml:"vector,omitempty" mapstructure:"vector"`
	FusionK           float64 `yaml:"fusion_k,omitempty" json:"fusion_k,omitempty" toml:"fusion_k,omitempty" mapstructure:"fusion_k"`
	EmbeddingProvider string  `yaml:"embedding_provider,omitempty" json:"embedding_provider,omitempty" toml:"embedding_provider,omitempty" mapstructure:"embedding_provider"` // "ollama", "openai", "none" (default)
	EmbeddingModel    string  `yaml:"embedding_model,omitempty" json:"embedding_model,omitempty" toml:"embedding_model,omitempty" mapstructure:"embedding_model"`             // Model name override (e.g. "qwen3-embedding:8b")
	EmbeddingBaseURL  string  `yaml:"embedding_base_url,omitempty" json:"embedding_base_url,omitempty" toml:"embedding_base_url,omitempty" mapstructure:"embedding_base_url"`   // Ollama base URL override (default: http://localhost:11434)
	OllamaAutoStart   bool    `yaml:"ollama_auto_start,omitempty" json:"ollama_auto_start,omitempty" toml:"ollama_auto_start,omitempty" mapstructure:"ollama_auto_start"`         // Auto-start Ollama when configured as provider
}

// MemoryConfig holds memory management configuration
type MemoryConfig struct {
	MaxObservationLength int     `yaml:"max_observation_length,omitempty" json:"max_observation_length,omitempty" toml:"max_observation_length,omitempty" mapstructure:"max_observation_length"`
	DedupeWindow         string  `yaml:"dedupe_window,omitempty" json:"dedupe_window,omitempty" toml:"dedupe_window,omitempty" mapstructure:"dedupe_window"`
	AutoArchiveDays      int     `yaml:"auto_archive_days,omitempty" json:"auto_archive_days,omitempty" toml:"auto_archive_days,omitempty" mapstructure:"auto_archive_days"`
	DecayHalfLifeDays    float64 `yaml:"importance_decay_half_life,omitempty" json:"importance_decay_half_life,omitempty" toml:"importance_decay_half_life,omitempty" mapstructure:"importance_decay_half_life"`
	MinArchiveScore      float64 `yaml:"min_archive_score,omitempty" json:"min_archive_score,omitempty" toml:"min_archive_score,omitempty" mapstructure:"min_archive_score"`
}

// LifecycleConfig holds lifecycle management configuration
type LifecycleConfig struct {
	EnableAutoArchive    bool   `yaml:"enable_auto_archive,omitempty" json:"enable_auto_archive,omitempty" toml:"enable_auto_archive,omitempty" mapstructure:"enable_auto_archive"`
	ArchiveCheckInterval string `yaml:"archive_check_interval,omitempty" json:"archive_check_interval,omitempty" toml:"archive_check_interval,omitempty" mapstructure:"archive_check_interval"`
}

// VectorConfig holds external vector index adapter configuration.
type VectorConfig struct {
	Provider string         `yaml:"provider,omitempty" json:"provider,omitempty" toml:"provider,omitempty" mapstructure:"provider"`
	Qdrant   QdrantConfig   `yaml:"qdrant,omitempty" json:"qdrant,omitempty" toml:"qdrant,omitempty" mapstructure:"qdrant"`
	Pgvector PGVectorConfig `yaml:"pgvector,omitempty" json:"pgvector,omitempty" toml:"pgvector,omitempty" mapstructure:"pgvector"`
}

// QdrantConfig holds connection parameters for the Qdrant external vector adapter.
type QdrantConfig struct {
	Host         string        `yaml:"host,omitempty" json:"host,omitempty" toml:"host,omitempty" mapstructure:"host"`
	Port         int           `yaml:"port,omitempty" json:"port,omitempty" toml:"port,omitempty" mapstructure:"port"`
	Collection   string        `yaml:"collection,omitempty" json:"collection,omitempty" toml:"collection,omitempty" mapstructure:"collection"`
	Dimension    int           `yaml:"dimension,omitempty" json:"dimension,omitempty" toml:"dimension,omitempty" mapstructure:"dimension"`
	APIKey       string        `yaml:"api_key,omitempty" json:"api_key,omitempty" toml:"api_key,omitempty" mapstructure:"api_key"`
	UseTLS       bool          `yaml:"use_tls,omitempty" json:"use_tls,omitempty" toml:"use_tls,omitempty" mapstructure:"use_tls"`
	MaxBatchSize int           `yaml:"max_batch_size,omitempty" json:"max_batch_size,omitempty" toml:"max_batch_size,omitempty" mapstructure:"max_batch_size"`
	MaxRetries   uint          `yaml:"max_retries,omitempty" json:"max_retries,omitempty" toml:"max_retries,omitempty" mapstructure:"max_retries"`
	Timeout      time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty" toml:"timeout,omitempty" mapstructure:"timeout"`
}

// PGVectorConfig holds connection parameters for the pgvector external vector adapter.
type PGVectorConfig struct {
	DSN                string        `yaml:"dsn,omitempty" json:"dsn,omitempty" toml:"dsn,omitempty" mapstructure:"dsn"`
	Schema             string        `yaml:"schema,omitempty" json:"schema,omitempty" toml:"schema,omitempty" mapstructure:"schema"`
	Table              string        `yaml:"table,omitempty" json:"table,omitempty" toml:"table,omitempty" mapstructure:"table"`
	Dimension          int           `yaml:"dimension,omitempty" json:"dimension,omitempty" toml:"dimension,omitempty" mapstructure:"dimension"`
	IndexType          string        `yaml:"index_type,omitempty" json:"index_type,omitempty" toml:"index_type,omitempty" mapstructure:"index_type"`
	HNSWM              int           `yaml:"hnsw_m,omitempty" json:"hnsw_m,omitempty" toml:"hnsw_m,omitempty" mapstructure:"hnsw_m"`
	HNSWEfConstruction int           `yaml:"hnsw_ef_construction,omitempty" json:"hnsw_ef_construction,omitempty" toml:"hnsw_ef_construction,omitempty" mapstructure:"hnsw_ef_construction"`
	IVFFlatLists       int           `yaml:"ivfflat_lists,omitempty" json:"ivfflat_lists,omitempty" toml:"ivfflat_lists,omitempty" mapstructure:"ivfflat_lists"`
	MaxBatchSize       int           `yaml:"max_batch_size,omitempty" json:"max_batch_size,omitempty" toml:"max_batch_size,omitempty" mapstructure:"max_batch_size"`
	Timeout            time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty" toml:"timeout,omitempty" mapstructure:"timeout"`
	MaxConns           int32         `yaml:"max_conns,omitempty" json:"max_conns,omitempty" toml:"max_conns,omitempty" mapstructure:"max_conns"`
	StatementTimeoutMs int           `yaml:"statement_timeout_ms,omitempty" json:"statement_timeout_ms,omitempty" toml:"statement_timeout_ms,omitempty" mapstructure:"statement_timeout_ms"`
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
		Remote:  MCPRemoteConfig{TokenEnv: "CORTEX_REMOTE_TOKEN", Timeout: 30 * time.Second},
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
// The configPath parameter is optional - if empty, it searches for cortex.yaml, cortex.yml, cortex.json, cortex.toml
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Set default values
	setDefaults(v)

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		// Configure viper name and search paths for multi-format lookup
		v.SetConfigName("cortex")
		v.AddConfigPath(".")
		v.AddConfigPath("./config")
		v.AddConfigPath(CortexDir())
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

	// Synchronize unified AI section with search config
	if cfg.AI.Provider != "" {
		if cfg.Search.EmbeddingProvider == "" {
			cfg.Search.EmbeddingProvider = cfg.AI.Provider
		}
	} else if cfg.Search.EmbeddingProvider != "" {
		cfg.AI.Provider = cfg.Search.EmbeddingProvider
	}
	if cfg.AI.Model != "" {
		if cfg.Search.EmbeddingModel == "" {
			cfg.Search.EmbeddingModel = cfg.AI.Model
		}
	} else if cfg.Search.EmbeddingModel != "" {
		cfg.AI.Model = cfg.Search.EmbeddingModel
	}
	if cfg.AI.BaseURL != "" {
		if cfg.Search.EmbeddingBaseURL == "" {
			cfg.Search.EmbeddingBaseURL = cfg.AI.BaseURL
		}
	} else if cfg.Search.EmbeddingBaseURL != "" {
		cfg.AI.BaseURL = cfg.Search.EmbeddingBaseURL
	}

	// Expand ~ in database path
	if strings.HasPrefix(cfg.Database.Path, "~/") || strings.HasPrefix(cfg.Database.Path, "~\\") {
		home, _ := os.UserHomeDir()
		if home != "" {
			cfg.Database.Path = filepath.Join(home, cfg.Database.Path[2:])
		}
	}

	if cfg.Server.Storage.DSN == "" {
		if dsn := os.Getenv("CORTEX_SERVER_STORAGE_DSN"); dsn != "" {
			cfg.Server.Storage.DSN = dsn
		} else if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
			cfg.Server.Storage.DSN = dsn
		} else if dsn := os.Getenv("POSTGRES_URL"); dsn != "" {
			cfg.Server.Storage.DSN = dsn
		}
	}
	if cfg.Server.Storage.MigrationDSN == "" {
		if dsn := os.Getenv("CORTEX_SERVER_STORAGE_MIGRATION_DSN"); dsn != "" {
			cfg.Server.Storage.MigrationDSN = dsn
		} else if cfg.Server.Storage.DSN != "" {
			cfg.Server.Storage.MigrationDSN = cfg.Server.Storage.DSN
		}
	}
	if cfg.HTTP.Token == "" {
		if tok := os.Getenv("CORTEX_HTTP_TOKEN"); tok != "" {
			cfg.HTTP.Token = tok
		}
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

	v.SetDefault("ai.provider", "")
	v.SetDefault("ai.model", "")
	v.SetDefault("ai.base_url", "")

	v.SetDefault("database.path", DefaultDBPath())
	v.SetDefault("database.in_memory", defaults.Database.InMemory)
	v.SetDefault("database.pragma.journal_mode", defaults.Database.Pragma.JournalMode)
	v.SetDefault("database.pragma.synchronous", defaults.Database.Pragma.Synchronous)
	v.SetDefault("database.pragma.cache_size", defaults.Database.Pragma.CacheSize)
	v.SetDefault("database.pragma.foreign_keys", defaults.Database.Pragma.ForeignKeys)
	v.SetDefault("database.pragma.temp_store", defaults.Database.Pragma.TempStore)
	v.SetDefault("database.pragma.mmap_size", defaults.Database.Pragma.MmapSize)

	v.SetDefault("mcp.enabled", defaults.MCP.Enabled)
	v.SetDefault("mcp.remote.enabled", defaults.MCP.Remote.Enabled)
	v.SetDefault("mcp.remote.url", defaults.MCP.Remote.URL)
	v.SetDefault("mcp.remote.token_env", defaults.MCP.Remote.TokenEnv)
	v.SetDefault("mcp.remote.timeout", defaults.MCP.Remote.Timeout)
	v.SetDefault("sync.enabled", false)
	v.SetDefault("sync.url", "")
	v.SetDefault("sync.token_env", "CORTEX_REMOTE_TOKEN")
	v.SetDefault("sync.interval", 30*time.Second)
	v.SetDefault("sync.timeout", 30*time.Second)

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

	if cfg.MCP.Remote.Enabled {
		// mcp.remote.url is a Bearer destination: enforce the shared
		// transport policy (HTTPS off-loopback, strict-loopback HTTP only)
		// at load time so misconfiguration fails fast (REM-TRANSPORT-001).
		if err := transportpolicy.ValidateBearerDestination(cfg.MCP.Remote.URL); err != nil {
			return fmt.Errorf("invalid mcp.remote.url: %w", err)
		}
		if strings.TrimSpace(cfg.MCP.Remote.TokenEnv) == "" {
			return fmt.Errorf("invalid mcp.remote.token_env: environment variable name is required")
		}
		if cfg.MCP.Remote.Timeout <= 0 {
			return fmt.Errorf("invalid mcp.remote.timeout: must be greater than zero")
		}
	}
	if cfg.Sync.Enabled {
		// sync.url is a Bearer destination: same shared transport policy.
		if err := transportpolicy.ValidateBearerDestination(cfg.Sync.URL); err != nil {
			return fmt.Errorf("invalid sync.url: %w", err)
		}
		if strings.TrimSpace(cfg.Sync.TokenEnv) == "" {
			return fmt.Errorf("invalid sync.token_env: environment variable name is required")
		}
		if cfg.Sync.Interval <= 0 || cfg.Sync.Timeout <= 0 {
			return fmt.Errorf("invalid sync interval and timeout: both must be greater than zero")
		}
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

// Validate checks a configuration without reading or writing a file.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("configuration is nil")
	}
	return validate(cfg)
}

// GetProperty retrieves a configuration value by dot-separated path.
func (cfg *Config) GetProperty(key string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	switch key {
	case "database.path":
		return cfg.Database.Path, nil
	case "database.in_memory":
		return fmt.Sprintf("%t", cfg.Database.InMemory), nil
	case "http.enabled":
		return fmt.Sprintf("%t", cfg.HTTP.Enabled), nil
	case "http.port":
		return fmt.Sprintf("%d", cfg.HTTP.Port), nil
	case "http.host":
		return cfg.HTTP.Host, nil
	case "http.token":
		return cfg.HTTP.Token, nil
	case "logging.level":
		return cfg.Logging.Level, nil
	case "logging.format":
		return cfg.Logging.Format, nil
	case "search.embedding_provider":
		return cfg.Search.EmbeddingProvider, nil
	case "search.embedding_model":
		return cfg.Search.EmbeddingModel, nil
	case "search.embedding_base_url":
		return cfg.Search.EmbeddingBaseURL, nil
	case "search.vector":
		return fmt.Sprintf("%t", cfg.Search.Vector), nil
	case "search.fts5":
		return fmt.Sprintf("%t", cfg.Search.FTS5), nil
	case "search.ollama_auto_start":
		return fmt.Sprintf("%t", cfg.Search.OllamaAutoStart), nil
	case "mcp.enabled":
		return fmt.Sprintf("%t", cfg.MCP.Enabled), nil
	case "mcp.remote.enabled":
		return fmt.Sprintf("%t", cfg.MCP.Remote.Enabled), nil
	case "mcp.remote.url":
		return cfg.MCP.Remote.URL, nil
	case "mcp.remote.token_env":
		return cfg.MCP.Remote.TokenEnv, nil
	case "sync.enabled":
		return fmt.Sprintf("%t", cfg.Sync.Enabled), nil
	case "sync.url":
		return cfg.Sync.URL, nil
	case "sync.token_env":
		return cfg.Sync.TokenEnv, nil
	case "sync.interval":
		return cfg.Sync.Interval.String(), nil
	case "memory.auto_archive_days":
		return fmt.Sprintf("%d", cfg.Memory.AutoArchiveDays), nil
	case "memory.importance_decay_half_life":
		return fmt.Sprintf("%.1f", cfg.Memory.DecayHalfLifeDays), nil
	case "memory.min_archive_score":
		return fmt.Sprintf("%.2f", cfg.Memory.MinArchiveScore), nil
	case "vector.provider":
		return cfg.Vector.Provider, nil
	case "ai.provider":
		return cfg.AI.Provider, nil
	case "ai.model":
		return cfg.AI.Model, nil
	case "ai.base_url":
		return cfg.AI.BaseURL, nil
	default:
		return "", fmt.Errorf("unknown configuration key: %q", key)
	}
}

// SetProperty updates a configuration value by dot-separated path.
func (cfg *Config) SetProperty(key, value string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	switch key {
	case "ai.provider":
		cfg.AI.Provider = strings.ToLower(value)
		cfg.Search.EmbeddingProvider = strings.ToLower(value)
	case "ai.model":
		cfg.AI.Model = value
		cfg.Search.EmbeddingModel = value
	case "ai.base_url":
		cfg.AI.BaseURL = value
		cfg.Search.EmbeddingBaseURL = value
	case "database.path":
		cfg.Database.Path = value
	case "database.in_memory":
		cfg.Database.InMemory = parseBool(value)
	case "http.enabled":
		cfg.HTTP.Enabled = parseBool(value)
	case "http.port":
		p, err := strconv.Atoi(value)
		if err != nil || p < 1 || p > 65535 {
			return fmt.Errorf("invalid port %q: must be integer 1-65535", value)
		}
		cfg.HTTP.Port = p
	case "http.host":
		cfg.HTTP.Host = value
	case "http.token":
		cfg.HTTP.Token = value
	case "logging.level":
		cfg.Logging.Level = strings.ToLower(value)
	case "logging.format":
		cfg.Logging.Format = strings.ToLower(value)
	case "search.embedding_provider":
		cfg.Search.EmbeddingProvider = strings.ToLower(value)
		cfg.AI.Provider = strings.ToLower(value)
	case "search.embedding_model":
		cfg.Search.EmbeddingModel = value
		cfg.AI.Model = value
	case "search.embedding_base_url":
		cfg.Search.EmbeddingBaseURL = value
		cfg.AI.BaseURL = value
	case "search.vector":
		cfg.Search.Vector = parseBool(value)
	case "search.fts5":
		cfg.Search.FTS5 = parseBool(value)
	case "search.ollama_auto_start":
		cfg.Search.OllamaAutoStart = parseBool(value)
	case "mcp.enabled":
		cfg.MCP.Enabled = parseBool(value)
	case "mcp.remote.enabled":
		cfg.MCP.Remote.Enabled = parseBool(value)
	case "mcp.remote.url":
		cfg.MCP.Remote.URL = value
	case "mcp.remote.token_env":
		cfg.MCP.Remote.TokenEnv = value
	case "sync.enabled":
		cfg.Sync.Enabled = parseBool(value)
	case "sync.url":
		cfg.Sync.URL = value
	case "sync.token_env":
		cfg.Sync.TokenEnv = value
	case "sync.interval":
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid duration %q (e.g. '30s', '1m'): %w", value, err)
		}
		cfg.Sync.Interval = d
	case "memory.auto_archive_days":
		days, err := strconv.Atoi(value)
		if err != nil || days < 1 {
			return fmt.Errorf("invalid auto_archive_days %q: must be positive integer", value)
		}
		cfg.Memory.AutoArchiveDays = days
	case "vector.provider":
		cfg.Vector.Provider = strings.ToLower(value)
	default:
		return fmt.Errorf("unknown or unsupported configuration key: %q", key)
	}
	return Validate(cfg)
}

func parseBool(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "true" || v == "1" || v == "yes" || v == "on"
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

// Minimal templates for initial setup
const MinimalDefaultYAML = `# Cortex Configuration (~/.cortex/cortex.yaml)
# Minimal configuration for local memory and agent tools.

database:
  path: ~/.cortex/cortex.db

http:
  port: 7438

ai:
  provider: "" # "ollama", "openai", "anthropic", or "" for keyword search
`

const MinimalDefaultJSON = `{
  "$schema": "https://cortex.dev/schemas/v2/config.json",
  "database": {
    "path": "~/.cortex/cortex.db"
  },
  "http": {
    "port": 7438
  },
  "ai": {
    "provider": ""
  }
}
`

const MinimalDefaultTOML = `# Cortex Configuration (~/.cortex/cortex.toml)
# Minimal configuration for local memory and agent tools.

[database]
path = "~/.cortex/cortex.db"

[http]
port = 7438

[ai]
provider = ""
`

// InitConfig creates a clean minimal config file of the requested format.
func InitConfig(targetPath string, format string, force bool) (string, error) {
	if format == "" {
		format = "yaml"
	}
	format = strings.ToLower(strings.TrimPrefix(format, "."))
	if targetPath == "" {
		ext := format
		if ext == "yml" {
			ext = "yaml"
		}
		targetPath = filepath.Join(CortexDir(), "cortex."+ext)
	}

	if _, err := os.Stat(targetPath); err == nil && !force {
		return targetPath, fmt.Errorf("config file already exists at %s (use --force to overwrite)", targetPath)
	}

	var content string
	switch format {
	case "json", "jsonc":
		content = MinimalDefaultJSON
	case "toml":
		content = MinimalDefaultTOML
	default:
		content = MinimalDefaultYAML
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}

	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	return targetPath, nil
}

// ensureDefaultConfig creates ~/.cortex/cortex.yaml with default values
// if it does not already exist. Errors are intentionally ignored (best-effort).
func ensureDefaultConfig(cfg *Config) error {
	path := filepath.Join(CortexDir(), "cortex.yaml")
	if _, err := os.Stat(path); err == nil {
		return nil // file already exists
	}
	_, err := InitConfig(path, "yaml", false)
	return err
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
// If path is empty, writes to the loaded path or default ~/.cortex/cortex.yaml.
// The write is atomic: data goes to a .tmp file first, then renamed.
func Save(cfg *Config, path string) error {
	if cfg == nil {
		return fmt.Errorf("save config: configuration is nil")
	}

	// Synchronize AI section before saving
	if cfg.Search.EmbeddingProvider != "" && cfg.AI.Provider == "" {
		cfg.AI.Provider = cfg.Search.EmbeddingProvider
	}
	if cfg.Search.EmbeddingModel != "" && cfg.AI.Model == "" {
		cfg.AI.Model = cfg.Search.EmbeddingModel
	}
	if cfg.Search.EmbeddingBaseURL != "" && cfg.AI.BaseURL == "" {
		cfg.AI.BaseURL = cfg.Search.EmbeddingBaseURL
	}
	if cfg.AI.Provider != "" && cfg.Search.EmbeddingProvider == "" {
		cfg.Search.EmbeddingProvider = cfg.AI.Provider
	}

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

	var data []byte
	var err error
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".jsonc") {
		data, err = json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal json config: %w", err)
		}
		data = append(data, '\n')
	} else if strings.HasSuffix(lower, ".toml") {
		data, err = toml.Marshal(cfg)
		if err != nil {
			return fmt.Errorf("marshal toml config: %w", err)
		}
	} else {
		data, err = marshalConfigPreservingExisting(cfg, path)
		if err != nil {
			return err
		}
	}

	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config: %w", err)
	}

	return nil
}

func marshalConfigPreservingExisting(cfg *Config, path string) ([]byte, error) {
	desiredData, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var desired yaml.Node
	if err := yaml.Unmarshal(desiredData, &desired); err != nil {
		return nil, fmt.Errorf("parse generated config: %w", err)
	}
	existingData, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return desiredData, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read existing config: %w", err)
	}
	var existing yaml.Node
	if err := yaml.Unmarshal(existingData, &existing); err != nil {
		return nil, fmt.Errorf("parse existing config: %w", err)
	}
	if len(existing.Content) != 1 || existing.Content[0].Kind != yaml.MappingNode || len(desired.Content) != 1 || desired.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("parse existing config: document root must be a mapping")
	}
	mergeYAMLNode(existing.Content[0], desired.Content[0])
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(4)
	if err := encoder.Encode(&existing); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("close config encoder: %w", err)
	}
	return output.Bytes(), nil
}

func mergeYAMLNode(existing, desired *yaml.Node) {
	if existing.Kind == yaml.MappingNode && desired.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(desired.Content); i += 2 {
			key, value := desired.Content[i], desired.Content[i+1]
			found := false
			for j := 0; j+1 < len(existing.Content); j += 2 {
				if existing.Content[j].Value == key.Value {
					mergeYAMLNode(existing.Content[j+1], value)
					found = true
					break
				}
			}
			if !found {
				if !isDefaultOrEmptySection(key.Value, value) {
					existing.Content = append(existing.Content, key, value)
				}
			}
		}
		return
	}
	head, line, foot := existing.HeadComment, existing.LineComment, existing.FootComment
	*existing = *desired
	if head != "" {
		existing.HeadComment = head
	}
	if line != "" {
		existing.LineComment = line
	}
	if foot != "" {
		existing.FootComment = foot
	}
}

func isDefaultOrEmptySection(key string, node *yaml.Node) bool {
	switch key {
	case "server":
		return !nodeContainsValue(node, "dsn", "tenant_id", "workspace_id")
	case "vector":
		return !nodeContainsValue(node, "provider")
	case "sync":
		return !nodeContainsValue(node, "url", "interval")
	case "pragma":
		return true // Never inject SQLite pragma block by default if not present
	case "lifecycle":
		return true // Never inject lifecycle block by default if not present
	case "memory":
		return true // Never inject memory tuning block by default if not present
	case "search":
		// Only inject if embedding_provider is customized and not using ai section
		return !nodeContainsValue(node, "embedding_provider", "embedding_model")
	default:
		return false
	}
}

func nodeContainsValue(node *yaml.Node, keys ...string) bool {
	if node == nil || node.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i].Value
		v := node.Content[i+1]
		for _, target := range keys {
			if k == target && v.Value != "" && v.Value != "false" && v.Value != "0" {
				return true
			}
		}
		if v.Kind == yaml.MappingNode && nodeContainsValue(v, keys...) {
			return true
		}
	}
	return false
}
