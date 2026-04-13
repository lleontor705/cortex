package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

	// LoadedFrom is the path of the config file that was loaded.
	// Used by Save() and ReloadConfig() to always use the same file.
	// Not serialized to YAML.
	LoadedFrom string `yaml:"-" mapstructure:"-"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	Name    string `yaml:"name" mapstructure:"name"`
	Version string `yaml:"version" mapstructure:"version"`
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
	Enabled bool   `yaml:"enabled" mapstructure:"enabled"`
	Port    int    `yaml:"port" mapstructure:"port"`
	Host    string `yaml:"host" mapstructure:"host"`
	Token   string `yaml:"token" mapstructure:"token"`
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
	EmbeddingProvider string `yaml:"embedding_provider" mapstructure:"embedding_provider"` // "ollama", "openai", "none" (default)
	EmbeddingModel    string `yaml:"embedding_model" mapstructure:"embedding_model"`       // Model name override (e.g. "qwen3-embedding:8b")
	EmbeddingBaseURL  string `yaml:"embedding_base_url" mapstructure:"embedding_base_url"` // Ollama base URL override (default: http://localhost:11434)
	OllamaAutoStart   bool   `yaml:"ollama_auto_start" mapstructure:"ollama_auto_start"`   // Auto-start Ollama when configured as provider
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

// Default configuration values
var defaults = Config{
	Server: ServerConfig{
		Name:    "cortex",
		Version: "0.1.0",
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

// String returns a string representation of the configuration (for debugging)
func (c *Config) String() string {
	return fmt.Sprintf(
		"Config{Server: %+v, Database: %+v, MCP: %+v, HTTP: %+v, Logging: %+v, Search: %+v, Memory: %+v, Lifecycle: %+v}",
		c.Server, c.Database, c.MCP, c.HTTP, c.Logging, c.Search, c.Memory, c.Lifecycle,
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
