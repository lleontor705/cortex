package main

import (
	"io"
	"os"
	"runtime/debug"

	"github.com/lleontor705/cortex/internal/cli"
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
	return cli.Run(args, stdout, stderr)
}
