package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/webkey"
)

var plaintextKeyPattern = regexp.MustCompile(`(?m)^(ctx_[A-Za-z0-9_-]+)$`)

func extractPlaintextKey(t *testing.T, output string) string {
	t.Helper()
	match := plaintextKeyPattern.FindStringSubmatch(output)
	if match == nil {
		t.Fatalf("no plaintext web key found in output:\n%s", output)
	}
	return match[1]
}

func runWebCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	code := Run(append([]string{"cortex"}, args...), stdout, stderr)
	return code, stdout.String(), stderr.String()
}

func TestWebKeyCLIShowMissingKeyReportsAbsence(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "web.key")

	code, stdout, stderr := runWebCLI(t, "web", "key", "show", "--key-file", keyPath)

	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "not found") {
		t.Fatalf("show stdout missing absence report: %q", stdout)
	}
	if !strings.Contains(stdout, keyPath) {
		t.Fatalf("show stdout missing key location %q: %q", keyPath, stdout)
	}
	if !strings.Contains(stdout, "cortex serve") {
		t.Fatalf("show stdout missing first-boot guidance: %q", stdout)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("show created a key file at %q (stat err = %v)", keyPath, err)
	}
}

func TestWebKeyCLIShowExistingKeyNeverPrintsPlaintext(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "web.key")
	store, err := webkey.NewStore(keyPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	secret, err := store.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	code, stdout, stderr := runWebCLI(t, "web", "key", "show", "--key-file", keyPath)

	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "present") {
		t.Fatalf("show stdout missing presence report: %q", stdout)
	}
	if !strings.Contains(stdout, keyPath) {
		t.Fatalf("show stdout missing key location %q: %q", keyPath, stdout)
	}
	if !strings.Contains(stdout, secret[:12]) {
		t.Fatalf("show stdout missing key prefix: %q", stdout)
	}
	if strings.Contains(stdout, secret) {
		t.Fatalf("show leaked the plaintext key: %q", stdout)
	}
}

func TestWebKeyCLIRegenerateRotatesAndPrintsPlaintextOnce(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "web.key")
	store, err := webkey.NewStore(keyPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	oldSecret, err := store.Generate()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	code, stdout, stderr := runWebCLI(t, "web", "key", "regenerate", "--key-file", keyPath)

	if code != 0 {
		t.Fatalf("regenerate code = %d, stderr = %q", code, stderr)
	}
	newSecret := extractPlaintextKey(t, stdout)
	if newSecret == oldSecret {
		t.Fatalf("regenerate did not rotate the key")
	}
	if got := strings.Count(stdout, newSecret); got != 1 {
		t.Fatalf("plaintext printed %d times, want exactly once:\n%s", got, stdout)
	}
	if !strings.Contains(stdout, "will not be shown again") {
		t.Fatalf("regenerate stdout missing keep-it-safe warning: %q", stdout)
	}
	if err := store.Verify(newSecret); err != nil {
		t.Fatalf("new key does not verify: %v", err)
	}
	if err := store.Verify(oldSecret); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("old key verify = %v, want ErrInvalidKey", err)
	}
}

func TestWebKeyCLIRegenerateOnUnwritableStoreFails(t *testing.T) {
	tmp := t.TempDir()
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	keyPath := filepath.Join(blocker, "web.key")

	code, _, stderr := runWebCLI(t, "web", "key", "regenerate", "--key-file", keyPath)

	if code == 0 {
		t.Fatalf("regenerate on unwritable store returned exit 0")
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatalf("regenerate failure produced no error output")
	}
	if _, err := os.Stat(keyPath); err == nil {
		t.Fatalf("failed regenerate left a key file at %q", keyPath)
	}
}

func TestWebKeyCLISurfacesMalformedKeyFile(t *testing.T) {
	keyPath := filepath.Join(t.TempDir(), "web.key")
	if err := os.WriteFile(keyPath, []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("seed malformed key file: %v", err)
	}

	code, _, stderr := runWebCLI(t, "web", "key", "show", "--key-file", keyPath)

	if code == 0 {
		t.Fatalf("show on malformed key file returned exit 0")
	}
	if !strings.Contains(stderr, "webkey") {
		t.Fatalf("show stderr missing surfaced error: %q", stderr)
	}
}

func TestWebKeyCLIUsageAndDispatch(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		wantOutput string
		stdout     bool
	}{
		{name: "web without subcommand", args: []string{"web"}, wantCode: 1, wantOutput: "Usage: cortex web"},
		{name: "key without subcommand", args: []string{"web", "key"}, wantCode: 1, wantOutput: "show|regenerate"},
		{name: "unknown key subcommand", args: []string{"web", "key", "bogus"}, wantCode: 1, wantOutput: "unknown web key subcommand"},
		{name: "unknown web subcommand", args: []string{"web", "bogus"}, wantCode: 1, wantOutput: "unknown web subcommand"},
		{name: "help lists web command", args: []string{"help"}, wantCode: 0, wantOutput: "web <subcommand>", stdout: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runWebCLI(t, tc.args...)
			if code != tc.wantCode {
				t.Fatalf("code = %d, want %d (stderr = %q)", code, tc.wantCode, stderr)
			}
			out := stderr
			if tc.stdout {
				out = stdout
			}
			if !strings.Contains(out, tc.wantOutput) {
				t.Fatalf("output %q missing %q", out, tc.wantOutput)
			}
		})
	}
}
