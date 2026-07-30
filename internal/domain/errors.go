package domain

import (
	"errors"
	"fmt"
)

// Common domain errors that can be returned by repository implementations.
var (
	// ErrNotFound indicates that the requested entity was not found.
	ErrNotFound = errors.New("entity not found")

	// ErrAlreadyExists indicates that an entity with the same key already exists.
	ErrAlreadyExists = errors.New("entity already exists")

	// ErrInvalidInput indicates that the input data is invalid.
	ErrInvalidInput = errors.New("invalid input")

	// ErrConflict indicates a conflict with the current state (e.g., optimistic locking).
	ErrConflict = errors.New("conflict with current state")

	// ErrUnauthorized indicates lack of permissions for the operation.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrSessionEnded indicates an attempt to modify an ended session.
	ErrSessionEnded = errors.New("session has already ended")

	// ErrInvalidRelation indicates an invalid edge relation type.
	ErrInvalidRelation = errors.New("invalid relation type")

	// ErrCircularReference indicates a circular reference in the knowledge graph.
	ErrCircularReference = errors.New("circular reference detected")

	// ErrVectorSearchDisabled indicates that vector search is not available.
	// This happens when the cortex_vectors build tag is not enabled.
	ErrVectorSearchDisabled = errors.New("vector search is disabled - rebuild with cortex_vectors tag")

	// ErrInvalidEmbedding indicates an invalid embedding vector.
	ErrInvalidEmbedding = errors.New("invalid embedding vector")

	// ErrDimensionMismatch indicates a vector whose dimension does not match
	// the declared model namespace. REQ-VEC-001 error scenario: mismatched
	// vectors MUST be rejected (not scored 0 and stored) to prevent
	// dimension-mismatch corruption. The legacy cosine path logged a warning
	// and returned 0 — this sentinel pins the corrected, fail-closed behavior.
	ErrDimensionMismatch = errors.New("vector dimension mismatch")

	// ErrNamespaceMismatch indicates a vector whose model/version namespace
	// differs from the index namespace. REQ-VEC-001: model-version namespace
	// prevents dimension-mismatch corruption; a cross-namespace upsert is
	// rejected rather than silently overwriting.
	ErrNamespaceMismatch = errors.New("vector model namespace mismatch")
)

// DimensionMismatchError wraps ErrDimensionMismatch with the expected and
// actual dimensions plus the offending namespace. It is the structured form
// returned by VectorIndex adapters when an upsert violates the model-version
// namespace invariant (REQ-VEC-001).
type DimensionMismatchError struct {
	Expected  int    // ModelInfo.Dimension declared on the point (or index namespace)
	Actual    int    // len(Vector) observed
	Namespace string // model:version namespace (may be empty)
}

// Error renders the mismatch with enough context to diagnose the cause.
func (e *DimensionMismatchError) Error() string {
	ns := ""
	if e.Namespace != "" {
		ns = " (namespace: " + e.Namespace + ")"
	}
	return fmt.Sprintf("vector dimension mismatch: expected %d, got %d%s", e.Expected, e.Actual, ns)
}

// Unwrap returns ErrDimensionMismatch so errors.Is(err, ErrDimensionMismatch)
// works for every adapter's mismatch rejection.
func (e *DimensionMismatchError) Unwrap() error {
	return ErrDimensionMismatch
}

// NewDimensionMismatchError constructs a structured DimensionMismatchError.
func NewDimensionMismatchError(expected, actual int, namespace string) *DimensionMismatchError {
	return &DimensionMismatchError{Expected: expected, Actual: actual, Namespace: namespace}
}

// IsDimensionMismatch reports whether err is a dimension-mismatch rejection.
// Adapters use this to classify upsert failures as non-retryable (the vector
// itself is wrong, not a transient backend issue).
func IsDimensionMismatch(err error) bool {
	var dme *DimensionMismatchError
	if errors.As(err, &dme) {
		return true
	}
	return errors.Is(err, ErrDimensionMismatch)
}

// NotFoundError wraps ErrNotFound with context about what was not found.
type NotFoundError struct {
	Type string // "observation", "session", "edge", "prompt"
	ID   interface{}
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s with ID %v not found", e.Type, e.ID)
}

func (e *NotFoundError) Unwrap() error {
	return ErrNotFound
}

// Classification codes for passive-type outcomes (REQ-FOUND-003).
//
// These codes let callers distinguish an intentional dedup skip from a policy
// rejection from a real persistence failure via errors.As plus a code check
// (IsClass). The legacy dedup path swallowed non-duplicate errors as dedup skips
// (REQ-MCPH-002 defect pin); ValidationError fixes that by carrying an explicit
// code. W1 stub: codes are defined here; wiring into the dedup/save path lands
// in W6.2.
const (
	// ClassDedupSkipped marks a duplicate observation that was intentionally
	// skipped. It is not an error: IsError is false.
	ClassDedupSkipped = "dedup_skipped"

	// ClassRejected marks an observation rejected for a content rule; no partial
	// row is written.
	ClassRejected = "rejected"

	// ClassFailed marks a persistence failure; Cause wraps the real error so it
	// is inspectable via errors.Unwrap / errors.Is.
	ClassFailed = "failed"
)

// ValidationError classifies a passive-type outcome so callers can distinguish
// an intentional dedup skip (ClassDedupSkipped) from a policy rejection
// (ClassRejected) from a real persistence failure (ClassFailed).
//
// This type is ALSO the legacy field-validation error: when Code is empty and
// Field is set, it preserves the original field-level validation semantics so
// every existing caller is unaffected (zero local-mode behavior change). The
// classification fields (Code, Rule, Cause) are only populated by the
// NewDedupSkipped / NewRejected / NewFailed constructors.
//
// W1 stub: the type and classification codes are defined here; wiring into the
// dedup/save path lands in W6.2 (REQ-MCPH-002).
type ValidationError struct {
	// Legacy field-validation field (unchanged behavior).
	Field string

	// Passive-outcome classification fields (REQ-FOUND-003). Code is "" for
	// legacy field-validation errors, so legacy and classification modes never
	// collide.
	Code    string // ClassDedupSkipped | ClassRejected | ClassFailed
	Rule    string // violated rule name (set for ClassRejected)
	Cause   error  // wrapped real error (set for ClassFailed)
	Message string // human-readable message (shared by legacy and classification)
}

// Error renders the validation error. For legacy field-validation (empty Code)
// it preserves the original format byte-for-byte. For passive-outcome
// classification it renders "code: message (rule: <rule>): cause".
func (e *ValidationError) Error() string {
	if e.Code != "" {
		msg := e.Code + ": " + e.Message
		if e.Rule != "" {
			msg += " (rule: " + e.Rule + ")"
		}
		if e.Cause != nil {
			msg += ": " + e.Cause.Error()
		}
		return msg
	}
	return fmt.Sprintf("validation error on field %q: %s", e.Field, e.Message)
}

// Unwrap returns the wrapped cause for a classified failure (ClassFailed). For
// legacy field-validation errors and code-only classifications (no Cause) it
// preserves the original ErrInvalidInput chain so errors.Is(err, ErrInvalidInput)
// keeps working unchanged.
func (e *ValidationError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrInvalidInput
}

// NewDedupSkipped constructs a ValidationError marking a duplicate observation
// that was intentionally skipped (ClassDedupSkipped; IsError is false).
func NewDedupSkipped(message string) *ValidationError {
	return &ValidationError{Code: ClassDedupSkipped, Message: message}
}

// NewRejected constructs a ValidationError marking a policy rejection for the
// given rule. No partial row is written for a rejected observation.
func NewRejected(rule, message string) *ValidationError {
	return &ValidationError{Code: ClassRejected, Rule: rule, Message: message}
}

// NewFailed constructs a ValidationError wrapping a real persistence failure
// (ClassFailed). The cause is inspectable via errors.Unwrap / errors.Is.
func NewFailed(cause error, message string) *ValidationError {
	return &ValidationError{Code: ClassFailed, Cause: cause, Message: message}
}

// IsClass reports whether err is a ValidationError whose Code equals code. It
// combines errors.As with a code check so callers can distinguish a dedup skip
// from a policy rejection from a persistence failure (REQ-FOUND-003).
func IsClass(err error, code string) bool {
	var ve *ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	return ve.Code == code
}

// ConflictError wraps ErrConflict with details about the conflict.
type ConflictError struct {
	Entity string
	Reason string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict with %s: %s", e.Entity, e.Reason)
}

func (e *ConflictError) Unwrap() error {
	return ErrConflict
}

// IsNotFoundError checks if an error is a NotFoundError.
func IsNotFoundError(err error) bool {
	var e *NotFoundError
	return errors.As(err, &e)
}

// IsValidationError checks if an error is a ValidationError.
func IsValidationError(err error) bool {
	var e *ValidationError
	return errors.As(err, &e)
}

// IsConflictError checks if an error is a ConflictError.
func IsConflictError(err error) bool {
	var e *ConflictError
	return errors.As(err, &e)
}
