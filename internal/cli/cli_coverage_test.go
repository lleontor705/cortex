package cli

// cli_coverage_test.go is the additive suite that lifts internal/cli statement
// coverage past the 75% gate. It targets the branches left uncovered by
// cli_test.go, cli_extra_test.go, and cli_gap_extra_test.go: the config
// get/set/show/validate/path/init flag matrix, the auth surface, the
// `doctor --server` HTTP probe, the Obsidian export path, the offline sync
// modes, web-key argument validation, and the code subcommands against a
// populated AST graph.
//
// Isolation contract (same as the sibling suites): setCLIEnv gives each test a
// unique SQLite database and an isolated HOME; embeddings are neutralized so
// app.Open never touches the network; the only HTTP traffic goes to httptest
// servers created inside the test; no t.Parallel because environment variables
// are mutated.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCLIConfigFile seeds $HOME/.cortex/cortex.yaml so config.Load("") resolves
// LoadedFrom to a real on-disk file, which the config/auth write paths require.
func writeCLIConfigFile(t *testing.T, body string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".cortex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	path := filepath.Join(dir, "cortex.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

type cliCase struct {
	name     string
	args     []string
	wantCode int
	inOut    string
	inErr    string
}

func runCases(t *testing.T, cases []cliCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errB := run(t, append([]string{"cortex"}, tc.args...)...)
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d (stdout = %q, stderr = %q)", code, tc.wantCode, out, errB)
			}
			if tc.inOut != "" && !strings.Contains(out, tc.inOut) {
				t.Fatalf("stdout = %q, missing %q", out, tc.inOut)
			}
			if tc.inErr != "" && !strings.Contains(errB, tc.inErr) {
				t.Fatalf("stderr = %q, missing %q", errB, tc.inErr)
			}
		})
	}
}

func TestCoverageConfigSurface(t *testing.T) {
	setCLIEnv(t)
	cfgPath := writeCLIConfigFile(t, "http:\n  port: 7438\n")
	badPath := filepath.Join(t.TempDir(), "broken.yaml")
	if err := os.WriteFile(badPath, []byte("{ this is not: [valid yaml"), 0o600); err != nil {
		t.Fatalf("seed malformed config: %v", err)
	}
	initTarget := filepath.Join(t.TempDir(), "cortex.yaml")

	runCases(t, []cliCase{
		{"path reports loaded source", []string{"config", "path"}, 0, cfgPath, ""},
		{"validate file equals", []string{"config", "validate", "--file=" + cfgPath}, 0, "Configuration valid", ""},
		{"validate config equals", []string{"config", "validate", "--config=" + cfgPath}, 0, "Configuration valid", ""},
		{"validate short flag", []string{"config", "validate", "-f", cfgPath}, 0, "Configuration valid", ""},
		{"validate malformed", []string{"config", "validate", "--file", badPath}, 1, "", "Configuration invalid"},
		{"show json via config equals", []string{"config", "show", "--format", "json", "--config=" + cfgPath}, 0, "{", ""},
		{"show malformed", []string{"config", "show", "-f", badPath}, 1, "", "error loading config"},
		{"get without key", []string{"config", "get"}, 1, "", "config get <key>"},
		{"get unknown key", []string{"config", "get", "no.such.key", "--file", cfgPath}, 1, "", "unknown configuration key"},
		{"get via config equals", []string{"config", "get", "http.port", "--config=" + cfgPath}, 0, "7438", ""},
		{"set without value", []string{"config", "set", "http.port"}, 1, "", "config set <key> <value>"},
		{"set invalid value", []string{"config", "set", "http.port", "not-a-port", "-f", cfgPath}, 1, "", "invalid port"},
		{"set writes loaded file", []string{"config", "set", "logging.level", "debug", "--config=" + cfgPath}, 0, "Successfully set logging.level = debug", ""},
		{"set relocates by format", []string{"config", "set", "http.port", "8181", "--format", "toml", "--file=" + cfgPath}, 0, filepath.Join(filepath.Dir(cfgPath), "cortex.toml"), ""},
		{"init creates yaml", []string{"config", "init", "--output=" + initTarget}, 0, "Initialized minimal YAML configuration", ""},
		{"init refuses to clobber", []string{"config", "init", "--file=" + initTarget}, 1, "", "already exists"},
		{"init force overwrites", []string{"config", "init", "--force", "--format", "json", "-o", initTarget}, 0, "Initialized minimal JSON configuration", ""},
		{"unknown subcommand", []string{"config", "bogus"}, 1, "", "unknown config subcommand"},
	})

	t.Run("set without a loaded file writes the default location", func(t *testing.T) {
		setCLIEnv(t)
		want := filepath.Join(os.Getenv("HOME"), ".cortex", "cortex.json")
		code, out, errB := run(t, "cortex", "config", "set", "http.port", "8182", "--format=json")
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errB)
		}
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("set did not persist %q: %v", want, err)
		}
	})
}

func TestCoverageAuthSurface(t *testing.T) {
	setCLIEnv(t)
	// viper binds CORTEX_HTTP_TOKEN to http.token; clear any ambient token so the
	// authentication state is driven solely by the config file under test.
	t.Setenv("CORTEX_HTTP_TOKEN", "")
	writeCLIConfigFile(t, "sync:\n  enabled: true\n  url: https://sync.example.test\n")

	runCases(t, []cliCase{
		{"missing subcommand", []string{"auth"}, 1, "", "cortex auth <login|status|logout>"},
		{"login without token", []string{"auth", "login"}, 1, "", "token required"},
		{"status anonymous", []string{"auth", "status"}, 0, "Anonymous / Unauthenticated", ""},
		{"logout reports success", []string{"auth", "logout"}, 0, "Successfully logged out", ""},
		{"unknown subcommand", []string{"auth", "bogus"}, 1, "", "unknown auth subcommand"},
	})

	t.Run("login persists the token and status reports hybrid mode", func(t *testing.T) {
		if code, _, errB := run(t, "cortex", "auth", "login", "--token", "ctx_coverage_token_value"); code != 0 {
			t.Fatalf("login code = %d, stderr = %q", code, errB)
		}
		code, out, errB := run(t, "cortex", "auth", "status")
		if code != 0 {
			t.Fatalf("status code = %d, stderr = %q", code, errB)
		}
		for _, want := range []string{"Hybrid", "Authenticated", "Role:      admin"} {
			if !strings.Contains(out, want) {
				t.Fatalf("status stdout = %q, missing %q", out, want)
			}
		}
	})
}

func TestCoverageDoctorServerHealth(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(status)
		case "/mcp":
			if r.Header.Get("Authorization") == "Bearer ctx_doctor_token" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	t.Run("healthy server without a bearer token", func(t *testing.T) {
		status = http.StatusOK
		t.Setenv("CORTEX_HTTP_TOKEN", "")
		t.Setenv("CORTEX_REMOTE_TOKEN", "")
		code, out, errB := run(t, "cortex", "doctor", "--server", srv.URL)
		if code != 0 {
			t.Fatalf("code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "[OK]   Server Health") || !strings.Contains(out, "Bearer Token: not configured") {
			t.Fatalf("doctor stdout = %q", out)
		}
	})

	t.Run("valid bearer verifies", func(t *testing.T) {
		t.Setenv("CORTEX_REMOTE_TOKEN", "")
		t.Setenv("CORTEX_HTTP_TOKEN", "ctx_doctor_token")
		code, out, errB := run(t, "cortex", "doctor", "--server", srv.URL)
		if code != 0 || !strings.Contains(out, "Bearer Authentication: verified") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errB)
		}
	})

	t.Run("rejected bearer token reports an issue", func(t *testing.T) {
		t.Setenv("CORTEX_REMOTE_TOKEN", "")
		t.Setenv("CORTEX_HTTP_TOKEN", "ctx_wrong_token")
		code, out, errB := run(t, "cortex", "doctor", "--server", srv.URL)
		if code != 1 || !strings.Contains(out, "invalid or rejected token") || !strings.Contains(out, "issue(s) found") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errB)
		}
	})

	t.Run("unhealthy health endpoint reports an issue", func(t *testing.T) {
		status = http.StatusInternalServerError
		t.Setenv("CORTEX_HTTP_TOKEN", "")
		t.Setenv("CORTEX_REMOTE_TOKEN", "")
		code, out, errB := run(t, "cortex", "doctor", "--server", srv.URL)
		if code != 1 || !strings.Contains(out, "returned status 500") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errB)
		}
	})

	t.Run("unreachable server reports connectivity failure", func(t *testing.T) {
		closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		closed.Close()
		code, out, errB := run(t, "cortex", "doctor", "--server", closed.URL)
		if code != 1 || !strings.Contains(out, "[FAIL] Server Connectivity") {
			t.Fatalf("code = %d, stdout = %q, stderr = %q", code, out, errB)
		}
	})
}

func TestCoverageObsidianExportBranches(t *testing.T) {
	setCLIEnv(t)
	vault := t.TempDir()

	runCases(t, []cliCase{
		{"vault is required", []string{"export", "--to-obsidian"}, 1, "", "--vault PATH"},
		{"exports public notes", []string{"save", "Public note", "visible body", "--project", "demo"}, 0, "Memory saved:", ""},
		{"excludes personal scope", []string{"save", "Private note", "hidden body", "--project", "demo", "--scope", "personal"}, 0, "Memory saved:", ""},
		{"export warns on personal", []string{"export", "--to-obsidian", "--vault", vault, "--project", "demo"}, 0, "observations to Obsidian", "excluded personal observation"},
		{"include-personal opts in", []string{"export", "--to-obsidian", "--vault", vault, "--project", "demo", "--include-personal"}, 0, "", "including personal/private observations"},
	})
}

func TestCoverageSyncOfflineModes(t *testing.T) {
	setCLIEnv(t)
	runCases(t, []cliCase{
		{"status inspects the file transport", []string{"sync", "--status"}, 0, "Local chunks:", ""},
		{"import on an empty transport", []string{"sync", "--import"}, 0, "Imported 0 chunks", ""},
		{"remote without a url fails", []string{"sync", "--remote"}, 1, "", "sync.url is not configured"},
	})
}

func TestCoverageWebKeyArgumentValidation(t *testing.T) {
	setCLIEnv(t)
	runCases(t, []cliCase{
		{"key-file without a path", []string{"web", "key", "show", "--key-file"}, 1, "", "--key-file requires a path"},
		{"unexpected positional argument", []string{"web", "key", "show", "--bogus"}, 1, "", "unexpected argument"},
	})
}

func TestCoverageSetupAndIngestGuardBranches(t *testing.T) {
	setCLIEnv(t)
	runCases(t, []cliCase{
		{"setup without an agent prints the wizard", []string{"setup"}, 0, "Supported agents: opencode", ""},
		{"setup rejects an unknown agent", []string{"setup", "no-such-agent", "--profile=dev"}, 1, "", "cortex:"},
		{"ingest fails for a missing directory", []string{"ingest", filepath.Join(t.TempDir(), "absent"), "--project", "cov"}, 1, "", "ast extraction failed"},
	})
}

func TestCoverageCodeSubcommandsWithGraph(t *testing.T) {
	setCLIEnv(t)
	codeDir := t.TempDir()
	files := map[string]string{
		"calc.go": `package fixture

func CalculateTax(amount float64) float64 {
	return amount * 0.18
}

func ProcessOrder(amount float64) float64 {
	return CalculateTax(amount)
}
`,
		"calc_test.go": `package fixture

import "testing"

func TestCalculateTax(t *testing.T) {
	if CalculateTax(100) != 18 {
		t.Fatal("unexpected tax")
	}
}
`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(codeDir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	if code, _, errB := run(t, "cortex", "ingest", codeDir, "--project", "cov"); code != 0 {
		t.Fatalf("ingest code = %d, stderr = %q", code, errB)
	}

	runCases(t, []cliCase{
		{"help", []string{"code", "help"}, 0, "cortex code <command>", ""},
		{"tests json", []string{"code", "tests", "CalculateTax", "--project=cov", "--json"}, 0, `"target"`, ""},
		{"tests hops flag", []string{"code", "tests", "CalculateTax", "--project=cov", "--hops=4"}, 0, "Impacted Tests for", ""},
		{"tests without target", []string{"code", "tests", "--project=cov"}, 1, "", "target symbol or file path is required"},
		{"find flags", []string{"code", "find", "Calculate", "--project=cov", "--regex", "--kind=function", "--file=calc.go", "--limit=5"}, 0, "matching", ""},
		{"find json", []string{"code", "find", "Calculate", "--project=cov", "--json"}, 0, "[", ""},
		{"find without query", []string{"code", "find", "--project=cov"}, 1, "", "search query is required"},
		{"map budget", []string{"code", "map", "--project=cov", "--budget=256"}, 0, "", ""},
		{"symbols flags", []string{"code", "symbols", "--project=cov", "--kind=function", "--file=calc.go"}, 0, "Indexed Symbols", ""},
		{"analyze", []string{"code", "analyze", "--project=cov"}, 0, "Architectural Analysis", ""},
		{"impact hops", []string{"code", "impact", "CalculateTax", "--project=cov", "--hops=2"}, 0, "Code Blast Radius", ""},
		{"graph ascii flags", []string{"code", "graph", "--project=cov", "--format=ascii", "--symbol=CalculateTax", "--hops=1", "--max-nodes=5"}, 0, "", ""},
		{"graph unknown format", []string{"code", "graph", "--project=cov", "--format=bogus"}, 1, "", "unknown format"},
		{"diff staged", []string{"code", "diff", "--staged", "--project=cov"}, 0, "", ""},
		{"unknown subcommand", []string{"code", "bogus"}, 1, "", "unknown code subcommand"},
	})
}
