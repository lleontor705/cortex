package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/config"
)

const modeUsage = `Usage:
  cortex mode [show] [--json]                 Show the resolved runtime mode and where it came from
  cortex mode set <mode> [--url URL] [--token-env NAME] [--config PATH]
                                              Persist the mode (local, hybrid, remote, server)
  cortex mode test                            Check the server used by hybrid/remote mode

Modes:
  local   embedded SQLite only
  hybrid  embedded SQLite + background sync to a Cortex server (--url = server base URL)
  remote  ` + "`cortex mcp`" + ` proxies every tool to a Cortex server (--url = server base URL)
  server  run the PostgreSQL-backed Cortex server (start it with ` + "`cortex`" + ` or ` + "`cortex --mode server`" + `)

Precedence: --mode / CORTEX_MODE > mode: in the config file > legacy keys
(mcp.remote.enabled, sync.enabled + sync.url) > local.
`

func runMode(args []string, stdout, stderr io.Writer) int {
	sub := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "show":
		return runModeShow(args, stdout, stderr)
	case "set":
		return runModeSet(args, stdout, stderr)
	case "test":
		return runModeTest(stdout, stderr)
	case "help", "-h", "--help":
		writef(stdout, "%s", modeUsage)
		return 0
	default:
		writef(stderr, "unknown mode subcommand %q\n\n%s", sub, modeUsage)
		return 1
	}
}

// modeReport is the machine-readable form of `cortex mode show --json`.
type modeReport struct {
	config.ModeResolution
	ConfigFile string `json:"config_file"`
}

func loadModeReport() (*config.Config, modeReport, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, modeReport{}, err
	}
	res, err := config.ResolveMode(cfg)
	if err != nil {
		return nil, modeReport{}, err
	}
	file := cfg.LoadedFrom
	if file == "" {
		file = "defaults (no config file loaded)"
	}
	return cfg, modeReport{ModeResolution: res, ConfigFile: file}, nil
}

func runModeShow(args []string, stdout, stderr io.Writer) int {
	jsonOutput := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOutput = true
		default:
			writef(stderr, "unknown flag %q\n\n%s", arg, modeUsage)
			return 1
		}
	}
	_, report, err := loadModeReport()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	if jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			writef(stderr, "cortex: %v\n", err)
			return 1
		}
		writeln(stdout, string(data))
		return 0
	}
	writeModeReport(stdout, report)
	return 0
}

func writeModeReport(w io.Writer, report modeReport) {
	writef(w, "Mode:        %s\n", report.Mode)
	writef(w, "Source:      %s\n", report.SourceLabel)
	writef(w, "Description: %s\n", report.Description)
	writef(w, "Config file: %s\n", report.ConfigFile)
	if report.RemoteURL != "" {
		writef(w, "Server URL:  %s\n", report.RemoteURL)
	}
	if report.MCPURL != "" {
		writef(w, "MCP URL:     %s\n", report.MCPURL)
	}
	if len(report.Warnings) > 0 {
		writeln(w, "Warnings:")
		for _, warning := range report.Warnings {
			writef(w, "  - %s\n", warning)
		}
	}
}

func runModeSet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		writef(stderr, "cortex mode set: missing mode\n\n%s", modeUsage)
		return 1
	}
	mode, err := config.ParseRuntimeMode(args[0])
	if err != nil {
		writef(stderr, "cortex mode set: %v\n", err)
		return 1
	}
	var rawURL, tokenEnv, configPath string
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		arg := rest[i]
		value := func() (string, bool) {
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				return arg[eq+1:], true
			}
			if i+1 < len(rest) {
				i++
				return rest[i], true
			}
			return "", false
		}
		var ok bool
		switch {
		case arg == "--url" || strings.HasPrefix(arg, "--url="):
			rawURL, ok = value()
		case arg == "--token-env" || strings.HasPrefix(arg, "--token-env="):
			tokenEnv, ok = value()
		case arg == "--config" || strings.HasPrefix(arg, "--config="):
			configPath, ok = value()
		default:
			writef(stderr, "cortex mode set: unknown flag %q\n\n%s", arg, modeUsage)
			return 1
		}
		if !ok {
			writef(stderr, "cortex mode set: %s requires a value\n", arg)
			return 1
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		writef(stderr, "cortex mode set: load config: %v\n", err)
		return 1
	}
	if err := applyModeSettings(cfg, mode, rawURL, tokenEnv); err != nil {
		writef(stderr, "cortex mode set: %v\n", err)
		return 1
	}
	if err := config.Validate(cfg); err != nil {
		writef(stderr, "cortex mode set: %v\n", err)
		return 1
	}
	path := cfg.LoadedFrom
	if path == "" {
		path = filepath.Join(config.CortexDir(), "cortex.yaml")
	}
	if err := config.Save(cfg, path); err != nil {
		writef(stderr, "cortex mode set: save config: %v\n", err)
		return 1
	}

	writef(stdout, "Saved mode %q to %s\n\n", mode, path)
	res, err := config.ResolveMode(cfg)
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	writeModeReport(stdout, modeReport{ModeResolution: res, ConfigFile: path})
	if env := strings.TrimSpace(os.Getenv(config.ModeEnvVar)); env != "" && res.Mode != mode {
		writef(stdout, "\nNote: %s=%s overrides the file in this shell.\n", config.ModeEnvVar, env)
	}
	switch mode {
	case config.RuntimeHybrid, config.RuntimeRemote:
		writeln(stdout, "\nNext: run `cortex mode test`, then restart your agents so `cortex mcp` picks up the new mode.")
	case config.RuntimeServer:
		writeln(stdout, "\nNext: start the server with `cortex` (no arguments) or `cortex --mode server`; server.* settings are required.")
	default:
		writeln(stdout, "\nNext: restart your agents so `cortex mcp` picks up the new mode.")
	}
	return 0
}

// applyModeSettings writes the mode key and keeps the legacy switches
// consistent with it, so older readers of mcp.remote.enabled / sync.enabled
// observe the same behavior the resolver selects. URLs are kept when switching
// away so a later `mode set` can reuse them.
func applyModeSettings(cfg *config.Config, mode config.RuntimeMode, rawURL, tokenEnv string) error {
	base := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	base = strings.TrimSuffix(base, "/mcp")
	if rawURL != "" && base == "" {
		return fmt.Errorf("--url must not be empty")
	}
	tokenEnv = strings.TrimSpace(tokenEnv)
	cfg.Mode = string(mode)

	switch mode {
	case config.RuntimeHybrid:
		if base != "" {
			cfg.Sync.URL = base
		}
		if strings.TrimSpace(cfg.Sync.URL) == "" {
			return fmt.Errorf("hybrid mode needs a server URL: pass --url https://your-cortex-server")
		}
		if tokenEnv != "" {
			cfg.Sync.TokenEnv = tokenEnv
		}
		cfg.Sync.Enabled = true
		cfg.MCP.Remote.Enabled = false
	case config.RuntimeRemote:
		if base != "" {
			cfg.MCP.Remote.URL = base + "/mcp"
		}
		if strings.TrimSpace(cfg.MCP.Remote.URL) == "" {
			if syncURL := strings.TrimRight(strings.TrimSpace(cfg.Sync.URL), "/"); syncURL != "" {
				cfg.MCP.Remote.URL = syncURL + "/mcp"
			} else {
				return fmt.Errorf("remote mode needs a server URL: pass --url https://your-cortex-server")
			}
		}
		if tokenEnv != "" {
			cfg.MCP.Remote.TokenEnv = tokenEnv
		}
		cfg.MCP.Remote.Enabled = true
		cfg.Sync.Enabled = false
	case config.RuntimeLocal, config.RuntimeServer:
		if base != "" || tokenEnv != "" {
			return fmt.Errorf("--url and --token-env only apply to hybrid and remote modes")
		}
		cfg.MCP.Remote.Enabled = false
		cfg.Sync.Enabled = false
	}
	return nil
}

func runModeTest(stdout, stderr io.Writer) int {
	_, report, err := loadModeReport()
	if err != nil {
		writef(stderr, "cortex: %v\n", err)
		return 1
	}
	switch report.Mode {
	case config.RuntimeHybrid, config.RuntimeRemote:
		if report.RemoteURL == "" {
			writef(stderr, "cortex mode test: %s mode has no server URL configured (run `cortex mode set %s --url URL`)\n", report.Mode, report.Mode)
			return 1
		}
		writef(stdout, "Mode: %s (%s)\n\n", report.Mode, report.SourceLabel)
		return runDoctorServer(report.RemoteURL, stdout, stderr)
	case config.RuntimeServer:
		writeln(stdout, "Mode: server — check a running server with `cortex doctor --server URL`.")
		return 0
	default:
		writeln(stdout, "Mode: local — nothing remote to test. Run `cortex doctor` for local checks.")
		return 0
	}
}
