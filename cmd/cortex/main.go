package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/lleontor705/cortex/internal/cli"
	"github.com/lleontor705/cortex/internal/config"
	"github.com/lleontor705/cortex/internal/platform"
	serverplatform "github.com/lleontor705/cortex/internal/platform/server"
)

// version is set by GoReleaser via ldflags at build time.
// Falls back to the module version from go install.
var version = "dev"

func main() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	cli.Version = version
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	mode, cleanArgs := platform.ParseMode(args)
	switch mode {
	case platform.ModeLocal:
		// Byte-identical local path: cli.Run delegates to app.Open internally
		// via openApp(). No double-wiring — platform.Select is proven by tests;
		// the live execution path preserves the existing main→cli→app chain.
		return cli.Run(cleanArgs, stdout, stderr)
	case platform.ModeServer:
		configPath := ""
		for i := 0; i < len(cleanArgs); i++ {
			if cleanArgs[i] == "--config" && i+1 < len(cleanArgs) {
				configPath = cleanArgs[i+1]
				i++
				continue
			}
			if len(cleanArgs[i]) > len("--config=") && cleanArgs[i][:len("--config=")] == "--config=" {
				configPath = cleanArgs[i][len("--config="):]
			}
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cortex: server config: %v\n", err)
			return 2
		}
		rt, err := serverplatform.Open(context.Background(), *cfg)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "cortex: server bootstrap: %v\n", err)
			return 2
		}
		defer func() { _ = rt.Close() }()
		_, _ = fmt.Fprintln(stdout, "cortex: server composition ready")
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "cortex: unknown mode %q (use --mode local or --mode server)\n", mode)
		return 2
	}
}
