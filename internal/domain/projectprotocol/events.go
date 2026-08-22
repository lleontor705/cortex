package projectprotocol

import (
	"time"
	"unicode/utf8"
)

// EventType classifies an immutable audit event on an artifact's history.
// Events are append-only and retained indefinitely (REQ-RET-001/002);
// authorized history remains listable even for soft-deleted artifacts.
type EventType string

const (
	// EventArtifactCreated records artifact creation with its first revision.
	EventArtifactCreated EventType = "artifact_created"
	// EventRevisionAppended records an immutable revision write.
	EventRevisionAppended EventType = "revision_appended"
	// EventActivated records an activation pointer transition.
	EventActivated EventType = "activated"
	// EventRolledBack records a rollback to a previous revision.
	EventRolledBack EventType = "rolled_back"
	// EventSoftDeleted records the soft-delete state transition.
	EventSoftDeleted EventType = "soft_deleted"
)

// Valid reports whether t is an accepted event type.
func (t EventType) Valid() bool {
	switch t {
	case EventArtifactCreated, EventRevisionAppended, EventActivated, EventRolledBack, EventSoftDeleted:
		return true
	}
	return false
}

// ArtifactEvent is one immutable audit record over an artifact. Revisions and
// activations carry their own detail; events provide the listable timeline
// surface (REQ-PAGE-001 applies: cursor pagination, limit default 20/max 100,
// stable ordering by created_at desc, id desc).
type ArtifactEvent struct {
	ID         string    `json:"id"`
	ArtifactID string    `json:"artifact_id"`
	Type       EventType `json:"type"`
	// Revision references the revision written, activated or rolled back to;
	// 0 when not applicable (e.g. soft delete).
	Revision int64 `json:"revision"`
	// ActivationRevision references the activation CAS token produced by the
	// event; 0 when not applicable.
	ActivationRevision int64     `json:"activation_revision"`
	Actor              string    `json:"actor,omitempty"`
	Reason             string    `json:"reason,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// Validate checks the persisted-event invariants. Stores call this when
// appending or loading events.
func (e ArtifactEvent) Validate() error {
	if e.ID == "" || len(e.ID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "event id must be 1..128 bytes"}
	}
	if e.ArtifactID == "" || len(e.ArtifactID) > MaxArtifactIDBytes {
		return &Error{Code: ErrCodeValidation, Message: "artifact id must be 1..128 bytes"}
	}
	if !e.Type.Valid() {
		return &Error{Code: ErrCodeValidation, Message: "unknown artifact event type"}
	}
	// The soft-delete transition is never anonymous: its audit event MUST
	// record the acting principal (REQ-RET-001 delete provenance).
	if e.Type == EventSoftDeleted && e.Actor == "" {
		return &Error{Code: ErrCodeValidation, Message: "soft-delete event must record the acting principal"}
	}
	if e.Actor != "" {
		if !utf8.ValidString(e.Actor) {
			return ErrInvalidUTF8
		}
		if len(e.Actor) > MaxActorBytes {
			return &Error{Code: ErrCodeValidation, Message: "event actor exceeds maximum length", Limit: MaxActorBytes}
		}
	}
	if e.ActivationRevision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "event activation revision must be non-negative"}
	}
	if e.Revision < 0 {
		return &Error{Code: ErrCodeValidation, Message: "event revision must be non-negative"}
	}
	if e.Reason != "" {
		if !utf8.ValidString(e.Reason) {
			return ErrInvalidUTF8
		}
		if n := utf8.RuneCountInString(e.Reason); n > MaxReasonRunes {
			return &Error{Code: ErrCodeValidation, Message: "event reason exceeds maximum length", Limit: MaxReasonRunes}
		}
	}
	return nil
}

// ArtifactEventPage is one bounded page of audit events.
type ArtifactEventPage struct {
	Items []ArtifactEvent `json:"items"`
	Page  PageInfo        `json:"page"`
}
