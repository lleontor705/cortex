package projectprotocol

import (
	"strings"
	"testing"
	"time"
)

func TestValidateKeyBoundaries(t *testing.T) {
	cases := []struct {
		key  string
		want bool
		note string
	}{
		{"a", true, "minimal"},
		{"0", true, "digit head"},
		{"a1", true, ""},
		{"a/b/c", true, "slashes"},
		{"a.b_c-d/e.f_g", true, "all tail classes"},
		{strings.Repeat("a", 128), true, "exactly 128"},
		{"", false, "empty"},
		{"A", false, "uppercase head"},
		{"aA", false, "uppercase tail"},
		{"-a", false, "punctuation head"},
		{".a", false, "dot head"},
		{"/a", false, "slash head"},
		{"_a", false, "underscore head"},
		{"a b", false, "space"},
		{"a\x00", false, "NUL"},
		{"aé", false, "non-ASCII"},
		{strings.Repeat("a", 129), false, "129 chars"},
	}
	for _, tc := range cases {
		err := ValidateKey(tc.key)
		if (err == nil) != tc.want {
			t.Errorf("ValidateKey(%q) note=%s: err=%v want_ok=%v", tc.key, tc.note, err, tc.want)
		}
	}
}

func TestValidateTitleBoundaries(t *testing.T) {
	if err := ValidateTitle(""); err == nil {
		t.Fatal("empty title accepted")
	}
	if err := ValidateTitle("x"); err != nil {
		t.Fatalf("single rune rejected: %v", err)
	}
	if err := ValidateTitle(strings.Repeat("é", 200)); err != nil {
		t.Fatalf("exactly 200 multibyte runes rejected: %v", err)
	}
	if err := ValidateTitle(strings.Repeat("é", 201)); err == nil {
		t.Fatal("201 runes accepted")
	}
	if err := ValidateTitle("ok\xff"); err == nil {
		t.Fatal("invalid UTF-8 title accepted")
	}
}

func TestParseKindAndScope(t *testing.T) {
	for _, ok := range []string{"skill", "rule"} {
		if _, err := ParseKind(ok); err != nil {
			t.Errorf("ParseKind(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Skill", "plugin", "workflow"} {
		if _, err := ParseKind(bad); err == nil {
			t.Errorf("ParseKind(%q) accepted", bad)
		}
	}
	if !ScopeProject.Valid() || !ScopeWorkspaceDefault.Valid() || Scope("tenant").Valid() {
		t.Fatal("scope validity broken")
	}
	if !StatusDraft.Valid() || !StatusActive.Valid() || !StatusDeleted.Valid() || Status("gone").Valid() {
		t.Fatal("status validity broken")
	}
}

func TestValidateProjectRefScopeRules(t *testing.T) {
	if err := ValidateProjectRef(ScopeWorkspaceDefault, ""); err != nil {
		t.Errorf("workspace default with empty project: %v", err)
	}
	if err := ValidateProjectRef(ScopeWorkspaceDefault, "p"); err == nil {
		t.Error("workspace default with project accepted")
	}
	if err := ValidateProjectRef(ScopeProject, "p"); err != nil {
		t.Errorf("project scope with project: %v", err)
	}
	if err := ValidateProjectRef(ScopeProject, ""); err == nil {
		t.Error("project scope without project accepted")
	}
	if err := ValidateProjectRef(ScopeProject, "p\x01"); err == nil {
		t.Error("control character in project accepted")
	}
}

func TestValidateContentTypeMarkdownOnly(t *testing.T) {
	if err := ValidateContentType(""); err != nil {
		t.Errorf("empty (default) rejected: %v", err)
	}
	if err := ValidateContentType(ContentTypeMarkdown); err != nil {
		t.Errorf("text/markdown rejected: %v", err)
	}
	for _, bad := range []string{"application/octet-stream", "text/x-shellscript", "text/markdown; charset=utf-8"} {
		if err := ValidateContentType(bad); err == nil {
			t.Errorf("executable-capable content type %q accepted", bad)
		}
	}
}

func TestPageSizeBounds(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, DefaultPageSize},
		{-5, DefaultPageSize},
		{1, 1},
		{20, 20},
		{100, MaxPageSize},
		{101, MaxPageSize},
		{100000, MaxPageSize},
	}
	for _, tc := range cases {
		if got := PageSizeBounds(tc.in); got != tc.want {
			t.Errorf("PageSizeBounds(%d)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestApprovedLimitConstants(t *testing.T) {
	// The approved limits are contract; any change is a spec amendment.
	if MaxArtifactContentBytes != 1048576 {
		t.Errorf("MaxArtifactContentBytes=%d want 1048576", MaxArtifactContentBytes)
	}
	if MaxArtifactMetadataBytes != 65536 {
		t.Errorf("MaxArtifactMetadataBytes=%d want 65536", MaxArtifactMetadataBytes)
	}
	if MaxEffectiveArtifacts != 2000 {
		t.Errorf("MaxEffectiveArtifacts=%d want 2000", MaxEffectiveArtifacts)
	}
	if MaxProtocolBundleBytes != 4194304 {
		t.Errorf("MaxProtocolBundleBytes=%d want 4194304", MaxProtocolBundleBytes)
	}
	if DefaultPageSize != 20 || MaxPageSize != 100 {
		t.Errorf("page size constants drifted: default=%d max=%d", DefaultPageSize, MaxPageSize)
	}
}

func TestValidateContentExactBoundary(t *testing.T) {
	// ASCII exactly at limit and one over.
	if err := ValidateContent(strings.Repeat("a", MaxArtifactContentBytes)); err != nil {
		t.Fatalf("exact 1MiB ASCII content rejected: %v", err)
	}
	err := ValidateContent(strings.Repeat("a", MaxArtifactContentBytes+1))
	if !errorHasCode(err, ErrCodeContentTooLarge) {
		t.Fatalf("1MiB+1 accepted or wrong code: %v", err)
	}
	// Multibyte exactly at limit: 2-byte runes fill the budget.
	if err := ValidateContent(strings.Repeat("é", MaxArtifactContentBytes/2)); err != nil {
		t.Fatalf("exact 1MiB multibyte content rejected: %v", err)
	}
	over := strings.Repeat("é", MaxArtifactContentBytes/2) + "a"
	if err := ValidateContent(over); !errorHasCode(err, ErrCodeContentTooLarge) {
		t.Fatalf("multibyte 1MiB+1 accepted: %v", err)
	}
	// A rune is never partially truncated: validity is checked before size.
	if err := ValidateContent("ok\xff"); !errorHasCode(err, ErrCodeInvalidUTF8) {
		t.Fatalf("invalid UTF-8 not reported as such: %v", err)
	}
	if err := ValidateContent(""); err == nil {
		t.Fatal("empty content accepted")
	}
}

// withCanonicalETag derives and assigns the canonical ETag so the artifact
// satisfies the derivation check in Validate.
func withCanonicalETag(a Artifact) Artifact {
	etag, err := a.CanonicalETag()
	if err != nil {
		panic(err)
	}
	a.ETag = etag
	return a
}

// liveArtifact builds a fully valid non-deleted artifact.
func liveArtifact() Artifact {
	return withCanonicalETag(Artifact{
		ID: "a1", Project: "p", Kind: KindSkill, Key: "k/1",
		Title: "T", Scope: ScopeProject, Status: StatusActive,
		LatestRevision: 2,
	})
}

// TestArtifactDeleteProvenance is the negative oracle for REQ-RET-001: the
// soft-delete result MUST carry deleted_at/deleted_by (MANDATORY
// actor)/reason, and live artifacts MUST NOT carry delete provenance.
func TestArtifactDeleteProvenance(t *testing.T) {
	now := timeNowUTC()
	deleted := liveArtifact()
	deleted.Status = StatusDeleted
	deleted.UpdatedAt = now
	// Missing provenance of any kind is rejected.
	if err := deleted.Validate(); err == nil {
		t.Fatal("deleted artifact without provenance accepted")
	}
	deleted.DeletedBy = "u1"
	if err := deleted.Validate(); err == nil {
		t.Fatal("deleted artifact without deleted_at accepted")
	}
	deleted.DeletedAt = &now
	if err := deleted.Validate(); err == nil {
		t.Fatal("deleted artifact without reason accepted")
	}
	deleted.DeleteReason = "deprecated"
	deleted = withCanonicalETag(deleted)
	if err := deleted.Validate(); err != nil {
		t.Fatalf("fully provenanced delete rejected: %v", err)
	}
	// Oversize reason is rejected.
	noReasonBound := deleted
	noReasonBound.DeleteReason = strings.Repeat("r", 1025)
	if err := noReasonBound.Validate(); err == nil {
		t.Fatal("oversize delete reason accepted")
	}
	// Invalid UTF-8 actor is rejected.
	badUTF8 := deleted
	badUTF8.DeletedBy = "u\xff"
	if err := badUTF8.Validate(); err == nil {
		t.Fatal("invalid UTF-8 deleted_by accepted")
	}
	// A deleted artifact with a MISSING deleted_by is rejected even when
	// every other provenance field is present (actor is mandatory).
	anonymous := deleted
	anonymous.DeletedBy = ""
	if err := anonymous.Validate(); err == nil {
		t.Fatal("anonymous delete (missing deleted_by) accepted")
	}
	// An oversize actor is rejected.
	oversizeActor := deleted
	oversizeActor.DeletedBy = strings.Repeat("u", MaxActorBytes+1)
	if err := oversizeActor.Validate(); err == nil {
		t.Fatal("oversize deleted_by accepted")
	}
	// Live artifacts must not carry provenance.
	live := liveArtifact()
	live.DeletedAt = &now
	if err := live.Validate(); err == nil {
		t.Fatal("active artifact with deleted_at accepted")
	}
	live = liveArtifact()
	live.DeletedBy = "leftover"
	if err := live.Validate(); err == nil {
		t.Fatal("active artifact with deleted_by accepted")
	}
	live = liveArtifact()
	live.DeleteReason = "leftover"
	if err := live.Validate(); err == nil {
		t.Fatal("active artifact with delete_reason accepted")
	}
}

// TestArtifactETagCanonicalDerivation is the negative oracle for artifact
// ETags: they are DERIVED from covered state, never free-form. Validate
// rejects missing/malformed/non-derivation-matching values, and every
// covered state transition (including the soft-delete provenance) changes
// the tag so a stale If-Match can never delete or mutate unseen state.
func TestArtifactETagCanonicalDerivation(t *testing.T) {
	base := liveArtifact()
	etag, err := base.CanonicalETag()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateETagShape(etag); err != nil {
		t.Fatalf("derived etag malformed: %v", err)
	}
	if !base.VerifyETag() {
		t.Fatal("canonical artifact does not verify")
	}
	// Deterministic: rebuilding the same state derives the same tag.
	if again, _ := liveArtifact().CanonicalETag(); again != etag {
		t.Fatal("etag derivation is not deterministic")
	}
	// Wall clocks are not covered: created_at/updated_at never change it.
	clocked := base
	clocked.CreatedAt = base.CreatedAt.Add(time.Hour)
	clocked.UpdatedAt = base.UpdatedAt.Add(2 * time.Hour)
	if got, _ := clocked.CanonicalETag(); got != etag {
		t.Fatal("etag changed with timestamps")
	}
	// Missing/malformed/foreign etags are rejected by Validate.
	for name, bad := range map[string]string{
		"empty":      "",
		"short":      `"abc"`,
		"unquoted":   strings.Repeat("a", 64),
		"uppercase":  `"` + strings.Repeat("A", 64) + `"`,
		"non-hex":    `"` + strings.Repeat("g", 64) + `"`,
		"foreign":    validETagHex(),
		"unbalanced": `"` + strings.Repeat("a", 63),
	} {
		mutated := base
		mutated.ETag = bad
		if err := mutated.Validate(); err == nil {
			t.Errorf("%s etag accepted", name)
		}
	}
	// Every covered field mutation invalidates the stale tag.
	now := timeNowUTC()
	mutations := map[string]func(*Artifact){
		"status":              func(a *Artifact) { a.Status = StatusDraft },
		"title":               func(a *Artifact) { a.Title = "T2" },
		"latest-revision":     func(a *Artifact) { a.LatestRevision = 3 },
		"precedence":          func(a *Artifact) { a.Precedence = 7 },
		"activation-revision": func(a *Artifact) { a.ActivationRevision = 2 },
		"active-revision":     func(a *Artifact) { r := int64(2); a.ActiveRevision = &r },
		"delete-provenance": func(a *Artifact) {
			a.Status = StatusDeleted
			a.DeletedAt = &now
			a.DeletedBy = "u1"
			a.DeleteReason = "r"
		},
	}
	for name, mutate := range mutations {
		mutated := base
		mutate(&mutated)
		if mutated.VerifyETag() {
			t.Errorf("%s mutation did not change the canonical etag", name)
		}
		if err := mutated.Validate(); err == nil {
			t.Errorf("%s mutation with stale etag accepted", name)
		}
		// Recomputing restores validity for otherwise-valid states.
		repaired := withCanonicalETag(mutated)
		if err := repaired.Validate(); err != nil {
			t.Errorf("%s mutation with recomputed etag rejected: %v", name, err)
		}
	}
}

// TestArtifactEventValidation pins the audit-event invariants, including the
// events of a soft-deleted artifact remaining valid history.
func TestArtifactEventValidation(t *testing.T) {
	now := timeNowUTC()
	ok := ArtifactEvent{
		ID: "e1", ArtifactID: "a1", Type: EventSoftDeleted,
		Actor: "u1", Reason: "deprecated", CreatedAt: now,
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid soft-delete event rejected: %v", err)
	}
	activated := ArtifactEvent{
		ID: "e2", ArtifactID: "a1", Type: EventActivated,
		Revision: 2, ActivationRevision: 3, Actor: "u1", CreatedAt: now,
	}
	if err := activated.Validate(); err != nil {
		t.Fatalf("valid activation event rejected: %v", err)
	}
	for _, in := range []ArtifactEvent{
		{ID: "", ArtifactID: "a1", Type: EventActivated},
		{ID: "e", ArtifactID: "", Type: EventActivated},
		{ID: "e", ArtifactID: "a1", Type: EventType("hard_deleted")},
		{ID: "e", ArtifactID: "a1", Type: EventRevisionAppended, Revision: -1},
		{ID: "e", ArtifactID: "a1", Type: EventActivated, ActivationRevision: -1},
		{ID: "e", ArtifactID: "a1", Type: EventActivated, Actor: "u\xff"},
		{ID: "e", ArtifactID: "a1", Type: EventRolledBack, Reason: strings.Repeat("r", 1025)},
		// The soft-delete transition is never anonymous.
		{ID: "e", ArtifactID: "a1", Type: EventSoftDeleted, Reason: "r"},
		// Oversize actors are rejected on every event type.
		{ID: "e", ArtifactID: "a1", Type: EventActivated, Actor: strings.Repeat("u", MaxActorBytes+1)},
		{ID: "e", ArtifactID: "a1", Type: EventSoftDeleted, Actor: strings.Repeat("u", MaxActorBytes+1)},
	} {
		if err := in.Validate(); err == nil {
			t.Errorf("invalid event accepted: %+v", in)
		}
	}
	// Other event types may legitimately omit the actor only when the
	// store has no principal; soft delete may not.
	if err := (ArtifactEvent{ID: "e", ArtifactID: "a1", Type: EventRevisionAppended, Revision: 2}).Validate(); err != nil {
		t.Fatalf("system-attributed revision event rejected: %v", err)
	}
}

func errorHasCode(err error, code ErrorCode) bool {
	if err == nil {
		return false
	}
	var typed *Error
	if e, ok := err.(*Error); ok {
		typed = e
		return typed.Code == code
	}
	return false
}

func timeNowUTC() time.Time {
	return time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC)
}
