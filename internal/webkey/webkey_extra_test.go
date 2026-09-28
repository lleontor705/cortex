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

// rawDigest is the base64.RawURLEncoding length of a 32-byte SHA-256 digest.
func rawDigest() string { return strings.Repeat("A", 43) }

func writeKeyFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
}

func TestLoadRejectsMalformedRecords(t *testing.T) {
	digest := rawDigest()
	cases := []struct {
		name    string
		content string
	}{
		{"empty file", ""},
		{"single prefix line", "prefix=ctx_abcdefgh\n"},
		{"single digest line", "digest=" + digest + "\n"},
		{"empty prefix value", "prefix=\ndigest=" + digest + "\n"},
		{"missing digest value", "prefix=ctx_abcdefgh\nprefix=ctx_klmnopqr\n"},
		{"unknown field", "flavor=salt\nprefix=ctx_abcdefgh\ndigest=" + digest + "\n"},
		{"extra trailing field", "prefix=ctx_abcdefgh\ndigest=" + digest + "\nnonce=1\n"},
		{"line without separator", "prefix ctx_abcdefgh\ndigest=" + digest + "\n"},
		{"wrong namespace", "prefix=key_abcdefgh\ndigest=" + digest + "\n"},
		{"short prefix value", "prefix=ctx_short\ndigest=" + digest + "\n"},
		{"truncated digest", "prefix=ctx_abcdefgh\ndigest=" + digest[:42] + "\n"},
		{"overlong digest", "prefix=ctx_abcdefgh\ndigest=" + digest + "A\n"},
		{"embedded separator", "prefix=ctx_abcdefgh\ndigest=" + digest[:20] + "=x\n"},
		{"crlf endings", "prefix=ctx_abcdefgh\r\ndigest=" + digest + "\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, s := newStore(t)
			writeKeyFile(t, path, tc.content)
			if _, err := s.Load(); !errors.Is(err, webkey.ErrMalformedKeyFile) {
				t.Fatalf("Load err=%v want ErrMalformedKeyFile", err)
			}
		})
	}
}

func TestLoadParsesWellFormedRecordWithSurroundingWhitespace(t *testing.T) {
	path, s := newStore(t)
	digest := rawDigest()
	writeKeyFile(t, path, "\n\nprefix=ctx_abcdefgh\ndigest="+digest+"\n\n")
	rec, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.Prefix != "ctx_abcdefgh" || rec.Digest != digest {
		t.Fatalf("record=%+v", rec)
	}
	if err := s.Verify("ctx_abcdefgh" + strings.Repeat("B", 20)); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("synthetic record must reject unrelated plaintext: %v", err)
	}
}

func TestVerifyRejectsMatchingPrefixWithWrongDigest(t *testing.T) {
	_, s := newStore(t)
	secret, err := s.Generate()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(rec.Prefix + strings.Repeat("x", 20)); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("matching-prefix wrong-digest err=%v want ErrInvalidKey", err)
	}
	if err := s.Verify(secret[:len(secret)-1]); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("truncated valid key err=%v want ErrInvalidKey", err)
	}
}

func TestPermissionMatrixFailsClosedOnRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	for _, perm := range []os.FileMode{0o644, 0o640, 0o604, 0o620, 0o607, 0o666} {
		t.Run(perm.String(), func(t *testing.T) {
			path, s := newStore(t)
			secret, err := s.Generate()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, perm); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(); !errors.Is(err, webkey.ErrInsecurePermissions) {
				t.Fatalf("Load mode=%o err=%v want ErrInsecurePermissions", perm, err)
			}
			if err := s.Verify(secret); !errors.Is(err, webkey.ErrInsecurePermissions) {
				t.Fatalf("Verify mode=%o err=%v want ErrInsecurePermissions", perm, err)
			}
			if err := os.Chmod(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(); err != nil {
				t.Fatalf("tightened mode must load: %v", err)
			}
			if err := s.Verify(secret); err != nil {
				t.Fatalf("tightened mode must verify: %v", err)
			}
		})
	}
}

func TestNilStoreIsSafeAndNotFound(t *testing.T) {
	var s *webkey.Store
	if got := s.Path(); got != "" {
		t.Fatalf("nil Path=%q want empty", got)
	}
	if exists, err := s.Exists(); exists || !errors.Is(err, webkey.ErrKeyNotFound) {
		t.Fatalf("nil Exists=%v,%v want false,ErrKeyNotFound", exists, err)
	}
	if _, err := s.Load(); !errors.Is(err, webkey.ErrKeyNotFound) {
		t.Fatalf("nil Load err=%v want ErrKeyNotFound", err)
	}
	if err := s.Verify("ctx_anything"); !errors.Is(err, webkey.ErrKeyNotFound) {
		t.Fatalf("nil Verify err=%v want ErrKeyNotFound", err)
	}
}

func TestNewStoreCleansPath(t *testing.T) {
	dir := t.TempDir()
	s, err := webkey.NewStore("  " + filepath.Join(dir, "sub", "..", "web.key") + "  ")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.Path(), filepath.Join(dir, "web.key"); got != want {
		t.Fatalf("Path=%q want %q", got, want)
	}
}

func TestGenerateAndRegenerateLifecycle(t *testing.T) {
	_, s := newStore(t)
	if exists, err := s.Exists(); err != nil || exists {
		t.Fatalf("Exists=%v,%v want false,nil", exists, err)
	}

	first, err := s.Regenerate()
	if err != nil {
		t.Fatalf("Regenerate on empty store: %v", err)
	}
	if err := s.Verify(first); err != nil {
		t.Fatalf("verify first: %v", err)
	}
	if exists, err := s.Exists(); err != nil || !exists {
		t.Fatalf("Exists=%v,%v want true,nil", exists, err)
	}
	if _, err := s.Generate(); !errors.Is(err, webkey.ErrKeyExists) {
		t.Fatalf("Generate over existing err=%v want ErrKeyExists", err)
	}

	second, err := s.Regenerate()
	if err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	if second == first {
		t.Fatal("rotation reused the previous secret")
	}
	if err := s.Verify(second); err != nil {
		t.Fatalf("verify second: %v", err)
	}
	if err := s.Verify(first); !errors.Is(err, webkey.ErrInvalidKey) {
		t.Fatalf("rotated-out key err=%v want ErrInvalidKey", err)
	}
	if rec, err := s.Load(); err != nil || rec.Prefix == "" || rec.Digest == "" {
		t.Fatalf("rotated record=%+v err=%v", rec, err)
	}
}

func TestEnsureFirstBootMalformedExistingFailsClosed(t *testing.T) {
	path, s := newStore(t)
	writeKeyFile(t, path, "prefix=ctx_abcdefgh\n")
	secret, minted, err := s.EnsureFirstBoot()
	if !errors.Is(err, webkey.ErrMalformedKeyFile) {
		t.Fatalf("err=%v want ErrMalformedKeyFile", err)
	}
	if minted || secret != "" {
		t.Fatalf("minted=%v secret=%q want false,empty", minted, secret)
	}
}

// Rotation is single-writer by contract. Concurrent readers must never observe
// a torn record or accept a foreign key; a rotation that cannot replace the
// file fails closed (Windows cannot rename over an open read handle). See
// gotchas/go-cov-web-packages for the reproduction.
func TestRotationRaceLeavesReadersConsistent(t *testing.T) {
	_, s := newStore(t)
	if _, err := s.Generate(); err != nil {
		t.Fatal(err)
	}
	bogus := "ctx_" + strings.Repeat("Z", 24)
	stop := make(chan struct{})
	torn := make(chan error, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := s.Verify(bogus)
				if err == nil {
					torn <- errors.New("foreign key verified during rotation")
					return
				}
				if errors.Is(err, webkey.ErrMalformedKeyFile) {
					torn <- err
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		_, _ = s.Regenerate()
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-torn:
		t.Fatalf("reader observed a torn or foreign record: %v", err)
	default:
	}
	if rec, err := s.Load(); err != nil || rec.Prefix == "" || rec.Digest == "" {
		t.Fatalf("final record=%+v err=%v", rec, err)
	}
}
