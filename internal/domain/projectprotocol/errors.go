package projectprotocol

import (
	"errors"
	"fmt"
)

// ErrorCode is the stable, transport-neutral failure classification for the
// Project Context Protocol domain. Transport layers map these codes onto
// their HTTP statuses / MCP error codes; the domain itself never leaks
// content, keys or secrets in messages.
type ErrorCode string

const (
	ErrCodeValidation             ErrorCode = "validation"
	ErrCodePayloadTooLarge        ErrorCode = "payload_too_large"
	ErrCodeContentTooLarge        ErrorCode = "content_too_large"
	ErrCodeMetadataTooLarge       ErrorCode = "metadata_too_large"
	ErrCodeInvalidUTF8            ErrorCode = "invalid_utf8"
	ErrCodeDuplicateMetadataKey   ErrorCode = "duplicate_metadata_key"
	ErrCodeEffectiveLimitExceeded ErrorCode = "effective_artifact_limit_exceeded"
	ErrCodeProtocolTooLarge       ErrorCode = "protocol_too_large"
	ErrCodeLimitExceeded          ErrorCode = "limit_exceeded"
	ErrCodeRevisionConflict       ErrorCode = "revision_conflict"
	ErrCodeActivationConflict     ErrorCode = "activation_conflict"
	ErrCodeIdempotencyConflict    ErrorCode = "idempotency_conflict"
	ErrCodeNotFound               ErrorCode = "not_found"
	ErrCodeUnsupportedType        ErrorCode = "unsupported_type"
)

// Error is the typed domain error. Limit carries the exact bound that was
// exceeded (0 when not applicable) so callers can report limits without
// echoing content.
type Error struct {
	Code    ErrorCode
	Message string
	Limit   int64
	Detail  string
}

func (e *Error) Error() string {
	if e == nil {
		return string(ErrCodeValidation)
	}
	if e.Limit > 0 {
		return fmt.Sprintf("projectprotocol: %s: %s (limit %d)", e.Code, e.Message, e.Limit)
	}
	return fmt.Sprintf("projectprotocol: %s: %s", e.Code, e.Message)
}

// Is compares by stable code so errors.As/errors.Is work across wrappers.
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

// Sentinel errors for the most common failure classes. Errors carrying a
// dynamic Limit (limit_exceeded, *_too_large) should be constructed with
// NewLimitError so the exact bound travels with the error.
var (
	ErrInvalidArtifact      = &Error{Code: ErrCodeValidation, Message: "invalid artifact"}
	ErrInvalidUTF8          = &Error{Code: ErrCodeInvalidUTF8, Message: "content is not valid UTF-8"}
	ErrDuplicateMetadataKey = &Error{Code: ErrCodeDuplicateMetadataKey, Message: "metadata contains a duplicate object key"}
	ErrContentTooLarge      = &Error{Code: ErrCodeContentTooLarge, Message: "artifact content exceeds the accepted size", Limit: MaxArtifactContentBytes}
	ErrMetadataTooLarge     = &Error{Code: ErrCodeMetadataTooLarge, Message: "canonical metadata exceeds the accepted size", Limit: MaxArtifactMetadataBytes}
	ErrEffectiveLimit       = &Error{Code: ErrCodeEffectiveLimitExceeded, Message: "effective artifact limit exceeded", Limit: MaxEffectiveArtifacts}
	ErrProtocolTooLarge     = &Error{Code: ErrCodeProtocolTooLarge, Message: "canonical protocol bundle exceeds the accepted size", Limit: MaxProtocolBundleBytes}
	ErrRevisionConflict     = &Error{Code: ErrCodeRevisionConflict, Message: "artifact changed since the expected revision"}
	ErrActivationConflict   = &Error{Code: ErrCodeActivationConflict, Message: "activation pointer changed since the expected activation revision"}
	ErrIdempotencyConflict  = &Error{Code: ErrCodeIdempotencyConflict, Message: "idempotency key was already used with a different payload"}
)

// NewLimitError builds a typed limit failure carrying the exact bound.
func NewLimitError(code ErrorCode, limit int64) *Error {
	return &Error{Code: code, Message: "approved limit exceeded", Limit: limit}
}

// AsError normalizes any error into a *Error, wrapping unknown errors as
// validation failures. The original message is dropped: transport-safe
// context can be attached via Detail by the caller.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return &Error{Code: ErrCodeValidation, Message: "invalid project protocol input"}
}
