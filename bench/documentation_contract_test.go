package bench

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestRetrievalBaselineDocumentationContract(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve documentation test location")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(currentFile))

	tests := []struct {
		path     string
		required []string
	}{
		{
			path: filepath.Join(repositoryRoot, "docs", "BENCHMARKS.md"),
			required: []string{
				"## What the baseline proves",
				"## What the baseline does not prove",
				"## Versioned retrieval protocol",
				"### Splits and evaluator",
				"### Metrics and uncertainty",
				"### Hardware and resources",
				"### Release gates",
				"## External and Cortex-reproduced evidence",
				"## Reproduce the baseline",
				"## Licences and limitations",
			},
		},
		{
			path: filepath.Join(repositoryRoot, "bench", "README.md"),
			required: []string{
				"## Retrieval baseline quick path",
				"go test -v -count=1 ./bench -run TestRetrievalBaselineDocumentationContract",
				"go test -v -count=1 ./bench/...",
				"LOCOMO, DMR, and LongMemEval",
				"stable-ID retrieval relevance",
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(filepath.Base(testCase.path), func(t *testing.T) {
			document, err := os.ReadFile(testCase.path)
			if err != nil {
				t.Fatalf("read %s: %v", testCase.path, err)
			}
			for _, marker := range testCase.required {
				if !strings.Contains(string(document), marker) {
					t.Errorf("%s is missing required marker %q", testCase.path, marker)
				}
			}
		})
	}

	benchmarkPath := filepath.Join(repositoryRoot, "docs", "BENCHMARKS.md")
	benchmarkDocument, err := os.ReadFile(benchmarkPath)
	if err != nil {
		t.Fatalf("read %s: %v", benchmarkPath, err)
	}
	benchmarkText := string(benchmarkDocument)

	legacyStart := strings.Index(benchmarkText, "## Results Summary")
	legacyEnd := strings.Index(benchmarkText, "## Methodology")
	if legacyStart == -1 || legacyEnd == -1 || legacyEnd <= legacyStart {
		t.Fatal("legacy results must remain in a distinct Results Summary section before Methodology")
	}
	legacyResults := benchmarkText[legacyStart:legacyEnd]
	for _, marker := range []string{
		"**Evidence classification:**",
		"**Evidence identity:**",
		"**Evaluator classification:**",
		"**Comparability:**",
	} {
		if !strings.Contains(legacyResults, marker) {
			t.Errorf("legacy results are missing structural evidence marker %q", marker)
		}
	}

	forbiddenClaims := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{name: "unsupported external LOCOMO accuracy", pattern: regexp.MustCompile(`(?i)Engram reports\s+80%\s+LOCOMO`)},
		{name: "unsupported retrieval multiplier", pattern: regexp.MustCompile(`(?i)(vector search improves retrieval|overall improvement).{0,30}12\s*[-–]\s*37x`)},
		{name: "unsupported provider parity", pattern: regexp.MustCompile(`(?i)Ollama.{0,20}matches OpenAI`)},
		{name: "unsupported dimensionality cause", pattern: regexp.MustCompile(`(?i)due to higher[- ]dimensional embeddings`)},
		{name: "unsupported provider speed claim", pattern: regexp.MustCompile(`(?i)Ollama is\s+1[.]7x faster`)},
		{name: "unsupported network-latency cause", pattern: regexp.MustCompile(`(?i)no network latency for inference`)},
		{name: "unsupported absolute temporal limitation", pattern: regexp.MustCompile(`(?i)FTS5 cannot answer temporal questions at all`)},
	}
	for _, claim := range forbiddenClaims {
		if claim.pattern.MatchString(benchmarkText) {
			t.Errorf("benchmark documentation contains %s; remove it or replace it with explicitly scoped evidence", claim.name)
		}
	}

	// REQ-LEG-001: also block Engram vendor/parity claims in benchmark docs.
	for _, claim := range []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{name: "100% API-compatible claim", pattern: regexp.MustCompile(`(?i)100%\s+API[- ]compatible`)},
		{name: "built-on-Engram claim", pattern: regexp.MustCompile(`(?i)built (?:on|upon)\b.{0,30}engram`)},
		{name: "Engram vendor URL", pattern: regexp.MustCompile(`(?i)Gentleman-Programming/engram`)},
	} {
		if claim.pattern.MatchString(benchmarkText) {
			t.Errorf("benchmark documentation contains %s; Engram compatibility surface must be removed", claim.name)
		}
	}
}

// TestEngramCompatibilitySurfaceRemoved enforces REQ-LEG-001 (W7): the repository
// MUST be free of ACTIVE Engram compatibility surface — no importer, no CLI
// migration flag, no parity claim, no compatibility framing in code/scripts/llms.
//
// The following references are NOT compatibility surface and are allowlisted:
//   - docs/BENCHMARKS.md: external research citation (spec REQ-LEG-001 scenario 1)
//   - internal/migration/preflight*.go, internal/migration/v2.go, internal/app/app.go:
//     W3 read-only refusal probe that DETECTS and REFUSES old Engram databases
//     (REQ-DB-002). Naming "Engram" in a refusal error message is operator-facing
//     diagnostics, not compatibility surface.
//   - internal/mcp/cortex_namespace_test.go: enforcement test that asserts
//     serverInstructions do NOT carry Engram framing (REQ-MCPH-003).
//   - review/, docs/research/: historical/research artifacts.
//
// Broader release docs (README.md, AGENTS.md, CLAUDE.md, docs/*.md) are owned by
// W18 (REQ-REL-001) and are excluded from this W7-scoped contract.
func TestEngramCompatibilitySurfaceRemoved(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve documentation test location")
	}
	repoRoot := filepath.Dir(filepath.Dir(currentFile))

	// REQ-LEG-001: these files MUST be deleted.
	mustNotExist := []string{
		"internal/migration/engram_import.go",
		"scripts/migrate-from-engram.sh",
	}
	for _, rel := range mustNotExist {
		abs := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); !os.IsNotExist(err) {
			t.Errorf("REQ-LEG-001: %s must be deleted (W7 Engram removal)", rel)
		}
	}

	// Active Engram compatibility surface patterns. These MUST NOT appear in any
	// source, script, plugin, or llms file.
	surfacePatterns := []struct {
		name    string
		pattern *regexp.Regexp
	}{
		{name: "--from-engram CLI flag", pattern: regexp.MustCompile(`(?i)--from-engram`)},
		{name: "Engram-compatible framing", pattern: regexp.MustCompile(`(?i)engram[- ]compatible`)},
		{name: "100% API-compatible with Engram", pattern: regexp.MustCompile(`(?i)(?:100%\s+)?API[- ]compatible.{0,30}engram`)},
		{name: "built-on-Engram foundation", pattern: regexp.MustCompile(`(?i)built (?:on|upon)\b.{0,30}engram`)},
		{name: "Migrating from Engram hint", pattern: regexp.MustCompile(`(?i)migrating from engram`)},
		{name: "migrate-from-engram script ref", pattern: regexp.MustCompile(`migrate-from-engram`)},
		{name: "ImportFromEngram symbol", pattern: regexp.MustCompile(`ImportFromEngram`)},
		{name: "EngramImport type", pattern: regexp.MustCompile(`EngramImport`)},
	}

	// Paths where Engram references are legitimate (refusal probe, enforcement,
	// research citation). Uses forward-slash relative paths from repoRoot.
	isAllowlisted := func(rel string) bool {
		rel = filepath.ToSlash(rel)
		exact := map[string]bool{
			"docs/BENCHMARKS.md":                    true,
			"internal/migration/preflight.go":       true,
			"internal/migration/preflight_test.go":  true,
			"internal/migration/v2.go":              true,
			"internal/app/app.go":                   true,
			"bench/documentation_contract_test.go":  true,
			"internal/mcp/cortex_namespace_test.go": true,
		}
		if exact[rel] {
			return true
		}
		prefixes := []string{
			"review/",        // historical review artifacts
			"docs/research/", // research artifacts
			".git/",          // VCS metadata
		}
		for _, p := range prefixes {
			if strings.HasPrefix(rel, p) {
				return true
			}
		}
		// W18-owned broader release docs (REQ-REL-001 owns the full sweep).
		w18Docs := map[string]bool{
			"README.md":               true,
			"AGENTS.md":               true,
			"CLAUDE.md":               true,
			"docs/COMPARISON.md":      true,
			"docs/RECOMMENDATIONS.md": true,
			"docs/ARCHITECTURE.md":    true,
			"docs/AGENT-SETUP.md":     true,
			"docs/INSTALLATION.md":    true,
			"docs/PLUGINS.md":         true,
		}
		return w18Docs[rel]
	}

	// File extensions to scan for active surface.
	scanExt := map[string]bool{
		".go":   true,
		".sh":   true,
		".ts":   true,
		".txt":  true,
		".md":   true,
		".json": true,
	}

	walkErr := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "node_modules" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if isAllowlisted(rel) {
			return nil
		}
		ext := filepath.Ext(rel)
		if !scanExt[ext] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(data)
		for _, sp := range surfacePatterns {
			if sp.pattern.MatchString(text) {
				t.Errorf("REQ-LEG-001: %s contains %s — active Engram compatibility surface must be removed", rel, sp.name)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk repository: %v", walkErr)
	}
}

func TestBaselineWorkflowContract(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve workflow test location")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(currentFile))

	ci, err := os.ReadFile(filepath.Join(repositoryRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	ciText := string(ci)
	ciText = strings.ReplaceAll(ciText, "\r\n", "\n")
	for _, branch := range []string{"      - main\n", "      - develop\n"} {
		if !strings.Contains(ciText, branch) {
			t.Errorf("CI pull_request branches missing %q", strings.TrimSpace(branch))
		}
	}
	if strings.Contains(ciText, "      - master\n") {
		t.Error("CI pull_request branches include unsupported master")
	}
	if !strings.Contains(ciText, "go test -v -count=1 ./bench ./bench/common ./bench/cortex ./bench/fixtures/cortex-native ./bench/cortex/cmd/baseline") {
		t.Error("CI baseline validation must use the direct offline Go command")
	}
	if !strings.Contains(ciText, "  race-detector:") {
		t.Error("CI must define a race-detector job")
	}
	if !strings.Contains(ciText, "go test -race -count=1 ./internal/store/search ./internal/store/bundle ./internal/mcp") {
		t.Error("CI must include a race detector gate over concurrent store packages (search, bundle, mcp)")
	}

	if !strings.Contains(ciText, "  coverage:") {
		t.Error("CI must define a dedicated coverage job")
	}
	if !strings.Contains(ciText, "    runs-on: ubuntu-latest\n") {
		t.Error("CI coverage job must run on Linux")
	}
	if !strings.Contains(ciText, "go test -tags postgres_integration -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...") {
		t.Error("CI coverage job must collect whole-project atomic coverage with PostgreSQL integration")
	}
	if !strings.Contains(ciText, "go tool cover -func coverage.out") {
		t.Error("CI coverage job must parse the coverage profile with go tool cover")
	}
	if !strings.Contains(ciText, "awk '$1 == \"total:\"") || !strings.Contains(ciText, "< 70.0") {
		t.Error("CI coverage job must fail when exact total coverage is below 70.0%")
	}
	if strings.Contains(ciText, "printf \"%.0f") || strings.Contains(ciText, "printf '%0.f") {
		t.Error("CI coverage threshold must not promote rounded percentages")
	}

	protocol, err := os.ReadFile(filepath.Join(repositoryRoot, "bench", "evidence", "cortex-native", "v1", "protocol.json"))
	if err != nil {
		t.Fatalf("read baseline protocol: %v", err)
	}
	for _, line := range strings.Split(string(protocol), "\n") {
		if strings.Contains(line, "baseline repro ") && !strings.Contains(line, " --protocol ") {
			t.Errorf("repro command omits required --protocol: %s", strings.TrimSpace(line))
		}
		if strings.Contains(line, "baseline run ") && strings.Contains(line, "--out bench/") {
			t.Errorf("representative run output is staged inside repository: %s", strings.TrimSpace(line))
		}
	}

	for _, path := range []string{
		filepath.Join(repositoryRoot, "bench", "README.md"),
		filepath.Join(repositoryRoot, "docs", "BENCHMARKS.md"),
	} {
		document, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read judge documentation %s: %v", path, err)
		}
		text := string(document)
		for _, forbidden := range []string{"GPT-4o", "claude-sonnet", "LLM-as-Judge (requires API key)", "Set an API key", "Without an API key"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains unsupported active judge guidance %q", path, forbidden)
			}
		}
		for _, required := range []string{"Ollama-only", "qwen2.5:7b-instruct", "OLLAMA_ENDPOINT", "OLLAMA_JUDGE_MODEL", "temperature=0", "seed=42", "not retrieval evidence"} {
			if !strings.Contains(text, required) {
				t.Errorf("%s is missing committed judge runtime contract %q", path, required)
			}
		}
	}
}

func TestPostgresCoverageWorkflowContract(t *testing.T) {
	t.Parallel()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve workflow test location")
	}
	repositoryRoot := filepath.Dir(filepath.Dir(currentFile))
	ci, err := os.ReadFile(filepath.Join(repositoryRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	ciText := strings.ReplaceAll(string(ci), "\r\n", "\n")
	for _, required := range []string{
		"image: postgres:16",
		"POSTGRES_USER: cortex_bootstrap",
		"POSTGRES_PASSWORD: cortex_bootstrap",
		"POSTGRES_DB: cortex_test",
		"pg_isready -U cortex_bootstrap -d cortex_test",
		"CORTEX_TEST_POSTGRES_DSN: postgres://cortex_test:cortex_test@localhost:5432/cortex_test?sslmode=disable",
		"CORTEX_TEST_POSTGRES_MIGRATION_DSN: postgres://cortex_bootstrap:cortex_bootstrap@localhost:5432/cortex_test?sslmode=disable",
		"CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN: postgres://cortex_admin_login:cortex_admin_login@localhost:5432/cortex_test?sslmode=disable",
		"go test -tags postgres_integration -covermode=atomic -coverpkg=./... -coverprofile=coverage.out ./...",
		"go tool cover -func coverage.out",
		"awk '$1 == \"total:\"",
		"coverage < 70.0",
		"go test -v -count=1 -tags \"integration postgres_integration\" ./...",
	} {
		if !strings.Contains(ciText, required) {
			t.Errorf("CI PostgreSQL coverage contract is missing %q", required)
		}
	}
	if strings.Contains(ciText, "t.Skip(\"CORTEX_TEST_POSTGRES_DSN") {
		t.Error("PostgreSQL harness must fail when DSN is missing, not skip")
	}
	if strings.Contains(ciText, "printf \"%.0f") || strings.Contains(ciText, "printf '%0.f") {
		t.Error("PostgreSQL coverage threshold must not use rounded percentages")
	}
	harness, err := os.ReadFile(filepath.Join(repositoryRoot, "internal", "store", "postgres", "postgres_integration_test.go"))
	if err != nil {
		t.Fatalf("read PostgreSQL harness: %v", err)
	}
	harnessText := string(harness)
	for _, required := range []string{
		"//go:build postgres_integration",
		"CORTEX_TEST_POSTGRES_DSN is required for postgres_integration tests",
		"CORTEX_TEST_POSTGRES_MIGRATION_DSN is required for privileged PostgreSQL fixture setup",
		"invalid CORTEX_TEST_POSTGRES_DSN",
	} {
		if !strings.Contains(harnessText, required) {
			t.Errorf("PostgreSQL harness contract is missing %q", required)
		}
	}
	if strings.Contains(harnessText, "t.Skip") {
		t.Error("PostgreSQL harness must fail on missing or invalid DSN, not skip")
	}
	makefile, err := os.ReadFile(filepath.Join(repositoryRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	if !strings.Contains(string(makefile), "$(GOTEST) -v -tags \"integration postgres_integration\" ./...") {
		t.Error("Makefile complete integration target must include PostgreSQL integration tests")
	}
}
