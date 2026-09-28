package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/database"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
)

// --- resolveSyncToken: all branches ---

func TestResolveSyncToken(t *testing.T) {
	tests := []struct {
		name      string
		tokenEnv  string // value for cfg.Sync.TokenEnv
		envKey    string // env var name to set (if different from tokenEnv)
		envValue  string // value for the env var
		httpToken string // cfg.HTTP.Token
		want      string
	}{
		{
			name:     "env_var_set",
			tokenEnv: "CORTEX_SYNC_TOKEN_TEST",
			envKey:   "CORTEX_SYNC_TOKEN_TEST",
			envValue: "from-env",
			want:     "from-env",
		},
		{
			name:     "literal_ctx_prefix",
			tokenEnv: "ctx_literal_token_123",
			want:     "ctx_literal_token_123",
		},
		{
			name:     "literal_ey_prefix",
			tokenEnv: "eyJhbGciOiJIUzI1NiJ9",
			want:     "eyJhbGciOiJIUzI1NiJ9",
		},
		{
			name:      "http_token_fallback",
			tokenEnv:  "NONEXISTENT_SYNC_KEY",
			httpToken: "http-fallback-token",
			want:      "http-fallback-token",
		},
		{
			name:     "all_empty",
			tokenEnv: "NONEXISTENT_SYNC_KEY",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envKey != "" && tt.envValue != "" {
				t.Setenv(tt.envKey, tt.envValue)
			}
			cfg := &config.Config{
				Sync: config.SyncConfig{TokenEnv: tt.tokenEnv},
				HTTP: config.HTTPConfig{Token: tt.httpToken},
			}
			got := resolveSyncToken(cfg)
			if got != tt.want {
				t.Errorf("resolveSyncToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- startHybridReplication: disabled, isHybrid, error, and loop branches ---

func TestStartHybridReplication_Disabled(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{Enabled: true, URL: "http://127.0.0.1:9999"},
	}
	cancel, done := startHybridReplication(context.Background(), cfg, nil, &bundle.Stores{}, false, true)
	if cancel != nil || done != nil {
		t.Fatal("expected nil cancel/done when disabled")
	}
}

func TestStartHybridReplication_IsHybridNoURL(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{Enabled: true},
	}
	cancel, done := startHybridReplication(context.Background(), cfg, nil, &bundle.Stores{}, true, false)
	if cancel != nil || done != nil {
		t.Fatal("expected nil cancel/done for hybrid without URL")
	}
}

func TestStartHybridReplication_SearchClientError(t *testing.T) {
	cfg := &config.Config{
		Sync: config.SyncConfig{
			Enabled: true,
			URL:     "http://example.com:19999",
			TokenEnv: "CORTEX_SYNC_TEST_TOK",
		},
		HTTP: config.HTTPConfig{Token: "test-token-value"},
	}
	t.Setenv("CORTEX_SYNC_TEST_TOK", "test-token-value")

	mgr, err := database.NewManager(database.InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	stores := &bundle.Stores{}
	cancel, done := startHybridReplication(context.Background(), cfg, mgr, stores, false, false)

	if cancel != nil {
		cancel()
		<-done
	}
	if stores.RemoteSearch != nil {
		t.Error("RemoteSearch should be nil when search client creation fails")
	}
}

func TestStartHybridReplication_SyncLoop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"cursor":0,"has_more":false}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Sync: config.SyncConfig{
			Enabled:  true,
			URL:      srv.URL,
			TokenEnv: "CORTEX_SYNC_LOOP_TOK",
			Interval: time.Minute,
		},
	}
	t.Setenv("CORTEX_SYNC_LOOP_TOK", "loop-test-token")

	mgr, err := database.NewManager(database.InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	stores := &bundle.Stores{}
	cancel, done := startHybridReplication(context.Background(), cfg, mgr, stores, false, false)
	if cancel == nil || done == nil {
		t.Fatal("expected non-nil cancel/done for valid loopback sync")
	}
	cancel()
	<-done
}

// --- ReloadConfig ---

func TestReloadConfig_HappyPath(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cortex.yaml")
	if err := os.WriteFile(cfgPath, []byte("database:\n  in_memory: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", filepath.Join(dir, "cortex.db"))
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")

	a, err := Open(context.Background(), Options{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if err := a.ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	if a.Config == nil {
		t.Fatal("Config is nil after ReloadConfig")
	}
}

func TestReloadConfig_EmptyPathReloadsDefaults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	a.Config.LoadedFrom = ""
	if err := a.ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig with empty LoadedFrom: %v", err)
	}
}

// --- Close: nil receiver and cancel paths ---

func TestClose_NilReceiver(t *testing.T) {
	var a *App
	if err := a.Close(); err != nil {
		t.Fatalf("Close on nil *App: %v", err)
	}
}

func TestClose_WithSyncCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"cursor":0,"has_more":false}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Sync: config.SyncConfig{
			Enabled:  true,
			URL:      srv.URL,
			TokenEnv: "CORTEX_CLOSE_SYNC_TOK",
			Interval: time.Minute,
		},
	}
	t.Setenv("CORTEX_CLOSE_SYNC_TOK", "close-test-token")

	mgr, err := database.NewManager(database.InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	a := &App{
		Config: cfg,
		DB:     mgr,
	}
	a.syncCancel, a.syncDone = startHybridReplication(context.Background(), cfg, mgr, &bundle.Stores{}, false, false)

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- Open: ollama auto-start and auto-archive branches ---

func TestOpen_OllamaAutoStart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "ollama")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "true")
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:11434")

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()
}

func TestOpen_AutoArchiveEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("CORTEX_LIFECYCLE_ENABLE_AUTO_ARCHIVE", "true")
	t.Setenv("CORTEX_LIFECYCLE_ARCHIVE_CHECK_INTERVAL", "invalid-duration")

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if a.archivalCancel == nil {
		t.Fatal("archivalCancel is nil when auto-archive is enabled")
	}
}

func TestOpen_AutoArchiveValidInterval(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cortex.db")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("CORTEX_LIFECYCLE_ENABLE_AUTO_ARCHIVE", "true")
	t.Setenv("CORTEX_LIFECYCLE_ARCHIVE_CHECK_INTERVAL", "2h")

	a, err := Open(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if a.archivalCancel == nil {
		t.Fatal("archivalCancel is nil when auto-archive is enabled")
	}
}

// --- startHybridReplication: sync error logging path in goroutine ---

func TestStartHybridReplication_SyncError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Sync: config.SyncConfig{
			Enabled:  true,
			URL:      srv.URL,
			TokenEnv: "CORTEX_SYNC_ERR_TOK",
			Interval: time.Minute,
		},
	}
	t.Setenv("CORTEX_SYNC_ERR_TOK", "err-token")

	mgr, err := database.NewManager(database.InMemoryConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer func() { _ = mgr.Close() }()

	stores := &bundle.Stores{}
	cancel, done := startHybridReplication(context.Background(), cfg, mgr, stores, false, false)
	if cancel == nil || done == nil {
		t.Fatal("expected non-nil cancel/done for sync error path")
	}
	time.Sleep(250 * time.Millisecond)
	cancel()
	<-done
}

// --- ReloadConfig: config.Load error return ---

func TestReloadConfig_LoadError(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cortex.yaml")
	if err := os.WriteFile(cfgPath, []byte("database:\n  in_memory: true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", filepath.Join(dir, "cortex.db"))
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")

	a, err := Open(context.Background(), Options{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if err := os.WriteFile(cfgPath, []byte("{{invalid yaml"), 0o600); err != nil {
		t.Fatalf("corrupt config: %v", err)
	}

	if err := a.ReloadConfig(); err == nil {
		t.Fatal("expected error from ReloadConfig with corrupted config")
	}
}

// --- ReloadConfig: ollama auto-start branch ---

func TestReloadConfig_OllamaAutoStart(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cortex.yaml")
	cfgContent := "database:\n  in_memory: true\nsearch:\n  embedding_provider: ollama\n  ollama_auto_start: true\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_DATABASE_PATH", filepath.Join(dir, "cortex.db"))
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "ollama")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "true")
	t.Setenv("OLLAMA_BASE_URL", "http://127.0.0.1:11434")

	a, err := Open(context.Background(), Options{ConfigPath: cfgPath})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if err := a.ReloadConfig(); err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
}

// --- Close: workerCancel drain path ---

func TestClose_WithWorkerCancel(t *testing.T) {
	called := false
	a := &App{
		Config:       &config.Config{},
		workerCancel: func() { called = true },
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !called {
		t.Fatal("workerCancel was not called during Close")
	}
}
