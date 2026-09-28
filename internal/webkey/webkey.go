// Package webkey manages the dedicated web-surface access credential for the
// embedded web UI. It is a separate credential namespace: the web key is never
// used as, defaulted to, or derived from http.token or the sync token.
package webkey

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultFileName is the conventional web key file name inside the Cortex
	// configuration directory.
	DefaultFileName = "web.key"

	secretSize   = 32
	prefixSize   = 12
	keyNamespace = "ctx_"
	digestDomain = "cortex/webkey/v1:"

	mintRetryAttempts = 50
	mintRetryDelay    = 2 * time.Millisecond
)

var (
	// ErrKeyNotFound indicates no web key file exists at the store path.
	ErrKeyNotFound = errors.New("webkey: web access key file not found")
	// ErrKeyExists indicates the web key file is already present.
	ErrKeyExists = errors.New("webkey: web access key file already exists")
	// ErrInvalidKey is the deterministic rejection for any failed verification.
	ErrInvalidKey = errors.New("webkey: invalid web access key")
	// ErrMalformedKeyFile indicates the key file cannot be parsed.
	ErrMalformedKeyFile = errors.New("webkey: malformed web key file")
	// ErrInsecurePermissions indicates the key file is group/other readable.
	ErrInsecurePermissions = errors.New("webkey: web key file permissions too permissive")
)

// Record is the non-secret at-rest representation of a web access key. It never
// carries the plaintext secret.
type Record struct {
	Prefix string
	Digest string
}

// Verifier validates a presented web access key.
type Verifier interface {
	Verify(secret string) error
}

// Store owns a single web key file at a caller-supplied path.
type Store struct {
	path string
}

var _ Verifier = (*Store)(nil)

// NewStore returns a Store bound to path. The path is never defaulted or read
// from global configuration; callers pass an explicit location.
func NewStore(path string) (*Store, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, errors.New("webkey: store path is required")
	}
	return &Store{path: filepath.Clean(trimmed)}, nil
}

// Path reports the key file location.
func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Exists reports whether a key file is present without revealing its contents.
func (s *Store) Exists() (bool, error) {
	if s == nil {
		return false, ErrKeyNotFound
	}
	_, err := os.Stat(s.path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("webkey: stat key file: %w", err)
	}
}

// Generate mints a new web key, persists it with an exclusive create, and
// returns the plaintext exactly once. It fails with ErrKeyExists when a key file
// already exists and never leaves a partial file behind.
func (s *Store) Generate() (string, error) {
	secret, err := mint()
	if err != nil {
		return "", err
	}
	if err := s.writeExclusive(secret); err != nil {
		return "", err
	}
	return secret, nil
}

// Regenerate atomically replaces the persisted key with a freshly minted one and
// returns the new plaintext exactly once. The previous key remains valid until
// the replacement write succeeds.
func (s *Store) Regenerate() (string, error) {
	secret, err := mint()
	if err != nil {
		return "", err
	}
	if err := s.writeAtomic(secret); err != nil {
		return "", err
	}
	return secret, nil
}

// EnsureFirstBoot mints and persists a key when none exists, returning the
// plaintext with minted=true exactly once. When a key already exists, including
// the loser of a concurrent first boot, it returns minted=false and no plaintext.
func (s *Store) EnsureFirstBoot() (string, bool, error) {
	secret, err := mint()
	if err != nil {
		return "", false, err
	}
	err = s.writeExclusive(secret)
	if err == nil {
		return secret, true, nil
	}
	if !errors.Is(err, ErrKeyExists) {
		return "", false, err
	}
	if _, err := s.loadDuringMint(); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// Load reads and validates the persisted record without exposing the secret.
func (s *Store) Load() (Record, error) {
	if s == nil {
		return Record{}, ErrKeyNotFound
	}
	if err := s.enforcePermissions(); err != nil {
		return Record{}, err
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Record{}, ErrKeyNotFound
		}
		return Record{}, fmt.Errorf("webkey: read key file: %w", err)
	}
	return parseRecord(raw)
}

// Verify checks secret against the persisted record using prefix lookup and a
// constant-time digest comparison.
func (s *Store) Verify(secret string) error {
	if secret == "" {
		return ErrInvalidKey
	}
	rec, err := s.Load()
	if err != nil {
		return err
	}
	if len(secret) < prefixSize || secret[:prefixSize] != rec.Prefix {
		return ErrInvalidKey
	}
	if !hmac.Equal([]byte(digestFor(rec.Prefix, secret)), []byte(rec.Digest)) {
		return ErrInvalidKey
	}
	return nil
}

func mint() (string, error) {
	buf := make([]byte, secretSize)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("webkey: generate secret: %w", err)
	}
	return keyNamespace + base64.RawURLEncoding.EncodeToString(buf), nil
}

// The digest key is derived from the public prefix so each record is
// self-contained: verification needs no second secret, and the 256-bit secret
// entropy keeps offline recovery infeasible even though the prefix is at rest.
func digestFor(prefix, secret string) string {
	key := sha256.Sum256([]byte(digestDomain + prefix))
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte(secret))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func encodeRecord(secret string) []byte {
	prefix := secret[:prefixSize]
	return []byte("prefix=" + prefix + "\ndigest=" + digestFor(prefix, secret) + "\n")
}

func parseRecord(raw []byte) (Record, error) {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		return Record{}, ErrMalformedKeyFile
	}
	var rec Record
	for _, line := range lines {
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			return Record{}, ErrMalformedKeyFile
		}
		switch name {
		case "prefix":
			if len(value) != prefixSize || !strings.HasPrefix(value, keyNamespace) {
				return Record{}, ErrMalformedKeyFile
			}
			rec.Prefix = value
		case "digest":
			if len(value) != base64.RawURLEncoding.EncodedLen(sha256.Size) {
				return Record{}, ErrMalformedKeyFile
			}
			rec.Digest = value
		default:
			return Record{}, ErrMalformedKeyFile
		}
	}
	if rec.Prefix == "" || rec.Digest == "" {
		return Record{}, ErrMalformedKeyFile
	}
	return rec, nil
}

func (s *Store) enforcePermissions() error {
	info, err := os.Stat(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrKeyNotFound
		}
		return fmt.Errorf("webkey: stat key file: %w", err)
	}
	if runtime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return ErrInsecurePermissions
	}
	return nil
}

func (s *Store) writeExclusive(secret string) error {
	content := encodeRecord(secret)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("webkey: prepare key directory: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrKeyExists
		}
		return fmt.Errorf("webkey: create key file: %w", err)
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(s.path)
		return fmt.Errorf("webkey: write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(s.path)
		return fmt.Errorf("webkey: close key file: %w", err)
	}
	return nil
}

func (s *Store) writeAtomic(secret string) error {
	content := encodeRecord(secret)
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("webkey: prepare key directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".web.key.tmp-*")
	if err != nil {
		return fmt.Errorf("webkey: create temp key file: %w", err)
	}
	tmpName := tmp.Name()
	discard := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o600); err != nil {
		discard()
		return fmt.Errorf("webkey: set key file mode: %w", err)
	}
	if _, err := tmp.Write(content); err != nil {
		discard()
		return fmt.Errorf("webkey: write temp key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		discard()
		return fmt.Errorf("webkey: sync temp key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("webkey: close temp key file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("webkey: replace key file: %w", err)
	}
	return nil
}

// loadDuringMint tolerates a concurrent winner whose exclusive create is still
// being written, so losers observe the complete record instead of a partial file.
func (s *Store) loadDuringMint() (Record, error) {
	var lastErr error
	for attempt := 0; attempt < mintRetryAttempts; attempt++ {
		rec, err := s.Load()
		if err == nil {
			return rec, nil
		}
		if !errors.Is(err, ErrMalformedKeyFile) && !errors.Is(err, ErrKeyNotFound) {
			return Record{}, err
		}
		lastErr = err
		time.Sleep(mintRetryDelay)
	}
	return Record{}, lastErr
}
