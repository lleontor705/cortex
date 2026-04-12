package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"log"

	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/database"
	"github.com/lleontor705/cortex/internal/domain/lifecycle"
	"github.com/lleontor705/cortex/internal/embedding"
	"github.com/lleontor705/cortex/internal/migration"
	"github.com/lleontor705/cortex/internal/ollama"
	"github.com/lleontor705/cortex/internal/store/bundle"
	entitystore "github.com/lleontor705/cortex/internal/store/entity"
	graphstore "github.com/lleontor705/cortex/internal/store/graph"
	"github.com/lleontor705/cortex/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/internal/store/scoring"
	"github.com/lleontor705/cortex/internal/store/search"
	"github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// Options controls how the application is opened.
type Options struct {
	InMemory   bool
	ConfigPath string
}

// App bundles the runtime dependencies needed by CLI and MCP entrypoints.
type App struct {
	Config         *config.Config
	DB             *database.Manager
	Migrator       *migration.Migrator
	Stores         *bundle.Stores
	archivalCancel context.CancelFunc
}

// Open loads configuration, opens the database, applies migrations, and wires stores.
func Open(ctx context.Context, opts Options) (*App, error) {
	cfg, err := loadConfig(opts)
	if err != nil {
		return nil, err
	}

	dbCfg := database.DefaultConfig()
	if opts.InMemory || cfg.Database.InMemory {
		dbCfg = database.InMemoryConfig()
	} else {
		// Ensure parent directory exists for the database file
		if dir := filepath.Dir(cfg.Database.Path); dir != "." {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, fmt.Errorf("app: create data directory %s: %w", dir, err)
			}
		}
		dbCfg.Path = cfg.Database.Path
		dbCfg.EnableWAL = cfg.Database.Pragma.JournalMode == "WAL"
		dbCfg.CacheSize = cfg.Database.Pragma.CacheSize
		dbCfg.MMapSize = int64(cfg.Database.Pragma.MmapSize)
	}

	manager, err := database.NewManager(dbCfg)
	if err != nil {
		return nil, err
	}

	migrator, err := migration.NewMigrator(manager.DB(), migrationsDir())
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	if err := migrator.Up(ctx); err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("app: apply migrations: %w", err)
	}

	stores := &bundle.Stores{
		Observations:      sqlitestore.NewStore(manager.DB()),
		Sessions:          session.NewStore(manager.DB()),
		Search:            search.NewStore(manager.DB()),
		Prompts:           prompt.NewStore(manager.DB()),
		Graph:             graphstore.NewStore(manager.DB()),
		Scoring:           scoringstore.NewStore(manager.DB()),
		Vectors:           sqlitestore.NewVectorStore(manager.DB()),
		TemporalSnapshots: sqlitestore.NewTemporalSnapshotRepository(manager.DB()),
		Entities:          entitystore.NewStore(manager.DB()),
		Metrics:           sqlitestore.NewMetricsRepository(manager.DB()),
		QualityMetrics:    sqlitestore.NewQualityMetricsRepository(manager.DB()),
	}

	// Wire graph store into search for temporal-aware graph expansion
	stores.Search.Graph = stores.Graph

	// Auto-start Ollama if configured
	if cfg.Search.EmbeddingProvider == "ollama" && cfg.Search.OllamaAutoStart {
		mgr := ollama.NewManager(cfg.Search.EmbeddingBaseURL)
		if err := mgr.EnsureRunning(context.Background()); err != nil {
			log.Printf("warning: could not auto-start ollama: %v", err)
		}
	}

	// Initialize embedding service if configured
	embCfg := embedding.Config{
		Provider: cfg.Search.EmbeddingProvider,
		Model:    cfg.Search.EmbeddingModel,
		BaseURL:  cfg.Search.EmbeddingBaseURL,
	}
	stores.Embeddings = embedding.New(embCfg)

	a := &App{
		Config:   cfg,
		DB:       manager,
		Migrator: migrator,
		Stores:   stores,
	}

	// Start auto-archival if enabled
	if cfg.Lifecycle.EnableAutoArchive {
		interval, parseErr := time.ParseDuration(cfg.Lifecycle.ArchiveCheckInterval)
		if parseErr != nil {
			interval = time.Hour
		}

		archivalSvc := lifecycle.NewArchivalService(stores.Observations, lifecycle.ArchivalConfig{
			MaxAgeDays:      cfg.Memory.AutoArchiveDays,
			MinArchiveScore: cfg.Memory.MinArchiveScore,
			CheckInterval:   interval,
		})
		a.archivalCancel = archivalSvc.Start(ctx)
	}

	return a, nil
}

// ReloadConfig re-reads the configuration from disk and reinitializes
// the embedding service. Useful after manual edits to cortex.yaml.
func (a *App) ReloadConfig() error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}
	a.Config = cfg

	// Auto-start Ollama if configured
	if cfg.Search.EmbeddingProvider == "ollama" && cfg.Search.OllamaAutoStart {
		mgr := ollama.NewManager(cfg.Search.EmbeddingBaseURL)
		if err := mgr.EnsureRunning(context.Background()); err != nil {
			log.Printf("warning: could not auto-start ollama on reload: %v", err)
		}
	}

	// Reinitialize embedding service
	a.Stores.Embeddings = embedding.New(embedding.Config{
		Provider: cfg.Search.EmbeddingProvider,
		Model:    cfg.Search.EmbeddingModel,
		BaseURL:  cfg.Search.EmbeddingBaseURL,
	})

	return nil
}

// Close releases resources held by the application.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	if a.archivalCancel != nil {
		a.archivalCancel()
	}
	if a.DB == nil {
		return nil
	}
	return a.DB.Close()
}

func loadConfig(opts Options) (*config.Config, error) {
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("app: load config: %w", err)
	}
	if opts.InMemory {
		cfg.Database.InMemory = true
		cfg.Database.Path = ":memory:"
	}
	return cfg, nil
}

func migrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "migrations"))
}
