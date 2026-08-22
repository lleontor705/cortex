package projectprotocol

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixedTime() time.Time {
	return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
}

func TestNewRevisionContentExactBoundary(t *testing.T) {
	base := RevisionInput{
		Title:          "t",
		Content:        strings.Repeat("a", MaxArtifactContentBytes),
		IdempotencyKey: "rev-1",
	}
	if _, err := NewRevision("art-1", 1, base, "actor", fixedTime()); err != nil {
		t.Fatalf("exact 1MiB content rejected: %v", err)
	}
	over := base
	over.Content = strings.Repeat("a", MaxArtifactContentBytes+1)
	_, err := NewRevision("art-1", 1, over, "actor", fixedTime())
	if !errorHasCode(err, ErrCodeContentTooLarge) {
		t.Fatalf("1MiB+1 accepted: %v", err)
	}
	invalid := base
	invalid.Content = "ok\xff"
	if _, err := NewRevision("art-1", 1, invalid, "actor", fixedTime()); !errorHasCode(err, ErrCodeInvalidUTF8) {
		t.Fatalf("invalid UTF-8 accepted: %v", err)
	}
}

func TestNewRevisionDefaultsAndValidation(t *testing.T) {
	rev, err := NewRevision("art-1", 3, RevisionInput{Title: "t", Content: "body", IdempotencyKey: "rev-2"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if rev.ContentType != ContentTypeMarkdown {
		t.Fatalf("content type default=%q want text/markdown", rev.ContentType)
	}
	if string(rev.Metadata) != "{}" {
		t.Fatalf("nil metadata not normalized to {}: %s", rev.Metadata)
	}
	if err := rev.Validate(); err != nil {
		t.Fatalf("constructed revision invalid: %v", err)
	}
	if !rev.VerifyDigest() {
		t.Fatal("constructed revision digest does not verify")
	}
	// Constructor rejections.
	if _, err := NewRevision("", 1, RevisionInput{Title: "t", Content: "b", IdempotencyKey: "k"}, "a", fixedTime()); err == nil {
		t.Error("empty artifact id accepted")
	}
	if _, err := NewRevision("art-1", 0, RevisionInput{Title: "t", Content: "b", IdempotencyKey: "k"}, "a", fixedTime()); err == nil {
		t.Error("revision number 0 accepted")
	}
	if _, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "b", ContentType: "text/x-python", IdempotencyKey: "k"}, "a", fixedTime()); err == nil {
		t.Error("executable content type accepted")
	}
	if _, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "b", Message: strings.Repeat("m", 1025), IdempotencyKey: "k"}, "a", fixedTime()); err == nil {
		t.Error("oversize message accepted")
	}
	if _, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "b", Metadata: json.RawMessage(`{"a":1,"a":2}`), IdempotencyKey: "k"}, "a", fixedTime()); !errorHasCode(err, ErrCodeDuplicateMetadataKey) {
		t.Errorf("duplicate metadata keys accepted: %v", err)
	}
}

// TestRevisionWriteRequiresIdempotencyKey is the negative oracle for
// REQ-ART-002: a revision write MUST NOT be constructible without a valid
// idempotency key.
func TestRevisionWriteRequiresIdempotencyKey(t *testing.T) {
	missing := RevisionInput{Title: "t", Content: "b"}
	if err := missing.Validate(); err == nil {
		t.Fatal("revision input without idempotency key accepted")
	}
	if _, err := NewRevision("art-1", 1, missing, "actor", fixedTime()); err == nil {
		t.Fatal("NewRevision accepted missing idempotency key")
	}
	for _, bad := range []string{"", "has space", strings.Repeat("k", 129), "não-ascii"} {
		in := RevisionInput{Title: "t", Content: "b", IdempotencyKey: IdempotencyKey(bad)}
		if err := in.Validate(); err == nil {
			t.Errorf("invalid idempotency key %q accepted on revision input", bad)
		}
	}
}

// TestRevisionTitleOptionalOnInput asserts the API contract (title?): an
// empty title on input means "inherit the artifact title"; the persisted
// revision still requires a concrete title once the store resolves it.
func TestRevisionTitleOptionalOnInput(t *testing.T) {
	rev, err := NewRevision("art-1", 2, RevisionInput{Content: "body", IdempotencyKey: "rev-3"}, "actor", fixedTime())
	if err != nil {
		t.Fatalf("empty revision title rejected on input: %v", err)
	}
	if rev.Title != "" {
		t.Fatalf("input title not preserved as empty: %q", rev.Title)
	}
	// The persisted revision must carry a resolved, concrete title: the
	// unresolved (empty) one fails persisted-revision validation, and the
	// store must resolve inheritance BEFORE constructing the revision so the
	// digest covers the resolved title.
	if err := rev.Validate(); err == nil {
		t.Fatal("persisted revision with unresolved (empty) title accepted")
	}
	resolved, err := NewRevision("art-1", 2, RevisionInput{Title: "inherited", Content: "body", IdempotencyKey: "rev-3"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Validate(); err != nil {
		t.Fatalf("resolved revision rejected: %v", err)
	}
	if rev.Digest == resolved.Digest {
		t.Fatal("title resolution must be digest-visible")
	}
	// An explicitly invalid provided title is still rejected at the input.
	invalid := RevisionInput{Title: strings.Repeat("t", 201), Content: "b", IdempotencyKey: "k"}
	if err := invalid.Validate(); err == nil {
		t.Fatal("oversize explicit revision title accepted")
	}
}

// TestRevisionRequestDigestSemantics pins the typed revision idempotency
// digest: same payload replays, different payload conflicts.
func TestRevisionRequestDigestSemantics(t *testing.T) {
	base := RevisionInput{Title: "t", Content: "body", Message: "m", IdempotencyKey: "rev-4"}
	d1, err := base.RequestDigest("art-1")
	if err != nil {
		t.Fatal(err)
	}
	// Same payload, different key: same digest.
	otherKey := base
	otherKey.IdempotencyKey = "rev-5"
	d2, err := otherKey.RequestDigest("art-1")
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("digest changed with idempotency key: %s vs %s", d1, d2)
	}
	// The digest is scoped to the artifact.
	d3, err := base.RequestDigest("art-2")
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d3 {
		t.Fatal("digest not scoped by artifact id")
	}
	// Content change flips the digest (conflict on key reuse).
	changed := base
	changed.Content = "different"
	d4, err := changed.RequestDigest("art-1")
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d4 {
		t.Fatal("digest collision across different content")
	}
	// Metadata canonicalization applies before hashing.
	spaced := RevisionInput{Content: "body", Metadata: json.RawMessage(`{ "a" : 1 }`), IdempotencyKey: "k"}
	tight := RevisionInput{Content: "body", Metadata: json.RawMessage(`{"a":1}`), IdempotencyKey: "k"}
	ds, err := spaced.RequestDigest("art-1")
	if err != nil {
		t.Fatal(err)
	}
	dt, err := tight.RequestDigest("art-1")
	if err != nil {
		t.Fatal(err)
	}
	if ds != dt {
		t.Fatalf("digest not canonical across metadata whitespace: %s vs %s", ds, dt)
	}
	if _, err := base.RequestDigest(""); err == nil {
		t.Fatal("empty artifact id accepted by RequestDigest")
	}
}

func TestRevisionDigestStableAcrossMetadataOrder(t *testing.T) {
	rawA := json.RawMessage(`{"x":1,"y":{"b":2,"a":3}}`)
	rawB := json.RawMessage(`{"y":{"a":3,"b":2},"x":1}`)
	revA, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "body", Metadata: rawA, IdempotencyKey: "k"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	revB, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "body", Metadata: rawB, IdempotencyKey: "k"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if revA.Digest != revB.Digest {
		t.Fatalf("digest differs across metadata key order: %s vs %s", revA.Digest, revB.Digest)
	}
	if string(revA.Metadata) != string(revB.Metadata) {
		t.Fatalf("stored metadata not canonical: %s vs %s", revA.Metadata, revB.Metadata)
	}
	// Provenance fields are excluded from the digest.
	revC, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "body", Metadata: rawA, IdempotencyKey: "k"}, "other-actor", fixedTime().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if revA.Digest != revC.Digest {
		t.Fatal("digest changed with provenance fields")
	}
	// The idempotency key is excluded from the digest (slot, not payload).
	revK, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "body", Metadata: rawA, IdempotencyKey: "different-key"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if revA.Digest != revK.Digest {
		t.Fatal("digest changed with idempotency key")
	}
	// Content changes do change the digest.
	revD, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "different", IdempotencyKey: "k"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	if revA.Digest == revD.Digest {
		t.Fatal("digest collision on different content")
	}
}

func TestRevisionDetectsMutation(t *testing.T) {
	rev, err := NewRevision("art-1", 1, RevisionInput{Title: "t", Content: "body", IdempotencyKey: "k"}, "actor", fixedTime())
	if err != nil {
		t.Fatal(err)
	}
	rev.Content = "tampered"
	if rev.VerifyDigest() {
		t.Fatal("digest still verifies after content mutation")
	}
	if err := rev.Validate(); !errorHasCode(err, ErrCodeValidation) {
		t.Fatalf("tampered revision validated: %v", err)
	}
}
