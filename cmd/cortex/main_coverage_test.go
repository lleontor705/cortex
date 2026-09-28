package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/config"
	serverplatform "github.com/lleontor705/cortex/v2/internal/platform/server"
)

// --- runContext: ParseModeStrict fail-closed path (line 125-128) ---

func TestRunContextUnknownModeFailsClosed(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "bogus"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown mode") {
		t.Fatalf("stderr = %q, want 'unknown mode'", stderr.String())
	}
}

func TestRunContextUnknownModeEqualsFormFailsClosed(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode=garbage", "help"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

// --- runContext: hybrid mode env-var path (line 131-133) ---

func TestRunContextHybridModeSetsEnv(t *testing.T) {
	t.Setenv("CORTEX_MODE", "")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "hybrid", "help"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if v := os.Getenv("CORTEX_MODE"); v != "hybrid" {
		t.Fatalf("CORTEX_MODE = %q, want 'hybrid'", v)
	}
}

// --- runContext: server config load error (line 162-166) ---

func TestRunContextServerConfigLoadError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config", missing}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "server config") {
		t.Fatalf("stderr = %q, want 'server config'", stderr.String())
	}
}

// --- runContext: server reindex bootstrap error (line 168-172) ---

func TestRunContextServerReindexBootstrapError(t *testing.T) {
	cfg := writeTempConfig(t)
	orig := openServerReindexJob
	openServerReindexJob = func(context.Context, config.Config) (serverReindexJob, error) {
		return nil, &fakeReindexError{"bootstrap failed"}
	}
	t.Cleanup(func() { openServerReindexJob = orig })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config", cfg, "reindex", "--project-id", "10000000-a000-0000-0000-000000000003"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "server reindex bootstrap") {
		t.Fatalf("stderr = %q, want 'server reindex bootstrap'", stderr.String())
	}
}

// --- runContext: server reindex project error (line 174-178) ---

func TestRunContextServerReindexProjectError(t *testing.T) {
	cfg := writeTempConfig(t)
	orig := openServerReindexJob
	openServerReindexJob = func(context.Context, config.Config) (serverReindexJob, error) {
		return &failingReindexJob{}, nil
	}
	t.Cleanup(func() { openServerReindexJob = orig })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config", cfg, "reindex", "--project-id", "10000000-a000-0000-0000-000000000003"}, stdout, stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "server reindex") {
		t.Fatalf("stderr = %q, want 'server reindex'", stderr.String())
	}
}

// --- runContext: server parseServerInvocation error (line 150-153) ---

func TestRunContextServerParseError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--unknown-flag"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "server arguments") {
		t.Fatalf("stderr = %q, want 'server arguments'", stderr.String())
	}
}

// --- runContext: server help via parseServerInvocation (line 154-157) ---

func TestRunContextServerHelpPath(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "help"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Cortex Server Mode") {
		t.Fatalf("stdout = %q, want 'Cortex Server Mode'", stdout.String())
	}
}

// --- runContext: server version via parseServerInvocation (line 158-161) ---

func TestRunContextServerVersionPath(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "version"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if !strings.HasPrefix(stdout.String(), "cortex ") {
		t.Fatalf("stdout = %q, want prefix 'cortex '", stdout.String())
	}
}

// --- runContext: unknown mode default branch (line 212-214) ---

func TestRunContextUnknownModeDefaultBranch(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "unknown"}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown mode") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// --- parseServerInvocation edge cases ---

func TestParseServerInvocationConfigMissingValue(t *testing.T) {
	_, err := parseServerInvocation([]string{"cortex", "--config"})
	if err == nil {
		t.Fatal("expected error for --config with no value")
	}
	if !strings.Contains(err.Error(), "--config requires a path") {
		t.Fatalf("error = %q, want '--config requires a path'", err.Error())
	}
}

func TestParseServerInvocationConfigEmptyValue(t *testing.T) {
	_, err := parseServerInvocation([]string{"cortex", "--config", ""})
	if err == nil {
		t.Fatal("expected error for --config with empty value")
	}
}

func TestParseServerInvocationProjectIDMissingValue(t *testing.T) {
	_, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id"})
	if err == nil {
		t.Fatal("expected error for --project-id with no value")
	}
}

func TestParseServerInvocationProjectIDDuplicate(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	_, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id", project, "--project-id", project})
	if err == nil {
		t.Fatal("expected error for duplicate --project-id")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("error = %q, want 'exactly one'", err.Error())
	}
}

func TestParseServerInvocationProjectIDEqualsFormDuplicate(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	_, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id=" + project, "--project-id", project})
	if err == nil {
		t.Fatal("expected error for duplicate --project-id (equals form then flag form)")
	}
}

func TestParseServerInvocationProjectIDEqualsFormDuplicateBothEquals(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	_, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id=" + project, "--project-id=" + project})
	if err == nil {
		t.Fatal("expected error for duplicate --project-id (both equals form)")
	}
}

func TestParseServerInvocationProjectIDEqualsFormSingle(t *testing.T) {
	project := "10000000-a000-0000-0000-000000000003"
	inv, err := parseServerInvocation([]string{"cortex", "reindex", "--project-id=" + project})
	if err != nil {
		t.Fatal(err)
	}
	if inv.projectID != project {
		t.Fatalf("projectID = %q, want %q", inv.projectID, project)
	}
}

func TestParseServerInvocationUnknownArgEqualsForm(t *testing.T) {
	_, err := parseServerInvocation([]string{"cortex", "--unknown-flag"})
	if err == nil {
		t.Fatal("expected error for unknown argument")
	}
}

// --- runContext: --mode= equals form paths ---

func TestRunContextServerConfigEqualsFormLoadError(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config=" + filepath.Join(t.TempDir(), "x.yaml")}, stdout, stderr)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestRunContextServerHelpEqualsModeForm(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode=server", "help"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %q", code, stderr.String())
	}
}

func TestRunContextServerVersionEqualsModeForm(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode=server", "version"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0, stderr = %q", code, stderr.String())
	}
}

// --- runContext: reindex --project-id= form (combined parse+dispatch) ---

func TestRunContextServerReindexProjectIDEqualsForm(t *testing.T) {
	cfg := writeTempConfig(t)
	project := "10000000-a000-0000-0000-000000000003"
	job := &fakeServerReindexJob{}
	orig := openServerReindexJob
	openServerReindexJob = func(context.Context, config.Config) (serverReindexJob, error) {
		return job, nil
	}
	t.Cleanup(func() { openServerReindexJob = orig })

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "--config", cfg, "reindex", "--project-id=" + project}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr.String())
	}
	if job.project != project {
		t.Fatalf("job.project = %q, want %q", job.project, project)
	}
}

// --- runContext: reindex --help skips --project-id requirement ---

func TestRunContextServerReindexHelpSkipsProjectID(t *testing.T) {
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), []string{"cortex", "--mode", "server", "reindex", "--help"}, stdout, stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
}

// --- helpers ---

func writeTempConfig(t *testing.T) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "server.yaml")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

type fakeReindexError struct{ msg string }

func (e *fakeReindexError) Error() string { return e.msg }

type failingReindexJob struct{}

func (j *failingReindexJob) ReindexProject(_ context.Context, _ string) (*serverplatform.ReindexResult, error) {
	return nil, &fakeReindexError{"reindex failed"}
}

func (j *failingReindexJob) Close() error { return nil }
