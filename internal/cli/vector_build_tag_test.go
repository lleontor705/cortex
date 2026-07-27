//go:build !cortex_vectors

// Build-tag detector for CLI tests: indicates whether the cortex_vectors
// build tag is active. Default (stub) build -> false.
package cli

const testVectorsEnabled = false
