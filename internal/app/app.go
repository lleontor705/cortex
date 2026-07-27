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
	"github.com/lleontor705/cortex/internal/domain"
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
	"github.com/lleontor705/cortex/internal/vector/sqlite_blob"
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
	workerCancel   context.CancelFunc // embedding worker drain (W4.1, REQ-EMB-001)
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
		// READ-ONLY COMPATIBILITY PROBE (W3.2, REQ-DB-002): inspect the
		// configured database file STRICTLY READ-ONLY before any
		// write-capable open or pragma. Old Cortex v1, Engram, corrupt,
		// ambiguous, or partially-initialized databases are refused without
		// mutation (byte-identical SHA-256, no WAL/journal sidecar, no
		// replacement database). A fresh or cortex-v2-compatible path proceeds.
		if _, err := migration.ProbeCompatibility(ctx, cfg.Database.Path); err != nil {
			return nil, fmt.Errorf("app: %w", err)
		}
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

	// Apply the v2 schema baseline (consolidated final-state schema of v1
	// 001-014 plus v2 corrections, recorded in cortex_meta). On a fresh target
	// this creates the full schema in one transaction; on an existing cortex-v2
	// database it is an idempotent no-op. Incompatible databases were already
	// refused read-only by the probe above. The v1 migrator (001-014) is RETIRED
	// on the v2 line and MUST NOT drive startup schema (ADR-03, REQ-DB-001).
	baseline, err := migration.NewV2Baseline()
	if err != nil {
		_ = manager.Close()
		return nil, err
	}
	if err := baseline.Apply(ctx, manager.DB()); err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("app: apply v2 schema baseline: %w", err)
	}

	// The migrator remains available for the CLI `migrate status/up/down`
	// commands. It is v2-aware: on a cortex-v2 database it treats v1 001-014 as
	// retired (Up is a no-op; Status reports them consolidated into the v2
	// baseline). It does NOT drive startup schema — the baseline above does.
	migrator, err := migration.NewMigrator(manager.DB(), migrationsDir())
	if err != nil {
		_ = manager.Close()
		return nil, err
	}

	stores := &bundle.Stores{
		Observations:      sqlitestore.NewStore(manager.DB()),
		Sessions:          session.NewStore(manager.DB()),
		Search:            search.NewStore(manager.DB()),
		Prompts:           prompt.NewStore(manager.DB()),
		Graph:             graphstore.NewStore(manager.DB()),
		Scoring:           scoringstore.NewStore(manager.DB()),
		Vectors:           sqlite_blob.New(manager.DB()),
		TemporalSnapshots: sqlitestore.NewTemporalSnapshotRepository(manager.DB()),
		Entities:          entitystore.NewStore(manager.DB()),
		Metrics:           sqlitestore.NewMetricsRepository(manager.DB()),
		QualityMetrics:    sqlitestore.NewQualityMetricsRepository(manager.DB()),
	}

	// Wire the UnitOfWork for atomic cross-store saves (W2.1, W4.1). Always
	// wired — it is harmless when the outbox is not used (zero-embedding mode).
	stores.UnitOfWork = bundle.NewSQLiteUnitOfWork(manager.DB(), domain.DefaultBusyRetryConfig())

	// Wire graph store into search for temporal-aware graph expansion
	stores.Search.Graph = stores.Graph

	// Wire request-scoped search feedback attribution (W5.1, REQ-RET-001):
	// search.Store.RecordFeedback persists via Observations.RecordSearchFeedback,
	// attributed to the originating SearchID. This replaces the removed shared
	// mutable search-query field.
	bundle.WireSearchFeedback(stores)

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

	// Start the durable embedding worker when embeddings AND vector storage are
	// available (ADR-04, W4.1, REQ-EMB-001). In zero-embedding mode (no provider
	// configured, or vec extension unavailable), the worker is NOT started and
	// the outbox stays nil — the local save path is byte-for-byte unchanged.
	//
	// W8.1: stores.Vectors is now a domain.VectorIndex (the sqlite_blob adapter).
	// Availability is checked via Health (the adapter reports degraded when the
	// cortex_vectors tag is not set, preserving the exact zero-CGO default).
	if stores.Embeddings != nil && domain.IsVectorIndexHealthy(context.Background(), stores.Vectors) {
		stores.Outbox = sqlitestore.NewOutboxStore(manager.DB())
		stores.Worker = embedding.NewWorker(
			stores.Outbox,
			stores.Observations,
			stores.Embeddings,
			stores.Vectors,
			embedding.WorkerConfig{},
		)
		a.workerCancel = stores.Worker.Start(ctx)
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
	// Reload from the same file that was originally loaded
	configPath := ""
	if a.Config != nil && a.Config.LoadedFrom != "" {
		configPath = a.Config.LoadedFrom
	}
	cfg, err := config.Load(configPath)
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
	// Drain the embedding worker BEFORE closing the DB: in-flight intents must
	// finalize (or be left leased for crash recovery) with no goroutine touching
	// the DB after Close (REQ-EMB-001: no detached fire-and-forget goroutines).
	if a.workerCancel != nil {
		a.workerCancel()
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
