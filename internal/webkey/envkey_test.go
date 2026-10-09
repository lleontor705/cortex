package webkey

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// mustMint mints a format-valid key for tests (same shape as mint()).
func mustMint(t *testing.T) string {
	t.Helper()
	buf := make([]byte, secretSize)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return keyNamespace + base64.RawURLEncoding.EncodeToString(buf)
}

func TestValidateSecret(t *testing.T) {
	t.Run("accepts the exact minted shape", func(t *testing.T) {
		key := mustMint(t)
		if err := ValidateSecret(key); err != nil {
			t.Fatalf("ValidateSecret(%q) = %v, want nil", key, err)
		}
	})
	t.Run("rejects wrong prefix", func(t *testing.T) {
		if err := ValidateSecret("tok_" + strings.Repeat("A", 43)); !errors.Is(err, ErrInvalidPinnedKey) {
			t.Fatalf("wrong prefix accepted: %v", err)
		}
	})
	t.Run("rejects wrong length", func(t *testing.T) {
		if err := ValidateSecret(mustMint(t) + "x"); !errors.Is(err, ErrInvalidPinnedKey) {
			t.Fatalf("oversized key accepted: %v", err)
		}
		if err := ValidateSecret(keyNamespace + "short"); !errors.Is(err, ErrInvalidPinnedKey) {
			t.Fatalf("undersized key accepted: %v", err)
		}
	})
	t.Run("rejects non-base64 payload", func(t *testing.T) {
		bad := keyNamespace + strings.Repeat("@", 43)
		if err := ValidateSecret(bad); !errors.Is(err, ErrInvalidPinnedKey) {
			t.Fatalf("non-base64 key accepted: %v", err)
		}
	})
	t.Run("rejects empty", func(t *testing.T) {
		if err := ValidateSecret(""); !errors.Is(err, ErrInvalidPinnedKey) {
			t.Fatalf("empty key accepted: %v", err)
		}
	})
}

func TestResolveEnvOverride(t *testing.T) {
	t.Run("unset resolves empty", func(t *testing.T) {
		t.Setenv(EnvKey, "")
		got, err := ResolveEnvOverride()
		if err != nil || got != "" {
			t.Fatalf("ResolveEnvOverride() = %q, %v; want \"\", nil", got, err)
		}
	})
	t.Run("blank resolves empty", func(t *testing.T) {
		t.Setenv(EnvKey, "   ")
		got, err := ResolveEnvOverride()
		if err != nil || got != "" {
			t.Fatalf("blank override must resolve empty, got %q, %v", got, err)
		}
	})
	t.Run("valid pin resolves trimmed", func(t *testing.T) {
		key := mustMint(t)
		t.Setenv(EnvKey, " "+key+" ")
		got, err := ResolveEnvOverride()
		if err != nil || got != key {
			t.Fatalf("ResolveEnvOverride() = %q, %v; want %q, nil", got, err, key)
		}
	})
	t.Run("invalid pin fails closed", func(t *testing.T) {
		t.Setenv(EnvKey, "definitely-not-a-key")
		if _, err := ResolveEnvOverride(); !errors.Is(err, ErrInvalidPinnedKey) {
			t.Fatalf("invalid override must fail closed, got %v", err)
		}
	})
}

func TestEnvVerifier(t *testing.T) {
	key := mustMint(t)
	v, err := NewEnvVerifier(key)
	if err != nil {
		t.Fatalf("NewEnvVerifier: %v", err)
	}
	if err := v.Verify(key); err != nil {
		t.Fatalf("Verify(pinned) = %v, want nil", err)
	}
	if err := v.Verify(key[:len(key)-1] + "A"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Verify(wrong) = %v, want ErrInvalidKey", err)
	}
	if err := v.Verify(""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Verify(empty) = %v, want ErrInvalidKey", err)
	}
	if _, err := NewEnvVerifier("bad"); !errors.Is(err, ErrInvalidPinnedKey) {
		t.Fatalf("NewEnvVerifier(invalid) = %v, want ErrInvalidPinnedKey", err)
	}
}

// TestEnvVerifierPinnedKeyNeverTouchesFilesystem pins the contract that an
// env-pinned verifier works with no key file on disk at all.
func TestEnvVerifierPinnedKeyNeverTouchesFilesystem(t *testing.T) {
	key := mustMint(t)
	v, err := NewEnvVerifier(key)
	if err != nil {
		t.Fatalf("NewEnvVerifier: %v", err)
	}
	// No Store, no file: verification is purely in-memory.
	if err := v.Verify(key); err != nil {
		t.Fatalf("Verify without any key file = %v, want nil", err)
	}
}
