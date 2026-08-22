package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/app"
	"github.com/lleontor705/cortex/internal/domain"
)

func TestRunSaveThenSearch(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"cortex", "save", "JWT auth", "Switched auth to JWT", "--type", "decision", "--project", "demo"}, stdout, stderr); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Memory saved:") {
		t.Fatalf("save stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()

	if code := Run([]string{"cortex", "search", "JWT", "--project", "demo"}, stdout, stderr); code != 0 {
		t.Fatalf("search code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "JWT auth") {
		t.Fatalf("search stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "explain:") {
		t.Fatalf("search stdout missing explainability = %q", stdout.String())
	}
}

func TestRunRevisions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"cortex", "save", "Original Title", "Original content", "--project", "demo"}, stdout, stderr); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, stderr.String())
	}

	a, err := app.Open(t.Context(), app.Options{})
	if err != nil {
		t.Fatalf("open app: %v", err)
	}
	defer func() { _ = a.Close() }()

	obs, err := a.Stores.Observations.GetByID(t.Context(), 1)
	if err != nil {
		t.Fatalf("get obs: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	obs.Title = "Updated Title"
	obs.Content = "Updated content"
	obs.Type = domain.TypeBugfix
	if err := a.Stores.Observations.Update(t.Context(), obs); err != nil {
		t.Fatalf("update obs: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "revisions", "1"}, stdout, stderr); code != 0 {
		t.Fatalf("revisions code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Revision history for observation #1") {
		t.Fatalf("revisions stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[update]") {
		t.Fatalf("revisions stdout missing reason = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Original Title") {
		t.Fatalf("revisions stdout missing original title = %q", stdout.String())
	}
}

func TestRunSetupCodexWritesConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	if code := Run([]string{"cortex", "setup", "codex"}, stdout, stderr); code != 0 {
		t.Fatalf("setup code = %d, stderr = %q", code, stderr.String())
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", configPath, err)
	}
	if !strings.Contains(string(raw), "[mcp_servers.cortex]") {
		t.Fatalf("config.toml missing [mcp_servers.cortex]: %q", string(raw))
	}
}

func TestRunMigrateStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Run([]string{"cortex", "migrate", "status"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("migrate status code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Migration Status") {
		t.Fatalf("migrate status stdout = %q", stdout.String())
	}
}

func TestRunExport(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Save an observation first
	Run([]string{"cortex", "save", "Export test", "Content to export", "--project", "demo"}, stdout, stderr)
	stdout.Reset()
	stderr.Reset()

	// Export to stdout
	code := Run([]string{"cortex", "export", "--project", "demo"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("export code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Export test") {
		t.Fatalf("export stdout = %q", stdout.String())
	}
}

func TestRunExportToFile(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	Run([]string{"cortex", "save", "File export", "File content", "--project", "demo"}, stdout, stderr)
	stdout.Reset()
	stderr.Reset()

	outFile := filepath.Join(t.TempDir(), "export.json")
	code := Run([]string{"cortex", "export", "--output", outFile}, stdout, stderr)
	if code != 0 {
		t.Fatalf("export code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Exported") {
		t.Fatalf("export stdout = %q", stdout.String())
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if !strings.Contains(string(data), "File export") {
		t.Fatalf("export file = %q", string(data))
	}
}

func TestRunImportFromJSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	// Create a JSON file with observations
	jsonData := `[{"title":"JSON import test","content":"Imported content","type":"manual","project":"demo","scope":"project","session_id":"json-session"}]`
	jsonFile := filepath.Join(t.TempDir(), "import.json")
	if err := os.WriteFile(jsonFile, []byte(jsonData), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Run([]string{"cortex", "import", "--from-json", "--path", jsonFile}, stdout, stderr)
	if code != 0 {
		t.Fatalf("import code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Imported") {
		t.Fatalf("import stdout = %q", stdout.String())
	}

	// Verify the observation was imported
	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"cortex", "search", "JSON import", "--project", "demo"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("search code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "JSON import test") {
		t.Fatalf("search stdout = %q", stdout.String())
	}
}

func TestRunSyncExportAndStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Save data first
	Run([]string{"cortex", "save", "Sync test", "Content to sync", "--project", "demo"}, stdout, stderr)
	stdout.Reset()
	stderr.Reset()

	// Export sync
	code := Run([]string{"cortex", "sync", "--all"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("sync export code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Exported chunk") {
		t.Fatalf("sync export stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()

	// Check status
	code = Run([]string{"cortex", "sync", "--status"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("sync status code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Local chunks:") {
		t.Fatalf("sync status stdout = %q", stdout.String())
	}
}

func TestRunSyncImport(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Import with nothing to import
	code := Run([]string{"cortex", "sync", "--import"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("sync import code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Imported 0 chunks") {
		t.Fatalf("sync import stdout = %q", stdout.String())
	}
}

func TestRunMergeProjects(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Save observations to different project variants
	Run([]string{"cortex", "save", "Auth from myapp", "Content A", "--project", "myapp"}, stdout, stderr)
	Run([]string{"cortex", "save", "Auth from MYAPP", "Content B", "--project", "MYAPP"}, stdout, stderr)
	stdout.Reset()
	stderr.Reset()

	// Merge
	code := Run([]string{"cortex", "merge-projects", "--from", "MYAPP", "--to", "myapp"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("merge-projects code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Merged into") {
		t.Fatalf("merge-projects stdout = %q", stdout.String())
	}
}

func TestRunMergeProjectsDryRun(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Run([]string{"cortex", "merge-projects", "--from", "A,B", "--to", "c", "--dry-run"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("merge-projects dry-run code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Dry run") {
		t.Fatalf("merge-projects dry-run stdout = %q", stdout.String())
	}
}

func TestRunMergeProjectsMissingArgs(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Run([]string{"cortex", "merge-projects"}, stdout, stderr)
	if code != 1 {
		t.Fatalf("merge-projects no args should fail, got code = %d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("merge-projects stderr = %q", stderr.String())
	}
}

func TestRunConfigCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	t.Run("config no args prints usage", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		if code := Run([]string{"cortex", "config"}, stdout, stderr); code != 1 {
			t.Fatalf("expected code 1, got %d", code)
		}
	})

	t.Run("config init formats", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		yamlPath := filepath.Join(home, "custom.yaml")
		if code := Run([]string{"cortex", "config", "init", "--format=yaml", "-o", yamlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config init yaml failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Initialized minimal YAML") {
			t.Errorf("stdout = %q", stdout.String())
		}

		jsonPath := filepath.Join(home, "custom.json")
		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"cortex", "config", "init", "--format=json", "-o", jsonPath}, stdout, stderr); code != 0 {
			t.Fatalf("config init json failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Initialized minimal JSON") {
			t.Errorf("stdout = %q", stdout.String())
		}

		tomlPath := filepath.Join(home, "custom.toml")
		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"cortex", "config", "init", "--format=toml", "-o", tomlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config init toml failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Initialized minimal TOML") {
			t.Errorf("stdout = %q", stdout.String())
		}
	})

	t.Run("config validate", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		yamlPath := filepath.Join(home, "custom.yaml")
		if code := Run([]string{"cortex", "config", "validate", "-f", yamlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config validate failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Configuration valid") {
			t.Errorf("stdout = %q", stdout.String())
		}
	})

	t.Run("config show yaml and json", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		yamlPath := filepath.Join(home, "custom.yaml")
		if code := Run([]string{"cortex", "config", "show", "-f", yamlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config show yaml failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Config{") {
			t.Errorf("stdout = %q", stdout.String())
		}

		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"cortex", "config", "show", "--format=json", "-f", yamlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config show json failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), `"http"`) {
			t.Errorf("stdout = %q", stdout.String())
		}
	})

	t.Run("config_set_and_get", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		yamlPath := filepath.Join(home, "custom.yaml")
		// Set http.port to 8099
		if code := Run([]string{"cortex", "config", "set", "http.port", "8099", "-f", yamlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config set failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "8099") {
			t.Errorf("stdout = %q", stdout.String())
		}

		// Get http.port
		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"cortex", "config", "get", "http.port", "-f", yamlPath}, stdout, stderr); code != 0 {
			t.Fatalf("config get failed: %s", stderr.String())
		}
		if strings.TrimSpace(stdout.String()) != "8099" {
			t.Errorf("expected 8099, got %q", stdout.String())
		}

		// Wizard
		stdout.Reset()
		stderr.Reset()
		if code := Run([]string{"cortex", "config", "wizard"}, stdout, stderr); code != 0 {
			t.Fatalf("config wizard failed: %s", stderr.String())
		}
		if !strings.Contains(stdout.String(), "Configuration Wizard") {
			t.Errorf("expected wizard title, got %q", stdout.String())
		}
	})
}

func TestRunAuthCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// Initial status: anonymous
	if code := Run([]string{"cortex", "auth", "status"}, stdout, stderr); code != 0 {
		t.Fatalf("auth status failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Authentication Status") {
		t.Errorf("stdout = %q", stdout.String())
	}

	// Login with token
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "auth", "login", "--token=ctx_secret_token_123456"}, stdout, stderr); code != 0 {
		t.Fatalf("auth login failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Successfully authenticated") {
		t.Errorf("stdout = %q", stdout.String())
	}

	// Status: authenticated
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "auth", "status"}, stdout, stderr); code != 0 {
		t.Fatalf("auth status failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Authenticated") {
		t.Errorf("stdout = %q", stdout.String())
	}

	// Logout
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"cortex", "auth", "logout"}, stdout, stderr); code != 0 {
		t.Fatalf("auth logout failed: %s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Successfully logged out") {
		t.Errorf("stdout = %q", stdout.String())
	}
}



