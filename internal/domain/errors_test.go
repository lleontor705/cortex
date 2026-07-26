package domain_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

// These tests pin REQ-FOUND-003: a ValidationError taxonomy that classifies
// passive-type outcomes (dedup_skipped, rejected, failed) so callers can
// distinguish an intentional dedup skip from a policy rejection from a real
// persistence failure via errors.As + code inspection.
//
// W1 scope: this is a STUB. The taxonomy type, codes, constructors, and IsClass
// are defined and proven here; wiring into the dedup/save path lands in W6.2
// (REQ-MCPH-002). No caller behavior changes.

// TestValidationError_Constructors_ErrorsAs proves each passive-outcome
// constructor yields a *domain.ValidationError discoverable via errors.As and
// carrying the expected classification code.
func TestValidationError_Constructors_ErrorsAs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		code string
	}{
		{"dedup_skipped", domain.NewDedupSkipped("duplicate dedup key"), domain.ClassDedupSkipped},
		{"rejected", domain.NewRejected("content-policy", "violates content rule"), domain.ClassRejected},
		{"failed", domain.NewFailed(errors.New("database is locked"), "persist failed"), domain.ClassFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var ve *domain.ValidationError
			if !errors.As(tc.err, &ve) {
				t.Fatalf("errors.As(%v, *ValidationError) = false; want true", tc.err)
			}
			if ve.Code != tc.code {
				t.Errorf("Code = %q; want %q", ve.Code, tc.code)
			}
		})
	}
}

// TestValidationError_DistinctCodes pins that the three classification codes are
// mutually distinct and match the spec literals.
func TestValidationError_DistinctCodes(t *testing.T) {
	t.Parallel()

	codes := []string{domain.ClassDedupSkipped, domain.ClassRejected, domain.ClassFailed}
	seen := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		if _, dup := seen[c]; dup {
			t.Fatalf("duplicate classification code %q among constants", c)
		}
		seen[c] = struct{}{}
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 distinct codes, got %d", len(seen))
	}

	want := map[string]string{
		domain.ClassDedupSkipped: "dedup_skipped",
		domain.ClassRejected:     "rejected",
		domain.ClassFailed:       "failed",
	}
	for constVal, literal := range want {
		if constVal != literal {
			t.Errorf("classification constant %q != spec literal %q", constVal, literal)
		}
	}
}

// TestValidationError_Unwrap_FailedCause proves a Failed ValidationError
// unwraps to the wrapped cause (REQ-FOUND-003 error scenario).
func TestValidationError_Unwrap_FailedCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("database is locked")
	failed := domain.NewFailed(cause, "persist failed")

	if got := errors.Unwrap(failed); got != cause {
		t.Errorf("errors.Unwrap(failed) = %v; want cause %v", got, cause)
	}
	if !errors.Is(failed, cause) {
		t.Errorf("errors.Is(failed, cause) = false; want true")
	}
}

// TestValidationError_DefectPin_NonDuplicateNotSwallowed is the defect-pin
// (REQ-FOUND-003 error scenario / REQ-MCPH-002): a non-duplicate persistence
// error wrapped in NewFailed is NOT classifiable as dedup_skipped, and a true
// dedup skip is not classifiable as failed.
func TestValidationError_DefectPin_NonDuplicateNotSwallowed(t *testing.T) {
	t.Parallel()

	failed := domain.NewFailed(fmt.Errorf("database is locked"), "persist failed")

	if domain.IsClass(failed, domain.ClassDedupSkipped) {
		t.Error("DEFECT-PIN VIOLATION: a failed persistence error was classifiable as dedup_skipped")
	}
	if !domain.IsClass(failed, domain.ClassFailed) {
		t.Error("expected a persistence failure to classify as failed")
	}

	skipped := domain.NewDedupSkipped("duplicate")
	if !domain.IsClass(skipped, domain.ClassDedupSkipped) {
		t.Error("expected a dedup skip to classify as dedup_skipped")
	}
	if domain.IsClass(skipped, domain.ClassFailed) {
		t.Error("a dedup skip must NOT classify as failed")
	}
	if domain.IsClass(skipped, domain.ClassRejected) {
		t.Error("a dedup skip must NOT classify as rejected")
	}
}

// TestValidationError_ErrorFormatting covers the Error() rendering for both
// legacy field-validation and the three classification codes.
func TestValidationError_ErrorFormatting(t *testing.T) {
	t.Parallel()

	// Legacy field-validation format is preserved byte-for-byte.
	legacy := &domain.ValidationError{Field: "title", Message: "cannot be empty"}
	if got := legacy.Error(); got != `validation error on field "title": cannot be empty` {
		t.Errorf("legacy Error() = %q; want preserved format", got)
	}

	// Classification formats.
	skipped := domain.NewDedupSkipped("dup key")
	if got := skipped.Error(); got != "dedup_skipped: dup key" {
		t.Errorf("dedup Error() = %q; want %q", got, "dedup_skipped: dup key")
	}

	rejected := domain.NewRejected("content-policy", "violated")
	if got := rejected.Error(); got != "rejected: violated (rule: content-policy)" {
		t.Errorf("rejected Error() = %q; want %q", got, "rejected: violated (rule: content-policy)")
	}

	failed := domain.NewFailed(errors.New("db locked"), "persist")
	if got := failed.Error(); got != "failed: persist: db locked" {
		t.Errorf("failed Error() = %q; want %q", got, "failed: persist: db locked")
	}
}

// TestValidationError_LegacyFieldValidation_Unchanged proves the type extension
// does not alter legacy field-validation behavior (zero local-mode behavior
// change).
func TestValidationError_LegacyFieldValidation_Unchanged(t *testing.T) {
	t.Parallel()

	legacy := &domain.ValidationError{Field: "title", Message: "cannot be empty"}

	if !domain.IsValidationError(legacy) {
		t.Error("legacy field-validation ValidationError must still be recognized by IsValidationError")
	}

	var ve *domain.ValidationError
	if !errors.As(legacy, &ve) {
		t.Error("errors.As must still match legacy ValidationError")
	}
	if ve.Field != "title" {
		t.Errorf("Field = %q; want %q", ve.Field, "title")
	}

	// Legacy field-validation error unwraps to ErrInvalidInput (unchanged chain).
	if got := errors.Unwrap(legacy); got != domain.ErrInvalidInput {
		t.Errorf("legacy Unwrap = %v; want ErrInvalidInput", got)
	}

	// Legacy field-validation error has no classification code.
	if domain.IsClass(legacy, domain.ClassDedupSkipped) {
		t.Error("legacy field-validation error must NOT classify as dedup_skipped")
	}
}
