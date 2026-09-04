package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCodeImpactAndGraph(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "cortex.db")
	t.Setenv("CORTEX_DATABASE_PATH", dbPath)
	t.Setenv("CORTEX_DATABASE_IN_MEMORY", "false")
	t.Setenv("CORTEX_SYNC_ENABLED", "false")

	codeDir := t.TempDir()
	goFile := filepath.Join(codeDir, "main.go")
	_ = os.WriteFile(goFile, []byte(`package main

func CalculateTax(amount float64) float64 {
	return amount * 0.18
}

func ProcessOrder(amount float64) float64 {
	return CalculateTax(amount)
}
`), 0o600)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// 1. Ingest code first
	exitCode := Run([]string{"cortex", "ingest", codeDir, "--project", "test-project"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("ingest failed: code=%d, stderr=%s", exitCode, stderr.String())
	}

	// 2. Test cortex code impact
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "impact", "CalculateTax", "--project=test-project"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("code impact failed: code=%d, stderr=%s", exitCode, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Code Blast Radius") {
		t.Fatalf("expected Code Blast Radius in output: %s", out)
	}
	if !strings.Contains(out, "CalculateTax") {
		t.Fatalf("expected CalculateTax in output: %s", out)
	}

	// 3. Test cortex code impact with --json
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "impact", "CalculateTax", "--project=test-project", "--json"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("code impact json failed: code=%d, stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"target": "CalculateTax"`) {
		t.Fatalf("expected json output: %s", stdout.String())
	}

	// 4. Test cortex code impact missing target
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "impact"}, stdout, stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure for missing target, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "target symbol or file path is required") {
		t.Fatalf("expected error message in stderr: %s", stderr.String())
	}

	// 5. Test cortex code graph --format=mermaid
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "graph", "--project=test-project", "--format=mermaid"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("code graph mermaid failed: code=%d, stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "flowchart TD") {
		t.Fatalf("expected flowchart TD in mermaid output: %s", stdout.String())
	}

	// 6. Test cortex code graph --format=ascii
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "graph", "--project=test-project", "--format=ascii"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("code graph ascii failed: code=%d, stderr=%s", exitCode, stderr.String())
	}

	// 7. Test cortex code graph --format=json
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "graph", "--project=test-project", "--format=json"}, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("code graph json failed: code=%d, stderr=%s", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"project": "test-project"`) {
		t.Fatalf("expected json code graph, got %s", stdout.String())
	}

	// 8. Test cortex code diff
	stdout.Reset()
	stderr.Reset()
	exitCode = Run([]string{"cortex", "code", "diff", "--project=test-project"}, stdout, stderr)
	// Even if git diff returns 0 or 1 depending on git repo existence in temp test dir, it shouldn't panic
	if exitCode != 0 && !strings.Contains(stderr.String(), "git diff failed") {
		t.Fatalf("unexpected diff failure: code=%d, stderr=%s", exitCode, stderr.String())
	}
}
