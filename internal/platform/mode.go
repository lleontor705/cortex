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

	// ModeServer runs the multi-tenant Postgres + OAuth + HTTP path.
	// In W1 this is a compiled-but-inert stub; full wiring arrives in W11.
	ModeServer Mode = "server"
)

// DefaultMode is used when no --mode flag is provided on the command line.
const DefaultMode Mode = ModeLocal

// Runtime holds the wired services for the selected Mode.
//
// In W1 only the Local path is populated. Server-mode fields (Postgres pool,
// OIDC verifier, RBAC engine, audit chain, quota limiter, etc.) are added in W11.
type Runtime struct {
	// App is the local-mode composition root from internal/app.
	// Non-nil when Mode == ModeLocal; nil for server mode (W11+).
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
// ModeServer → W1 STUB: returns an error without starting any goroutine,
//
//	opening any listener, or constructing any Postgres/OIDC client.
//	The full server composition root lands in W11.
func Select(mode Mode, ctx context.Context, opts app.Options) (*Runtime, error) {
	switch mode {
	case ModeLocal:
		return Local(ctx, opts)
	case ModeServer:
		// W1 stub: server mode is compiled but completely inert.
		// No goroutine, no network listener, no Postgres/OIDC client.
		// Full wiring arrives in W11.
		return nil, fmt.Errorf("server mode is not yet implemented in W1; use --mode local")
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
