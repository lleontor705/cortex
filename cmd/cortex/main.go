package main

import (
	"io"
	"os"

	"github.com/lleontor705/cortex/internal/cli"
)

// version is set by GoReleaser via ldflags at build time.
var version = "dev"

func main() {
	cli.Version = version
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, stdout, stderr)
}
