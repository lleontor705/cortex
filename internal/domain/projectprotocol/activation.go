package projectprotocol

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

// Activation is the audited compare-and-swap pointer from an artifact to
// exactly one of its revisions within a scope. Rollback creates a NEW
// activation event pointing at a previous revision; it never rewrites
// history. ActivationRevision is the monotonic CAS token: concurrent
// activate/rollback operations must win exactly once by comparing
// ExpectedActivationRevision against the stored value.
type Activation struct {
	ArtifactID         string    `json:"artifact_id"`
	Revision           int64     `json:"revision"`
	ActivationRevision int64     `json:"activation_revision"`
	ActivatedBy        string    `json:"activated_by"`
	ActivatedAt        time.Time `json:"activated_at"`
	Reason             string    `json:"reason,omitempty"`
}

// Validate checks the activation invariants.
func (a Activation) Validate() error {
	if a.ArtifactID == "" || len(a.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if a.Revision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "revision must be at least 1"}
	}
	if a.ActivationRevision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "activation revision must be at least 1"}
	}
	if a.ActivatedBy != "" && !utf8.ValidString(a.ActivatedBy) {
		return ErrInvalidUTF8
	}
	return nil
}

// Preconditions is the optimistic concurrency guard for artifact writes.
// Exactly one form may be set: ExpectedRevision compares against the stored
// latest revision number; IfMatchETag compares against the stored artifact
// ETag. A stale precondition MUST fail with revision_conflict and mutate
// nothing.
type Preconditions struct {
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
	IfMatchETag      string `json:"if_match_etag,omitempty"`
}

// Validate enforces the exclusive union and the canonical ETag grammar.
func (p Preconditions) Validate() error {
	hasRevision := p.ExpectedRevision != nil
	hasETag := p.IfMatchETag != ""
	if hasRevision == hasETag {
		return &Error{Code: ErrCodeValidation, Message: "exactly one precondition (expected_revision or if_match_etag) is required"}
	}
	if hasRevision && *p.ExpectedRevision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "expected_revision must be non-negative"}
	}
	if hasETag {
		if err := ValidateETagShape(p.IfMatchETag); err != nil {
			return err
		}
	}
	return nil
}

// ActivateInput activates one revision under activation CAS.
type ActivateInput struct {
	ArtifactID                 string `json:"artifact_id"`
	Revision                   int64  `json:"revision"`
	ExpectedActivationRevision int64  `json:"expected_activation_revision"`
}

// Validate checks the activation request invariants.
func (in ActivateInput) Validate() error {
	if in.ArtifactID == "" || len(in.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if in.Revision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "revision must be at least 1"}
	}
	if in.ExpectedActivationRevision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "expected_activation_revision must be non-negative"}
	}
	return nil
}

// RollbackInput repoints the activation at a previous revision under
// activation CAS, recording an explicit reason for the audit trail.
type RollbackInput struct {
	ArtifactID                 string `json:"artifact_id"`
	ToRevision                 int64  `json:"to_revision"`
	ExpectedActivationRevision int64  `json:"expected_activation_revision"`
	Reason                     string `json:"reason"`
}

// Validate checks the rollback request invariants.
func (in RollbackInput) Validate() error {
	if in.ArtifactID == "" || len(in.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if in.ToRevision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "to_revision must be at least 1"}
	}
	if in.ExpectedActivationRevision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "expected_activation_revision must be non-negative"}
	}
	if err := validateReason(in.Reason); err != nil {
		return err
	}
	return nil
}

// SoftDeleteInput is the only deletion transition in v1: it marks the
// artifact deleted (excluded from default lists and the effective protocol)
// while revisions, activations and events are retained indefinitely. There
// is deliberately NO hard-delete or purge input.
//
// IfMatchETag carries the REQUIRED and ONLY compare-and-swap guard for
// deletion (REQ-API-003: "delete requires If-Match"): the caller MUST
// present the artifact's current canonical ETag. There is deliberately NO
// expected_revision form — deletion is ETag-addressed, so a stale request
// can never delete state it has not seen. A stale ETag fails with
// revision_conflict and mutates nothing. DeletedBy (the acting principal)
// and Reason are mandatory; the store assigns DeletedAt.
type SoftDeleteInput struct {
	ArtifactID  string `json:"artifact_id"`
	IfMatchETag string `json:"if_match_etag"`
	DeletedBy   string `json:"deleted_by"`
	Reason      string `json:"reason"` // required
}

// Validate checks the soft-delete request invariants: the If-Match ETag is
// required (and is the only accepted precondition), the acting principal is
// mandatory, and the reason is required and bounded.
func (in SoftDeleteInput) Validate() error {
	if in.ArtifactID == "" || len(in.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if err := ValidateETagShape(in.IfMatchETag); err != nil {
		return err
	}
	if err := validateActor(in.DeletedBy, "deleted_by"); err != nil {
		return err
	}
	if err := validateReason(in.Reason); err != nil {
		return err
	}
	return nil
}

func validateReason(reason string) error {
	if reason == "" {
		return &Error{Code: ErrCodeValidation, Message: "reason must not be empty"}
	}
	if !utf8.ValidString(reason) {
		return ErrInvalidUTF8
	}
	if n := utf8.RuneCountInString(reason); n > MaxReasonRunes {
		return &Error{Code: ErrCodeValidation, Message: "reason exceeds maximum length", Limit: MaxReasonRunes}
	}
	return nil
}

// IdempotencyKey is the caller-supplied idempotency identifier for artifact
// creation and revision writes. Keys are compared within the
// principal-derived workspace/project scope, never globally.
type IdempotencyKey string

// ValidateIdempotencyKey enforces 1..128 printable ASCII bytes (no control
// characters, no invalid UTF-8).
func ValidateIdempotencyKey(s string) error {
	if s == "" {
		return &Error{Code: ErrCodeValidation, Message: "idempotency key must not be empty"}
	}
	if len(s) > MaxIdempotencyKeyBytes {
		return &Error{Code: ErrCodeValidation, Message: "idempotency key exceeds maximum length", Limit: MaxIdempotencyKeyBytes}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return &Error{Code: ErrCodeValidation, Message: "idempotency key must be printable ASCII without spaces"}
		}
	}
	return nil
}

// NewIdempotencyKey validates and wraps a key.
func NewIdempotencyKey(s string) (IdempotencyKey, error) {
	if err := ValidateIdempotencyKey(s); err != nil {
		return "", err
	}
	return IdempotencyKey(s), nil
}

// String returns the raw key.
func (k IdempotencyKey) String() string { return string(k) }

// IdempotencyVerdict classifies a retry against a stored record.
type IdempotencyVerdict string

const (
	// IdempotencyNew means no record exists for the key: execute the write.
	IdempotencyNew IdempotencyVerdict = "new"
	// IdempotencyReplay means the same key and payload digest were seen:
	// return the original result without re-executing.
	IdempotencyReplay IdempotencyVerdict = "replay"
	// IdempotencyConflict means the key was reused with a different payload
	// digest: fail with idempotency_conflict and mutate nothing.
	IdempotencyConflict IdempotencyVerdict = "conflict"
)

// IdempotencyRecord is the durable evidence of a previous keyed write.
type IdempotencyRecord struct {
	Key           IdempotencyKey `json:"key"`
	RequestDigest string         `json:"request_digest"` // sha256:<hex> of the canonical request payload
	ArtifactID    string         `json:"artifact_id"`
	Revision      int64          `json:"revision"`
}

// Validate checks the record invariants.
func (r IdempotencyRecord) Validate() error {
	if err := ValidateIdempotencyKey(string(r.Key)); err != nil {
		return err
	}
	if r.RequestDigest == "" {
		return &Error{Code: ErrCodeValidation, Message: "request digest must be present"}
	}
	return nil
}

// ClassifyIdempotency applies the verdict rules: same key+digest replays,
// same key with a different digest conflicts, absent key is new.
func ClassifyIdempotency(stored *IdempotencyRecord, key IdempotencyKey, requestDigest string) IdempotencyVerdict {
	if stored == nil {
		return IdempotencyNew
	}
	if stored.Key != key {
		return IdempotencyNew
	}
	if stored.RequestDigest == requestDigest {
		return IdempotencyReplay
	}
	return IdempotencyConflict
}

// SaveArtifactInput is the transport-neutral artifact creation payload.
// Project/Kind/Key/Title/Content are validated by ValidateSaveArtifactInput;
// derived identity, revision numbers and timestamps are store-assigned.
type SaveArtifactInput struct {
	Project        string          `json:"project"`
	Kind           string          `json:"kind"`
	Key            string          `json:"key"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	ContentType    string          `json:"content_type,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Precedence     int32           `json:"precedence,omitempty"`
	IdempotencyKey IdempotencyKey  `json:"idempotency_key"`
}

// ValidateSaveArtifactInput checks the full creation contract, including the
// approved content and canonical metadata limits and the REQUIRED
// idempotency key: creation is idempotent per key + request digest and MUST
// NOT be dispatched without one. An empty project means the workspace
// default scope.
func ValidateSaveArtifactInput(in SaveArtifactInput) error {
	scope := ScopeWorkspaceDefault
	if in.Project != "" {
		scope = ScopeProject
	}
	if err := ValidateProjectRef(scope, in.Project); err != nil {
		return err
	}
	if _, err := ParseKind(in.Kind); err != nil {
		return err
	}
	if err := ValidateKey(in.Key); err != nil {
		return err
	}
	if err := ValidateTitle(in.Title); err != nil {
		return err
	}
	if err := ValidateContent(in.Content); err != nil {
		return err
	}
	if err := ValidateContentType(in.ContentType); err != nil {
		return err
	}
	if len(in.Metadata) > 0 {
		if _, err := CanonicalizeMetadata(in.Metadata); err != nil {
			return err
		}
	}
	if err := ValidateIdempotencyKey(in.IdempotencyKey.String()); err != nil {
		return err
	}
	return nil
}

// RequestDigest returns the canonical "sha256:<hex>" digest of the creation
// request payload, excluding the idempotency key itself. Stores persist it
// in the IdempotencyRecord so retries with the same key replay and key reuse
// with a different payload conflicts.
func (in SaveArtifactInput) RequestDigest() (string, error) {
	metadata, err := decodedMetadata(in.Metadata)
	if err != nil {
		return "", err
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = ContentTypeMarkdown
	}
	digest, _, err := CanonicalDigest(map[string]any{
		"content":      in.Content,
		"content_type": contentType,
		"key":          in.Key,
		"kind":         in.Kind,
		"metadata":     metadata,
		"precedence":   in.Precedence,
		"project":      in.Project,
		"title":        in.Title,
	})
	return digest, err
}
