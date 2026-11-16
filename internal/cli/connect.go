package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/credential"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
)

const connectUsage = `Usage:
  cortex connect <server-url> [--as remote|hybrid] [--token-stdin | --token-env NAME] [--config PATH]
  cortex disconnect [--keep-mode] [--config PATH]

connect verifies the server and the token (/health, /api/me) BEFORE saving
anything, stores the token in the OS keychain (or references --token-env),
and writes mode + remote.url + remote.token to the config file.

  --as remote   ` + "`cortex mcp`" + ` proxies every tool to the server (default)
  --as hybrid   keep the local database and sync it to the server in the background

The token is read from --token-env, from stdin when piped (or --token-stdin),
or from a hidden prompt. It is never accepted as a command-line argument.
`

// connectStdin and connectIsTerminal are seams for tests.
var (
	connectStdin      io.Reader = os.Stdin
	connectIsTerminal           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	connectReadSecret           = func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }
)

type connectOptions struct {
	url, as, tokenEnv, configPath string
	tokenStdin                    bool
}

func parseConnectArgs(args []string) (connectOptions, error) {
	opts := connectOptions{as: string(config.RuntimeRemote)}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if eq := strings.IndexByte(arg, '='); eq >= 0 {
				return arg[eq+1:], nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			i++
			return args[i], nil
		}
		var err error
		switch {
		case arg == "--as" || strings.HasPrefix(arg, "--as="):
			opts.as, err = value()
		case arg == "--token-env" || strings.HasPrefix(arg, "--token-env="):
			opts.tokenEnv, err = value()
		case arg == "--config" || strings.HasPrefix(arg, "--config="):
			opts.configPath, err = value()
		case arg == "--token-stdin":
			opts.tokenStdin = true
		case arg == "--token" || strings.HasPrefix(arg, "--token="):
			return opts, errors.New("--token is not accepted (it would leak into shell history); pipe the token on stdin, use the prompt, or --token-env NAME")
		case strings.HasPrefix(arg, "-"):
			return opts, fmt.Errorf("unknown flag %q", arg)
		case opts.url == "":
			opts.url = arg
		default:
			return opts, fmt.Errorf("unexpected argument %q", arg)
		}
		if err != nil {
			return opts, err
		}
	}
	if opts.url == "" {
		return opts, errors.New("missing server URL")
	}
	switch config.RuntimeMode(strings.ToLower(strings.TrimSpace(opts.as))) {
	case config.RuntimeRemote, config.RuntimeHybrid:
		opts.as = strings.ToLower(strings.TrimSpace(opts.as))
	default:
		return opts, fmt.Errorf("--as must be remote or hybrid, got %q", opts.as)
	}
	if opts.tokenEnv != "" && opts.tokenStdin {
		return opts, errors.New("use either --token-env or --token-stdin, not both")
	}
	return opts, nil
}

func runConnect(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		writef(stdout, "%s", connectUsage)
		return 0
	}
	opts, err := parseConnectArgs(args)
	if err != nil {
		writef(stderr, "cortex connect: %v\n\n%s", err, connectUsage)
		return 1
	}
	base := config.NormalizeServerURL(opts.url)
	if err := transportpolicy.ValidateBearerDestination(base); err != nil {
		writef(stderr, "cortex connect: refusing %s: %v\n", base, err)
		return 1
	}

	token, tokenRef, err := acquireConnectToken(opts, stdout)
	if err != nil {
		writef(stderr, "cortex connect: %v\n", err)
		return 1
	}

	writef(stdout, "Checking %s ...\n", base)
	identity, err := verifyServerToken(base, token)
	if err != nil {
		writef(stderr, "cortex connect: %v\nNothing was saved.\n", err)
		return 1
	}
	writef(stdout, "  [OK] server reachable\n  [OK] token accepted%s\n", identity)

	cfg, err := config.Load(opts.configPath)
	if err != nil {
		writef(stderr, "cortex connect: load config: %v\n", err)
		return 1
	}
	if tokenRef == config.RemoteTokenKeychain {
		if err := credential.Set(base, token); err != nil {
			writef(stderr, "cortex connect: %v\nThe OS keychain is unavailable; export the token and use --token-env NAME instead. Nothing was saved.\n", err)
			return 1
		}
	}
	cleared := strings.TrimSpace(cfg.HTTP.Token) == token
	if cleared {
		// An older `cortex auth login` / TUI connect left this remote token in
		// plaintext; it now lives in the keychain (or env) instead.
		cfg.HTTP.Token = ""
	}
	cfg.SetRemote(base, tokenRef)
	cfg.Mode = opts.as
	if opts.as == string(config.RuntimeRemote) {
		cfg.MCP.Remote.Enabled, cfg.Sync.Enabled = true, false
	} else {
		cfg.MCP.Remote.Enabled, cfg.Sync.Enabled = false, true
	}
	if err := config.Validate(cfg); err != nil {
		writef(stderr, "cortex connect: %v\n", err)
		return 1
	}
	path := cfg.LoadedFrom
	if path == "" {
		path = filepath.Join(config.CortexDir(), "cortex.yaml")
	}
	if err := config.Save(cfg, path); err != nil {
		writef(stderr, "cortex connect: save config: %v\n", err)
		return 1
	}

	where := "OS keychain"
	if tokenRef != config.RemoteTokenKeychain {
		where = "environment variable " + strings.TrimPrefix(tokenRef, "env:")
	}
	writef(stdout, "\nConnected: mode %s, server %s\n  token:  %s\n  config: %s\n", opts.as, base, where, path)
	if cleared {
		writeln(stdout, "  removed the plaintext copy of this token from http.token")
	}
	writeln(stdout, "\nRestart your agents so `cortex mcp` picks up the new mode. `cortex mode` shows the result.")
	return 0
}

// acquireConnectToken returns the bearer and its config reference. The token
// is never taken from argv.
func acquireConnectToken(opts connectOptions, stdout io.Writer) (token, ref string, err error) {
	if name := strings.TrimSpace(opts.tokenEnv); name != "" {
		token = strings.TrimSpace(os.Getenv(name))
		if token == "" {
			return "", "", fmt.Errorf("environment variable %s is empty", name)
		}
		return token, "env:" + name, nil
	}
	if opts.tokenStdin || !connectIsTerminal() {
		line, err := bufio.NewReader(io.LimitReader(connectStdin, 64<<10)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", "", fmt.Errorf("read token from stdin: %w", err)
		}
		token = strings.TrimSpace(line)
	} else {
		writef(stdout, "Token for the Cortex server (input hidden): ")
		secret, err := connectReadSecret()
		writeln(stdout, "")
		if err != nil {
			return "", "", fmt.Errorf("read token: %w", err)
		}
		token = strings.TrimSpace(string(secret))
	}
	if token == "" {
		return "", "", errors.New("empty token")
	}
	return token, config.RemoteTokenKeychain, nil
}

// verifyServerToken checks /health and /api/me with the bearer and returns a
// short identity suffix for display (" as <id> (roles)").
func verifyServerToken(base, token string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: transportpolicy.CheckBearerRedirect}
	resp, err := client.Get(base + "/health")
	if err != nil {
		return "", fmt.Errorf("server unreachable: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server health returned HTTP %d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodGet, base+"/api/me", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = client.Do(req)
	if err != nil {
		return "", fmt.Errorf("identity check failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return "", fmt.Errorf("the server rejected the token (HTTP %d)", resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return "", fmt.Errorf("identity check returned HTTP %d", resp.StatusCode)
	}
	var me struct {
		ID    string   `json:"id"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&me); err != nil || me.ID == "" {
		return "", nil
	}
	suffix := " as " + me.ID
	if len(me.Roles) > 0 {
		suffix += " (" + strings.Join(me.Roles, ", ") + ")"
	}
	return suffix, nil
}

func runDisconnect(args []string, stdout, stderr io.Writer) int {
	keepMode := false
	configPath := ""
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--keep-mode":
			keepMode = true
		case arg == "--config" && i+1 < len(args):
			i++
			configPath = args[i]
		case strings.HasPrefix(arg, "--config="):
			configPath = strings.TrimPrefix(arg, "--config=")
		case arg == "-h" || arg == "--help" || arg == "help":
			writef(stdout, "%s", connectUsage)
			return 0
		default:
			writef(stderr, "cortex disconnect: unknown argument %q\n\n%s", arg, connectUsage)
			return 1
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		writef(stderr, "cortex disconnect: load config: %v\n", err)
		return 1
	}
	server := cfg.RemoteServerURL()
	if server == "" {
		writeln(stdout, "No Cortex server is configured; nothing to disconnect.")
		return 0
	}
	if cfg.Remote.Token == config.RemoteTokenKeychain {
		if err := credential.Delete(server); err != nil {
			writef(stderr, "cortex disconnect: %v\n", err)
			return 1
		}
	}
	cfg.Remote.Token = ""
	if !keepMode {
		cfg.Mode = string(config.RuntimeLocal)
		cfg.MCP.Remote.Enabled, cfg.Sync.Enabled = false, false
	}
	if err := config.Validate(cfg); err != nil {
		writef(stderr, "cortex disconnect: %v\n", err)
		return 1
	}
	if err := config.Save(cfg, cfg.LoadedFrom); err != nil {
		writef(stderr, "cortex disconnect: save config: %v\n", err)
		return 1
	}
	writef(stdout, "Disconnected from %s: stored token removed", server)
	if !keepMode {
		writef(stdout, ", mode set to local")
	}
	writeln(stdout, ".")
	return 0
}
