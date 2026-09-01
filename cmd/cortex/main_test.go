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

func TestParseServerInvocationRecognizesSynchronousReindex(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	inv, err := parseServerInvocation([]string{"cortex", "--config", "server.yaml", "reindex", "--project-id", project})
	if err != nil {
		t.Fatal(err)
	}
	if inv.configPath != "server.yaml" || !inv.reindex || inv.projectID != project {
		t.Fatalf("invocation = %+v", inv)
	}
	if _, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id", project, "--tenant-id", "forged"}); err == nil {
		t.Fatal("server reindex accepted a tenant override")
	}
	if _, err := parseServerInvocation([]string{"cortex", "reindex"}); err == nil {
		t.Fatal("server reindex accepted a missing project UUID")
	}
	if _, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id", "label"}); err == nil {
		t.Fatal("server reindex accepted a project label")
	}
}

func TestParseServerInvocationPreservesServeMode(t *testing.T) {
	inv, err := parseServerInvocation([]string{"cortex", "--config=server.yaml"})
	if err != nil || inv.reindex || inv.configPath != "server.yaml" {
		t.Fatalf("invocation = %+v, error = %v", inv, err)
	}
}
