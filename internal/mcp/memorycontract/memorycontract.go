// Package memorycontract is the shared, pure MCP contract for the durable
// memory write surface (design RD6, REM-SAVE-001, REM-MCP-001).
//
// It defines the tool names, hint annotations, the exact structured output
// schema, the handoff input schema, and the lowering of domain write results
// and errors into the structured payloads published by cortex_save and
// cortex_handoff. The local MCP server and the authenticated server runtime
// both consume this package so the published contract is byte-identical on
// every route; the proxy forwards results without reinterpreting them.
//
// The package is deliberately pure: no I/O, no stores, no transport clients.
// It depends only on the domain model, the pure transport policy, and the
// standard library, and never logs or embeds payloads, idempotency keys,
// hashes, or tokens.
package memorycontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/transportpolicy"
)

// Tool names in the cortex_* namespace (REQ-MCP-001). Single source of truth
// for local and server registration.
const (
	// ToolSave is the proactive memory save tool.
	ToolSave = "cortex_save"
	// ToolHandoff is the durable, idempotent handoff tool.
	ToolHandoff = "cortex_handoff"
)

// Shared hint annotations for the durable write tools. Both are read/write,
// non-destructive, closed-world; handoff is additionally idempotent because
// the same (scope, key, payload) replays the same observation (REM-HANDOFF-002).
type Hints struct {
	Title       string
	ReadOnly    bool
	Destructive bool
	Idempotent  bool
	OpenWorld   bool
}

var (
	// SaveHints annotates cortex_save.
	SaveHints = Hints{Title: "Save Memory"}
	// HandoffHints annotates cortex_handoff.
	HandoffHints = Hints{Title: "Record Handoff", Idempotent: true}
)

// Stable, machine-readable structured error codes (design: error contract).
// The domain handoff codes are reused verbatim; transport is the proxy-only
// classification for failures that never produced an MCP result.
const (
	CodeValidation      = string(domain.HandoffErrorValidation)
	CodePayloadTooLarge = string(domain.HandoffErrorPayloadTooLarge)
	CodeUnauthorized    = string(domain.HandoffErrorUnauthorized)
	CodeForbidden       = string(domain.HandoffErrorForbidden)
	CodeConflict        = string(domain.HandoffErrorConflict)
	CodeUnavailable     = string(domain.HandoffErrorUnavailable)
	CodeTimeout         = string(domain.HandoffErrorTimeout)
	CodePersistence     = string(domain.HandoffErrorPersistence)
	CodeTransport       = "transport"
)

// MaxErrorMessageLength bounds every structured error message so a hostile or
// verbose underlying failure can never leak unbounded text into a result.
const MaxErrorMessageLength = 200

// WriteOutputSchemaJSON is the exact output schema for cortex_save and
// cortex_handoff results: a structured payload carrying the exclusive
// observation reference (local_id XOR public_id) and the closed write status,
// or a structured error payload.
var WriteOutputSchemaJSON = json.RawMessage(`{
	"type": "object",
	"properties": {
		"observation_ref": {
			"oneOf": [
				{
					"type": "object",
					"properties": {
						"local_id": {"type": "integer", "minimum": 1}
					},
					"required": ["local_id"],
					"additionalProperties": false
				},
				{
					"type": "object",
					"properties": {
						"public_id": {"type": "string", "format": "uuid"}
					},
					"required": ["public_id"],
					"additionalProperties": false
				}
			]
		},
		"status": {"type": "string", "enum": ["created", "replayed", "updated"]},
		"error": {
			"type": "object",
			"properties": {
				"code": {"type": "string"},
				"message": {"type": "string"}
			},
			"required": ["code", "message"],
			"additionalProperties": false
		}
	},
	"additionalProperties": false
}`)

// HandoffInputSchemaJSON is the shared input schema for cortex_handoff. The
// relation target accepts both namespaces at the schema level so the server
// runtime can publish the identical contract; the LOCAL handler accepts only
// local_id because the local namespace is SQLite-only (REM-MCP-001: local uses
// local_id, server/proxy use public_id UUID).
var HandoffInputSchemaJSON = json.RawMessage(`{
	"type": "object",
	"properties": {
		"idempotency_key": {
			"type": "string",
			"minLength": 1,
			"description": "Stable idempotency key; identical key+payload replays, differing payload conflicts"
		},
		"observation": {
			"type": "object",
			"properties": {
				"title":       {"type": "string", "minLength": 1},
				"content":     {"type": "string", "minLength": 1},
				"type":        {"type": "string"},
				"project":     {"type": "string"},
				"scope":       {"type": "string", "enum": ["project", "personal"]},
				"session_id":  {"type": "string", "description": "Session the observation belongs to; the local runtime requires a preexisting session and validates it before any mutation"},
				"topic_key":   {"type": "string"},
				"confidence":  {"type": "number", "minimum": 0, "maximum": 1},
				"source":      {"type": "string"},
				"tags":        {"type": "array", "items": {"type": "string"}}
			},
			"required": ["title", "content"],
			"additionalProperties": false
		},
		"relation": {
			"type": "object",
			"properties": {
				"target": {
					"oneOf": [
						{
							"type": "object",
							"properties": {"local_id": {"type": "integer", "minimum": 1}},
							"required": ["local_id"],
							"additionalProperties": false
						},
						{
							"type": "object",
							"properties": {"public_id": {"type": "string", "format": "uuid"}},
							"required": ["public_id"],
							"additionalProperties": false
						}
					]
				},
				"type":       {"type": "string", "minLength": 1},
				"weight":     {"type": "number"},
				"confidence": {"type": "number"},
				"reasoning":  {"type": "string"}
			},
			"required": ["target", "type"],
			"additionalProperties": false
		},
		"capability_tuple": {
			"description": "Opaque JSON evidence forwarded with the handoff; stored as data, never interpreted"
		}
	},
	"required": ["idempotency_key", "observation"],
	"additionalProperties": false
}`)

// ObservationRefPayload is the wire form of domain.ObservationRef: exactly one
// namespace set (XOR), matching the output schema's oneOf.
type ObservationRefPayload struct {
	LocalID  *int64  `json:"local_id,omitempty"`
	PublicID *string `json:"public_id,omitempty"`
}

// Validate enforces the exclusive union invariant.
func (p ObservationRefPayload) Validate() error {
	if (p.LocalID == nil) == (p.PublicID == nil) {
		return fmt.Errorf("observation_ref must set exactly one of local_id or public_id")
	}
	if p.LocalID != nil && *p.LocalID <= 0 {
		return fmt.Errorf("local_id must be a positive integer")
	}
	if p.PublicID != nil {
		if _, err := uuid.Parse(*p.PublicID); err != nil {
			return fmt.Errorf("public_id must be a UUID")
		}
	}
	return nil
}

// NewLocalRefPayload builds a validated local-namespace reference.
func NewLocalRefPayload(id int64) (ObservationRefPayload, error) {
	payload := ObservationRefPayload{LocalID: &id}
	if err := payload.Validate(); err != nil {
		return ObservationRefPayload{}, err
	}
	return payload, nil
}

// NewPublicRefPayload builds a validated public-namespace reference.
func NewPublicRefPayload(id string) (ObservationRefPayload, error) {
	payload := ObservationRefPayload{PublicID: &id}
	if err := payload.Validate(); err != nil {
		return ObservationRefPayload{}, err
	}
	return payload, nil
}

// SaveStructured is the structuredContent payload of a successful
// cortex_save or cortex_handoff call.
type SaveStructured struct {
	ObservationRef ObservationRefPayload `json:"observation_ref"`
	Status         string                `json:"status"`
}

// FromWriteResult lowers a validated domain write result into the structured
// payload. It fails closed: an invalid reference or status produces an error,
// never a fabricated success payload.
func FromWriteResult(result domain.ObservationWriteResult) (SaveStructured, error) {
	if err := result.Ref.Validate(); err != nil {
		return SaveStructured{}, fmt.Errorf("write result reference is invalid: %w", err)
	}
	if !ValidStatus(result.Status) {
		return SaveStructured{}, fmt.Errorf("write status %q is outside the closed set", result.Status)
	}
	payload := SaveStructured{Status: string(result.Status)}
	if result.Ref.LocalID != nil {
		ref, err := NewLocalRefPayload(*result.Ref.LocalID)
		if err != nil {
			return SaveStructured{}, err
		}
		payload.ObservationRef = ref
		return payload, nil
	}
	ref, err := NewPublicRefPayload(result.Ref.PublicID.String())
	if err != nil {
		return SaveStructured{}, err
	}
	payload.ObservationRef = ref
	return payload, nil
}

// ValidStatus reports whether status belongs to the closed write-status set.
func ValidStatus(status domain.WriteStatus) bool {
	return status == domain.WriteStatusCreated ||
		status == domain.WriteStatusReplayed ||
		status == domain.WriteStatusUpdated
}

// ErrorBody is the stable, redacted, bounded error classification.
type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// ErrorStructured is the structuredContent payload of a failed tool call.
// It never carries a reference, status, key, hash, payload, or token.
type ErrorStructured struct {
	Error ErrorBody `json:"error"`
}

// Constant, redacted message texts for generic classifications. The raw
// underlying error text is NEVER echoed: it may embed full URLs (userinfo,
// query strings) or payload fragments. Only the typed handoff and validation
// messages — both constructed from constant domain text — are ever surfaced,
// and even those are bounded by MaxErrorMessageLength.
const (
	msgUnknownFailure   = "unknown failure"
	msgTimedOut         = "operation timed out"
	msgTransportFailure = "transport failure"
	msgWriteFailed      = "write could not be persisted"
)

// FromError lowers any error into the stable structured error contract.
// Messages for generic classifications are CONSTANT and redacted; typed
// handoff and validation errors contribute only their safe, pre-redacted
// message, always bounded to MaxErrorMessageLength runes. Transport failures
// are detected ONLY through explicit typed matches — *url.Error, net.Error,
// and *transportpolicy.Error — never through a generic Unwrap probe, which
// would misclassify wrapped persistence failures (SQL errors, busy locks) as
// transport problems.
func FromError(err error) ErrorStructured {
	if err == nil {
		return ErrorStructured{Error: ErrorBody{Code: CodePersistence, Message: msgUnknownFailure}}
	}
	var handoff *domain.HandoffError
	if errors.As(err, &handoff) && handoff != nil {
		return ErrorStructured{Error: ErrorBody{
			Code:      string(handoff.Code),
			Message:   boundMessage(handoff.Message),
			Retryable: handoff.Retryable,
		}}
	}
	var validation *domain.ValidationError
	if errors.As(err, &validation) && validation != nil {
		// ClassFailed wraps a REAL persistence failure: its Cause carries raw
		// driver/SQL text that must never surface. Classify as persistence
		// with the constant redacted message — no Message, no Cause.
		if validation.Code == domain.ClassFailed {
			return ErrorStructured{Error: ErrorBody{
				Code:      CodePersistence,
				Message:   msgWriteFailed,
				Retryable: true,
			}}
		}
		// Real input validations only (legacy field validation with empty
		// Code, policy rejection, dedup classification): their messages are
		// constructed from constant domain text, never from wrapped driver
		// output, so they may surface — always bounded.
		message := validation.Message
		if validation.Code == "" {
			message = validation.Error() // legacy format; Cause is always nil here
		}
		return ErrorStructured{Error: ErrorBody{
			Code:    CodeValidation,
			Message: boundMessage(message),
		}}
	}
	if isTimeout(err) {
		return ErrorStructured{Error: ErrorBody{
			Code:      CodeTimeout,
			Message:   msgTimedOut,
			Retryable: true,
		}}
	}
	// Explicit typed transport detection only. *url.Error is checked first
	// because it wraps the underlying cause of every net/http client failure;
	// *transportpolicy.Error covers policy rejections (e.g. a Bearer redirect
	// blocked before credentials were forwarded); net.Error covers remaining
	// transport-socket failures.
	var policyErr *transportpolicy.Error
	if errors.As(err, &policyErr) {
		return transportBody()
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return transportBody()
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return transportBody()
	}
	return ErrorStructured{Error: ErrorBody{
		Code:      CodePersistence,
		Message:   msgWriteFailed,
		Retryable: true,
	}}
}

// transportBody is the constant transport classification.
func transportBody() ErrorStructured {
	return ErrorStructured{Error: ErrorBody{
		Code:      CodeTransport,
		Message:   msgTransportFailure,
		Retryable: true,
	}}
}

// Validationf builds a validation error payload with a formatted, bounded
// message. Use for request-shape rejections before any persistence runs.
func Validationf(format string, args ...any) ErrorStructured {
	return ErrorStructured{Error: ErrorBody{
		Code:    CodeValidation,
		Message: boundMessage(fmt.Sprintf(format, args...)),
	}}
}

// Unavailablef builds an unavailable error payload for a missing dependency.
func Unavailablef(format string, args ...any) ErrorStructured {
	return ErrorStructured{Error: ErrorBody{
		Code:      CodeUnavailable,
		Message:   boundMessage(fmt.Sprintf(format, args...)),
		Retryable: true,
	}}
}

// boundMessage truncates msg to MaxErrorMessageLength runes, marking the cut.
func boundMessage(msg string) string {
	if utf8.RuneCountInString(msg) <= MaxErrorMessageLength {
		return msg
	}
	runes := []rune(msg)
	return string(runes[:MaxErrorMessageLength]) + "…[truncated]"
}

// isTimeout reports whether err (or its chain) is a deadline/timeout.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
