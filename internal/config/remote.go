package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lleontor705/cortex/v2/internal/credential"
)

// CurrentConfigVersion is the config schema written by `cortex config migrate`
// and `cortex connect`. Version 1 (no `version:` key) keeps loading unchanged.
const CurrentConfigVersion = 2

// RemoteTokenKeychain is the remote.token value that reads the bearer from
// the OS keychain entry for remote.url.
const RemoteTokenKeychain = "keychain"

// RemoteConfig is the single description of the Cortex server a client talks
// to, shared by hybrid sync and the remote MCP proxy (schema v2). It replaces
// the duplicated sync.url / sync.token_env / mcp.remote.url /
// mcp.remote.token_env pairs.
type RemoteConfig struct {
	// URL is the server base URL (no /mcp suffix).
	URL string `yaml:"url,omitempty" json:"url,omitempty" toml:"url,omitempty" mapstructure:"url"`
	// Token says where the bearer lives: "keychain" (OS keychain entry for
	// URL) or "env:NAME" (environment variable). Empty falls back to the
	// legacy token_env keys and http.token.
	Token string `yaml:"token,omitempty" json:"token,omitempty" toml:"token,omitempty" mapstructure:"token"`
	// Timeout bounds every request to the server (0 keeps the legacy value).
	Timeout time.Duration `yaml:"timeout,omitempty" json:"timeout,omitempty" toml:"timeout,omitempty" mapstructure:"timeout"`
}

// remoteDerived records the legacy values applyRemoteBlock derived from the
// remote block, so Save can tell a real edit of a legacy key (TUI, config set)
// from the derived copy and fold it back into the block.
type remoteDerived struct {
	syncURL, mcpURL string
}

// NormalizeServerURL trims whitespace, trailing slashes and a /mcp suffix.
func NormalizeServerURL(raw string) string {
	base := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimRight(strings.TrimSuffix(base, "/mcp"), "/")
}

// applyRemoteBlock projects remote.* onto the legacy keys every consumer
// reads (sync.url, mcp.remote.url, token_env, timeouts) in memory only.
func applyRemoteBlock(cfg *Config) {
	cfg.remote = remoteDerived{}
	base := NormalizeServerURL(cfg.Remote.URL)
	if base == "" {
		return
	}
	cfg.Remote.URL = base
	cfg.Sync.URL = base
	cfg.MCP.Remote.URL = base + "/mcp"
	if name, ok := strings.CutPrefix(cfg.Remote.Token, "env:"); ok && strings.TrimSpace(name) != "" {
		cfg.Sync.TokenEnv = strings.TrimSpace(name)
		cfg.MCP.Remote.TokenEnv = strings.TrimSpace(name)
	}
	if cfg.Remote.Timeout > 0 {
		cfg.Sync.Timeout = cfg.Remote.Timeout
		cfg.MCP.Remote.Timeout = cfg.Remote.Timeout
	}
	cfg.remote = remoteDerived{syncURL: cfg.Sync.URL, mcpURL: cfg.MCP.Remote.URL}
}

// foldRemoteBlock prepares the on-disk form of cfg: an edited legacy URL
// (one that differs from the value derived at load) becomes the new
// remote.url, and the derived legacy URLs are not persisted.
func foldRemoteBlock(cfg *Config) {
	if NormalizeServerURL(cfg.Remote.URL) == "" {
		return
	}
	switch {
	case strings.TrimSpace(cfg.Sync.URL) != "" && cfg.Sync.URL != cfg.remote.syncURL:
		cfg.Remote.URL = NormalizeServerURL(cfg.Sync.URL)
	case strings.TrimSpace(cfg.MCP.Remote.URL) != "" && cfg.MCP.Remote.URL != cfg.remote.mcpURL:
		cfg.Remote.URL = NormalizeServerURL(cfg.MCP.Remote.URL)
	}
	cfg.Remote.URL = NormalizeServerURL(cfg.Remote.URL)
	cfg.Sync.URL = ""
	cfg.MCP.Remote.URL = ""
}

// RemoteServerURL returns the configured server base URL: remote.url, else
// the legacy sync.url / mcp.remote.url.
func (c *Config) RemoteServerURL() string {
	for _, raw := range []string{c.Remote.URL, c.Sync.URL, c.MCP.Remote.URL} {
		if base := NormalizeServerURL(raw); base != "" {
			return base
		}
	}
	return ""
}

// ResolveRemoteBearer is the single client-side bearer lookup for talking to
// the Cortex server. legacyTokenEnv is the token_env key of the caller
// (sync.token_env or mcp.remote.token_env). It returns the token and a short
// description of where it came from (never the token itself).
//
// Precedence:
//  1. remote.token: keychain → the OS keychain entry for the server URL;
//  2. remote.token: env:NAME → that environment variable;
//  3. the legacy chain (ResolveRemoteToken): env var named by legacyTokenEnv,
//     a literal token in that key, then http.token.
func ResolveRemoteBearer(cfg *Config, legacyTokenEnv string) (token, source string, err error) {
	if cfg == nil {
		return "", "", errors.New("configuration not available")
	}
	ref := strings.TrimSpace(cfg.Remote.Token)
	switch {
	case ref == RemoteTokenKeychain:
		server := cfg.RemoteServerURL()
		if server == "" {
			return "", "", errors.New("remote.token is keychain but no remote.url is configured")
		}
		token, err := credential.Get(server)
		if err != nil {
			return "", "", fmt.Errorf("remote token from OS keychain: %w (run `cortex connect %s`)", err, server)
		}
		return token, "OS keychain", nil
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(ref, "env:"))
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, "env " + name, nil
		}
		return "", "", fmt.Errorf("remote token environment variable %s is empty", name)
	case ref != "":
		return "", "", fmt.Errorf("invalid remote.token %q: use %q or \"env:NAME\"", ref, RemoteTokenKeychain)
	}
	if token := ResolveRemoteToken(legacyTokenEnv, ""); token != "" {
		if IsLiteralToken(strings.TrimSpace(legacyTokenEnv)) {
			return token, "literal token in config (run `cortex config migrate`)", nil
		}
		return token, "env " + strings.TrimSpace(legacyTokenEnv), nil
	}
	if token := strings.TrimSpace(cfg.HTTP.Token); token != "" {
		return token, "http.token in config (run `cortex config migrate`)", nil
	}
	return "", "", nil
}

func validateRemote(cfg *Config) error {
	ref := strings.TrimSpace(cfg.Remote.Token)
	if ref != "" && ref != RemoteTokenKeychain && !strings.HasPrefix(ref, "env:") {
		return fmt.Errorf("remote.token must be %q or \"env:NAME\", got %q (tokens are never stored in the config file)", RemoteTokenKeychain, ref)
	}
	if name, ok := strings.CutPrefix(ref, "env:"); ok && strings.TrimSpace(name) == "" {
		return errors.New("remote.token env: reference needs a variable name")
	}
	if ref != "" && NormalizeServerURL(cfg.Remote.URL) == "" {
		return errors.New("remote.token is set but remote.url is empty")
	}
	if cfg.Remote.Timeout < 0 {
		return errors.New("remote.timeout must not be negative")
	}
	if cfg.Version != 0 && cfg.Version != 1 && cfg.Version != CurrentConfigVersion {
		return fmt.Errorf("unsupported config version %d (this Cortex understands 1 and %d)", cfg.Version, CurrentConfigVersion)
	}
	return nil
}

// SetRemote points the config at a Cortex server (schema v2): it records the
// base URL and token reference, marks the file as version 2, and re-derives
// the legacy keys in memory so validation and consumers see them at once.
func (c *Config) SetRemote(serverURL, tokenRef string) {
	c.Version = CurrentConfigVersion
	c.Remote.URL = NormalizeServerURL(serverURL)
	c.Remote.Token = strings.TrimSpace(tokenRef)
	applyRemoteBlock(c)
}
