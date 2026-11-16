package config

import (
	"os"
	"strings"
)

// literalTokenPrefixes are the token shapes historically accepted as a literal
// value placed directly in a *.token_env key instead of an environment
// variable name. Kept for compatibility; new configs should reference an
// environment variable.
var literalTokenPrefixes = []string{"ctx_", "ey", "cortex_admin_"}

// ResolveRemoteToken is the single client-side bearer resolution used by
// every component that talks to a remote Cortex server (MCP proxy, hybrid
// replication, one-shot `cortex sync --remote`).
//
// Precedence:
//  1. the environment variable named by tokenEnv;
//  2. tokenEnv itself when it is a literal token (legacy compatibility);
//  3. fallback (the token stored by `cortex auth login` / the TUI connect
//     wizard in http.token).
func ResolveRemoteToken(tokenEnv, fallback string) string {
	tokenEnv = strings.TrimSpace(tokenEnv)
	if tokenEnv != "" {
		if token := strings.TrimSpace(os.Getenv(tokenEnv)); token != "" {
			return token
		}
		if IsLiteralToken(tokenEnv) {
			return tokenEnv
		}
	}
	return strings.TrimSpace(fallback)
}

// IsLiteralToken reports whether value looks like a bearer token rather than an
// environment variable name.
func IsLiteralToken(value string) bool {
	for _, prefix := range literalTokenPrefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
