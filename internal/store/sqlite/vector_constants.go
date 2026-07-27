// Package sqlite: shared embedding-dimension constants.
//
// These constants are shared between the stub (default, zero-CGO) and the
// cortex_vectors-enabled BLOB scan builds. Declaring them in a build-tag-
// agnostic file lets the sqlite_blob adapter (W8.1, ADR-05) reference them
// under BOTH builds without duplicating or guarding them.
//
// Moving them here from vector_store_enabled.go is a pure refactor: the
// enabled build referenced them via package scope and continues to do so; the
// stub build gains visibility (it does not use them at runtime but the
// adapter declares them in its Capabilities).
package sqlite

// Embedding dimension bounds. Common dimensions: 384 (MiniLM), 768
// (nomic-embed-text), 1536 (OpenAI text-embedding-3-small).
const (
	// DefaultEmbeddingDimension is the default dimension for embeddings.
	DefaultEmbeddingDimension = 768

	// MinEmbeddingDimension and MaxEmbeddingDimension set valid bounds.
	MinEmbeddingDimension = 64
	MaxEmbeddingDimension = 4096
)
