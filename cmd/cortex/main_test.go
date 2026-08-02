package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUnknownCommandReturnsError(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := run([]string{"cortex", "wat"}, stdout, stderr)
	if code == 0 {
		t.Fatalf("run() code = %d, want non-zero", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("run() stderr was empty")
	}
}

func TestRunHelpReturnsZero(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := run([]string{"cortex", "help"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("run() code = %d, want 0", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("run() stdout was empty")
	}
	if stderr.Len() != 0 {
		t.Fatalf("run() stderr = %q, want empty", stderr.String())
	}
}

func TestRunServerReportsBootstrapFailureWithoutClaimingReadiness(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config", configPath}, stdout, stderr)
	if code != 2 || !strings.Contains(stderr.String(), "server bootstrap") {
		t.Fatalf("runContext() = %d, stderr %q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "server endpoint") || strings.Contains(stdout.String(), "composition ready") {
		t.Fatalf("runContext() claimed readiness on bootstrap failure: %q", stdout.String())
	}
}
