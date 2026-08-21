package projectprotocol

import (
	"time"
	"unicode/utf8"
)

// Kind is the artifact kind label. Artifacts are non-executable data; kinds
// exist to organize retrieval, not to select execution behavior.
type Kind string

const (
	KindSkill Kind = "skill"
	KindRule  Kind = "rule"
)

// Valid reports whether k is an accepted kind.
func (k Kind) Valid() bool {
	return k == KindSkill || k == KindRule
}

// ParseKind validates a kind from untrusted input.
func ParseKind(s string) (Kind, error) {
	k := Kind(s)
	if !k.Valid() {
		return "", &Error{Code: ErrCodeValidation, Message: "kind must be skill or rule"}
	}
	return k, nil
}

// Scope identifies the resolution scope of an artifact.
type Scope string

const (
	// ScopeWorkspaceDefault is the workspace-wide default scope
	// (project reference empty).
	ScopeWorkspaceDefault Scope = "workspace_default"
	// ScopeProject is an explicit project scope and wins resolution over
	// the workspace default for the same key.
	ScopeProject Scope = "project"
)

// Valid reports whether s is an accepted scope.
func (s Scope) Valid() bool {
	return s == ScopeWorkspaceDefault || s == ScopeProject
}

// Status is the artifact lifecycle state. Deleted is the soft-delete state
// transition: revisions, activations and events are retained.
type Status string

const (
	StatusDraft   Status = "draft"
	StatusActive  Status = "active"
	StatusDeleted Status = "deleted"
)

// Valid reports whether st is an accepted status.
func (st Status) Valid() bool {
	return st == StatusDraft || st == StatusActive || st == StatusDeleted
}

// ContentTypeMarkdown is the only accepted artifact content type. Artifacts
// are markdown documents and never executable payloads.
const ContentTypeMarkdown = "text/markdown"

// Field bounds for validated artifact fields.
const (
	// MaxKeyRunes bounds an artifact key (1..128 runes).
	MaxKeyRunes = 128
	// MaxTitleRunes bounds an artifact title (1..200 runes).
	MaxTitleRunes = 200
	// MaxMessageRunes bounds a revision message (0..1024 runes).
	MaxMessageRunes = 1024
	// MaxReasonRunes bounds a soft-delete/rollback reason (1..1024 runes).
	MaxReasonRunes = 1024
	// MaxIdempotencyKeyBytes bounds an idempotency key (1..128 bytes).
	MaxIdempotencyKeyBytes = 128
	// MaxProjectRunes bounds a project reference (1..128 runes) when the
	// scope is project; the workspace default uses the empty reference.
	MaxProjectRunes = 128
	// MaxArtifactIDBytes bounds store-assigned artifact identifiers.
	MaxArtifactIDBytes = 128
	// MaxActorBytes bounds provenance actors (deleted_by, event actors):
	// 1..256 bytes of valid UTF-8.
	MaxActorBytes = 256
)

// ValidateKey enforces the stable artifact key grammar:
// 1..128 runes matching [a-z0-9][a-z0-9._/-]*.
func ValidateKey(key string) error {
	if key == "" {
		return &Error{Code: ErrCodeValidation, Message: "key must not be empty"}
	}
	if n := utf8.RuneCountInString(key); n > MaxKeyRunes {
		return &Error{Code: ErrCodeValidation, Message: "key exceeds maximum length", Limit: MaxKeyRunes}
	}
	for i, r := range key {
		if i == 0 {
			if !isKeyHead(r) {
				return &Error{Code: ErrCodeValidation, Message: "key must start with [a-z0-9]"}
			}
			continue
		}
		if !isKeyTail(r) {
			return &Error{Code: ErrCodeValidation, Message: "key may only contain [a-z0-9._/-] after the first character"}
		}
	}
	return nil
}

func isKeyHead(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
}

func isKeyTail(r rune) bool {
	return isKeyHead(r) || r == '.' || r == '_' || r == '/' || r == '-'
}

// ValidateTitle enforces 1..200 valid-UTF-8 runes.
func ValidateTitle(title string) error {
	if title == "" {
		return &Error{Code: ErrCodeValidation, Message: "title must not be empty"}
	}
	if !utf8.ValidString(title) {
		return ErrInvalidUTF8
	}
	if n := utf8.RuneCountInString(title); n > MaxTitleRunes {
		return &Error{Code: ErrCodeValidation, Message: "title exceeds maximum length", Limit: MaxTitleRunes}
	}
	return nil
}

// ValidateProjectRef validates the project reference for a scope. The
// workspace default scope must use the empty reference; the project scope
// requires 1..128 valid-UTF-8 runes without control characters.
func ValidateProjectRef(scope Scope, project string) error {
	if !scope.Valid() {
		return &Error{Code: ErrCodeValidation, Message: "scope must be workspace_default or project"}
	}
	switch scope {
	case ScopeWorkspaceDefault:
		if project != "" {
			return &Error{Code: ErrCodeValidation, Message: "workspace_default scope must use an empty project reference"}
		}
	case ScopeProject:
		if project == "" {
			return &Error{Code: ErrCodeValidation, Message: "project scope requires a project reference"}
		}
		if !utf8.ValidString(project) {
			return ErrInvalidUTF8
		}
		if n := utf8.RuneCountInString(project); n > MaxProjectRunes {
			return &Error{Code: ErrCodeValidation, Message: "project reference exceeds maximum length", Limit: MaxProjectRunes}
		}
		for _, r := range project {
			if r < 0x20 || r == 0x7f {
				return &Error{Code: ErrCodeValidation, Message: "project reference must not contain control characters"}
			}
		}
	}
	return nil
}

// ValidateContent enforces the artifact content contract: valid UTF-8,
// non-empty, and at most MaxArtifactContentBytes bytes after decode. Exactly
// at the limit is accepted; one byte more is rejected without truncation.
func ValidateContent(content string) error {
	if content == "" {
		return &Error{Code: ErrCodeValidation, Message: "content must not be empty"}
	}
	if !utf8.ValidString(content) {
		return ErrInvalidUTF8
	}
	if len(content) > MaxArtifactContentBytes {
		return ErrContentTooLarge
	}
	return nil
}

// ValidateContentType enforces the single non-executable content type.
func ValidateContentType(contentType string) error {
	if contentType == "" {
		return nil // defaults to text/markdown
	}
	if contentType != ContentTypeMarkdown {
		return &Error{Code: ErrCodeValidation, Message: "content_type must be text/markdown"}
	}
	return nil
}

// Artifact is the logical, stable artifact record. Content lives exclusively
// in immutable revisions; the artifact row carries identity and state.
type Artifact struct {
	ID             string `json:"id"`
	Project        string `json:"project"`
	Kind           Kind   `json:"kind"`
	Key            string `json:"key"`
	Title          string `json:"title"`
	Scope          Scope  `json:"scope"`
	Status         Status `json:"status"`
	Precedence     int32  `json:"precedence"`
	LatestRevision int64  `json:"latest_revision"`
	ActiveRevision *int64 `json:"active_revision"`
	// ActivationRevision is the monotonic compare-and-swap token for the
	// activation pointer. Every activate/rollback increments it exactly once.
	ActivationRevision int64     `json:"activation_revision"`
	ETag               string    `json:"etag"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`

	// Soft-delete provenance (REQ-RET-001). The three fields are set exactly
	// when Status transitions to deleted and are retained indefinitely;
	// artifacts in any other status MUST NOT carry them.
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	DeletedBy    string     `json:"deleted_by,omitempty"`
	DeleteReason string     `json:"delete_reason,omitempty"`
}

// validateActor enforces the provenance actor contract: required, valid
// UTF-8, at most MaxActorBytes bytes. Soft-delete provenance and soft-delete
// audit events MUST always record the acting principal (REQ-RET-001).
func validateActor(value, field string) error {
	if value == "" {
		return &Error{Code: ErrCodeValidation, Message: field + " must not be empty"}
	}
	if !utf8.ValidString(value) {
		return ErrInvalidUTF8
	}
	if len(value) > MaxActorBytes {
		return &Error{Code: ErrCodeValidation, Message: field + " exceeds maximum length", Limit: MaxActorBytes}
	}
	return nil
}

// ValidateETagShape checks that etag is a well-formed canonical entity tag:
// a quoted string of exactly 64 lowercase hex characters, the shape produced
// by ETag() over canonical bytes. If-Match preconditions and artifact ETags
// share this grammar.
func ValidateETagShape(etag string) error {
	if len(etag) != 66 || etag[0] != '"' || etag[65] != '"' {
		return &Error{Code: ErrCodeValidation, Message: "etag must be a quoted 64-hex-character string"}
	}
	for i := 1; i < 65; i++ {
		c := etag[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return &Error{Code: ErrCodeValidation, Message: "etag must contain only lowercase hex characters"}
		}
	}
	return nil
}

// etagPayload builds the canonical state covered by the artifact ETag: every
// identity/state field plus the soft-delete provenance, deliberately
// excluding created_at/updated_at (wall clocks are not content state) and the
// ETag itself. The delete provenance is covered so the ETag changes exactly
// when the artifact is soft-deleted: a stale If-Match can never delete twice.
func (a Artifact) etagPayload() map[string]any {
	var activeRevision any
	if a.ActiveRevision != nil {
		activeRevision = *a.ActiveRevision
	}
	var deletedAt any
	if a.DeletedAt != nil {
		deletedAt = a.DeletedAt.UTC().UnixNano()
	}
	return map[string]any{
		"activation_revision": a.ActivationRevision,
		"active_revision":     activeRevision,
		"deleted_at":          deletedAt,
		"deleted_by":          a.DeletedBy,
		"delete_reason":       a.DeleteReason,
		"id":                  a.ID,
		"key":                 a.Key,
		"kind":                string(a.Kind),
		"latest_revision":     a.LatestRevision,
		"precedence":          a.Precedence,
		"project":             a.Project,
		"scope":               string(a.Scope),
		"status":              string(a.Status),
		"title":               a.Title,
	}
}

// CanonicalETag derives the artifact's canonical entity tag from its
// validated state: ETag() over the canonical JSON of etagPayload. Stores
// MUST assign exactly this value when persisting state transitions; the
// derivation is deterministic and content-stable across clock jitter.
func (a Artifact) CanonicalETag() (string, error) {
	canonical, err := CanonicalJSON(a.etagPayload())
	if err != nil {
		return "", err
	}
	return ETag(canonical), nil
}

// VerifyETag recomputes the canonical ETag and reports whether it still
// matches, detecting any post-assignment mutation of covered state.
func (a Artifact) VerifyETag() bool {
	want, err := a.CanonicalETag()
	if err != nil {
		return false
	}
	return want == a.ETag
}

// Validate performs the full artifact invariant check for persisted or
// constructed artifacts.
func (a Artifact) Validate() error {
	if a.ID == "" || len(a.ID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if err := ValidateProjectRef(a.Scope, a.Project); err != nil {
		return err
	}
	if _, err := ParseKind(string(a.Kind)); err != nil {
		return err
	}
	if err := ValidateKey(a.Key); err != nil {
		return err
	}
	if err := ValidateTitle(a.Title); err != nil {
		return err
	}
	if !a.Status.Valid() {
		return &Error{Code: ErrCodeValidation, Message: "status must be draft, active or deleted"}
	}
	if a.LatestRevision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "latest_revision must be non-negative"}
	}
	if a.ActiveRevision != nil && (*a.ActiveRevision < 1 || *a.ActiveRevision > a.LatestRevision) {
		return &Error{Code: ErrCodeValidation, Message: "active_revision must reference an existing revision"}
	}
	if a.ActivationRevision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "activation_revision must be non-negative"}
	}
	if err := a.validateDeleteProvenance(); err != nil {
		return err
	}
	if err := ValidateETagShape(a.ETag); err != nil {
		return err
	}
	if !a.VerifyETag() {
		return &Error{Code: ErrCodeValidation, Message: "artifact etag does not match its canonical derivation"}
	}
	return nil
}

// validateDeleteProvenance enforces the soft-delete provenance contract:
// deleted artifacts carry deleted_at/deleted_by/reason — with deleted_by
// MANDATORY (the acting principal is never anonymous) — and artifacts in any
// other status carry none of them.
func (a Artifact) validateDeleteProvenance() error {
	if a.Status == StatusDeleted {
		if a.DeletedAt == nil {
			return &Error{Code: ErrCodeValidation, Message: "deleted artifact must record deleted_at"}
		}
		if err := validateActor(a.DeletedBy, "deleted_by"); err != nil {
			return err
		}
		if a.DeleteReason == "" {
			return &Error{Code: ErrCodeValidation, Message: "deleted artifact must record delete_reason"}
		}
		if n := utf8.RuneCountInString(a.DeleteReason); n > MaxReasonRunes {
			return &Error{Code: ErrCodeValidation, Message: "delete_reason exceeds maximum length", Limit: MaxReasonRunes}
		}
	} else if a.DeletedAt != nil || a.DeletedBy != "" || a.DeleteReason != "" {
		return &Error{Code: ErrCodeValidation, Message: "delete provenance present on a non-deleted artifact"}
	}
	return nil
}

// ResolvableArtifact is the manifest-level summary of one ACTIVE artifact
// consumed by the effective protocol resolver. It deliberately carries no
// content: resolution MUST count and select before any content fetch
// (REQ-LIMIT-002, REQ-DOS-002).
type ResolvableArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Kind       Kind   `json:"kind"`
	Key        string `json:"key"`
	Scope      Scope  `json:"scope"`
	Precedence int32  `json:"precedence"`
	Revision   int64  `json:"revision"`
	Digest     string `json:"digest"`
}

// Validate checks the summary invariants.
func (r ResolvableArtifact) Validate() error {
	if r.ArtifactID == "" || len(r.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if _, err := ParseKind(string(r.Kind)); err != nil {
		return err
	}
	if err := ValidateKey(r.Key); err != nil {
		return err
	}
	if !r.Scope.Valid() {
		return &Error{Code: ErrCodeValidation, Message: "scope must be workspace_default or project"}
	}
	if r.Revision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "revision must be at least 1"}
	}
	return nil
}

// ArtifactFilter selects artifacts for listing.
type ArtifactFilter struct {
	// Project filters by project reference; empty means the workspace
	// default scope.
	Project        string `json:"project"`
	Kind           *Kind  `json:"kind,omitempty"`
	ActiveOnly     bool   `json:"active"`
	IncludeDeleted bool   `json:"include_deleted"`
	Query          string `json:"q,omitempty"`
}

// PageRequest is the bounded cursor pagination input. Limit is normalized by
// PageSizeBounds; cursors are opaque, snapshot-bound tokens produced by the
// store layer.
type PageRequest struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

// Normalize clamps the request to the approved page bounds.
func (p PageRequest) Normalize() PageRequest {
	return PageRequest{Cursor: p.Cursor, Limit: PageSizeBounds(p.Limit)}
}

// PageInfo carries the pagination output contract.
type PageInfo struct {
	NextCursor       string `json:"next_cursor"`
	HasMore          bool   `json:"has_more"`
	SnapshotRevision string `json:"snapshot_revision"`
}

// ArtifactPage is one bounded page of artifacts.
type ArtifactPage struct {
	Items []*Artifact `json:"items"`
	Page  PageInfo    `json:"page"`
}
