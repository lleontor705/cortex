package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunIngest(t *testing.T) {
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

type Order struct {
	ID string
}
`), 0o600)

	tsFile := filepath.Join(codeDir, "user.ts")
	_ = os.WriteFile(tsFile, []byte(`export class UserService {
  getUser(id: string) {
    return { id };
  }
}
`), 0o600)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := Run([]string{"cortex", "ingest", codeDir, "--project", "test-project"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("ingest failed: code=%d, stderr=%s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "AST Ingestion Complete!") {
		t.Fatalf("unexpected output: %s", out)
	}
	if !strings.Contains(out, "Symbols extracted:") {
		t.Fatalf("missing symbols extracted: %s", out)
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"cortex", "search", "CalculateTax", "--project", "test-project"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("search failed: code=%d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CalculateTax") {
		t.Fatalf("search did not find ingested symbol: %s", stdout.String())
	}
}
