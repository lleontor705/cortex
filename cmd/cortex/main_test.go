package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunNoArgsNonInteractivePrintsUsage(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	code := run([]string{"cortex"}, stdout, stderr)
	if code != 1 {
		t.Fatalf("run() code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("run() stdout missing Usage: %q", stdout.String())
	}
}

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

func TestParseServerInvocationHelpAndVersionFlags(t *testing.T) {
	for _, flag := range []string{"-h", "--help", "help"} {
		inv, err := parseServerInvocation([]string{"cortex", flag})
		if err != nil {
			t.Fatalf("parseServerInvocation with %q returned error: %v", flag, err)
		}
		if !inv.help {
			t.Fatalf("expected help=true for %q", flag)
		}
	}

	for _, flag := range []string{"-v", "--version", "version"} {
		inv, err := parseServerInvocation([]string{"cortex", flag})
		if err != nil {
			t.Fatalf("parseServerInvocation with %q returned error: %v", flag, err)
		}
		if !inv.version {
			t.Fatalf("expected version=true for %q", flag)
		}
	}

	// Reindex with --help should not require --project-id
	inv, err := parseServerInvocation([]string{"cortex", "reindex", "--help"})
	if err != nil {
		t.Fatalf("reindex with --help returned error: %v", err)
	}
	if !inv.help {
		t.Fatal("expected help=true for reindex --help")
	}
}

func TestRunServerHelpAndVersion(t *testing.T) {
	for _, flag := range []string{"--help", "-h", "help"} {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := runContext(context.Background(), []string{"cortex", "--mode", "server", flag}, stdout, stderr)
		if code != 0 {
			t.Fatalf("runContext(--mode server %s) code = %d, want 0, stderr = %q", flag, code, stderr.String())
		}
		out := stdout.String()
		for _, want := range []string{"--config", "reindex", "--project-id"} {
			if !strings.Contains(out, want) {
				t.Fatalf("runContext(--mode server %s) stdout missing %q:\n%s", flag, want, out)
			}
		}
	}

	for _, flag := range []string{"--version", "-v", "version"} {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		code := runContext(context.Background(), []string{"cortex", "--mode", "server", flag}, stdout, stderr)
		if code != 0 {
			t.Fatalf("runContext(--mode server %s) code = %d, want 0, stderr = %q", flag, code, stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "cortex ") {
			t.Fatalf("runContext(--mode server %s) stdout = %q, want prefix 'cortex '", flag, stdout.String())
		}
	}
}

