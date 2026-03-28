package main

import (
	"io"
	"os"

	"github.com/lleontor705/cortex/internal/cli"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return cli.Run(args, stdout, stderr)
}
