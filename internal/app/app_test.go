package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/database"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
)

func TestOpenInMemoryProvidesWorkingStores(t *testing.T) {
	ctx := context.Background()

	a, err := Open(ctx, Options{InMemory: true})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = a.Close() }()

	session := &domain.Session{
		ID:        "session-1",
		Project:   "demo",
		Directory: ".",
	}
	if err := a.Stores.Sessions.Create(ctx, session); err != nil {
		t.Fatalf("Sessions.Create() error = %v", err)
	}

	obs := &domain.Observation{
		SessionID: "session-1",
		Title:     "JWT auth middleware",
		Content:   "Switched auth flow to JWT",
		Type:      domain.TypeDecision,
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}
	if err := a.Stores.Observations.Save(ctx, obs); err != nil {
		t.Fatalf("Observations.Save() error = %v", err)
	}

	results, err := a.Stores.Search.Search(ctx, "JWT auth", domain.SearchOptions{Project: "demo", Limit: 5})
	if err != nil {
		t.Fatalf("Search.Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("Search.Search() returned no results")
	}
}

// countingSyncLoop is a deterministic replication-loop starter: it records
// start/stop events so restart logic is asserted without a live endpoint.
type countingSyncLoop struct {
	mu      sync.Mutex
	starts  int
	stops   int
	running int
	peak    int
}

func (f *countingSyncLoop) start(_ context.Context, cfg *config.Config, _ *database.Manager, _ *bundle.Stores, isHybrid, disabled bool) (context.CancelFunc, <-chan struct{}) {
	if disabled || strings.TrimSpace(cfg.Sync.URL) == "" || (!cfg.Sync.Enabled && !isHybrid) {
		return nil, nil
	}
	done := make(chan struct{})
	f.mu.Lock()
	f.starts++
	f.running++
	if f.running > f.peak {
		f.peak = f.running
	}
	f.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			f.running--
			f.stops++
			f.mu.Unlock()
			close(done)
		})
	}, done
}

func (f *countingSyncLoop) snapshot() (starts, stops, running, peak int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops, f.running, f.peak
}

func TestSyncSignatureTracksWireInputs(t *testing.T) {
	base := &config.Config{Sync: config.SyncConfig{Enabled: true, URL: "https://a.example/mcp"}}
	first := syncSignature(base, false, false)
	second := syncSignature(base, false, false)
	if first != second {
		t.Fatal("signature is not stable for identical config")
	}
	changed := &config.Config{Sync: config.SyncConfig{Enabled: true, URL: "https://b.example/mcp"}}
	if syncSignature(base, false, false) == syncSignature(changed, false, false) {
		t.Fatal("signature ignores a sync.url change")
	}
	if syncSignature(base, false, false) == syncSignature(base, true, false) {
		t.Fatal("signature ignores the hybrid flag")
	}
}

func TestReconcileSyncLoop_ExactlyOneLoop(t *testing.T) {
	fake := &countingSyncLoop{}
	app := &App{syncStarter: fake.start, baseCtx: context.Background()}
	cfg := &config.Config{Sync: config.SyncConfig{Enabled: true, URL: "https://sync.example/mcp"}}
	app.Config = cfg

	app.reconcileSyncLoop(context.Background(), cfg)
	app.reconcileSyncLoop(context.Background(), cfg) // identical config -> no restart
	if starts, _, running, peak := fake.snapshot(); starts != 1 || running != 1 || peak != 1 {
		t.Fatalf("idempotent reconcile: starts=%d running=%d peak=%d", starts, running, peak)
	}

	changed := &config.Config{Sync: config.SyncConfig{Enabled: true, URL: "https://sync2.example/mcp"}}
	app.Config = changed
	app.reconcileSyncLoop(context.Background(), changed)
	starts, stops, running, peak := fake.snapshot()
	if starts != 2 || stops != 1 || running != 1 || peak != 1 {
		t.Fatalf("url change: starts=%d stops=%d running=%d peak=%d", starts, stops, running, peak)
	}
	if !app.SyncLoopRunning() {
		t.Fatal("expected exactly one live loop after restart")
	}
}

func TestReconcileSyncLoop_StopsWhenDisabled(t *testing.T) {
	fake := &countingSyncLoop{}
	app := &App{syncStarter: fake.start, baseCtx: context.Background()}
	enabled := &config.Config{Sync: config.SyncConfig{Enabled: true, URL: "https://sync.example/mcp"}}
	app.Config = enabled
	app.reconcileSyncLoop(context.Background(), enabled)
	if !app.SyncLoopRunning() {
		t.Fatal("expected loop running while sync enabled")
	}

	disabled := &config.Config{}
	app.Config = disabled
	app.reconcileSyncLoop(context.Background(), disabled)
	if app.SyncLoopRunning() {
		t.Fatal("loop still running after sync became disabled")
	}
	if _, stops, running, _ := fake.snapshot(); stops != 1 || running != 0 {
		t.Fatalf("expected 1 stop and 0 running, got stops=%d running=%d", stops, running)
	}
}

func writeSyncConfig(t *testing.T, path, url string) {
	t.Helper()
	body := "database:\n  in_memory: true\nsync:\n  enabled: true\n  url: " + url + "\n  token_env: CORTEX_TEST_SYNC_TOK\n  interval: 45s\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func TestReloadConfig_ReconcilesSyncLoop(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cortex.yaml")
	writeSyncConfig(t, cfgPath, "https://sync-a.example/mcp")
	t.Setenv("HOME", dir)
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("CORTEX_TEST_SYNC_TOK", "tok-a")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fake := &countingSyncLoop{}
	app := &App{Config: cfg, Stores: &bundle.Stores{}, syncStarter: fake.start, baseCtx: context.Background()}

	if err := app.ReloadConfig(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if starts, _, running, _ := fake.snapshot(); starts != 1 || running != 1 {
		t.Fatalf("after first reload: starts=%d running=%d", starts, running)
	}

	writeSyncConfig(t, cfgPath, "https://sync-b.example/mcp")
	if err := app.ReloadConfig(); err != nil {
		t.Fatalf("reload 2: %v", err)
	}
	starts, stops, running, peak := fake.snapshot()
	if starts != 2 || stops != 1 || running != 1 || peak != 1 {
		t.Fatalf("after url change reload: starts=%d stops=%d running=%d peak=%d", starts, stops, running, peak)
	}
}

func TestReloadConfig_ConcurrentReloadsKeepSingleLoop(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cortex.yaml")
	writeSyncConfig(t, cfgPath, "https://sync-race.example/mcp")
	t.Setenv("HOME", dir)
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("CORTEX_TEST_SYNC_TOK", "tok-race")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	fake := &countingSyncLoop{}
	app := &App{Config: cfg, Stores: &bundle.Stores{}, syncStarter: fake.start, baseCtx: context.Background()}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = app.ReloadConfig()
			_ = app.SyncStatus()
		}()
	}
	wg.Wait()

	if starts, _, running, peak := fake.snapshot(); starts != 1 || running != 1 || peak != 1 {
		t.Fatalf("concurrent reloads: starts=%d running=%d peak=%d", starts, running, peak)
	}
}

func TestSyncStatusReportsHonestState(t *testing.T) {
	t.Setenv("CORTEX_TEST_SYNC_STATUS_TOK", "tok")
	cfg := &config.Config{Sync: config.SyncConfig{Enabled: true, URL: "https://sync.example/mcp", TokenEnv: "CORTEX_TEST_SYNC_STATUS_TOK"}}
	app := &App{Config: cfg}

	status := app.SyncStatus()
	if !status.Configured || !status.TokenSet || status.Running {
		t.Fatalf("unexpected status without a loop: %+v", status)
	}

	t.Setenv("CORTEX_TEST_SYNC_STATUS_TOK", "")
	cfg.HTTP.Token = ""
	if app.SyncStatus().TokenSet {
		t.Fatal("TokenSet should be false when no token resolves")
	}

	derived := SyncStatusFor(cfg, false)
	if !derived.Configured || derived.Running {
		t.Fatalf("config-derived status wrong: %+v", derived)
	}
}
