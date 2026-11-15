package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"log"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/database"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/lifecycle"
	"github.com/lleontor705/cortex/v2/internal/embedding"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/ollama"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	entitystore "github.com/lleontor705/cortex/v2/internal/store/entity"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/prompt"
	scoringstore "github.com/lleontor705/cortex/v2/internal/store/scoring"
	"github.com/lleontor705/cortex/v2/internal/store/search"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	cortsync "github.com/lleontor705/cortex/v2/internal/sync"
	"github.com/lleontor705/cortex/v2/internal/vector/sqlite_blob"
)

// Options controls how the application is opened.
type Options struct {
	InMemory          bool
	ConfigPath        string
	DisableRemoteSync bool
}

// App bundles the runtime dependencies needed by CLI and MCP entrypoints.
type App struct {
	Config         *config.Config
	DB             *database.Manager
	Migrator       *migration.Migrator
	Stores         *bundle.Stores
	archivalCancel context.CancelFunc
	workerCancel   context.CancelFunc // embedding worker drain (W4.1, REQ-EMB-001)

	// reloadMu serializes ReloadConfig so concurrent reloads cannot interleave
	// embedding reinitialization or sync-loop reconciliation.
	reloadMu sync.Mutex

	// syncMu guards the mutable sync-lifecycle fields below and the Config
	// pointer they fingerprint. Config is read under syncMu by SyncStatus so a
	// concurrent ReloadConfig cannot race the pointer swap. closed is set by
	// Close under syncMu, serializing shutdown against reconcileSyncLoop.
	syncMu        sync.Mutex
	syncCancel    context.CancelFunc
	syncDone      <-chan struct{}
	syncSignature string
	syncHybrid    bool
	syncDisabled  bool
	closed        bool
	baseCtx       context.Context
	syncStarter   syncLoopStarter
}

// syncLoopStarter starts a replication loop and returns its cancel function and
// completion channel, or (nil, nil) when no loop is warranted. It is a seam so
// the restart logic can be exercised deterministically without a live endpoint.
type syncLoopStarter func(ctx context.Context, cfg *config.Config, db *database.Manager, stores *bundle.Stores, isHybrid, disabled bool) (context.CancelFunc, <-chan struct{})

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

	codeStore, err := sqlitestore.NewCodeStore(manager.DB())
	if err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("app: initialize code store: %w", err)
	}

	transientStore, err := sqlitestore.NewTransientPayloadStore(manager.DB())
	if err != nil {
		_ = manager.Close()
		return nil, fmt.Errorf("app: initialize transient payload store: %w", err)
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
		Code:              codeStore,
		TransientPayloads: transientStore,
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
		Provider:   cfg.Search.EmbeddingProvider,
		APIKey:     config.ResolveEmbeddingAPIKey(cfg.Search.EmbeddingProvider),
		Model:      cfg.Search.EmbeddingModel,
		BaseURL:    cfg.Search.EmbeddingBaseURL,
		Dimensions: cfg.Search.EmbeddingDimensions,
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
		// Optional near-duplicate detection at ingest (default OFF; enable
		// with CORTEX_INGEST_DEDUP=true). Wired only when the graph store is
		// available so duplicates_of edges can be recorded.
		stores.Worker.EnableDedup(stores.Graph, embedding.DedupConfigFromEnv())
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

	a.syncHybrid = hybridRequested()
	a.syncDisabled = opts.DisableRemoteSync
	a.baseCtx = ctx
	a.syncStarter = startHybridReplication
	a.reconcileSyncLoop(ctx, cfg)

	return a, nil
}

// hybridRequested reports whether the hybrid composition was selected through
// the stable CORTEX_MODE surface. cmd/cortex sets CORTEX_MODE=hybrid when it
// resolves --mode hybrid, so this predicate covers both entry surfaces without
// creating an import cycle (internal/platform already imports internal/app).
func hybridRequested() bool {
	return strings.ToLower(strings.TrimSpace(os.Getenv("CORTEX_MODE"))) == "hybrid"
}

// hybridDegradedWarning is the named degradation notice emitted when the hybrid
// composition is requested but remote sync is unconfigured. Hybrid then
// continues as the plain local SQLite composition (D2 locked semantics).
const hybridDegradedWarning = "hybrid mode running without remote sync: sync.url not configured; continuing as local"

// resolveSyncToken applies the historical token precedence: the environment
// variable named by sync.token_env, then a literal token embedded in
// sync.token_env, then the local HTTP token.
func resolveSyncToken(cfg *config.Config) string {
	token := os.Getenv(cfg.Sync.TokenEnv)
	if token == "" && (strings.HasPrefix(cfg.Sync.TokenEnv, "ctx_") || strings.HasPrefix(cfg.Sync.TokenEnv, "ey")) {
		token = cfg.Sync.TokenEnv
	}
	if token == "" && cfg.HTTP.Token != "" {
		token = cfg.HTTP.Token
	}
	return token
}

// startHybridReplication wires the hybrid composition's remote sync/replication
// path: ModeHybrid is exactly the local SQLite composition plus this loop, with
// no separate store (decision D2). For backward compatibility an explicit
// sync.enabled in a local configuration starts the same loop, and a hybrid run
// with an unconfigured sync.url logs hybridDegradedWarning and continues as
// local. It returns the loop's cancel function and completion channel, or
// (nil, nil) when no replication loop is started.
func startHybridReplication(ctx context.Context, cfg *config.Config, db *database.Manager, stores *bundle.Stores, isHybrid, disabled bool) (context.CancelFunc, <-chan struct{}) {
	if !cfg.Sync.Enabled && !isHybrid {
		return nil, nil
	}

	configuredURL := strings.TrimSpace(cfg.Sync.URL) != ""
	if configuredURL {
		if token := resolveSyncToken(cfg); token != "" {
			remoteSearchClient, err := cortsync.NewRemoteSearchClient(cfg.Sync.URL, token, cfg.Sync.Timeout)
			if err != nil {
				log.Printf("warning: remote search disabled: %v", err)
			} else {
				stores.RemoteSearch = remoteSearchClient
			}
		}
	}

	if disabled {
		return nil, nil
	}
	if !configuredURL {
		if isHybrid {
			log.Printf("warning: %s", hybridDegradedWarning)
		}
		return nil, nil
	}

	remote, syncErr := cortsync.NewRemoteSyncer(db.DB(), cfg.Sync.URL, resolveSyncToken(cfg), cfg.Sync.Timeout)
	if syncErr != nil {
		log.Printf("warning: remote sync disabled: %v", syncErr)
		return nil, nil
	}

	syncCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		run := func() {
			if _, err := remote.Sync(syncCtx); err != nil && syncCtx.Err() == nil {
				log.Printf("warning: remote sync failed; local writes remain available: %v", err)
			}
		}
		run()
		interval := cfg.Sync.Interval
		if interval <= 0 {
			interval = time.Minute
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-syncCtx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
	return cancel, done
}

// syncSignature fingerprints every input that determines whether the running
// replication loop must be rebuilt. The loop captures its syncer (endpoint +
// token) at construction, so a change to any of these requires a fresh loop.
func syncSignature(cfg *config.Config, isHybrid, disabled bool) string {
	if cfg == nil {
		return ""
	}
	return strings.Join([]string{
		fmt.Sprintf("hybrid=%t", isHybrid),
		fmt.Sprintf("disabled=%t", disabled),
		fmt.Sprintf("enabled=%t", cfg.Sync.Enabled),
		cfg.Sync.URL,
		resolveSyncToken(cfg),
		cfg.Sync.Interval.String(),
		cfg.Sync.Timeout.String(),
	}, "\x1f")
}

// reconcileSyncLoop makes the live replication loop match cfg. It is
// idempotent: an unchanged signature leaves the running loop untouched, so
// exactly one loop runs at any time regardless of how often ReloadConfig fires.
// After Close it is a no-op: a reconcile that completes concurrently with
// shutdown must not resurrect a loop on a closed App.
func (a *App) reconcileSyncLoop(ctx context.Context, cfg *config.Config) {
	if a == nil || cfg == nil {
		return
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.closed {
		return
	}

	signature := syncSignature(cfg, a.syncHybrid, a.syncDisabled)
	if signature == a.syncSignature {
		return
	}
	a.stopSyncLocked()

	starter := a.syncStarter
	if starter == nil {
		starter = startHybridReplication
	}
	a.syncCancel, a.syncDone = starter(ctx, cfg, a.DB, a.Stores, a.syncHybrid, a.syncDisabled)
	a.syncSignature = signature
}

// stopSyncLocked cancels the running loop and waits for its goroutine to exit.
// The caller must hold syncMu.
func (a *App) stopSyncLocked() {
	if a.syncCancel != nil {
		a.syncCancel()
		if a.syncDone != nil {
			<-a.syncDone
		}
	}
	a.syncCancel, a.syncDone = nil, nil
}

// SyncStatus reports the live state of the hybrid replication loop so status
// surfaces can report honest progress instead of assuming success.
type SyncStatus struct {
	Configured bool // a sync endpoint is configured (hybrid or sync.enabled)
	Running    bool // a replication loop goroutine is currently active
	TokenSet   bool // a bearer token resolves through the precedence chain
}

// SyncStatus returns a race-safe snapshot of the live sync state.
func (a *App) SyncStatus() SyncStatus {
	if a == nil {
		return SyncStatus{}
	}
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	status := SyncStatusFor(a.Config, a.syncHybrid)
	if a.syncDisabled {
		status.Configured = false
	}
	status.Running = a.syncCancel != nil
	return status
}

// SyncStatusFor derives the sync state implied by a configuration alone, using
// the same token precedence as the runtime. It is used by status surfaces that
// have no live App (for example a config-only test or a pre-reload snapshot).
func SyncStatusFor(cfg *config.Config, hybridMode bool) SyncStatus {
	if cfg == nil {
		return SyncStatus{}
	}
	configured := (hybridMode || cfg.Sync.Enabled) && strings.TrimSpace(cfg.Sync.URL) != ""
	return SyncStatus{
		Configured: configured,
		Running:    false,
		TokenSet:   resolveSyncToken(cfg) != "",
	}
}

// SyncLoopRunning reports whether a replication loop is currently active.
func (a *App) SyncLoopRunning() bool {
	return a.SyncStatus().Running
}

// ReloadConfig re-reads the configuration from disk, reinitializes the
// embedding service, and reconciles the hybrid replication loop so a changed
// sync configuration takes effect without a process restart. Useful after
// manual edits to cortex.yaml or a TUI save.
func (a *App) ReloadConfig() error {
	a.reloadMu.Lock()
	defer a.reloadMu.Unlock()

	// Reload from the same file that was originally loaded
	configPath := ""
	a.syncMu.Lock()
	if a.Config != nil && a.Config.LoadedFrom != "" {
		configPath = a.Config.LoadedFrom
	}
	a.syncMu.Unlock()

	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("reload config: %w", err)
	}

	// Auto-start Ollama if configured
	if cfg.Search.EmbeddingProvider == "ollama" && cfg.Search.OllamaAutoStart {
		mgr := ollama.NewManager(cfg.Search.EmbeddingBaseURL)
		if err := mgr.EnsureRunning(context.Background()); err != nil {
			log.Printf("warning: could not auto-start ollama on reload: %v", err)
		}
	}

	// Reinitialize embedding service
	embeddings := embedding.New(embedding.Config{
		Provider:   cfg.Search.EmbeddingProvider,
		APIKey:     config.ResolveEmbeddingAPIKey(cfg.Search.EmbeddingProvider),
		Model:      cfg.Search.EmbeddingModel,
		BaseURL:    cfg.Search.EmbeddingBaseURL,
		Dimensions: cfg.Search.EmbeddingDimensions,
	})

	a.syncMu.Lock()
	a.Config = cfg
	if a.Stores != nil {
		a.Stores.Embeddings = embeddings
	}
	a.syncMu.Unlock()

	ctx := a.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	a.reconcileSyncLoop(ctx, cfg)

	return nil
}

// Close releases resources held by the application.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.syncMu.Lock()
	a.closed = true
	a.stopSyncLocked()
	a.syncMu.Unlock()
	// Drain the embedding worker BEFORE closing the DB: in-flight intents must
	// finalize (or be left leased for crash recovery) with no goroutine touching
	// the DB after Close (REQ-EMB-001: no detached fire-and-forget goroutines).
	if a.workerCancel != nil {
		a.workerCancel()
	}
	if a.archivalCancel != nil {
		a.archivalCancel()
	}
	// Close the embedding service's idle HTTP connections so the Transport's
	// persistConn goroutines are reaped (Task A: no HTTP keepalive goroutine
	// leak). The concrete embedding backends (*ollamaService, *openAIService)
	// implement io.Closer; test fakes and the nil case do not. Type-asserting
	// here avoids bloating the embedding.Service interface with Close().
	if a.Stores != nil && a.Stores.Embeddings != nil {
		if closer, ok := a.Stores.Embeddings.(io.Closer); ok {
			_ = closer.Close()
		}
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
