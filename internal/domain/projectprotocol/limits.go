package projectprotocol

// Approved v1 limits. These constants are the single source of truth shared
// by local CLI, HTTP, MCP and store validation; layers MUST NOT define their
// own divergent copies.
//
// Exactly-at-limit values are accepted; limit+1 is rejected atomically with
// zero effects and no truncation (REQ-LIMIT-001..003).
const (
	// MaxArtifactContentBytes bounds artifact content measured in UTF-8
	// bytes after decode. Content is never normalized or truncated.
	MaxArtifactContentBytes = 1 << 20 // 1 MiB

	// MaxArtifactMetadataBytes bounds the canonical JSON encoding of the
	// artifact metadata object. The limit applies to canonical bytes, not
	// to the raw transport encoding.
	MaxArtifactMetadataBytes = 1 << 16 // 64 KiB

	// MaxEffectiveArtifacts bounds the number of active artifacts in the
	// effective protocol after project-over-workspace resolution.
	MaxEffectiveArtifacts = 2000

	// MaxProtocolBundleBytes bounds the canonical JSON encoding of the full
	// effective protocol bundle.
	MaxProtocolBundleBytes = 1 << 22 // 4 MiB

	// OrdinaryRequestTransportBytes is the HTTP transport cap for ordinary
	// JSON request bodies (encoded, pre-decode). Informational for
	// transport layers; semantic limits above remain authoritative.
	OrdinaryRequestTransportBytes = 1 << 20 // 1 MiB

	// LargeMutationTransportBytes is the HTTP transport cap for artifact
	// create/revision envelopes so that a valid escaped 1 MiB content plus
	// 64 KiB metadata still reaches semantic validation.
	LargeMutationTransportBytes = 8 << 20 // 8 MiB

	// MCPAbsoluteRequestBytes is the absolute encoded request cap for the
	// MCP endpoint.
	MCPAbsoluteRequestBytes = 8 << 20 // 8 MiB

	// MCPProtocolResponseTargetBytes is the MCP response cap target: it
	// must accommodate the canonical 4 MiB structured bundle plus a
	// bounded envelope.
	MCPProtocolResponseTargetBytes = 5 << 20 // 5 MiB

	// DefaultPageSize is the default page size for list operations.
	DefaultPageSize = 20

	// MaxPageSize is the maximum page size for list operations.
	MaxPageSize = 100
)

// PageSizeBounds validates and normalizes a requested page size.
// Non-positive values return DefaultPageSize; values above MaxPageSize are
// clamped to MaxPageSize.
func PageSizeBounds(requested int) int {
	if requested <= 0 {
		return DefaultPageSize
	}
	if requested > MaxPageSize {
		return MaxPageSize
	}
	return requested
}
