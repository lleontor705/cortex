package cli

// cli_extra_test.go is the additive, strictly-TDD behavior suite for the
// internal/cli package (coverage-70-and-lint, G1B, issue #46).
//
// Isolation contract honored by every app-opening test:
//   - unique on-disk SQLite database per test (CORTEX_DATABASE_IN_MEMORY=false)
//   - isolated HOME/USERPROFILE via t.Setenv so config.Load never reads or
//     writes real user configuration
//   - embedding providers neutralized so app.Open never auto-starts Ollama or
//     touches the network
//   - controlled stdout/stderr buffers; no MCP stdio, TUI, server, or network
//   - no t.Parallel (environment mutation must not run concurrently)

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// setCLIEnv isolates the CLI execution environment for a single test: a unique
// on-disk SQLite database (in-memory disabled), an isolated HOME/USERPROFILE so
// config.Load never touches real user config, and all embedding providers
// neutralized so app.Open never auto-starts Ollama or hits the network.
func setCLIEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	dbPath := filepath.Join(home, "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_EMBEDDING_PROVIDER", "none")
	t.Setenv("CORTEX_SEARCH_OLLAMA_AUTO_START", "false")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OLLAMA_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "")
	return dbPath
}

// run executes the CLI with controlled writers and returns (exitCode, stdout, stderr).
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	out, errB := &bytes.Buffer{}, &bytes.Buffer{}
	code := Run(args, out, errB)
	return code, out.String(), errB.String()
}

// ---------------------------------------------------------------------------
// Pure helper behavior
// ---------------------------------------------------------------------------

func TestExtraTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short unchanged", "hello", 10, "hello"},
		{"exact length unchanged", "hello", 5, "hello"},
		{"empty unchanged", "", 3, ""},
		{"ascii truncates with ellipsis", "hello world", 5, "hello..."},
		{"rune-aware not byte-aware", "héllo world", 5, "héllo..."},
		{"multibyte counted as runes", "😀😀😀😀😀😀", 3, "😀😀😀..."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncate(tc.in, tc.max); got != tc.want {
				t.Fatalf("truncate(%q,%d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestExtraDefaultSessionID(t *testing.T) {
	cases := []struct {
		project, want string
	}{
		{"", "manual-save"},
		{"cortex", "manual-save-cortex"},
	}
	for _, tc := range cases {
		if got := defaultSessionID(tc.project); got != tc.want {
			t.Fatalf("defaultSessionID(%q) = %q, want %q", tc.project, got, tc.want)
		}
	}
}

func TestExtraProjectOrDefault(t *testing.T) {
	if got := projectOrDefault(""); got != "default" {
		t.Fatalf("projectOrDefault('') = %q, want 'default'", got)
	}
	if got := projectOrDefault("cortex"); got != "cortex" {
		t.Fatalf("projectOrDefault('cortex') = %q, want 'cortex'", got)
	}
}

func TestExtraCurrentDir(t *testing.T) {
	got := currentDir()
	if got == "" {
		t.Fatal("currentDir() returned empty string")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Skipf("os.Getwd error: %v", err)
	}
	if got != wd {
		t.Fatalf("currentDir() = %q, want os.Getwd() = %q", got, wd)
	}
}

func TestExtraIsLoopbackHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"  localhost  ", true},
		{"127.0.0.1", true},
		{"127.1.2.3", true},
		{"::1", true},
		{"8.8.8.8", false},
		{"example.com", false},
		{"10.0.0.1", false},
	}
	for _, tc := range cases {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Fatalf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestExtraTakeSessions(t *testing.T) {
	mk := func(ids ...string) []*domain.Session {
		out := make([]*domain.Session, 0, len(ids))
		for _, id := range ids {
			out = append(out, &domain.Session{ID: id})
		}
		return out
	}
	t.Run("empty returns empty", func(t *testing.T) {
		if got := takeSessions(nil, 5); len(got) != 0 {
			t.Fatalf("takeSessions(nil,5) = %d items, want 0", len(got))
		}
	})
	t.Run("fewer than n returns all preserving identity", func(t *testing.T) {
		got := takeSessions(mk("a", "b"), 5)
		if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
			t.Fatalf("takeSessions(<n) = %+v", got)
		}
	})
	t.Run("more than n returns first n preserving order", func(t *testing.T) {
		got := takeSessions(mk("a", "b", "c", "d", "e", "f"), 3)
		if len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
			t.Fatalf("takeSessions(>n) = %+v", got)
		}
	})
	t.Run("exact n returns all", func(t *testing.T) {
		got := takeSessions(mk("a", "b"), 2)
		if len(got) != 2 {
			t.Fatalf("takeSessions(=n) = %d items, want 2", len(got))
		}
	})
}

func TestExtraFormatSearchBreakdown(t *testing.T) {
	cases := []struct {
		name string
		b    domain.SearchScoreBreakdown
		want string
	}{
		{"empty", domain.SearchScoreBreakdown{}, ""},
		{"strategy only", domain.SearchScoreBreakdown{Strategy: "hybrid"}, "strategy=hybrid"},
		{"topic exact", domain.SearchScoreBreakdown{TopicKeyExact: true}, "topic_key_exact=true"},
		{"bm25 formats to 4 decimals", domain.SearchScoreBreakdown{KeywordBM25: 0.5}, "bm25=0.5000"},
		{"fusion formats to 4 decimals", domain.SearchScoreBreakdown{FusionScore: 1.0}, "fusion=1.0000"},
		{"all combined in fixed order", domain.SearchScoreBreakdown{Strategy: "keyword", TopicKeyExact: true, KeywordBM25: 1.23456, FusionScore: 2.0},
			"strategy=keyword | topic_key_exact=true | bm25=1.2346 | fusion=2.0000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatSearchBreakdown(tc.b); got != tc.want {
				t.Fatalf("formatSearchBreakdown = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Dispatch: usage, version, unknown
// ---------------------------------------------------------------------------

func TestExtraRunNoArgsPrintsUsageAndExits1(t *testing.T) {
	code, out, _ := run(t, "cortex")
	if code != 1 {
		t.Fatalf("no-args code = %d, want 1", code)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "Persistent memory") {
		t.Fatalf("no-args stdout missing usage = %q", out)
	}
}

func TestExtraRunHelpVariantsExit0(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		code, out, _ := run(t, "cortex", arg)
		if code != 0 {
			t.Fatalf("help variant %q code = %d, want 0", arg, code)
		}
		if !strings.Contains(out, "Commands:") {
			t.Fatalf("help %q missing 'Commands:' section = %q", arg, out)
		}
	}
}

func TestExtraRunVersionVariants(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, out, _ := run(t, "cortex", arg)
		if code != 0 {
			t.Fatalf("version %q code = %d, want 0", arg, code)
		}
		if !strings.Contains(out, "cortex dev") {
			t.Fatalf("version %q output = %q, want substring 'cortex dev'", arg, out)
		}
	}
}

func TestExtraRunUnknownCommandExits1(t *testing.T) {
	code, _, errB := run(t, "cortex", "bogus-command")
	if code != 1 {
		t.Fatalf("unknown code = %d, want 1", code)
	}
	if !strings.Contains(errB, "unknown command: bogus-command") {
		t.Fatalf("unknown stderr = %q, want substring 'unknown command: bogus-command'", errB)
	}
	if !strings.Contains(errB, "Usage:") {
		t.Fatalf("unknown stderr missing usage = %q", errB)
	}
}

func TestExtraUsageListsAllCommands(t *testing.T) {
	_, out, _ := run(t, "cortex", "help")
	for _, s := range []string{
		"mcp", "search", "save", "timeline", "revisions", "context", "stats",
		"setup", "import", "export", "sync", "merge-projects", "reindex",
		"doctor", "gc", "migrate", "tui", "serve", "version", "help",
	} {
		if !strings.Contains(out, s) {
			t.Fatalf("usage missing command %q in:\n%s", s, out)
		}
	}
}

// ---------------------------------------------------------------------------
// context / stats
// ---------------------------------------------------------------------------

func TestExtraRunContextEmptyDB(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "context")
	if code != 0 {
		t.Fatalf("context code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "No previous session memories found.") {
		t.Fatalf("context empty stdout = %q", out)
	}
}

func TestExtraRunContextShowsSessionsAndObservations(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "Ctx Title", "Ctx body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "context", "demo")
	if code != 0 {
		t.Fatalf("context code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "## Recent Sessions") {
		t.Fatalf("context missing sessions header = %q", out)
	}
	if !strings.Contains(out, "## Recent Observations") {
		t.Fatalf("context missing observations header = %q", out)
	}
	if !strings.Contains(out, "Ctx Title") {
		t.Fatalf("context missing observation title = %q", out)
	}
	if !strings.Contains(out, "manual-save-demo") || !strings.Contains(out, "active") {
		t.Fatalf("context missing session id/status = %q", out)
	}
}

func TestExtraRunStats(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "Stat obs", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "stats")
	if code != 0 {
		t.Fatalf("stats code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Cortex Memory Stats") {
		t.Fatalf("stats missing header = %q", out)
	}
	if !strings.Contains(out, "Observations: 1") {
		t.Fatalf("stats missing observation count = %q", out)
	}
	if !strings.Contains(out, "demo") {
		t.Fatalf("stats missing project name = %q", out)
	}
}

// ---------------------------------------------------------------------------
// search / save validation + behavior
// ---------------------------------------------------------------------------

func TestExtraRunSearchValidation(t *testing.T) {
	setCLIEnv(t)
	t.Run("no args prints usage exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "search")
		if code != 1 || !strings.Contains(errB, "usage: cortex search") {
			t.Fatalf("search no-args code=%d stderr=%q", code, errB)
		}
	})
	t.Run("invalid limit exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "search", "term", "--limit", "abc")
		if code != 1 || !strings.Contains(errB, "invalid --limit") {
			t.Fatalf("search bad limit code=%d stderr=%q", code, errB)
		}
	})
	t.Run("only flags -> empty query exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "search", "--project", "demo")
		if code != 1 || !strings.Contains(errB, "search query is required") {
			t.Fatalf("search empty query code=%d stderr=%q", code, errB)
		}
	})
}

func TestExtraRunSearchNoResults(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "search", "zzznonexistentterm12345", "--project", "demo")
	if code != 0 {
		t.Fatalf("search code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "No memories found for:") {
		t.Fatalf("search no-results stdout = %q", out)
	}
}

func TestExtraRunSaveValidation(t *testing.T) {
	setCLIEnv(t)
	code, _, errB := run(t, "cortex", "save", "only-title")
	if code != 1 || !strings.Contains(errB, "usage: cortex save") {
		t.Fatalf("save missing content code=%d stderr=%q", code, errB)
	}
}

// ---------------------------------------------------------------------------
// timeline / revisions validation + behavior
// ---------------------------------------------------------------------------

func TestExtraRunTimelineValidation(t *testing.T) {
	setCLIEnv(t)
	t.Run("no args prints usage exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "timeline")
		if code != 1 || !strings.Contains(errB, "usage: cortex timeline") {
			t.Fatalf("timeline no-args code=%d stderr=%q", code, errB)
		}
	})
	t.Run("invalid id exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "timeline", "abc")
		if code != 1 || !strings.Contains(errB, "invalid observation id") {
			t.Fatalf("timeline bad id code=%d stderr=%q", code, errB)
		}
	})
	t.Run("invalid before exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "timeline", "1", "--before", "xx")
		if code != 1 || !strings.Contains(errB, "invalid --before") {
			t.Fatalf("timeline bad before code=%d stderr=%q", code, errB)
		}
	})
}

func TestExtraRunTimelineHighlightsTarget(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "Timeline Target", "Timeline body content", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "timeline", "1")
	if code != 0 {
		t.Fatalf("timeline code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, ">>> #1") {
		t.Fatalf("timeline missing highlight marker = %q", out)
	}
	if !strings.Contains(out, "Timeline Target") {
		t.Fatalf("timeline missing title = %q", out)
	}
	if !strings.Contains(out, "Timeline body content") {
		t.Fatalf("timeline missing content = %q", out)
	}
}

func TestExtraRunRevisionsValidation(t *testing.T) {
	setCLIEnv(t)
	t.Run("no args prints usage exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "revisions")
		if code != 1 || !strings.Contains(errB, "usage: cortex revisions") {
			t.Fatalf("revisions no-args code=%d stderr=%q", code, errB)
		}
	})
	t.Run("invalid id exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "revisions", "notanumber")
		if code != 1 || !strings.Contains(errB, "invalid observation id") {
			t.Fatalf("revisions bad id code=%d stderr=%q", code, errB)
		}
	})
}

func TestExtraRunRevisionsNoHistory(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "NoRevisions", "body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	code, out, errB := run(t, "cortex", "revisions", "1")
	if code != 0 {
		t.Fatalf("revisions code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "No revision history found for observation #1") {
		t.Fatalf("revisions no-history stdout = %q", out)
	}
}

// ---------------------------------------------------------------------------
// setup validation
// ---------------------------------------------------------------------------

func TestExtraRunSetupListsAgents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	code, out, errB := run(t, "cortex", "setup")
	if code != 0 {
		t.Fatalf("setup no-agent code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Supported agents:") {
		t.Fatalf("setup missing 'Supported agents:' = %q", out)
	}
	for _, agent := range []string{"opencode", "claude-code", "gemini-cli", "codex"} {
		if !strings.Contains(out, agent) {
			t.Fatalf("setup missing agent %q = %q", agent, out)
		}
	}
}

func TestExtraRunSetupUnsupportedAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	code, _, errB := run(t, "cortex", "setup", "nope-agent")
	if code != 1 {
		t.Fatalf("setup unsupported code = %d, want 1", code)
	}
	if !strings.Contains(errB, "cortex:") {
		t.Fatalf("setup unsupported stderr = %q", errB)
	}
}

// ---------------------------------------------------------------------------
// import validation
// ---------------------------------------------------------------------------

func TestExtraRunImportValidation(t *testing.T) {
	t.Run("no args prints usage exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "import")
		if code != 1 || !strings.Contains(errB, "usage: cortex import") {
			t.Fatalf("import no-args code=%d stderr=%q", code, errB)
		}
	})
	t.Run("unknown source exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "import", "--from-csv", "--path", "x")
		if code != 1 || !strings.Contains(errB, "unknown import source") {
			t.Fatalf("import unknown source code=%d stderr=%q", code, errB)
		}
	})
	t.Run("from-json missing path exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "import", "--from-json")
		if code != 1 || !strings.Contains(errB, "--path is required for --from-json") {
			t.Fatalf("import json missing path code=%d stderr=%q", code, errB)
		}
	})
}

func TestExtraRunImportFromJSONErrors(t *testing.T) {
	t.Run("nonexistent file exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "import", "--from-json", "--path", filepath.Join(t.TempDir(), "nope.json"))
		if code != 1 || !strings.Contains(errB, "failed to open file") {
			t.Fatalf("import nonexistent code=%d stderr=%q", code, errB)
		}
	})
	t.Run("invalid json exit1", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		code, _, errB := run(t, "cortex", "import", "--from-json", "--path", bad)
		if code != 1 || !strings.Contains(errB, "invalid JSON") {
			t.Fatalf("import invalid json code=%d stderr=%q", code, errB)
		}
	})
}

// ---------------------------------------------------------------------------
// migrate validation + behavior
// ---------------------------------------------------------------------------

func TestExtraRunMigrateValidation(t *testing.T) {
	t.Run("no args prints usage exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "migrate")
		if code != 1 || !strings.Contains(errB, "usage: cortex migrate") {
			t.Fatalf("migrate no-args code=%d stderr=%q", code, errB)
		}
	})
	t.Run("unknown subcommand exit1", func(t *testing.T) {
		setCLIEnv(t)
		code, _, errB := run(t, "cortex", "migrate", "sideways")
		if code != 1 || !strings.Contains(errB, "unknown migrate subcommand") {
			t.Fatalf("migrate unknown code=%d stderr=%q", code, errB)
		}
	})
}

func TestExtraRunMigrateStatusShowsApplied(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "migrate", "status")
	if code != 0 {
		t.Fatalf("migrate status code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Migration Status") {
		t.Fatalf("migrate status missing header = %q", out)
	}
	if !strings.Contains(out, "applied") {
		t.Fatalf("migrate status missing 'applied' marker = %q", out)
	}
}

func TestExtraRunMigrateUpIdempotent(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "migrate", "up")
	if code != 0 {
		t.Fatalf("migrate up code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Migrations applied successfully") {
		t.Fatalf("migrate up stdout = %q", out)
	}
}

// ---------------------------------------------------------------------------
// export (equals-form parsing) + persisted rows
// ---------------------------------------------------------------------------

func TestExtraRunExportEqualsFormAndPersistedRows(t *testing.T) {
	setCLIEnv(t)
	if code, _, errB := run(t, "cortex", "save", "Export Eq", "Eq body", "--project", "demo"); code != 0 {
		t.Fatalf("save code = %d, stderr = %q", code, errB)
	}
	outFile := filepath.Join(t.TempDir(), "out.json")
	code, out, errB := run(t, "cortex", "export", "--project=demo", "--output", outFile)
	if code != 0 {
		t.Fatalf("export code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Exported 1 observations") {
		t.Fatalf("export stdout = %q", out)
	}
	raw, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	var got []domain.Observation
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("export file not valid JSON: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Export Eq" {
		t.Fatalf("export persisted rows = %+v", got)
	}
}

// ---------------------------------------------------------------------------
// sync validation
// ---------------------------------------------------------------------------

func TestExtraRunSyncStatus(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "sync", "--status")
	if code != 0 {
		t.Fatalf("sync status code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Local chunks:") || !strings.Contains(out, "Remote chunks:") || !strings.Contains(out, "Pending import:") {
		t.Fatalf("sync status stdout = %q", out)
	}
}

// ---------------------------------------------------------------------------
// merge-projects validation
// ---------------------------------------------------------------------------

func TestExtraRunMergeProjectsValidation(t *testing.T) {
	t.Run("missing --to exit1", func(t *testing.T) {
		code, _, errB := run(t, "cortex", "merge-projects", "--from", "a")
		if code != 1 || !strings.Contains(errB, "usage:") {
			t.Fatalf("merge missing-to code=%d stderr=%q", code, errB)
		}
	})
	t.Run("dry run splits sources", func(t *testing.T) {
		code, out, errB := run(t, "cortex", "merge-projects", "--from", "alpha,beta", "--to", "canonical", "--dry-run")
		if code != 0 {
			t.Fatalf("merge dry-run code = %d, stderr = %q", code, errB)
		}
		if !strings.Contains(out, "would merge [alpha beta]") || !strings.Contains(out, `"canonical"`) {
			t.Fatalf("merge dry-run stdout = %q", out)
		}
	})
}

// ---------------------------------------------------------------------------
// reindex / doctor / gc validation (offline paths only)
// ---------------------------------------------------------------------------

func TestExtraRunReindexNoProvider(t *testing.T) {
	setCLIEnv(t) // provider "none" -> Embeddings nil
	code, _, errB := run(t, "cortex", "reindex")
	if code != 1 {
		t.Fatalf("reindex code = %d, want 1 (no provider configured)", code)
	}
	if !strings.Contains(errB, "no embedding provider configured") {
		t.Fatalf("reindex stderr = %q", errB)
	}
}

func TestExtraRunDoctorFreshDB(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "doctor")
	if code != 0 {
		t.Fatalf("doctor code = %d, stderr = %q", code, errB)
	}
	// The vector-store status line depends on the build tag: the stub
	// (default) reports disabled, the cortex_vectors build reports enabled.
	vectorStatus := "[WARN] Vector store: disabled"
	if testVectorsEnabled {
		vectorStatus = "[OK]   Vector store: enabled"
	}
	for _, want := range []string{
		"Cortex Doctor",
		"[OK]   Database:",
		"[OK]   FTS5 search:",
		"[OK]   Knowledge graph:",
		vectorStatus,
		"[WARN] Embeddings: not configured",
		"All checks passed.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor missing %q in:\n%s", want, out)
		}
	}
}

func TestExtraRunGCNothingToCollect(t *testing.T) {
	setCLIEnv(t)
	code, out, errB := run(t, "cortex", "gc")
	if code != 0 {
		t.Fatalf("gc code = %d, stderr = %q", code, errB)
	}
	if !strings.Contains(out, "Nothing to collect.") {
		t.Fatalf("gc stdout = %q", out)
	}
}
