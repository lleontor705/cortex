package privacy_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func TestPrivacyUnsafeMetadataKeyLabel(t *testing.T) {
	const canary = "metadata-key-canary-7f3c"
	meta := map[string]string{
		canary: "<private>unclosed",
	}

	err := privacy.ValidateMetadata(meta)
	if err == nil {
		t.Fatalf("expected error from ValidateMetadata with invalid private marker, got nil")
	}

	var privErr *privacy.Error
	if !errors.As(err, &privErr) {
		t.Fatalf("expected *privacy.Error, got %T", err)
	}

	if privErr.Code != privacy.ErrCodeInvalidMarker {
		t.Errorf("expected Code %q, got %q", privacy.ErrCodeInvalidMarker, privErr.Code)
	}

	if privErr.Field != "metadata" {
		t.Errorf("expected Field %q, got %q", "metadata", privErr.Field)
	}

	if strings.Contains(err.Error(), canary) {
		t.Errorf("Error() leaked canary: %s", err.Error())
	}

	if strings.Contains(privErr.Field, canary) {
		t.Errorf("Error.Field leaked canary: %s", privErr.Field)
	}

	data, mErr := json.Marshal(err)
	if mErr != nil {
		t.Fatalf("json.Marshal(err) failed: %v", mErr)
	}
	if strings.Contains(string(data), canary) {
		t.Errorf("json.Marshal(err) leaked canary: %s", string(data))
	}
}

func TestPrivacyDuplicateNamedField(t *testing.T) {
	const canary = "duplicate-name-canary-9a2e"
	fields := []privacy.NamedField{
		{Name: canary, Value: "Valid public content one", Required: true},
		{Name: canary, Value: "Valid public content two", Required: true},
	}

	got, err := privacy.ProtectNamedFields(fields...)
	if got != nil {
		t.Errorf("expected nil result for duplicate named fields, got %v", got)
	}
	if err == nil {
		t.Fatalf("expected error for duplicate named fields, got nil")
	}

	var privErr *privacy.Error
	if !errors.As(err, &privErr) {
		t.Fatalf("expected *privacy.Error, got %T", err)
	}

	if privErr.Code != privacy.ErrCodeValidation {
		t.Errorf("expected Code %q, got %q", privacy.ErrCodeValidation, privErr.Code)
	}

	if privErr.Field != "named_fields" {
		t.Errorf("expected Field %q, got %q", "named_fields", privErr.Field)
	}

	if strings.Contains(err.Error(), canary) {
		t.Errorf("Error() leaked canary: %s", err.Error())
	}

	if strings.Contains(privErr.Field, canary) {
		t.Errorf("Error.Field leaked canary: %s", privErr.Field)
	}

	data, mErr := json.Marshal(err)
	if mErr != nil {
		t.Fatalf("json.Marshal(err) failed: %v", mErr)
	}
	if strings.Contains(string(data), canary) {
		t.Errorf("json.Marshal(err) leaked canary: %s", string(data))
	}
}
