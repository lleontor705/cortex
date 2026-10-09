package embedding

// Typed provider errors for the OpenAI-compatible embedding surface (P5
// provider-conformance). Before this type existed the client collapsed every
// non-200 into "API returned status %d", which made the nan.builders contract
// quirk — an UNKNOWN MODEL id answered with HTTP 401 auth_error —
// indistinguishable from a bad API key, sending operators to rotate
// credentials instead of fixing the model id.
//
// SECURITY: APIError is derived exclusively from the bounded response body.
// It never contains the API key and never echoes the request payload.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// apiErrorMaxBody bounds how much of an error body is parsed; provider error
// payloads are tiny and anything larger is truncated, never trusted.
const apiErrorMaxBody = 64 << 10

// apiErrorMessageMax bounds the surfaced provider message length.
const apiErrorMessageMax = 200

// Error classes returned by APIError.Class. The model-not-found class exists
// because OpenAI-compatible gateways disagree on unknown-model signaling:
// first-party OpenAI returns 404 model_not_found, Nan returns 401 auth_error
// with a body that names the model.
const (
	ClassAuth             = "auth-failure"
	ClassModelNotFound    = "model-not-found"
	ClassEndpointNotFound = "endpoint-not-found"
	ClassRateLimited      = "rate-limited"
	ClassServer           = "provider-server-error"
	ClassBadRequest       = "bad-request"
	ClassUnknown          = "unknown"
)

// APIError is a typed, secret-free provider error.
type APIError struct {
	Provider string
	Status   int
	// Type is the provider error type/code (e.g. auth_error, model_not_found).
	Type string
	// Message is the bounded provider message; never contains credentials.
	Message string
}

func (e *APIError) Error() string {
	if e == nil {
		return "embedding: nil provider error"
	}
	switch {
	case e.Type != "" && e.Message != "":
		return fmt.Sprintf("%s: API error %d (%s): %s", e.Provider, e.Status, e.Type, e.Message)
	case e.Message != "":
		return fmt.Sprintf("%s: API error %d: %s", e.Provider, e.Status, e.Message)
	default:
		return fmt.Sprintf("%s: API error %d", e.Provider, e.Status)
	}
}

// Class buckets the error into an actionable category. The Nan quirk is
// codified here: a 401/403 whose bounded body names an unknown model
// classifies as model-not-found, NOT auth-failure.
func (e *APIError) Class() string {
	if e == nil {
		return ClassUnknown
	}
	body := strings.ToLower(e.Type + " " + e.Message)
	modelMissing := strings.Contains(body, "model") &&
		(strings.Contains(body, "not found") ||
			strings.Contains(body, "unknown") ||
			strings.Contains(body, "does not exist") ||
			strings.Contains(body, "invalid model") ||
			strings.Contains(body, "no such model") ||
			strings.Contains(body, "model_not_found"))
	switch {
	case e.Status == 401 || e.Status == 403:
		if modelMissing {
			return ClassModelNotFound
		}
		return ClassAuth
	case e.Status == 404:
		if modelMissing {
			return ClassModelNotFound
		}
		return ClassEndpointNotFound
	case e.Status == 400:
		if modelMissing {
			return ClassModelNotFound
		}
		return ClassBadRequest
	case e.Status == 429:
		return ClassRateLimited
	case e.Status >= 500:
		return ClassServer
	default:
		return ClassUnknown
	}
}

// ClassifyError extracts a typed provider error from err when one is present.
func ClassifyError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) && ae != nil {
		return ae, true
	}
	return nil, false
}

// NewAPIError parses a bounded provider error body into an APIError. It never
// fails: unparsable bodies degrade to a status-only error. The reader is NOT
// closed — the caller owns the body lifecycle.
func NewAPIError(provider string, status int, body io.Reader) *APIError {
	ae := &APIError{Provider: provider, Status: status}
	if body == nil {
		return ae
	}
	b, err := io.ReadAll(io.LimitReader(body, apiErrorMaxBody))
	if err != nil || len(b) == 0 {
		return ae
	}
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
		// Non-envelope variants some gateways emit.
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	if err := json.Unmarshal(b, &parsed); err == nil {
		ae.Message = firstNonEmpty(parsed.Error.Message, parsed.Message, parsed.Detail)
		ae.Type = firstNonEmpty(parsed.Error.Type, codeToString(parsed.Error.Code))
	}
	if len(ae.Message) > apiErrorMessageMax {
		ae.Message = ae.Message[:apiErrorMessageMax]
	}
	return ae
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func codeToString(code any) string {
	switch v := code.(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return ""
	}
}
