package platform

import (
	"context"

	"github.com/lleontor705/cortex/v2/internal/app"
)

// Local wires the local-mode Runtime by delegating to [app.Open].
//
// This function MUST NOT duplicate or alter app wiring. It calls app.Open with
// the exact same (ctx, opts) arguments, preserving byte-identical local behavior:
// the same SQLite database, the same store bundle, the same config defaults,
// the same archival lifecycle, the same stdio-MCP transport.
//
// The returned Runtime wraps the *app.App unchanged.
func Local(ctx context.Context, opts app.Options) (*Runtime, error) {
	a, err := app.Open(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Runtime{App: a}, nil
}
