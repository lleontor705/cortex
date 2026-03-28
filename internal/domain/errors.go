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
)

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

// ValidationError wraps ErrInvalidInput with details about what failed validation.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error on field %q: %s", e.Field, e.Message)
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidInput
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
