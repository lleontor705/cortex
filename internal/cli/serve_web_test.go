package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	cortexhttp "github.com/lleontor705/cortex/v2/internal/http"
	"github.com/lleontor705/cortex/v2/internal/webkey"
)

// stubServe replaces the blocking network seam and the key-file resolver for a
// single test, restoring both so the process-wide CLI state stays clean.
func stubServe(t *testing.T, keyPath string) *bool {
	t.Helper()
	prevListen := serveListenAndServe
	prevKeyFile := webKeyFileFn
	launched := false
	serveListenAndServe = func(*cortexhttp.Server) error {
		launched = true
		return nil
	}
	webKeyFileFn = func() string { return keyPath }
	t.Cleanup(func() {
		serveListenAndServe = prevListen
		webKeyFileFn = prevKeyFile
	})
	return &launched
}

func TestServeFirstBootMintsWebKeyOnce(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("CORTEX_HTTP_HOST", "127.0.0.1")
	keyPath := filepath.Join(t.TempDir(), "web.key")
	launched := stubServe(t, keyPath)

	code, stdout, stderr := run(t, "cortex", "serve")
	if code != 0 {
		t.Fatalf("first serve code = %d, stderr = %q", code, stderr)
	}
	if !*launched {
		t.Fatal("serve never reached ListenAndServe")
	}
	secret := extractPlaintextKey(t, stdout)
	if got := strings.Count(stdout, secret); got != 1 {
		t.Fatalf("plaintext printed %d times on first boot, want exactly once:\n%s", got, stdout)
	}

	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("first boot did not mint %q: %v", keyPath, err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("web key mode = %v, want 0600", info.Mode().Perm())
	}
	store, err := webkey.NewStore(keyPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if err := store.Verify(secret); err != nil {
		t.Fatalf("minted key does not verify: %v", err)
	}

	*launched = false
	code, secondStdout, secondStderr := run(t, "cortex", "serve")
	if code != 0 {
		t.Fatalf("second serve code = %d, stderr = %q", code, secondStderr)
	}
	if !*launched {
		t.Fatal("second serve never reached ListenAndServe")
	}
	if plaintextKeyPattern.MatchString(secondStdout) {
		t.Fatalf("second boot reprinted the plaintext key:\n%s", secondStdout)
	}
}

func TestServeBannerEmitsPipeSafeKeyValueLines(t *testing.T) {
	dbPath := setCLIEnv(t)
	t.Setenv("CORTEX_HTTP_HOST", "127.0.0.1")
	keyPath := filepath.Join(t.TempDir(), "web.key")
	store, err := webkey.NewStore(keyPath)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := store.Generate(); err != nil {
		t.Fatalf("seed key: %v", err)
	}
	stubServe(t, keyPath)

	code, stdout, stderr := run(t, "cortex", "serve")
	if code != 0 {
		t.Fatalf("serve code = %d, stderr = %q", code, stderr)
	}

	want := map[string]string{
		"mode":     "local",
		"store":    dbPath,
		"sync":     "disabled",
		"web":      "mounted",
		"key_file": keyPath,
	}
	for key, value := range want {
		if !strings.Contains(stdout, key+"="+value) {
			t.Fatalf("banner missing %s=%s in:\n%s", key, value, stdout)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if !strings.Contains(line, "=") {
			t.Fatalf("banner line %q is not key=value", line)
		}
		if strings.Contains(line, "|") {
			t.Fatalf("banner line %q is not pipe-safe", line)
		}
	}
}

func TestServeFailsClosedWhenWebKeyStoreUnusable(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("CORTEX_HTTP_HOST", "127.0.0.1")
	keyPath := filepath.Join(t.TempDir(), "web.key")
	if err := os.WriteFile(keyPath, []byte("garbage\n"), 0o600); err != nil {
		t.Fatalf("seed malformed key file: %v", err)
	}
	launched := stubServe(t, keyPath)

	code, _, stderr := run(t, "cortex", "serve")

	if code == 0 {
		t.Fatal("serve returned exit 0 with an unusable web key store")
	}
	if *launched {
		t.Fatal("serve reached ListenAndServe despite the key store failure")
	}
	if !strings.Contains(stderr, "web access key") {
		t.Fatalf("serve stderr missing fail-closed report: %q", stderr)
	}
}

func TestServeNonLoopbackRefusalPrecedesWebMint(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("CORTEX_HTTP_HOST", "0.0.0.0")
	t.Setenv("CORTEX_HTTP_TOKEN", "")
	keyPath := filepath.Join(t.TempDir(), "web.key")
	launched := stubServe(t, keyPath)

	code, _, stderr := run(t, "cortex", "serve")

	if code != 1 {
		t.Fatalf("non-loopback serve code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "refusing to expose HTTP API") {
		t.Fatalf("non-loopback serve stderr = %q", stderr)
	}
	if *launched {
		t.Fatal("refused serve still reached ListenAndServe")
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused serve still minted a web key at %q (stat err = %v)", keyPath, err)
	}
}

// TestServePinnedWebKeySkipsMintAndFile pins the ephemeral-host contract: with
// CORTEX_WEB_KEY set, serve never mints or writes a key file, never reprints
// the plaintext, and reports the pin in its output.
func TestServePinnedWebKeySkipsMintAndFile(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("CORTEX_HTTP_HOST", "127.0.0.1")
	keyPath := filepath.Join(t.TempDir(), "web.key")
	launched := stubServe(t, keyPath)
	pinned := mustMintFormatKey(t)
	t.Setenv(webkey.EnvKey, pinned)

	code, stdout, stderr := run(t, "cortex", "serve")
	if code != 0 {
		t.Fatalf("pinned serve code = %d, stderr = %q", code, stderr)
	}
	if !*launched {
		t.Fatal("pinned serve never reached ListenAndServe")
	}
	if plaintextKeyPattern.MatchString(stdout) {
		t.Fatalf("pinned serve reprinted a plaintext key:\n%s", stdout)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pinned serve created a key file at %q (stat err = %v)", keyPath, err)
	}
	if !strings.Contains(stdout, "pinned by "+webkey.EnvKey) {
		t.Fatalf("pinned serve output missing pin report:\n%s", stdout)
	}
}

// TestServeInvalidPinnedWebKeyFailsClosed pins the fail-closed rule: an
// invalid CORTEX_WEB_KEY aborts serve instead of silently regenerating.
func TestServeInvalidPinnedWebKeyFailsClosed(t *testing.T) {
	setCLIEnv(t)
	t.Setenv("CORTEX_HTTP_HOST", "127.0.0.1")
	keyPath := filepath.Join(t.TempDir(), "web.key")
	stubServe(t, keyPath)
	t.Setenv(webkey.EnvKey, "not-a-valid-key")

	code, _, stderr := run(t, "cortex", "serve")
	if code == 0 {
		t.Fatal("serve with an invalid pinned key must fail, got exit 0")
	}
	if !strings.Contains(stderr, "web access key unavailable") {
		t.Fatalf("serve stderr missing fail-closed reason: %q", stderr)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed serve must not create a key file at %q (stat err = %v)", keyPath, err)
	}
}
