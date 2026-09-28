package platform

import (
	"context"
	"fmt"
	"strings"

	"github.com/lleontor705/cortex/v2/internal/app"
)

// Mode selects which backend the binary runs. The set is closed and contains
// exactly three stable values:
//
//   - ModeLocal ("local") — the single-binary SQLite composition with the local
//     stdio MCP server. It has no network dependency and is byte-compatible
//     with pre-v2 behavior.
//
//   - ModeHybrid ("hybrid") — the local SQLite composition plus the remote
//     sync/replication loop. Hybrid is NOT a distinct store or a third backend:
//     it is exactly ModeLocal's composition with replication layered on by
//     internal/app (see startHybridReplication). The literal string "hybrid" is
//     part of the public surface (CORTEX_MODE / --mode) and its meaning is
//     locked by decision D2; it must never be renamed and must never acquire
//     new semantics.
//
//   - ModeServer ("server") — the single-tenant PostgreSQL composition served
//     over authenticated HTTP and streamable MCP. cmd/cortex is the sole bridge
//     to that composition root; internal/platform/server owns it.
//
// Mode is a closed set. Every consumer must reject unknown values instead of
// falling back to a silent default.
type Mode string

const (
	// ModeLocal runs the single-binary SQLite + stdio-MCP path.
	// This is byte-identical to pre-v2 behavior.
	ModeLocal Mode = "local"

	// ModeHybrid runs the local SQLite composition with remote replication
	// enabled. It is ModeLocal plus the sync loop, never a separate store.
	ModeHybrid Mode = "hybrid"

	// ModeServer runs the single-tenant PostgreSQL + authenticated HTTP path.
	// cmd/cortex is the sole bridge to the server composition root.
	ModeServer Mode = "server"
)

// DefaultMode is used when no --mode flag is provided on the command line.
const DefaultMode Mode = ModeLocal

// Valid reports whether m is one of the three closed-set triad modes.
func (m Mode) Valid() bool {
	switch m {
	case ModeLocal, ModeHybrid, ModeServer:
		return true
	default:
		return false
	}
}

// Runtime holds the wired services for the selected Mode.
//
// This type is the local composition selector. The server runtime is owned by
// internal/platform/server and wired directly by cmd/cortex.
type Runtime struct {
	// App is the local-mode composition root from internal/app.
	// Non-nil when Mode == ModeLocal || Mode == ModeHybrid; nil for server mode.
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
// ModeLocal, ModeHybrid → delegates to [Local], which calls app.Open
// byte-identically (same SQLite database, same stores, same config defaults).
// Hybrid carries no extra wiring here: replication is started by internal/app
// itself when it observes the hybrid composition.
//
// ModeServer returns an error because this local-only API intentionally cannot
// import the server composition root. cmd/cortex performs that bridge.
func Select(mode Mode, ctx context.Context, opts app.Options) (*Runtime, error) {
	switch mode {
	case ModeLocal, ModeHybrid:
		return Local(ctx, opts)
	case ModeServer:
		return nil, fmt.Errorf("server mode is wired by cmd/cortex, not platform.Select")
	default:
		return nil, fmt.Errorf("unknown mode %q: use --mode local, --mode hybrid, or --mode server", mode)
	}
}

// ParseModeStrict extracts the --mode flag from args and validates it against
// the closed triad. It returns the resolved Mode, a copy of args with the flag
// removed, and a non-nil error for any value outside the triad.
//
// Both syntactic forms are accepted:
//
//	cortex --mode local <command>    (space-separated)
//	cortex --mode=local <command>    (equals form)
//
// The flag may appear before or after the subcommand. When --mode is absent,
// DefaultMode (ModeLocal) is returned and args are returned unchanged (same
// slice values, no allocation).
//
// Unknown values fail closed: the returned error enumerates the three valid
// modes and args are returned unchanged, so a caller can abort startup without
// observing a partially parsed command line.
func ParseModeStrict(args []string) (Mode, []string, error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--mode" && i+1 < len(args) {
			return resolveMode(args[i+1], args, i, i+2)
		}
		if strings.HasPrefix(args[i], "--mode=") {
			return resolveMode(strings.TrimPrefix(args[i], "--mode="), args, i, i+1)
		}
	}

	return DefaultMode, args, nil
}

// ParseMode is the two-value compatibility surface used by cmd/cortex. It
// delegates to ParseModeStrict and drops the error: an unknown value yields the
// raw rejected Mode, which never satisfies Mode.Valid(), so the caller's
// switch default fails closed while still enumerating the three valid modes.
//
// New callers that can surface a startup error must use ParseModeStrict.
func ParseMode(args []string) (Mode, []string) {
	mode, clean, _ := ParseModeStrict(args)
	return mode, clean
}

// resolveMode validates raw against the triad and, on success, returns the mode
// with args[start:end] (the flag and any attached value) stripped. On failure
// it returns the raw rejected mode and args unchanged.
func resolveMode(raw string, args []string, start, end int) (Mode, []string, error) {
	mode := Mode(raw)
	if !mode.Valid() {
		return mode, args, fmt.Errorf("unknown mode %q: use --mode local, --mode hybrid, or --mode server", raw)
	}

	clean := make([]string, 0, len(args)-(end-start))
	clean = append(clean, args[:start]...)
	clean = append(clean, args[end:]...)
	return mode, clean, nil
}
