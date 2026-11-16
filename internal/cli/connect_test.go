package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/credential"
)

const connectTestToken = "ctx_connect_test_token"

func fakeCortexServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/api/me":
			if r.Header.Get("Authorization") != "Bearer "+connectTestToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"alice","roles":["admin"]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// connectTestEnv isolates HOME/CWD, swaps the keychain for memory and feeds
// stdin with the given token (non-terminal path).
func connectTestEnv(t *testing.T, stdinToken string) string {
	t.Helper()
	path := modeTestEnv(t)
	credential.UseInMemoryStore()
	prevStdin, prevTerm := connectStdin, connectIsTerminal
	connectStdin = strings.NewReader(stdinToken + "\n")
	connectIsTerminal = func() bool { return false }
	t.Cleanup(func() { connectStdin, connectIsTerminal = prevStdin, prevTerm })
	return path
}

func TestConnectStoresTokenInKeychainAndSwitchesMode(t *testing.T) {
	srv := fakeCortexServer(t)
	path := connectTestEnv(t, connectTestToken)

	code, out, errOut := runCLI(t, "connect", srv.URL+"/mcp", "--as", "hybrid")
	if code != 0 {
		t.Fatalf("connect: code=%d out=%q err=%q", code, out, errOut)
	}
	if !strings.Contains(out, "token accepted as alice (admin)") || !strings.Contains(out, "OS keychain") {
		t.Fatalf("connect output = %q", out)
	}
	if stored, err := credential.Get(srv.URL); err != nil || stored != connectTestToken {
		t.Fatalf("keychain = %q, %v", stored, err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), connectTestToken) {
		t.Fatalf("token written to config:\n%s", raw)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "hybrid" || cfg.Version != 2 || cfg.Remote.URL != srv.URL || cfg.Remote.Token != "keychain" || !cfg.Sync.Enabled {
		t.Fatalf("saved config: mode=%q version=%d remote=%+v sync=%v", cfg.Mode, cfg.Version, cfg.Remote, cfg.Sync.Enabled)
	}
	if token, source, err := config.ResolveRemoteBearer(cfg, cfg.Sync.TokenEnv); err != nil || token != connectTestToken || source != "OS keychain" {
		t.Fatalf("bearer = %q %q %v", token, source, err)
	}

	// `cortex mode test` now authenticates through the keychain.
	t.Setenv("CORTEX_HTTP_TOKEN", "")
	t.Setenv("CORTEX_REMOTE_TOKEN", "")
	if code, out, _ := runCLI(t, "mode", "test"); code != 0 || !strings.Contains(out, "Bearer Authentication: verified") {
		t.Fatalf("mode test after connect: code=%d out=%q", code, out)
	}

	code, out, _ = runCLI(t, "disconnect")
	if code != 0 || !strings.Contains(out, "mode set to local") {
		t.Fatalf("disconnect: code=%d out=%q", code, out)
	}
	if _, err := credential.Get(srv.URL); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("token still in keychain: %v", err)
	}
	cfg, _ = config.Load(path)
	if cfg.Mode != "local" || cfg.Remote.Token != "" || cfg.Sync.Enabled {
		t.Fatalf("after disconnect: mode=%q remote=%+v sync=%v", cfg.Mode, cfg.Remote, cfg.Sync.Enabled)
	}
}

func TestConnectRejectsBadTokenAndSavesNothing(t *testing.T) {
	srv := fakeCortexServer(t)
	path := connectTestEnv(t, "ctx_wrong")
	code, _, errOut := runCLI(t, "connect", srv.URL)
	if code == 0 || !strings.Contains(errOut, "rejected the token") || !strings.Contains(errOut, "Nothing was saved") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	if _, err := credential.Get(srv.URL); !errors.Is(err, credential.ErrNotFound) {
		t.Fatal("rejected token stored in keychain")
	}
	if _, err := os.Stat(path); err == nil {
		raw, _ := os.ReadFile(path)
		if strings.Contains(string(raw), "remote:") {
			t.Fatalf("config written after a failed connect:\n%s", raw)
		}
	}
}

func TestConnectWithTokenEnvReferencesVariable(t *testing.T) {
	srv := fakeCortexServer(t)
	path := connectTestEnv(t, "")
	t.Setenv("TEAM_CORTEX_TOKEN", connectTestToken)
	if code, out, errOut := runCLI(t, "connect", srv.URL, "--token-env", "TEAM_CORTEX_TOKEN"); code != 0 {
		t.Fatalf("connect --token-env: %q %q", out, errOut)
	}
	cfg, _ := config.Load(path)
	if cfg.Remote.Token != "env:TEAM_CORTEX_TOKEN" || cfg.Mode != "remote" || !cfg.MCP.Remote.Enabled {
		t.Fatalf("config = mode %q remote %+v", cfg.Mode, cfg.Remote)
	}
	if _, err := credential.Get(srv.URL); !errors.Is(err, credential.ErrNotFound) {
		t.Fatal("--token-env must not write the keychain")
	}
}

func TestConnectArgumentValidation(t *testing.T) {
	connectTestEnv(t, "x")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"connect"}, "missing server URL"},
		{[]string{"connect", "https://x.example", "--token", "ctx_leak"}, "not accepted"},
		{[]string{"connect", "https://x.example", "--as", "server"}, "--as must be remote or hybrid"},
		{[]string{"connect", "http://cortex.example"}, "refusing"},
		{[]string{"connect", "https://x.example", "--token-env", "A", "--token-stdin"}, "not both"},
	} {
		if code, _, errOut := runCLI(t, tc.args...); code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: code=%d err=%q, want %q", tc.args, code, errOut, tc.want)
		}
	}
}

func TestConfigMigrateMovesLiteralTokenToKeychain(t *testing.T) {
	path := modeTestEnv(t)
	credential.UseInMemoryStore()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `# legacy v1 config
mcp:
    remote:
        enabled: true
        url: https://cortex.example/mcp
        token_env: ctx_plaintext_secret
custom: keep
`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCLI(t, "config", "migrate", "--dry-run")
	if code != 0 || !strings.Contains(out, "Dry run") || !strings.Contains(out, "OS keychain") {
		t.Fatalf("dry run: code=%d out=%q err=%q", code, out, errOut)
	}
	if raw, _ := os.ReadFile(path); string(raw) != legacy {
		t.Fatal("dry run changed the file")
	}

	if code, out, errOut = runCLI(t, "config", "migrate"); code != 0 {
		t.Fatalf("migrate: code=%d out=%q err=%q", code, out, errOut)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "ctx_plaintext_secret") {
		t.Fatalf("plaintext token survived migration:\n%s", raw)
	}
	for _, want := range []string{"custom: keep", "# legacy v1 config", "version: 2", "mode: remote"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("migrated file missing %q:\n%s", want, raw)
		}
	}
	if stored, err := credential.Get("https://cortex.example"); err != nil || stored != "ctx_plaintext_secret" {
		t.Fatalf("keychain = %q, %v", stored, err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.URL != "https://cortex.example" || cfg.Remote.Token != "keychain" {
		t.Fatalf("remote = %+v", cfg.Remote)
	}
	if code, out, _ = runCLI(t, "config", "migrate"); code != 0 || !strings.Contains(out, "nothing to migrate") {
		t.Fatalf("second migrate: %d %q", code, out)
	}
}
