// Package opencode embeds the canonical OpenCode plugin in Cortex binaries.
package opencode

import _ "embed"

//go:embed cortex.ts
var source string

// Source returns the canonical TypeScript plugin source.
func Source() string { return source }
