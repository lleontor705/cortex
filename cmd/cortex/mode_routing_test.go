package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolateModeEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("CORTEX_MODE", "")
	t.Chdir(dir)
	return dir
}

func runMain(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	code := runContext(context.Background(), append([]string{"cortex"}, args...), stdout, stderr)
	return code, stdout.String(), stderr.String()
}

func TestEnvServerModeRoutesToServer(t *testing.T) {
	isolateModeEnv(t)
	t.Setenv("CORTEX_MODE", "server")
	code, out, errOut := runMain(t, "--help")
	if code != 0 || !strings.Contains(out, "--config") {
		t.Fatalf("CORTEX_MODE=server --help: code=%d out=%q err=%q (want server help)", code, out, errOut)
	}
}

func TestInvalidEnvModeFailsClosed(t *testing.T) {
	isolateModeEnv(t)
	t.Setenv("CORTEX_MODE", "bogus")
	code, _, errOut := runMain(t, "status")
	if code != 2 || !strings.Contains(errOut, "CORTEX_MODE") {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestRemoteModeFlagIsAccepted(t *testing.T) {
	isolateModeEnv(t)
	code, out, errOut := runMain(t, "--mode", "remote", "mode")
	if code != 0 || !strings.Contains(out, "Mode:        remote") {
		t.Fatalf("--mode remote mode: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestFileServerModeRoutesOnlyServerEntry(t *testing.T) {
	dir := isolateModeEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "cortex.yaml"), []byte("mode: server\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Starting the binary without a subcommand selects the server composition.
	code, out, errOut := runMain(t, "--help")
	if code != 0 || !strings.Contains(out, "--config") {
		t.Fatalf("mode: server --help: code=%d out=%q err=%q (want server help)", code, out, errOut)
	}
	// Management commands still run through the CLI.
	code, out, errOut = runMain(t, "mode")
	if code != 0 || !strings.Contains(out, "Mode:        server") {
		t.Fatalf("mode: server `mode`: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestWithServerConfigInjectsPathOnce(t *testing.T) {
	got := withServerConfig([]string{"cortex", "reindex"}, "/etc/cortex/cortex.yaml")
	if strings.Join(got, " ") != "cortex --config /etc/cortex/cortex.yaml reindex" {
		t.Fatalf("got %v", got)
	}
	keep := []string{"cortex", "--config=/x.yaml"}
	if got := withServerConfig(keep, "/y.yaml"); len(got) != 2 {
		t.Fatalf("explicit --config overridden: %v", got)
	}
}
