package platform

import (
	"context"
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/internal/app"
)

// Mode selects which backend the binary runs.
type Mode string

const (
	// ModeLocal runs the single-binary SQLite + stdio-MCP path.
	// This is byte-identical to pre-v2 behavior.
	ModeLocal Mode = "local"

	// ModeServer runs the multi-tenant PostgreSQL + authenticated HTTP path.
	// cmd/cortex is the sole bridge to the server composition root.
	ModeServer Mode = "server"
)

// DefaultMode is used when no --mode flag is provided on the command line.
const DefaultMode Mode = ModeLocal

// Runtime holds the wired services for the selected Mode.
//
// This type is the local composition selector. The server runtime is owned by
// internal/platform/server and wired directly by cmd/cortex.
type Runtime struct {
	// App is the local-mode composition root from internal/app.
	// Non-nil when Mode == ModeLocal; nil for server mode.
	App *app.App
}

// Close releases resources held by the Runtime.
func (r *Runtime) Close() error {
	if r == nil || r.App == nil {
		return nil
	}
	return r.App.Close()
}

// Select wires the Runtime for the given Mode.
//
// ModeLocal  → delegates to [Local], which calls app.Open byte-identically
//
//	(same SQLite database, same stores, same config defaults).
//
// ModeServer returns an error because this local-only API intentionally cannot
// import the server composition root. cmd/cortex performs that bridge.
func Select(mode Mode, ctx context.Context, opts app.Options) (*Runtime, error) {
	switch mode {
	case ModeLocal:
		return Local(ctx, opts)
	case ModeServer:
		return nil, fmt.Errorf("server mode is wired by cmd/cortex, not platform.Select")
	default:
		return nil, fmt.Errorf("unknown mode %q: use --mode local or --mode server", mode)
	}
}

// ParseMode extracts the --mode flag from args, returning the resolved Mode
// and a cleaned copy of args with the flag removed.
//
// When --mode is absent, DefaultMode (ModeLocal) is returned and args are
// returned unchanged (same slice values, no allocation).
//
// Supports both syntactic forms:
//
//	cortex --mode local <command>    (space-separated)
//	cortex --mode=local <command>    (equals form)
//
// The flag may appear before or after the subcommand without affecting
// downstream argument parsing in internal/cli.
func ParseMode(args []string) (Mode, []string) {
	mode := DefaultMode

	for i := 0; i < len(args); i++ {
		if args[i] == "--mode" && i+1 < len(args) {
			mode = Mode(args[i+1])
			// Build cleaned args excluding this flag and its value.
			clean := make([]string, 0, len(args)-2)
			clean = append(clean, args[:i]...)
			clean = append(clean, args[i+2:]...)
			return mode, clean
		}
		if strings.HasPrefix(args[i], "--mode=") {
			mode = Mode(strings.TrimPrefix(args[i], "--mode="))
			clean := make([]string, 0, len(args)-1)
			clean = append(clean, args[:i]...)
			clean = append(clean, args[i+1:]...)
			return mode, clean
		}
	}

	return mode, args
}
