package testutil

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

// Assertion helpers for Cortex tests.
// These functions provide domain-specific assertions to make tests more readable.

// AssertObservationEqual asserts that two observations are equal.
// It compares all fields except CreatedAt and UpdatedAt which are compared
// with a tolerance of 1 second to handle timing differences.
func AssertObservationEqual(t *testing.T, expected, actual *domain.Observation) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}
	if expected == nil || actual == nil {
		t.Fatalf("AssertObservationEqual: expected %v, got %v", expected, actual)
	}

	if expected.ID != actual.ID {
		t.Errorf("Observation.ID: expected %d, got %d", expected.ID, actual.ID)
	}
	if expected.Title != actual.Title {
		t.Errorf("Observation.Title: expected %q, got %q", expected.Title, actual.Title)
	}
	if expected.Content != actual.Content {
		t.Errorf("Observation.Content: expected %q, got %q", expected.Content, actual.Content)
	}
	if expected.Type != actual.Type {
		t.Errorf("Observation.Type: expected %q, got %q", expected.Type, actual.Type)
	}
	if expected.Project != actual.Project {
		t.Errorf("Observation.Project: expected %q, got %q", expected.Project, actual.Project)
	}
	if expected.Scope != actual.Scope {
		t.Errorf("Observation.Scope: expected %q, got %q", expected.Scope, actual.Scope)
	}
	if expected.SessionID != actual.SessionID {
		t.Errorf("Observation.SessionID: expected %q, got %q", expected.SessionID, actual.SessionID)
	}
	if expected.TopicKey != actual.TopicKey {
		t.Errorf("Observation.TopicKey: expected %q, got %q", expected.TopicKey, actual.TopicKey)
	}

	// Compare timestamps with tolerance
	AssertWithinDuration(t, expected.CreatedAt, actual.CreatedAt, time.Second)
	AssertWithinDuration(t, expected.UpdatedAt, actual.UpdatedAt, time.Second)
}

// AssertSessionEqual asserts that two sessions are equal.
func AssertSessionEqual(t *testing.T, expected, actual *domain.Session) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}
	if expected == nil || actual == nil {
		t.Fatalf("AssertSessionEqual: expected %v, got %v", expected, actual)
	}

	if expected.ID != actual.ID {
		t.Errorf("Session.ID: expected %q, got %q", expected.ID, actual.ID)
	}
	if expected.Project != actual.Project {
		t.Errorf("Session.Project: expected %q, got %q", expected.Project, actual.Project)
	}
	if expected.Directory != actual.Directory {
		t.Errorf("Session.Directory: expected %q, got %q", expected.Directory, actual.Directory)
	}
	if expected.Summary != actual.Summary {
		t.Errorf("Session.Summary: expected %q, got %q", expected.Summary, actual.Summary)
	}

	AssertWithinDuration(t, expected.StartedAt, actual.StartedAt, time.Second)

	// Handle nil EndedAt
	if expected.EndedAt == nil && actual.EndedAt != nil {
		t.Error("Session.EndedAt: expected nil, got non-nil")
	} else if expected.EndedAt != nil && actual.EndedAt == nil {
		t.Error("Session.EndedAt: expected non-nil, got nil")
	} else if expected.EndedAt != nil && actual.EndedAt != nil {
		AssertWithinDuration(t, *expected.EndedAt, *actual.EndedAt, time.Second)
	}
}

// AssertEdgeEqual asserts that two edges are equal.
func AssertEdgeEqual(t *testing.T, expected, actual *domain.Edge) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}
	if expected == nil || actual == nil {
		t.Fatalf("AssertEdgeEqual: expected %v, got %v", expected, actual)
	}

	if expected.ID != actual.ID {
		t.Errorf("Edge.ID: expected %d, got %d", expected.ID, actual.ID)
	}
	if expected.FromObsID != actual.FromObsID {
		t.Errorf("Edge.FromObsID: expected %d, got %d", expected.FromObsID, actual.FromObsID)
	}
	if expected.ToObsID != actual.ToObsID {
		t.Errorf("Edge.ToObsID: expected %d, got %d", expected.ToObsID, actual.ToObsID)
	}
	if expected.RelationType != actual.RelationType {
		t.Errorf("Edge.RelationType: expected %q, got %q", expected.RelationType, actual.RelationType)
	}
	if expected.Weight != actual.Weight {
		t.Errorf("Edge.Weight: expected %f, got %f", expected.Weight, actual.Weight)
	}

	AssertWithinDuration(t, expected.CreatedAt, actual.CreatedAt, time.Second)
}

// AssertPromptEqual asserts that two prompts are equal.
func AssertPromptEqual(t *testing.T, expected, actual *domain.Prompt) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}
	if expected == nil || actual == nil {
		t.Fatalf("AssertPromptEqual: expected %v, got %v", expected, actual)
	}

	if expected.ID != actual.ID {
		t.Errorf("Prompt.ID: expected %d, got %d", expected.ID, actual.ID)
	}
	if expected.Content != actual.Content {
		t.Errorf("Prompt.Content: expected %q, got %q", expected.Content, actual.Content)
	}
	if expected.Project != actual.Project {
		t.Errorf("Prompt.Project: expected %q, got %q", expected.Project, actual.Project)
	}
	if expected.SessionID != actual.SessionID {
		t.Errorf("Prompt.SessionID: expected %q, got %q", expected.SessionID, actual.SessionID)
	}

	AssertWithinDuration(t, expected.CreatedAt, actual.CreatedAt, time.Second)
}

// AssertErrorIs asserts that err wraps the target error.
func AssertErrorIs(t *testing.T, err error, target error) {
	t.Helper()

	if err == nil {
		t.Fatalf("AssertErrorIs: expected error wrapping %v, got nil", target)
	}

	if !errors.Is(err, target) {
		t.Errorf("AssertErrorIs: expected error to wrap %v, got %v", target, err)
	}
}

// AssertErrorAs asserts that err can be unwrapped to the target type.
func AssertErrorAs(t *testing.T, err error, target any) {
	t.Helper()

	if err == nil {
		t.Fatalf("AssertErrorAs: expected error, got nil")
	}

	if !errors.As(err, target) {
		t.Errorf("AssertErrorAs: expected error to be assignable to %T, got %T", target, err)
	}
}

// AssertNoError fails the test if err is not nil.
func AssertNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Errorf("AssertNoError: unexpected error: %v", err)
	}
}

// RequireNoError fails immediately if err is not nil.
// This is similar to AssertNoError but uses Fatalf which stops test execution.
func RequireNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("RequireNoError: %v", err)
	}
}

// AssertError fails the test if err is nil.
func AssertError(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Error("AssertError: expected error, got nil")
	}
}

// AssertWithinDuration asserts that two times are within the specified duration of each other.
func AssertWithinDuration(t *testing.T, expected, actual time.Time, delta time.Duration) {
	t.Helper()

	diff := expected.Sub(actual)
	if diff < 0 {
		diff = -diff
	}

	if diff > delta {
		t.Errorf("AssertWithinDuration: expected %v to be within %v of %v, diff was %v",
			expected, delta, actual, diff)
	}
}

// AssertEqual asserts that two values are equal using reflect.DeepEqual.
func AssertEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("AssertEqual: expected %v, got %v", expected, actual)
	}
}

// AssertNotEqual asserts that two values are not equal.
func AssertNotEqual(t *testing.T, expected, actual any) {
	t.Helper()

	if reflect.DeepEqual(expected, actual) {
		t.Errorf("AssertNotEqual: expected values to be different, both are %v", expected)
	}
}

// AssertTrue asserts that a condition is true.
func AssertTrue(t *testing.T, condition bool, message ...string) {
	t.Helper()

	if !condition {
		msg := "expected condition to be true"
		if len(message) > 0 {
			msg = message[0]
		}
		t.Error(msg)
	}
}

// AssertFalse asserts that a condition is false.
func AssertFalse(t *testing.T, condition bool, message ...string) {
	t.Helper()

	if condition {
		msg := "expected condition to be false"
		if len(message) > 0 {
			msg = message[0]
		}
		t.Error(msg)
	}
}

// AssertNil asserts that a value is nil.
func AssertNil(t *testing.T, value any) {
	t.Helper()

	if value != nil {
		t.Errorf("AssertNil: expected nil, got %v", value)
	}
}

// AssertNotNil asserts that a value is not nil.
func AssertNotNil(t *testing.T, value any) {
	t.Helper()

	if value == nil {
		t.Error("AssertNotNil: expected non-nil value")
	}
}

// AssertLen asserts that a slice, map, or string has the expected length.
func AssertLen(t *testing.T, value any, expectedLen int) {
	t.Helper()

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Map && v.Kind() != reflect.String {
		t.Fatalf("AssertLen: expected slice, map, or string, got %T", value)
	}

	if v.Len() != expectedLen {
		t.Errorf("AssertLen: expected length %d, got %d", expectedLen, v.Len())
	}
}

// AssertContains asserts that a slice contains an element.
func AssertContains[T comparable](t *testing.T, slice []T, element T) {
	t.Helper()

	for _, item := range slice {
		if item == element {
			return
		}
	}

	t.Errorf("AssertContains: slice does not contain %v", element)
}

// AssertNotContains asserts that a slice does not contain an element.
func AssertNotContains[T comparable](t *testing.T, slice []T, element T) {
	t.Helper()

	for _, item := range slice {
		if item == element {
			t.Errorf("AssertNotContains: slice should not contain %v", element)
			return
		}
	}
}

// AssertPanics asserts that a function panics.
func AssertPanics(t *testing.T, fn func()) {
	t.Helper()

	defer func() {
		if r := recover(); r == nil {
			t.Error("AssertPanics: expected panic, but function did not panic")
		}
	}()

	fn()
}

// PanicRecovered asserts that a function panics and returns the recovered value.
// Returns nil if the function does not panic.
//
// Example:
//
//	recovered := testutil.PanicRecovered(t, func() {
//	    panic("test panic")
//	})
//	if recovered != "test panic" {
//	    t.Errorf("expected 'test panic', got %v", recovered)
//	}
func PanicRecovered(t *testing.T, fn func()) interface{} {
	t.Helper()

	var recovered interface{}

	defer func() {
		if r := recover(); r != nil {
			recovered = r
		} else {
			t.Error("PanicRecovered: expected panic, but function did not panic")
		}
	}()

	fn()
	return recovered
}

// AssertNotFoundError asserts that an error is a NotFoundError with the expected type and ID.
func AssertNotFoundError(t *testing.T, err error, expectedType string, expectedID interface{}) {
	t.Helper()

	AssertError(t, err)

	var notFound *domain.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("AssertNotFoundError: expected NotFoundError, got %T", err)
	}

	if notFound.Type != expectedType {
		t.Errorf("NotFoundError.Type: expected %q, got %q", expectedType, notFound.Type)
	}
	if notFound.ID != expectedID {
		t.Errorf("NotFoundError.ID: expected %v, got %v", expectedID, notFound.ID)
	}
}

// AssertValidationError asserts that an error is a ValidationError with the expected field.
func AssertValidationError(t *testing.T, err error, expectedField string) {
	t.Helper()

	AssertError(t, err)

	var validation *domain.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("AssertValidationError: expected ValidationError, got %T", err)
	}

	if validation.Field != expectedField {
		t.Errorf("ValidationError.Field: expected %q, got %q", expectedField, validation.Field)
	}
}

// AssertConflictError asserts that an error is a ConflictError.
func AssertConflictError(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Error("AssertConflictError: expected error, got nil")
		return
	}

	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) {
		t.Errorf("AssertConflictError: expected ConflictError, got %T", err)
	}
}

// AssertObservationListEqual asserts that two observation slices are equal.
func AssertObservationListEqual(t *testing.T, expected, actual []*domain.Observation) {
	t.Helper()

	if len(expected) != len(actual) {
		t.Errorf("AssertObservationListEqual: expected %d observations, got %d",
			len(expected), len(actual))
		return
	}

	for i := range expected {
		AssertObservationEqual(t, expected[i], actual[i])
	}
}

// AssertEdgeListEqual asserts that two edge slices are equal.
func AssertEdgeListEqual(t *testing.T, expected, actual []*domain.Edge) {
	t.Helper()

	if len(expected) != len(actual) {
		t.Errorf("AssertEdgeListEqual: expected %d edges, got %d",
			len(expected), len(actual))
		return
	}

	for i := range expected {
		AssertEdgeEqual(t, expected[i], actual[i])
	}
}

// AssertPromptListEqual asserts that two prompt slices are equal.
func AssertPromptListEqual(t *testing.T, expected, actual []*domain.Prompt) {
	t.Helper()

	if len(expected) != len(actual) {
		t.Errorf("AssertPromptListEqual: expected %d prompts, got %d",
			len(expected), len(actual))
		return
	}

	for i := range expected {
		AssertPromptEqual(t, expected[i], actual[i])
	}
}

// AssertObservationIDs asserts that observations have the expected IDs in order.
func AssertObservationIDs(t *testing.T, observations []*domain.Observation, expectedIDs ...int64) {
	t.Helper()

	if len(observations) != len(expectedIDs) {
		t.Errorf("AssertObservationIDs: expected %d observations, got %d",
			len(expectedIDs), len(observations))
		return
	}

	for i, obs := range observations {
		if obs.ID != expectedIDs[i] {
			t.Errorf("Observation[%d].ID: expected %d, got %d", i, expectedIDs[i], obs.ID)
		}
	}
}

// AssertSessionIDs asserts that sessions have the expected IDs in order.
func AssertSessionIDs(t *testing.T, sessions []*domain.Session, expectedIDs ...string) {
	t.Helper()

	if len(sessions) != len(expectedIDs) {
		t.Errorf("AssertSessionIDs: expected %d sessions, got %d",
			len(expectedIDs), len(sessions))
		return
	}

	for i, sess := range sessions {
		if sess.ID != expectedIDs[i] {
			t.Errorf("Session[%d].ID: expected %q, got %q", i, expectedIDs[i], sess.ID)
		}
	}
}

// AssertObservationTypes asserts that observations have the expected types.
func AssertObservationTypes(t *testing.T, observations []*domain.Observation, expectedTypes ...string) {
	t.Helper()

	if len(observations) != len(expectedTypes) {
		t.Errorf("AssertObservationTypes: expected %d observations, got %d",
			len(expectedTypes), len(observations))
		return
	}

	for i, obs := range observations {
		if obs.Type != expectedTypes[i] {
			t.Errorf("Observation[%d].Type: expected %q, got %q", i, expectedTypes[i], obs.Type)
		}
	}
}

// AssertImportanceScoreEqual asserts that two importance scores are equal.
func AssertImportanceScoreEqual(t *testing.T, expected, actual *domain.ImportanceScore) {
	t.Helper()

	if expected == nil && actual == nil {
		return
	}
	if expected == nil || actual == nil {
		t.Fatalf("AssertImportanceScoreEqual: expected %v, got %v", expected, actual)
	}

	if expected.ObservationID != actual.ObservationID {
		t.Errorf("ImportanceScore.ObservationID: expected %d, got %d",
			expected.ObservationID, actual.ObservationID)
	}
	if expected.Score != actual.Score {
		t.Errorf("ImportanceScore.Score: expected %f, got %f", expected.Score, actual.Score)
	}
	if expected.AccessCount != actual.AccessCount {
		t.Errorf("ImportanceScore.AccessCount: expected %d, got %d",
			expected.AccessCount, actual.AccessCount)
	}

	AssertWithinDuration(t, expected.LastAccessed, actual.LastAccessed, time.Second)
	AssertWithinDuration(t, expected.UpdatedAt, actual.UpdatedAt, time.Second)
}
