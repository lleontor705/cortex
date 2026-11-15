package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/app"
	"github.com/lleontor705/cortex/v2/internal/config"
)

func writeConfigWithToken(t *testing.T, token string) *config.Config {
	t.Helper()
	// A leftover CORTEX_HTTP_TOKEN from another test would shadow the file
	// token at load time; neutralize it so the file is the only source.
	t.Setenv("CORTEX_HTTP_TOKEN", "")
	path := filepath.Join(t.TempDir(), "cortex.yaml")
	body := "# keep me\nhttp:\n  token: " + token + "\ndatabase:\n  path: test.db\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// A save that does not touch the credential must never erase a pre-existing
// http.token: clearAbsentSecrets only clears secrets the desired config stops
// supplying, so the form copy has to carry the loaded token forward.
func TestSaveLocalConfigPreservesExistingHTTPToken(t *testing.T) {
	cfg := writeConfigWithToken(t, "preserved-secret")
	if cfg.HTTP.Token != "preserved-secret" {
		t.Fatalf("precondition: loaded token = %q", cfg.HTTP.Token)
	}

	values := localConfigValues{databasePath: "test.db", httpEnabled: true, httpHost: "127.0.0.1", httpPort: "7438", syncInterval: "30s"}
	if msg := saveLocalConfig(&Deps{Config: cfg}, values)().(localConfigSavedMsg); msg.err != nil {
		t.Fatalf("saveLocalConfig: %v", msg.err)
	}

	reloaded, err := config.Load(cfg.LoadedFrom)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.HTTP.Token != "preserved-secret" {
		t.Fatalf("unrelated save erased http.token: %q", reloaded.HTTP.Token)
	}
}

func TestSyncConfigToastHonestStates(t *testing.T) {
	cases := []struct {
		name     string
		status   app.SyncStatus
		hybrid   bool
		contains string
		typ      string
	}{
		{"running", app.SyncStatus{Configured: true, Running: true, TokenSet: true}, true, "running", "success"},
		{"restart-required", app.SyncStatus{Configured: true, TokenSet: true}, true, "restart", "success"},
		{"token-unresolved", app.SyncStatus{Configured: true, TokenSet: false}, true, "token", "warning"},
		{"local", app.SyncStatus{}, true, "Local", "success"},
		{"remote-proxy", app.SyncStatus{Configured: true, Running: true}, false, "Remote MCP Proxy", "success"},
	}
	for _, tc := range cases {
		msg, typ := syncConfigToast(tc.status, tc.hybrid)
		if typ != tc.typ || !strings.Contains(msg, tc.contains) {
			t.Errorf("%s: got (%q, %q), want type %q containing %q", tc.name, msg, typ, tc.typ, tc.contains)
		}
	}
}
