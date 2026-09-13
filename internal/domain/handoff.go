package domain

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

// MaxHandoffPayloadSize matches the runtime's accepted request-body limit.
// Durable handoffs reject larger canonical payloads rather than truncating them.
const MaxHandoffPayloadSize = 1 << 20

// HandoffErrorCode is the stable, transport-neutral handoff failure class.
type HandoffErrorCode string

const (
	HandoffErrorValidation      HandoffErrorCode = "validation"
	HandoffErrorPayloadTooLarge HandoffErrorCode = "payload_too_large"
	HandoffErrorUnauthorized    HandoffErrorCode = "unauthorized"
	HandoffErrorForbidden       HandoffErrorCode = "forbidden"
	HandoffErrorConflict        HandoffErrorCode = "conflict"
	HandoffErrorUnavailable     HandoffErrorCode = "unavailable"
	HandoffErrorTimeout         HandoffErrorCode = "timeout"
	HandoffErrorPersistence     HandoffErrorCode = "persistence"
)

// HandoffError contains only a stable classification and safe message. It must
// never contain an idempotency key, observation reference, payload, or secret.
type HandoffError struct {
	Code      HandoffErrorCode
	Message   string
	Retryable bool
	Operation string
	Context   string
}

func (e *HandoffError) Error() string {
	if e == nil {
		return ErrHandoffUnavailable.Message
	}
	return e.Message
}

func (e *HandoffError) Is(target error) bool {
	other, ok := target.(*HandoffError)
	return ok && e != nil && other != nil && e.Code == other.Code
}

var (
	ErrHandoffValidation      = &HandoffError{Code: HandoffErrorValidation, Message: "invalid handoff request"}
	ErrHandoffPayloadTooLarge = &HandoffError{Code: HandoffErrorPayloadTooLarge, Message: "handoff payload exceeds accepted size"}
	ErrHandoffUnauthorized    = &HandoffError{Code: HandoffErrorUnauthorized, Message: "handoff authorization required"}
	ErrHandoffForbidden       = &HandoffError{Code: HandoffErrorForbidden, Message: "handoff is not permitted"}
	ErrHandoffConflict        = &HandoffError{Code: HandoffErrorConflict, Message: "handoff conflicts with an existing receipt"}
	ErrHandoffUnavailable     = &HandoffError{Code: HandoffErrorUnavailable, Message: "handoff service unavailable", Retryable: true}
	ErrHandoffTimeout         = &HandoffError{Code: HandoffErrorTimeout, Message: "handoff timed out", Retryable: true}
	ErrHandoffPersistence     = &HandoffError{Code: HandoffErrorPersistence, Message: "handoff could not be persisted", Retryable: true}
)

// Validate enforces the exclusive local/public ObservationRef union.
func (r ObservationRef) Validate() error {
	if (r.LocalID == nil) == (r.PublicID == nil) {
		return ErrHandoffValidation
	}
	return nil
}

// SaveObservationInput is the transport-neutral observation portion of a
// handoff. Derived identifiers and timestamps are deliberately excluded.
type SaveObservationInput struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Type       string   `json:"type"`
	Project    string   `json:"project"`
	Scope      string   `json:"scope"`
	SessionID  string   `json:"session_id"`
	TopicKey   string   `json:"topic_key"`
	Confidence float64  `json:"confidence"`
	Source     string   `json:"source"`
	Tags       []string `json:"tags,omitempty"`
}

type HandoffRelationInput struct {
	Target     ObservationRef `json:"target"`
	Type       string         `json:"type"`
	Weight     float64        `json:"weight"`
	Confidence float64        `json:"confidence"`
	Reasoning  string         `json:"reasoning"`
}

type HandoffRequest struct {
	IdempotencyKey  string                `json:"idempotency_key"`
	Observation     SaveObservationInput  `json:"observation"`
	Relation        *HandoffRelationInput `json:"relation,omitempty"`
	CapabilityTuple json.RawMessage       `json:"capability_tuple,omitempty"`
}

// CanonicalHandoff is the complete payload persisted in a receipt. The
// idempotency key and request-derived security scope are intentionally absent.
type CanonicalHandoff struct {
	Observation     SaveObservationInput  `json:"observation"`
	Relation        *HandoffRelationInput `json:"relation,omitempty"`
	CapabilityTuple json.RawMessage       `json:"capability_tuple,omitempty"`
}

// CanonicalizeHandoff returns deterministic full JSON bytes and their SHA-256.
// CapabilityTuple is normalized as JSON data only; it is never interpreted.
func CanonicalizeHandoff(req HandoffRequest) (CanonicalHandoff, []byte, [32]byte, error) {
	req = cloneHandoffRequest(req)
	if !validHandoffText(req) || req.IdempotencyKey == "" || req.Observation.Title == "" || req.Observation.Content == "" {
		return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffValidation
	}

	tupleVal, err := decodeTuple(req.CapabilityTuple)
	if err != nil {
		return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffValidation
	}

	meta := make(map[string]string)
	if req.IdempotencyKey != "" {
		meta["idempotency_key"] = req.IdempotencyKey
	}
	if req.Observation.Project != "" {
		meta["project"] = req.Observation.Project
	}
	if req.Observation.TopicKey != "" {
		meta["topic_key"] = req.Observation.TopicKey
	}
	if req.Observation.Scope != "" {
		meta["scope"] = req.Observation.Scope
	}
	if req.Observation.Type != "" {
		meta["type"] = req.Observation.Type
	}
	if req.Observation.Source != "" {
		meta["source"] = req.Observation.Source
	}
	if req.Observation.SessionID != "" {
		meta["session_id"] = req.Observation.SessionID
	}
	for i, tag := range req.Observation.Tags {
		meta[fmt.Sprintf("tag[%d]", i)] = tag
	}
	if req.Relation != nil && req.Relation.Type != "" {
		meta["relation_type"] = req.Relation.Type
	}
	if tupleVal != nil {
		collectCapabilityMetadata(meta, tupleVal)
	}
	if err := privacy.ValidateMetadata(meta); err != nil {
		return CanonicalHandoff{}, nil, [32]byte{}, err
	}

	named := []privacy.NamedField{
		{Name: "title", Value: req.Observation.Title, Required: true},
		{Name: "content", Value: req.Observation.Content, Required: true},
	}
	if req.Relation != nil && req.Relation.Reasoning != "" {
		named = append(named, privacy.NamedField{
			Name: "reasoning", Value: req.Relation.Reasoning, Required: false,
		})
	}
	protectedFields, err := privacy.ProtectNamedFields(named...)
	if err != nil {
		return CanonicalHandoff{}, nil, [32]byte{}, err
	}
	req.Observation.Title = protectedFields["title"].ProtectedValue
	req.Observation.Content = protectedFields["content"].ProtectedValue
	if req.Relation != nil && req.Relation.Reasoning != "" {
		req.Relation.Reasoning = protectedFields["reasoning"].ProtectedValue
	}

	requestPayload, err := json.Marshal(req)
	if err != nil {
		return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffValidation
	}
	if len(requestPayload) > MaxHandoffPayloadSize {
		return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffPayloadTooLarge
	}
	if req.Relation != nil {
		if req.Relation.Type == "" || req.Relation.Target.Validate() != nil {
			return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffValidation
		}
	}

	var tuple json.RawMessage
	if len(req.CapabilityTuple) > 0 {
		tuple, err = json.Marshal(tupleVal)
		if err != nil {
			return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffValidation
		}
	}
	canonical := CanonicalHandoff{
		Observation:     req.Observation,
		Relation:        cloneRelation(req.Relation),
		CapabilityTuple: append(json.RawMessage(nil), tuple...),
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffValidation
	}
	if len(payload) > MaxHandoffPayloadSize {
		return CanonicalHandoff{}, nil, [32]byte{}, ErrHandoffPayloadTooLarge
	}
	return canonical, payload, sha256.Sum256(payload), nil
}

func decodeTuple(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if !utf8.Valid(raw) {
		return nil, errors.New("invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
}

func collectCapabilityMetadata(meta map[string]string, val any) {
	idx := 0
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				meta[fmt.Sprintf("capability_tuple_key[%d]", idx)] = k
				idx++
				walk(t[k])
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		case string:
			meta[fmt.Sprintf("capability_tuple_val[%d]", idx)] = t
			idx++
			trimmed := strings.TrimSpace(t)
			if (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
				(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) {
				var inner any
				dec := json.NewDecoder(strings.NewReader(trimmed))
				dec.UseNumber()
				if err := dec.Decode(&inner); err == nil {
					var extra any
					if errors.Is(dec.Decode(&extra), io.EOF) {
						walk(inner)
					}
				}
			}
		}
	}
	walk(val)
}

type HandoffScope string

type HandoffAuthorizer interface {
	AuthorizeAll(context.Context, Principal, HandoffRequest) (HandoffScope, error)
}

type HandoffExecutor interface {
	ExecuteHandoff(context.Context, HandoffScope, string, CanonicalHandoff, [32]byte) (ObservationWriteResult, error)
}

// HandoffCoordinator keeps authorization and scope derivation ahead of the
// single UoW boundary. CapabilityTuple is merely forwarded as opaque evidence.
type HandoffCoordinator struct {
	authorizer HandoffAuthorizer
	executor   HandoffExecutor
}

func NewHandoffCoordinator(authorizer HandoffAuthorizer, executor HandoffExecutor) *HandoffCoordinator {
	return &HandoffCoordinator{authorizer: authorizer, executor: executor}
}

func (c *HandoffCoordinator) Execute(ctx context.Context, principal Principal, req HandoffRequest) (ObservationWriteResult, error) {
	request := cloneHandoffRequest(req)
	canonical, _, hash, err := CanonicalizeHandoff(request)
	if err != nil {
		return ObservationWriteResult{}, err
	}
	if c == nil || c.authorizer == nil || c.executor == nil {
		return ObservationWriteResult{}, ErrHandoffUnavailable
	}
	scope, err := c.authorizer.AuthorizeAll(ctx, clonePrincipal(principal), cloneHandoffRequest(request))
	if err != nil {
		return ObservationWriteResult{}, dependencyHandoffError(err, HandoffErrorUnavailable, "authorize")
	}
	if scope == "" {
		return ObservationWriteResult{}, ErrHandoffForbidden
	}
	result, err := c.executor.ExecuteHandoff(ctx, scope, request.IdempotencyKey, cloneCanonicalHandoff(canonical), hash)
	if err != nil {
		return ObservationWriteResult{}, dependencyHandoffError(err, HandoffErrorPersistence, "execute")
	}
	if result.Ref.Validate() != nil || !validWriteStatus(result.Status) {
		return ObservationWriteResult{}, ErrHandoffPersistence
	}
	return result, nil
}

func validHandoffText(req HandoffRequest) bool {
	values := []string{
		req.IdempotencyKey, req.Observation.Title, req.Observation.Content,
		req.Observation.Type, req.Observation.Project, req.Observation.Scope,
		req.Observation.SessionID, req.Observation.TopicKey, req.Observation.Source,
	}
	values = append(values, req.Observation.Tags...)
	if req.Relation != nil {
		values = append(values, req.Relation.Type, req.Relation.Reasoning)
	}
	for _, value := range values {
		if !utf8.ValidString(value) {
			return false
		}
	}
	return true
}

func cloneHandoffRequest(req HandoffRequest) HandoffRequest {
	req.Observation.Tags = append([]string(nil), req.Observation.Tags...)
	req.Relation = cloneRelation(req.Relation)
	req.CapabilityTuple = append(json.RawMessage(nil), req.CapabilityTuple...)
	return req
}

func cloneRelation(relation *HandoffRelationInput) *HandoffRelationInput {
	if relation == nil {
		return nil
	}
	clone := *relation
	if relation.Target.LocalID != nil {
		id := *relation.Target.LocalID
		clone.Target.LocalID = &id
	}
	if relation.Target.PublicID != nil {
		id := *relation.Target.PublicID
		clone.Target.PublicID = &id
	}
	return &clone
}

func cloneCanonicalHandoff(canonical CanonicalHandoff) CanonicalHandoff {
	canonical.Observation.Tags = append([]string(nil), canonical.Observation.Tags...)
	canonical.Relation = cloneRelation(canonical.Relation)
	canonical.CapabilityTuple = append(json.RawMessage(nil), canonical.CapabilityTuple...)
	return canonical
}

func clonePrincipal(principal Principal) Principal {
	principal.WorkspaceIDs = append([]string(nil), principal.WorkspaceIDs...)
	principal.Roles = append([]string(nil), principal.Roles...)
	principal.Scopes = append([]string(nil), principal.Scopes...)
	principal.ProjectIDs = append([]string(nil), principal.ProjectIDs...)
	principal.ClassificationClearance = append([]string(nil), principal.ClassificationClearance...)
	return principal
}

func dependencyHandoffError(err error, fallback HandoffErrorCode, operation string) *HandoffError {
	code := fallback
	var typed *HandoffError
	if errors.As(err, &typed) && typed != nil {
		code = typed.Code
	}
	switch code {
	case HandoffErrorValidation:
		return &HandoffError{Code: code, Message: ErrHandoffValidation.Message, Operation: operation, Context: "dependency rejected request"}
	case HandoffErrorPayloadTooLarge:
		return &HandoffError{Code: code, Message: ErrHandoffPayloadTooLarge.Message, Operation: operation, Context: "dependency rejected request size"}
	case HandoffErrorUnauthorized:
		return &HandoffError{Code: code, Message: ErrHandoffUnauthorized.Message, Operation: operation, Context: "authorization dependency denied request"}
	case HandoffErrorForbidden:
		return &HandoffError{Code: code, Message: ErrHandoffForbidden.Message, Operation: operation, Context: "authorization dependency denied request"}
	case HandoffErrorConflict:
		return &HandoffError{Code: code, Message: ErrHandoffConflict.Message, Operation: operation, Context: "persistence dependency reported conflict"}
	case HandoffErrorUnavailable:
		return &HandoffError{Code: code, Message: ErrHandoffUnavailable.Message, Retryable: true, Operation: operation, Context: "authorization dependency failed"}
	case HandoffErrorTimeout:
		return &HandoffError{Code: code, Message: ErrHandoffTimeout.Message, Retryable: true, Operation: operation, Context: "dependency timed out"}
	default:
		return &HandoffError{Code: HandoffErrorPersistence, Message: ErrHandoffPersistence.Message, Retryable: true, Operation: operation, Context: "persistence dependency failed"}
	}
}

func validWriteStatus(status WriteStatus) bool {
	return status == WriteStatusCreated || status == WriteStatusReplayed || status == WriteStatusUpdated
}
