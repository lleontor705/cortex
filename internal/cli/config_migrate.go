package cli

import (
	"io"
	"os"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/credential"
)

const migrateUsage = `Usage: cortex config migrate [--dry-run] [--config PATH]

Upgrades the config file to schema v2:
  - writes an explicit mode: key (from the legacy mcp.remote.enabled / sync.enabled);
  - replaces sync.url / mcp.remote.url with one remote.url;
  - moves a plaintext token found in sync.token_env / mcp.remote.token_env into
    the OS keychain (remote.token: keychain), or references the token
    environment variable (remote.token: env:NAME).
Unknown keys and comments are preserved. --dry-run prints the plan only.
`

func runConfigMigrate(args []string, stdout, stderr io.Writer) int {
	dryRun := false
	configPath := ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--dry-run":
			dryRun = true
		case (arg == "--config" || arg == "--file" || arg == "-f") && i+1 < len(args):
			i++
			configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "-h" || arg == "--help" || arg == "help":
			writef(stdout, "%s", migrateUsage)
			return 0
		default:
			writef(stderr, "cortex config migrate: unknown argument %q\n\n%s", arg, migrateUsage)
			return 1
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		writef(stderr, "cortex config migrate: %v\n", err)
		return 1
	}
	res, err := config.ResolveFileMode(cfg)
	if err != nil {
		writef(stderr, "cortex config migrate: %v\n", err)
		return 1
	}

	var plan, notes []string
	if cfg.Mode == "" && res.Source != config.ModeSourceDefault {
		plan = append(plan, "set mode: "+string(res.Mode)+" (was derived from "+res.SourceLabel+")")
		cfg.Mode = string(res.Mode)
	}

	server := cfg.RemoteServerURL()
	switch res.Mode {
	case config.RuntimeRemote:
		if base := config.NormalizeServerURL(cfg.MCP.Remote.URL); base != "" {
			server = base
		}
	case config.RuntimeHybrid:
		if base := config.NormalizeServerURL(cfg.Sync.URL); base != "" {
			server = base
		}
	}
	syncBase, mcpBase := config.NormalizeServerURL(cfg.Sync.URL), config.NormalizeServerURL(cfg.MCP.Remote.URL)
	if syncBase != "" && mcpBase != "" && syncBase != mcpBase {
		notes = append(notes, "sync.url and mcp.remote.url pointed to different servers; kept "+server)
	}

	tokenRef, keychainToken := cfg.Remote.Token, ""
	if server != "" && tokenRef == "" {
		legacyEnv := strings.TrimSpace(cfg.Sync.TokenEnv)
		if res.Mode == config.RuntimeRemote {
			legacyEnv = strings.TrimSpace(cfg.MCP.Remote.TokenEnv)
		}
		switch {
		case config.IsLiteralToken(legacyEnv):
			keychainToken, tokenRef = legacyEnv, config.RemoteTokenKeychain
			plan = append(plan, "move the plaintext token from token_env into the OS keychain")
			cfg.Sync.TokenEnv, cfg.MCP.Remote.TokenEnv = "CORTEX_REMOTE_TOKEN", "CORTEX_REMOTE_TOKEN"
		case legacyEnv != "" && strings.TrimSpace(os.Getenv(legacyEnv)) != "":
			tokenRef = "env:" + legacyEnv
			plan = append(plan, "set remote.token: env:"+legacyEnv)
		case strings.TrimSpace(cfg.HTTP.Token) != "" && (res.Mode == config.RuntimeRemote || res.Mode == config.RuntimeHybrid):
			keychainToken, tokenRef = strings.TrimSpace(cfg.HTTP.Token), config.RemoteTokenKeychain
			plan = append(plan, "copy the server token from http.token into the OS keychain")
			notes = append(notes, "http.token still holds a plaintext copy (it is also the local `cortex serve` token); if you do not use `cortex serve`, clear it with `cortex config set http.token \"\"`")
		}
	}
	if server != "" && config.NormalizeServerURL(cfg.Remote.URL) != server {
		plan = append(plan, "set remote.url: "+server+" (replaces sync.url / mcp.remote.url)")
	}
	if cfg.Version != config.CurrentConfigVersion {
		plan = append(plan, "set version: 2")
	}

	if len(plan) == 0 {
		writef(stdout, "%s is already on schema v2; nothing to migrate.\n", displayConfigPath(cfg))
		return 0
	}
	writef(stdout, "Migration plan for %s:\n", displayConfigPath(cfg))
	for _, step := range plan {
		writef(stdout, "  - %s\n", step)
	}
	for _, note := range notes {
		writef(stdout, "  note: %s\n", note)
	}
	if dryRun {
		writeln(stdout, "\nDry run: nothing was changed.")
		return 0
	}

	if keychainToken != "" {
		if err := credential.Set(server, keychainToken); err != nil {
			writef(stderr, "cortex config migrate: %v\nNothing was changed; export the token and rerun, or keep the current file.\n", err)
			return 1
		}
	}
	if server != "" {
		cfg.SetRemote(server, tokenRef)
	} else {
		cfg.Version = config.CurrentConfigVersion
	}
	if err := config.Validate(cfg); err != nil {
		writef(stderr, "cortex config migrate: %v\n", err)
		return 1
	}
	if err := config.Save(cfg, cfg.LoadedFrom); err != nil {
		writef(stderr, "cortex config migrate: save: %v\n", err)
		return 1
	}
	writef(stdout, "\nMigrated %s to schema v2. Check it with `cortex mode`.\n", cfg.LoadedFrom)
	return 0
}

func displayConfigPath(cfg *config.Config) string {
	if cfg.LoadedFrom == "" {
		return "the default config"
	}
	return cfg.LoadedFrom
}
