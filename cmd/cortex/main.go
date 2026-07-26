package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/lleontor705/cortex/internal/cli"
	"github.com/lleontor705/cortex/internal/platform"
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
		// W1 stub: server mode is compiled but inert.
		// No goroutine, no listener, no PG/OIDC client. Full wiring in W11.
		_, _ = fmt.Fprintln(stderr, "cortex: server mode is not yet implemented in W1; use --mode local")
		return 2
	default:
		_, _ = fmt.Fprintf(stderr, "cortex: unknown mode %q (use --mode local or --mode server)\n", mode)
		return 2
	}
}
