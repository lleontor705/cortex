// Package web serves the compiled Next.js export of the Cortex web UI from
// inside the Go binary. It is a local-safe package: it depends only on the
// standard library and the local web-key verifier, never on PostgreSQL, authz,
// or the server composition root.
package web

import (
	"embed"
	"io/fs"
)

// The all: prefix is required: without it go:embed silently skips underscore
// and dot entries, which would drop the entire Next.js _next/ asset tree.
//
//go:embed all:dist
var embeddedDist embed.FS

// assets is the embedded export rooted at dist, so request paths map directly
// to filesystem paths (e.g. "/graph/index.html").
var assets = mustSub(embeddedDist, "dist")

// Assets returns the read-only filesystem backing the embedded web export. It
// always contains at least the committed placeholder document, so callers can
// rely on it even when the web toolchain never ran in a checkout.
func Assets() fs.FS {
	return assets
}

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// The subtree is a compile-time constant, so a failure here means the
		// embedded layout is broken rather than a runtime condition.
		panic("web: embedded " + dir + " subtree unavailable: " + err.Error())
	}
	return sub
}
