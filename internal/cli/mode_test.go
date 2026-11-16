package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
)

// modeTestEnv isolates HOME, CWD and CORTEX_MODE and returns the config file
// path `cortex mode set` writes to.
func modeTestEnv(t *testing.T) string {
	t.Helper()
	home := filepath.Dir(setCLIEnv(t))
	t.Chdir(home)
	t.Setenv("CORTEX_MODE", "")
	return filepath.Join(home, ".cortex", "cortex.yaml")
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(append([]string{"cortex"}, args...), stdout, stderr)
	return code, stdout.String(), stderr.String()
}

func TestModeSetRemoteThenShowAndBack(t *testing.T) {
	path := modeTestEnv(t)

	code, out, errOut := runCLI(t, "mode", "set", "remote", "--url", "https://cortex.example/mcp/", "--token-env", "MY_TOKEN")
	if code != 0 {
		t.Fatalf("mode set remote: code=%d stderr=%q", code, errOut)
	}
	if !strings.Contains(out, "Mode:        remote") || !strings.Contains(out, "MCP URL:     https://cortex.example/mcp") {
		t.Fatalf("set output = %q", out)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "remote" || !cfg.MCP.Remote.Enabled || cfg.MCP.Remote.URL != "https://cortex.example/mcp" || cfg.MCP.Remote.TokenEnv != "MY_TOKEN" || cfg.Sync.Enabled {
		t.Fatalf("saved config = mode %q remote %+v sync %v", cfg.Mode, cfg.MCP.Remote, cfg.Sync.Enabled)
	}

	code, out, _ = runCLI(t, "mode", "show", "--json")
	if code != 0 {
		t.Fatalf("mode show --json code=%d", code)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if report["mode"] != "remote" || report["source"] != "file" || report["config_file"] != path {
		t.Fatalf("report = %v", report)
	}

	// The env var overrides the file and is reported as the source.
	t.Setenv("CORTEX_MODE", "local")
	if _, out, _ = runCLI(t, "mode"); !strings.Contains(out, "Mode:        local") || !strings.Contains(out, "CORTEX_MODE") {
		t.Fatalf("env override output = %q", out)
	}
	t.Setenv("CORTEX_MODE", "")

	if code, _, errOut = runCLI(t, "mode", "set", "local"); code != 0 {
		t.Fatalf("mode set local: %q", errOut)
	}
	cfg, _ = config.Load(path)
	if cfg.Mode != "local" || cfg.MCP.Remote.Enabled || cfg.MCP.Remote.URL == "" {
		t.Fatalf("local switch should disable remote but keep its URL: %+v", cfg.MCP.Remote)
	}
}

func TestModeSetValidation(t *testing.T) {
	modeTestEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"mode", "set"}, "missing mode"},
		{[]string{"mode", "set", "proxy"}, "unknown mode"},
		{[]string{"mode", "set", "hybrid"}, "needs a server URL"},
		{[]string{"mode", "set", "local", "--url", "https://x"}, "only apply to hybrid and remote"},
		{[]string{"mode", "set", "remote", "--url", "http://cortex.example"}, "mcp.remote.url"},
		{[]string{"mode", "set", "remote", "--bogus"}, "unknown flag"},
		{[]string{"mode", "nope"}, "unknown mode subcommand"},
	} {
		code, _, errOut := runCLI(t, tc.args...)
		if code == 0 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: code=%d stderr=%q, want %q", tc.args, code, errOut, tc.want)
		}
	}
}

func TestModeTestProbesConfiguredServer(t *testing.T) {
	modeTestEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" && r.Header.Get("Authorization") != "Bearer ctx_mode_test" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("CORTEX_HTTP_TOKEN", "")
	t.Setenv("CORTEX_REMOTE_TOKEN", "ctx_mode_test")

	if code, _, errOut := runCLI(t, "mode", "set", "hybrid", "--url", srv.URL); code != 0 {
		t.Fatalf("mode set hybrid: %q", errOut)
	}
	code, out, errOut := runCLI(t, "mode", "test")
	if code != 0 || !strings.Contains(out, "Mode: hybrid") || !strings.Contains(out, "Bearer Authentication: verified") {
		t.Fatalf("mode test: code=%d out=%q err=%q", code, out, errOut)
	}

	if code, out, _ = runCLI(t, "mode", "set", "local"); code != 0 {
		t.Fatal(out)
	}
	if code, out, _ = runCLI(t, "mode", "test"); code != 0 || !strings.Contains(out, "nothing remote to test") {
		t.Fatalf("local mode test: code=%d out=%q", code, out)
	}
}

func TestServerModeConfigRefusesLocalDatabaseCommands(t *testing.T) {
	path := modeTestEnv(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCLI(t, "mode", "set", "server"); code != 0 {
		t.Fatalf("mode set server: %q", errOut)
	}
	code, _, errOut := runCLI(t, "search", "anything")
	if code == 0 || !strings.Contains(errOut, "resolved mode is server") {
		t.Fatalf("search under server mode: code=%d stderr=%q", code, errOut)
	}
	// Management commands keep working so the mode can be changed back.
	if code, out, _ := runCLI(t, "mode"); code != 0 || !strings.Contains(out, "Mode:        server") {
		t.Fatalf("mode show under server mode: %d %q", code, out)
	}
}

func TestMCPRemoteModeWithoutURLFailsClearly(t *testing.T) {
	modeTestEnv(t)
	t.Setenv("CORTEX_MODE", "remote")
	code, _, errOut := runCLI(t, "mcp")
	if code == 0 || !strings.Contains(errOut, "cortex mode set remote --url") {
		t.Fatalf("mcp remote without url: code=%d stderr=%q", code, errOut)
	}
}
