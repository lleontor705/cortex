package backup

import (
	"bytes"
	"os"
	"strings"
)

// Secret is a named credential value the caller asserts must never appear in
// an exported archive. Names are only used in error messages.
type Secret struct {
	Name  string
	Value string
}

// KnownEnvSecrets is the ordered list of Cortex credential environment
// variables whose values must never leak into a logical backup.
var KnownEnvSecrets = []string{
	"CORTEX_EMBEDDING_API_KEY",
	"CORTEX_LLM_API_KEY",
	"CORTEX_RERANK_API_KEY",
	"CORTEX_HTTP_TOKEN",
	"CORTEX_REMOTE_TOKEN",
	"CORTEX_WEB_KEY",
	"CORTEX_SYNC_TOKEN",
	"CORTEX_POSTGRES_DSN",
	"CORTEX_TEST_POSTGRES_DSN",
	"CORTEX_TEST_POSTGRES_MIGRATION_DSN",
	"CORTEX_TEST_POSTGRES_AUTHZ_ADMIN_DSN",
}

// CollectEnvSecrets gathers non-trivial (>= minSecretLen runes) values for the
// known credential environment variables plus any extra (name, value) pairs
// supplied by the caller (e.g. tokens read from configuration).
func CollectEnvSecrets(extra ...Secret) []Secret {
	const minSecretLen = 8
	var out []Secret
	for _, name := range KnownEnvSecrets {
		if v := strings.TrimSpace(os.Getenv(name)); len(v) >= minSecretLen {
			out = append(out, Secret{Name: name, Value: v})
		}
	}
	for _, s := range extra {
		if s.Value == "" || len(strings.TrimSpace(s.Value)) < minSecretLen {
			continue
		}
		out = append(out, s)
	}
	return out
}

// assertNoSecrets fails closed if any configured secret value appears in the
// serialized payload bytes.
func assertNoSecrets(payload []byte, secrets []Secret) error {
	for _, s := range secrets {
		if s.Value == "" {
			continue
		}
		if bytes.Contains(payload, []byte(s.Value)) {
			return &ErrSecretDetected{Name: s.Name}
		}
	}
	return nil
}

// formatSecretNames renders the guarded secret names for diagnostics.
func formatSecretNames(secrets []Secret) string {
	names := make([]string, 0, len(secrets))
	for _, s := range secrets {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}
