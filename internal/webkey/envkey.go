package webkey

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"
)

// EnvKey is the environment variable that pins the web access key on
// deployments without a writable state volume (e.g. ephemeral container
// hosts). When set, the key file is never minted or consulted: verification
// uses the pinned secret in memory and the previous key file is ignored.
const EnvKey = "CORTEX_WEB_KEY"

// mintedKeyLength is the exact length of a minted key: the ctx_ namespace
// prefix plus the RawURL-base64 encoding of a 256-bit secret (43 chars). A
// pinned key must match the exact minted shape so an operator typo can never
// silently lock or unlock the surface.
const mintedKeyLength = len(keyNamespace) + 43

var (
	// ErrInvalidPinnedKey is the deterministic fail-closed rejection for any
	// CORTEX_WEB_KEY value that does not match the minted key format.
	ErrInvalidPinnedKey = errors.New("webkey: CORTEX_WEB_KEY does not match the minted key format (ctx_ + 43 base64url characters)")
)

// ValidateSecret enforces the minted key format: the ctx_ namespace prefix
// followed by exactly 43 RawURL-base64 characters decoding to a 256-bit
// secret. It is the fail-closed gate for operator-pinned keys.
func ValidateSecret(secret string) error {
	if len(secret) != mintedKeyLength || !strings.HasPrefix(secret, keyNamespace) {
		return ErrInvalidPinnedKey
	}
	raw, err := base64.RawURLEncoding.DecodeString(secret[len(keyNamespace):])
	if err != nil || len(raw) != secretSize {
		return ErrInvalidPinnedKey
	}
	return nil
}

// ResolveEnvOverride returns the trimmed CORTEX_WEB_KEY override ("" when the
// variable is unset or blank). A non-blank value that fails ValidateSecret is
// an error so callers can abort startup instead of silently regenerating.
func ResolveEnvOverride() (string, error) {
	raw := strings.TrimSpace(os.Getenv(EnvKey))
	if raw == "" {
		return "", nil
	}
	if err := ValidateSecret(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// envVerifier verifies presented keys against an operator-pinned secret
// without touching the filesystem. The pinned secret never leaves memory.
type envVerifier struct {
	secret string
}

var _ Verifier = envVerifier{}

// NewEnvVerifier builds a Verifier for a validated operator-pinned web key.
func NewEnvVerifier(secret string) (Verifier, error) {
	pinned := strings.TrimSpace(secret)
	if err := ValidateSecret(pinned); err != nil {
		return nil, err
	}
	return envVerifier{secret: pinned}, nil
}

// Verify compares the presented key against the pinned secret in constant
// time. The comparison is over the full plaintexts so any character
// difference rejects deterministically.
func (v envVerifier) Verify(presented string) error {
	if presented == "" {
		return ErrInvalidKey
	}
	if !constantTimeEqual([]byte(presented), []byte(v.secret)) {
		return ErrInvalidKey
	}
	return nil
}
