package webkey_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/webkey"
)

func newStore(t *testing.T) (string, *webkey.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "web.key")
	s, err := webkey.NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return path, s
}

func TestGenerateReturnsPlaintextOnceAndPersistsPrefixAndDigestOnly(t *testing.T) {
	path, s := newStore(t)
	secret, err := s.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(secret, "ctx_") {
		t.Fatalf("secret %q lacks ctx_ namespace prefix", secret)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("plaintext secret persisted at rest")
	}
	rec, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.Prefix != secret[:12] {
		t.Fatalf("prefix=%q want %q", rec.Prefix, secret[:12])
	}
	if rec.Digest == "" || rec.Digest == secret {
		t.Fatalf("digest=%q must be a derived hash", rec.Digest)
	}
	if err := s.Verify(secret); err != nil {
		t.Fatalf("Verify(good): %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode=%o want 600", got)
		}
	}
}

func TestVerifyRejectsWrongAndEmptySecrets(t *testing.T) {
	_, s := newStore(t)
	if _, err := s.Generate(); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ctx_wrongsecret", "ctx_short", ""} {
		if err := s.Verify(secret); !errors.Is(err, webkey.ErrInvalidKey) {
			t.Fatalf("Verify(%q) err=%v want ErrInvalidKey", secret, err)
		}
	}
}

func TestVerifyRejectsTamperedDigest(t *testing.T) {
	path, s := newStore(t)
	secret, err := s.Generate()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	digest := []byte(rec.Digest)
	if digest[0] == 'A' {
		digest[0] = 'B'
	} else {
		digest[0] = 'A'
	}
	tampered := "prefix=" + rec.Prefix + "\ndigest=" + string(digest) + "\n"
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(secret); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("err=%v want ErrInvalidKey", err)
	}
}

func TestRegenerateRotatesSecretAndInvalidatesOldKey(t *testing.T) {
	_, s := newStore(t)
	old, err := s.Generate()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := s.Regenerate()
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if fresh == old {
		t.Fatal("regenerate reused the previous secret")
	}
	if err := s.Verify(fresh); err != nil {
		t.Fatalf("new key invalid: %v", err)
	}
	if err := s.Verify(old); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("old key err=%v want ErrInvalidKey", err)
	}
}

func TestEnsureFirstBootConcurrentYieldsSingleWinner(t *testing.T) {
	_, s := newStore(t)
	const workers = 8
	var wg sync.WaitGroup
	secrets := make([]string, workers)
	minted := make([]bool, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			secrets[i], minted[i], errs[i] = s.EnsureFirstBoot()
		}(i)
	}
	wg.Wait()

	winner := ""
	wins := 0
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if minted[i] {
			wins++
			winner = secrets[i]
			continue
		}
		if secrets[i] != "" {
			t.Fatalf("worker %d leaked plaintext on load", i)
		}
	}
	if wins != 1 {
		t.Fatalf("minted winners=%d want exactly 1", wins)
	}
	if winner == "" || s.Verify(winner) != nil {
		t.Fatal("winner plaintext must verify against the persisted record")
	}
}

func TestEnsureFirstBootLoadsExistingWithoutPlaintext(t *testing.T) {
	_, s := newStore(t)
	secret, err := s.Generate()
	if err != nil {
		t.Fatal(err)
	}
	got, minted, err := s.EnsureFirstBoot()
	if err != nil {
		t.Fatal(err)
	}
	if minted {
		t.Fatal("existing key reported as freshly minted")
	}
	if got != "" {
		t.Fatalf("plaintext leaked on load: %q", got)
	}
	if err := s.Verify(secret); err != nil {
		t.Fatalf("existing key invalid: %v", err)
	}
	if _, err := s.Generate(); !errors.Is(err, webkey.ErrKeyExists) {
		t.Fatalf("Generate on existing key err=%v want ErrKeyExists", err)
	}
}

func TestMissingKeyErrorTaxonomy(t *testing.T) {
	if _, err := webkey.NewStore("   "); err == nil {
		t.Fatal("empty store path must be rejected")
	}
	_, s := newStore(t)
	if _, err := s.Load(); !errors.Is(err, webkey.ErrKeyNotFound) {
		t.Fatalf("Load err=%v want ErrKeyNotFound", err)
	}
	if err := s.Verify("ctx_anything"); !errors.Is(err, webkey.ErrKeyNotFound) {
		t.Fatalf("Verify err=%v want ErrKeyNotFound", err)
	}
	if exists, err := s.Exists(); err != nil || exists {
		t.Fatalf("Exists=%v,%v want false,nil", exists, err)
	}
}

func TestGenerateFailsClosedWithoutPartialFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "web.key")
	s, err := webkey.NewStore(target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Generate(); err == nil {
		t.Fatal("Generate into a non-directory must fail closed")
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial key file left behind: %v", err)
	}
}

func TestLoadRejectsGroupReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	path, s := newStore(t)
	if _, err := s.Generate(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); !errors.Is(err, webkey.ErrInsecurePermissions) {
		t.Fatalf("err=%v want ErrInsecurePermissions", err)
	}
}

func TestRegenerateFailureKeepsPreviousKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	s, err := webkey.NewStore(filepath.Join(dir, "web.key"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.Generate()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Regenerate(); err == nil {
		t.Fatal("Regenerate into a read-only directory must fail")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(old); err != nil {
		t.Fatalf("previous key must remain valid: %v", err)
	}
}
