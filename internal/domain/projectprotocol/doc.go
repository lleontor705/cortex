// Package projectprotocol defines the Project Context Protocol domain
// contract: skill and rule artifacts, immutable revisions, activation
// compare-and-swap (CAS) pointers, idempotency DTOs, canonical hashing and
// the deterministic effective protocol resolution with its approved limits.
//
// Artifacts are NON-EXECUTABLE data. Kinds "skill" and "rule" are inert
// labels and the only accepted content type is text/markdown; nothing in
// this package interprets, evaluates or executes artifact content. Consumers
// (plugins, agents) MUST treat artifacts as documentation, never as code
// authority.
//
// The package is deliberately free of persistence, HTTP/MCP and provider
// concerns: it depends on the Go standard library only. Store and transport
// layers validate through these same types and limits so local, HTTP and MCP
// paths cannot disagree (see the ProjectProtocolStore port in
// internal/domain/interfaces.go).
//
// v1 retention policy (REQ-RET-001): revisions, activations and audit events
// are immutable and retained indefinitely. Deletion is exclusively the
// soft-delete state transition; this package defines NO hard-delete or purge
// operation.
package projectprotocol
