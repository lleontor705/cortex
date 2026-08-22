package projectprotocol

import (
	"strings"
	"testing"
)

func TestPreconditionsExclusiveUnion(t *testing.T) {
	rev := int64(5)
	etag := validETagHex()
	if err := (Preconditions{}).Validate(); err == nil {
		t.Error("empty preconditions accepted")
	}
	if err := (Preconditions{ExpectedRevision: &rev}).Validate(); err != nil {
		t.Errorf("expected_revision rejected: %v", err)
	}
	if err := (Preconditions{IfMatchETag: etag}).Validate(); err != nil {
		t.Errorf("if_match_etag rejected: %v", err)
	}
	if err := (Preconditions{ExpectedRevision: &rev, IfMatchETag: etag}).Validate(); err == nil {
		t.Error("both preconditions accepted")
	}
	neg := int64(-1)
	if err := (Preconditions{ExpectedRevision: &neg}).Validate(); err == nil {
		t.Error("negative expected_revision accepted")
	}
	// Malformed If-Match ETags are rejected (canonical shape only).
	for _, bad := range []string{`"abc"`, "abc", "", `"ABC` + strings.Repeat("a", 60) + `"`, `"` + strings.Repeat("g", 64) + `"`} {
		if err := (Preconditions{IfMatchETag: bad}).Validate(); err == nil {
			t.Errorf("malformed if_match_etag %q accepted", bad)
		}
	}
}

func TestActivateRollbackInputValidation(t *testing.T) {
	ok := ActivateInput{ArtifactID: "a1", Revision: 2, ExpectedActivationRevision: 1}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid activate rejected: %v", err)
	}
	bad := ActivateInput{ArtifactID: "", Revision: 2, ExpectedActivationRevision: 1}
	if err := bad.Validate(); err == nil {
		t.Error("missing artifact id accepted")
	}
	zero := ActivateInput{ArtifactID: "a1", Revision: 0, ExpectedActivationRevision: 1}
	if err := zero.Validate(); err == nil {
		t.Error("revision 0 accepted")
	}
	neg := ActivateInput{ArtifactID: "a1", Revision: 1, ExpectedActivationRevision: -1}
	if err := neg.Validate(); err == nil {
		t.Error("negative CAS token accepted")
	}

	rb := RollbackInput{ArtifactID: "a1", ToRevision: 1, ExpectedActivationRevision: 0, Reason: "restore"}
	if err := rb.Validate(); err != nil {
		t.Errorf("valid rollback rejected: %v", err)
	}
	for _, in := range []RollbackInput{
		{ArtifactID: "a1", ToRevision: 0, ExpectedActivationRevision: 0, Reason: "r"},
		{ArtifactID: "", ToRevision: 1, ExpectedActivationRevision: 0, Reason: "r"},
		{ArtifactID: "a1", ToRevision: 1, ExpectedActivationRevision: 0, Reason: ""},
		{ArtifactID: "a1", ToRevision: 1, ExpectedActivationRevision: 0, Reason: strings.Repeat("r", 1025)},
	} {
		if err := in.Validate(); err == nil {
			t.Errorf("invalid rollback accepted: %+v", in)
		}
	}
}

// TestSoftDeleteInputValidation is the negative oracle for the delete
// contract: If-Match ETag is the ONLY accepted precondition (there is no
// expected_revision form for deletion), the acting principal is mandatory,
// and the reason is required.
func TestSoftDeleteInputValidation(t *testing.T) {
	ok := SoftDeleteInput{
		ArtifactID:  "a1",
		IfMatchETag: validETagHex(),
		DeletedBy:   "u1",
		Reason:      "deprecated",
	}
	if err := ok.Validate(); err != nil {
		t.Errorf("valid soft delete rejected: %v", err)
	}
	for name, in := range map[string]SoftDeleteInput{
		"missing-artifact":   {ArtifactID: "", IfMatchETag: validETagHex(), DeletedBy: "u", Reason: "r"},
		"oversize-artifact":  {ArtifactID: strings.Repeat("a", 129), IfMatchETag: validETagHex(), DeletedBy: "u", Reason: "r"},
		"missing-etag":       {ArtifactID: "a1", DeletedBy: "u", Reason: "r"},
		"short-etag":         {ArtifactID: "a1", IfMatchETag: `"abc"`, DeletedBy: "u", Reason: "r"},
		"unquoted-etag":      {ArtifactID: "a1", IfMatchETag: strings.Repeat("a", 64), DeletedBy: "u", Reason: "r"},
		"uppercase-hex-etag": {ArtifactID: "a1", IfMatchETag: `"` + strings.Repeat("A", 64) + `"`, DeletedBy: "u", Reason: "r"},
		"missing-actor":      {ArtifactID: "a1", IfMatchETag: validETagHex(), Reason: "r"},
		"invalid-utf8-actor": {ArtifactID: "a1", IfMatchETag: validETagHex(), DeletedBy: "u\xff", Reason: "r"},
		"oversize-actor":     {ArtifactID: "a1", IfMatchETag: validETagHex(), DeletedBy: strings.Repeat("u", MaxActorBytes+1), Reason: "r"},
		"missing-reason":     {ArtifactID: "a1", IfMatchETag: validETagHex(), DeletedBy: "u"},
		"oversize-reason":    {ArtifactID: "a1", IfMatchETag: validETagHex(), DeletedBy: "u", Reason: strings.Repeat("r", MaxReasonRunes+1)},
	} {
		if err := in.Validate(); err == nil {
			t.Errorf("%s soft delete accepted: %+v", name, in)
		}
	}
}

// validETagHex builds a canonical-shaped quoted entity tag.
func validETagHex() string {
	return `"` + strings.Repeat("a", 64) + `"`
}

func TestIdempotencyKeyValidation(t *testing.T) {
	if _, err := NewIdempotencyKey("abc-123"); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
	if err := ValidateIdempotencyKey(strings.Repeat("k", 128)); err != nil {
		t.Errorf("128-byte key rejected: %v", err)
	}
	for _, bad := range []string{"", strings.Repeat("k", 129), "with space", "with\x01ctrl", "não-ascii"} {
		if err := ValidateIdempotencyKey(bad); err == nil {
			t.Errorf("invalid key accepted: %q", bad)
		}
	}
}

func TestClassifyIdempotency(t *testing.T) {
	key, _ := NewIdempotencyKey("op-1")
	digest := "sha256:" + strings.Repeat("a", 64)
	other := "sha256:" + strings.Repeat("b", 64)

	if got := ClassifyIdempotency(nil, key, digest); got != IdempotencyNew {
		t.Errorf("nil record: %v", got)
	}
	stored := &IdempotencyRecord{Key: key, RequestDigest: digest}
	if got := ClassifyIdempotency(stored, key, digest); got != IdempotencyReplay {
		t.Errorf("same key+digest: %v", got)
	}
	if got := ClassifyIdempotency(stored, key, other); got != IdempotencyConflict {
		t.Errorf("same key different digest: %v", got)
	}
	otherKey, _ := NewIdempotencyKey("op-2")
	if got := ClassifyIdempotency(stored, otherKey, digest); got != IdempotencyNew {
		t.Errorf("different key: %v", got)
	}
	if err := (IdempotencyRecord{Key: key}).Validate(); err == nil {
		t.Error("record without digest accepted")
	}
}

func TestValidateSaveArtifactInput(t *testing.T) {
	ok := SaveArtifactInput{
		Project: "proj", Kind: "skill", Key: "python/testing",
		Title: "T", Content: "body",
		Metadata: []byte(`{"a":1}`), IdempotencyKey: "op-9",
	}
	if err := ValidateSaveArtifactInput(ok); err != nil {
		t.Fatalf("valid save rejected: %v", err)
	}
	workspace := ok
	workspace.Project = ""
	if err := ValidateSaveArtifactInput(workspace); err != nil {
		t.Fatalf("workspace default save rejected: %v", err)
	}
	// Every dimension is enforced.
	mutations := map[string]func(*SaveArtifactInput){
		"kind":            func(in *SaveArtifactInput) { in.Kind = "plugin" },
		"key":             func(in *SaveArtifactInput) { in.Key = "Bad Key" },
		"title":           func(in *SaveArtifactInput) { in.Title = "" },
		"content":         func(in *SaveArtifactInput) { in.Content = "x\xff" },
		"contenttype":     func(in *SaveArtifactInput) { in.ContentType = "application/x-sh" },
		"metadata":        func(in *SaveArtifactInput) { in.Metadata = []byte(`{"a":1,"a":2}`) },
		"idemkey":         func(in *SaveArtifactInput) { in.IdempotencyKey = "has space" },
		"idemkey-missing": func(in *SaveArtifactInput) { in.IdempotencyKey = "" },
		"project":         func(in *SaveArtifactInput) { in.Project = "p\x7f" },
	}
	for name, mutate := range mutations {
		in := ok
		mutate(&in)
		if err := ValidateSaveArtifactInput(in); err == nil {
			t.Errorf("%s mutation accepted", name)
		}
	}
}

func TestSaveArtifactRequestDigestSemantics(t *testing.T) {
	base := SaveArtifactInput{
		Project: "proj", Kind: "skill", Key: "python/testing",
		Title: "T", Content: "body", IdempotencyKey: "op-1",
	}
	d1, err := base.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	// Same payload under a different key: same digest (the key is the slot,
	// not the compared value).
	otherKey := base
	otherKey.IdempotencyKey = "op-2"
	d2, err := otherKey.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 {
		t.Fatalf("digest changed with idempotency key: %s vs %s", d1, d2)
	}
	// Semantically identical metadata (whitespace) yields the same digest.
	spaced := base
	spaced.Metadata = []byte(`{ "a" : 1 }`)
	d3, err := spaced.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	tight := base
	tight.Metadata = []byte(`{"a":1}`)
	d4, err := tight.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	if d3 != d4 {
		t.Fatalf("digest not canonical across metadata whitespace: %s vs %s", d3, d4)
	}
	// Different content yields a different digest.
	changed := base
	changed.Content = "different"
	d5, err := changed.RequestDigest()
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d5 {
		t.Fatal("digest collision across different content")
	}
	// Invalid metadata fails the digest instead of hashing garbage.
	badMeta := base
	badMeta.Metadata = []byte(`{"a":1,"a":2}`)
	if _, err := badMeta.RequestDigest(); !errorHasCode(err, ErrCodeDuplicateMetadataKey) {
		t.Fatalf("duplicate metadata accepted in digest: %v", err)
	}
}

func TestActivationValidate(t *testing.T) {
	a := Activation{ArtifactID: "a1", Revision: 2, ActivationRevision: 4, ActivatedBy: "u"}
	if err := a.Validate(); err != nil {
		t.Errorf("valid activation rejected: %v", err)
	}
	if err := (Activation{ArtifactID: "a1", Revision: 0, ActivationRevision: 1}).Validate(); err == nil {
		t.Error("revision 0 accepted")
	}
	if err := (Activation{ArtifactID: "a1", Revision: 1, ActivationRevision: 0}).Validate(); err == nil {
		t.Error("activation revision 0 accepted")
	}
}
