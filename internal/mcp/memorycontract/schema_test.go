package memorycontract

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mark3labs/mcp-go/mcp"
)

// Validate the SDK's serialized structuredContent against the advertised
// cortex_save/cortex_handoff schema, not merely the Go payload types.
func TestWriteOutputSchemaSerializedResults(t *testing.T) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(WriteOutputSchemaJSON, &schema); err != nil {
		t.Fatal(err)
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalRefPayload(1)
	if err != nil {
		t.Fatal(err)
	}
	public, err := NewPublicRefPayload("6e9656d8-3b43-4e48-9786-3e61c09e8e70")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		payload any
		invalid bool
	}{
		{"local_created", SaveStructured{ObservationRef: local, Status: "created"}, false},
		{"public_replayed", SaveStructured{ObservationRef: public, Status: "replayed"}, false},
		{"local_updated", SaveStructured{ObservationRef: local, Status: "updated"}, false},
		{"validation_non_retryable", Validationf("invalid request"), false},
		{"timeout_retryable", FromError(context.DeadlineExceeded), false},
		{"unavailable_retryable", Unavailablef("service unavailable"), false},
		{"unknown_error_field", map[string]any{"error": map[string]any{"code": "validation", "message": "invalid", "unexpected": true}}, true},
		{"retryable_must_be_boolean", map[string]any{"error": map[string]any{"code": "timeout", "message": "timeout", "retryable": "true"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := mcp.NewToolResultStructured(tc.payload, "result")
			_, result.IsError = tc.payload.(ErrorStructured)
			raw, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			var wire map[string]any
			if decodeErr := json.Unmarshal(raw, &wire); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			content, ok := wire["structuredContent"].(map[string]any)
			if !ok {
				t.Fatal("SDK result omitted structuredContent")
			}
			if payload, isError := tc.payload.(ErrorStructured); isError {
				body, bodyOK := content["error"].(map[string]any)
				if !bodyOK || body["retryable"] != payload.Error.Retryable {
					t.Fatal("serialized error lost its retryability")
				}
			}
			validationErr := resolved.Validate(content)
			if tc.invalid {
				if validationErr == nil {
					t.Fatal("schema accepted an invalid error payload")
				}
			} else if validationErr != nil {
				t.Fatalf("advertised output schema rejects SDK structuredContent: %v", validationErr)
			}
		})
	}
}
