package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// RuntimeMode is the single vocabulary for how a Cortex process runs.
//
//   - local:  embedded SQLite only.
//   - hybrid: embedded SQLite plus background replication to a Cortex server.
//   - remote: thin client; `cortex mcp` proxies every tool to a Cortex server.
//   - server: the PostgreSQL-backed Cortex server itself.
type RuntimeMode string

const (
	RuntimeLocal  RuntimeMode = "local"
	RuntimeHybrid RuntimeMode = "hybrid"
	RuntimeRemote RuntimeMode = "remote"
	RuntimeServer RuntimeMode = "server"
)

// RuntimeModes lists the valid modes in display order.
var RuntimeModes = []RuntimeMode{RuntimeLocal, RuntimeHybrid, RuntimeRemote, RuntimeServer}

// ModeEnvVar is the environment variable that selects the runtime mode.
const ModeEnvVar = "CORTEX_MODE"

// ParseRuntimeMode validates raw (case-insensitive, trimmed) against the
// closed set of modes.
func ParseRuntimeMode(raw string) (RuntimeMode, error) {
	mode := RuntimeMode(strings.ToLower(strings.TrimSpace(raw)))
	for _, valid := range RuntimeModes {
		if mode == valid {
			return mode, nil
		}
	}
	return "", fmt.Errorf("unknown mode %q: use local, hybrid, remote, or server", strings.TrimSpace(raw))
}

// ModeSource says which input decided the resolved mode.
type ModeSource string

const (
	ModeSourceEnv     ModeSource = "env"     // CORTEX_MODE (also set by the --mode flag)
	ModeSourceFile    ModeSource = "file"    // top-level `mode:` key
	ModeSourceLegacy  ModeSource = "legacy"  // derived from mcp.remote.enabled / sync.enabled
	ModeSourceDefault ModeSource = "default" // nothing configured
)

// ModeResolution is the outcome of ResolveMode: the mode every subsystem
// executes AND reports, where it came from, and non-fatal inconsistencies.
type ModeResolution struct {
	Mode        RuntimeMode `json:"mode"`
	Source      ModeSource  `json:"source"`
	SourceLabel string      `json:"source_label"`
	Description string      `json:"description"`
	// RemoteURL is the server base URL for hybrid/remote (no /mcp suffix).
	RemoteURL string `json:"remote_url,omitempty"`
	// MCPURL is the remote MCP endpoint used by `cortex mcp` in remote mode.
	MCPURL   string   `json:"mcp_url,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ResolveMode is the single mode resolver. Precedence:
//
//  1. CORTEX_MODE (the global --mode flag is exported to it by cmd/cortex);
//  2. the top-level `mode:` key in the config file;
//  3. legacy keys: mcp.remote.enabled → remote, sync.enabled + sync.url → hybrid;
//  4. local.
//
// An invalid CORTEX_MODE or `mode:` value is returned as an error so callers
// fail closed instead of silently running a different mode.
func ResolveMode(cfg *Config) (ModeResolution, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	var res ModeResolution

	switch env := strings.TrimSpace(os.Getenv(ModeEnvVar)); {
	case env != "":
		mode, err := ParseRuntimeMode(env)
		if err != nil {
			return ModeResolution{}, fmt.Errorf("%s: %w", ModeEnvVar, err)
		}
		res.Mode, res.Source, res.SourceLabel = mode, ModeSourceEnv, ModeEnvVar+" / --mode"
	case strings.TrimSpace(cfg.Mode) != "":
		mode, err := ParseRuntimeMode(cfg.Mode)
		if err != nil {
			return ModeResolution{}, fmt.Errorf("config mode: %w", err)
		}
		res.Mode, res.Source, res.SourceLabel = mode, ModeSourceFile, "config file (mode:)"
	case cfg.MCP.Remote.Enabled:
		res.Mode, res.Source, res.SourceLabel = RuntimeRemote, ModeSourceLegacy, "legacy mcp.remote.enabled"
	case cfg.Sync.Enabled && strings.TrimSpace(cfg.Sync.URL) != "":
		res.Mode, res.Source, res.SourceLabel = RuntimeHybrid, ModeSourceLegacy, "legacy sync.enabled + sync.url"
	default:
		res.Mode, res.Source, res.SourceLabel = RuntimeLocal, ModeSourceDefault, "default"
	}

	res.RemoteURL, res.MCPURL = remoteEndpoints(cfg, res.Mode)
	res.Description = describeMode(res)
	res.Warnings = modeWarnings(cfg, res)
	return res, nil
}

// remoteEndpoints derives the server base URL and MCP endpoint for a mode.
// Remote prefers mcp.remote.url and falls back to sync.url + "/mcp"; hybrid
// uses sync.url.
func remoteEndpoints(cfg *Config, mode RuntimeMode) (base, mcpURL string) {
	syncURL := strings.TrimRight(strings.TrimSpace(cfg.Sync.URL), "/")
	remoteURL := strings.TrimRight(strings.TrimSpace(cfg.MCP.Remote.URL), "/")
	switch mode {
	case RuntimeRemote:
		switch {
		case remoteURL != "":
			return strings.TrimSuffix(remoteURL, "/mcp"), remoteURL
		case syncURL != "":
			return syncURL, syncURL + "/mcp"
		}
	case RuntimeHybrid:
		return syncURL, ""
	}
	return "", ""
}

func describeMode(res ModeResolution) string {
	switch res.Mode {
	case RuntimeHybrid:
		target := res.RemoteURL
		if target == "" {
			target = "no sync.url configured"
		}
		return "Hybrid (local SQLite + background sync to " + target + ")"
	case RuntimeRemote:
		target := res.MCPURL
		if target == "" {
			target = "no remote URL configured"
		}
		return "Remote (MCP tools proxied to " + target + ")"
	case RuntimeServer:
		return "Server (PostgreSQL with row-level security)"
	default:
		return "Local (embedded SQLite)"
	}
}

func modeWarnings(cfg *Config, res ModeResolution) []string {
	var warnings []string
	switch res.Mode {
	case RuntimeHybrid:
		if res.RemoteURL == "" {
			warnings = append(warnings, "hybrid mode without sync.url: background sync cannot start (run `cortex mode set hybrid --url URL`)")
		}
		if cfg.MCP.Remote.Enabled {
			warnings = append(warnings, "mcp.remote.enabled is ignored in hybrid mode; `cortex mcp` serves the local database")
		}
	case RuntimeRemote:
		if res.MCPURL == "" {
			warnings = append(warnings, "remote mode without a server URL: `cortex mcp` cannot start (run `cortex mode set remote --url URL`)")
		}
		if cfg.Sync.Enabled {
			warnings = append(warnings, "sync.enabled is ignored in remote mode; background sync does not run")
		}
	case RuntimeLocal:
		if res.Source != ModeSourceDefault && (cfg.MCP.Remote.Enabled || cfg.Sync.Enabled) {
			warnings = append(warnings, "remote settings (mcp.remote / sync) are ignored in local mode")
		}
	}
	if res.Mode != RuntimeServer && strings.TrimSpace(cfg.Server.Storage.DSN) != "" {
		warnings = append(warnings, "server.storage.dsn is set but only `cortex --mode server` uses it")
	}
	return warnings
}

// DetectMode reports the resolved mode and its description. It is kept for
// existing callers; an invalid mode setting is reported in the description
// instead of being silently replaced.
func (c *Config) DetectMode() (OperatingMode, string) {
	res, err := ResolveMode(c)
	if err != nil {
		return ModeLocal, "Invalid mode configuration: " + err.Error()
	}
	return OperatingMode(res.Mode), res.Description
}

// FileMode reads only the top-level `mode:` key of the config file that Load
// would pick (same search order), without applying defaults, environment
// overrides, or writing a default file. cmd/cortex uses it to route a
// `mode: server` config to the server composition before any local state is
// opened. It returns "" when no file exists or the key is absent.
func FileMode() (RuntimeMode, string, error) {
	v := viper.New()
	v.SetConfigName("cortex")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath(CortexDir())
	v.AddConfigPath("/etc/cortex")
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("error reading config file: %w", err)
	}
	raw := strings.TrimSpace(v.GetString("mode"))
	if raw == "" {
		return "", v.ConfigFileUsed(), nil
	}
	mode, err := ParseRuntimeMode(raw)
	if err != nil {
		return "", v.ConfigFileUsed(), fmt.Errorf("%s: %w", v.ConfigFileUsed(), err)
	}
	return mode, v.ConfigFileUsed(), nil
}
