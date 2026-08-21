package projectprotocol

import (
	"encoding/json"
	"time"
	"unicode/utf8"
)

// Revision is an immutable content snapshot of an artifact. Revisions are
// append-only: once constructed they are never mutated, and stores MUST NOT
// update or delete them (REQ-RET-001). The zero value is not valid; use
// NewRevision, the only constructor that computes the digest.
type Revision struct {
	ArtifactID  string          `json:"artifact_id"`
	Revision    int64           `json:"revision"` // 1-based monotonic
	Title       string          `json:"title"`
	Content     string          `json:"content"`
	ContentType string          `json:"content_type"` // always text/markdown
	Metadata    json.RawMessage `json:"metadata"`     // canonical JSON object bytes
	Message     string          `json:"message,omitempty"`
	Digest      string          `json:"digest"` // sha256 of the canonical revision payload
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
}

// RevisionInput is the transport-neutral payload for creating a revision.
// Derived fields (revision number, digest, actor, timestamp) are assigned by
// NewRevision at construction time, never taken from client input.
//
// IdempotencyKey is REQUIRED on every revision write (REQ-ART-002): stores
// replay same key+digest requests and conflict on key reuse with a different
// payload. Title is optional per the API contract (title?); an empty title
// means "inherit the artifact title" and the store resolves it before the
// immutable revision is persisted.
type RevisionInput struct {
	Title          string          `json:"title,omitempty"`
	Content        string          `json:"content"`
	ContentType    string          `json:"content_type,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	Message        string          `json:"message,omitempty"`
	IdempotencyKey IdempotencyKey  `json:"idempotency_key"`
}

// Validate checks the full revision input contract before construction:
// a required idempotency key, valid UTF-8 non-empty content within
// MaxArtifactContentBytes, an optional valid title, canonical metadata
// within MaxArtifactMetadataBytes, and the single non-executable content
// type.
func (in RevisionInput) Validate() error {
	if err := ValidateIdempotencyKey(in.IdempotencyKey.String()); err != nil {
		return err
	}
	if err := ValidateContent(in.Content); err != nil {
		return err
	}
	if in.Title != "" {
		if err := ValidateTitle(in.Title); err != nil {
			return err
		}
	}
	if err := ValidateContentType(in.ContentType); err != nil {
		return err
	}
	if in.Message != "" {
		if !utf8.ValidString(in.Message) {
			return ErrInvalidUTF8
		}
		if n := utf8.RuneCountInString(in.Message); n > MaxMessageRunes {
			return &Error{Code: ErrCodeValidation, Message: "revision message exceeds maximum length", Limit: MaxMessageRunes}
		}
	}
	if len(in.Metadata) > 0 {
		if _, err := CanonicalizeMetadata(in.Metadata); err != nil {
			return err
		}
	}
	return nil
}

// RequestDigest returns the canonical "sha256:<hex>" digest of the revision
// request payload bound to artifactID. It deliberately excludes the
// idempotency key itself (the key selects the comparison slot; the digest is
// the compared value) and provenance fields. Stores persist it in the
// IdempotencyRecord for replay/conflict classification.
func (in RevisionInput) RequestDigest(artifactID string) (string, error) {
	if artifactID == "" || len(artifactID) > MaxArtifactIDBytes {
		return "", &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	metadata, err := decodedMetadata(in.Metadata)
	if err != nil {
		return "", err
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = ContentTypeMarkdown
	}
	digest, _, err := CanonicalDigest(map[string]any{
		"artifact_id":  artifactID,
		"content":      in.Content,
		"content_type": contentType,
		"metadata":     metadata,
		"message":      in.Message,
		"title":        in.Title,
	})
	return digest, err
}

// decodedMetadata decodes raw metadata into canonical-ready values, treating
// absent metadata as the empty object.
func decodedMetadata(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	value, err := DecodeCanonicalRaw([]byte(raw))
	if err != nil {
		return nil, err
	}
	return value, nil
}

// NewRevision validates the input and constructs the immutable revision with
// its canonical digest. The digest covers artifact_id, revision, title,
// content, content_type, canonical metadata and message; provenance fields
// (created_by/created_at) are deliberately excluded so the same content
// always yields the same digest. Metadata is stored canonically, making the
// digest stable across key insertion order.
func NewRevision(artifactID string, revisionNumber int64, in RevisionInput, createdBy string, createdAt time.Time) (Revision, error) {
	if artifactID == "" || len(artifactID) > MaxArtifactIDBytes {
		return Revision{}, &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if revisionNumber < 1 {
		return Revision{}, &Error{Code: ErrCodeValidation, Message: "revision number must be at least 1"}
	}
	if createdBy != "" && !utf8.ValidString(createdBy) {
		return Revision{}, ErrInvalidUTF8
	}
	if err := in.Validate(); err != nil {
		return Revision{}, err
	}
	contentType := in.ContentType
	if contentType == "" {
		contentType = ContentTypeMarkdown
	}
	var metadata json.RawMessage
	if len(in.Metadata) > 0 {
		canonical, err := CanonicalizeMetadata(in.Metadata)
		if err != nil {
			return Revision{}, err
		}
		metadata = json.RawMessage(canonical)
	} else {
		metadata = json.RawMessage("{}")
	}
	rev := Revision{
		ArtifactID:  artifactID,
		Revision:    revisionNumber,
		Title:       in.Title,
		Content:     in.Content,
		ContentType: contentType,
		Metadata:    metadata,
		Message:     in.Message,
		CreatedBy:   createdBy,
		CreatedAt:   createdAt,
	}
	payload := revisionDigestPayload(rev)
	digest, _, err := CanonicalDigest(payload)
	if err != nil {
		return Revision{}, err
	}
	rev.Digest = digest
	return rev, nil
}

// revisionDigestPayload builds the digest-covered canonical payload. The
// metadata is decoded from its canonical bytes so the payload encodes as a
// sorted object.
func revisionDigestPayload(rev Revision) map[string]any {
	metadata, err := DecodeCanonicalRaw([]byte(rev.Metadata))
	if err != nil || metadata == nil {
		metadata = map[string]any{}
	}
	if _, ok := metadata.(map[string]any); !ok {
		metadata = map[string]any{}
	}
	return map[string]any{
		"artifact_id":  rev.ArtifactID,
		"revision":     rev.Revision,
		"title":        rev.Title,
		"content":      rev.Content,
		"content_type": rev.ContentType,
		"metadata":     metadata,
		"message":      rev.Message,
	}
}

// VerifyDigest recomputes the canonical digest and reports whether it still
// matches, detecting any post-construction mutation of digest-covered fields.
func (r Revision) VerifyDigest() bool {
	digest, _, err := CanonicalDigest(revisionDigestPayload(r))
	if err != nil {
		return false
	}
	return digest == r.Digest
}

// Validate performs the persisted-revision invariant check (stores call this
// when loading or writing revisions).
func (r Revision) Validate() error {
	if r.ArtifactID == "" || len(r.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if r.Revision < 1 {
		return &Error{Code: ErrCodeValidation, Message: "revision number must be at least 1"}
	}
	if err := ValidateContent(r.Content); err != nil {
		return err
	}
	if err := ValidateTitle(r.Title); err != nil {
		return err
	}
	if r.ContentType != ContentTypeMarkdown {
		return &Error{Code: ErrCodeValidation, Message: "content_type must be text/markdown"}
	}
	if len(r.Metadata) == 0 {
		return &Error{Code: ErrCodeValidation, Message: "metadata must be present (canonical object)"}
	}
	if _, err := CanonicalizeMetadata([]byte(r.Metadata)); err != nil {
		return err
	}
	if r.Digest == "" {
		return &Error{Code: ErrCodeValidation, Message: "digest must be present"}
	}
	if !r.VerifyDigest() {
		return &Error{Code: ErrCodeValidation, Message: "revision digest mismatch"}
	}
	return nil
}

// RevisionPage is one bounded page of revisions.
type RevisionPage struct {
	Items []Revision `json:"items"`
	Page  PageInfo   `json:"page"`
}
