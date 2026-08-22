package projectprotocol

import (
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"
)

func candidate(id string, kind Kind, key string, scope Scope, precedence int32, revision int64) ResolvableArtifact {
	return ResolvableArtifact{
		ArtifactID: id,
		Kind:       kind,
		Key:        key,
		Scope:      scope,
		Precedence: precedence,
		Revision:   revision,
		Digest:     "sha256:" + strings.Repeat("0", 64),
	}
}

func TestResolveProjectOverWorkspace(t *testing.T) {
	candidates := []ResolvableArtifact{
		candidate("w1", KindSkill, "python/testing", ScopeWorkspaceDefault, 0, 3),
		candidate("p1", KindSkill, "python/testing", ScopeProject, 0, 1),
		candidate("w2", KindRule, "commit/style", ScopeWorkspaceDefault, 0, 2),
		candidate("w3", KindSkill, "aaa/first", ScopeWorkspaceDefault, 0, 1),
	}
	res, err := Resolve(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Effective) != 3 {
		t.Fatalf("effective=%d want 3", len(res.Effective))
	}
	// Sorted by kind then key: rule/commit/style, skill/aaa/first,
	// skill/python/testing (project winner).
	if res.Effective[0].ArtifactID != "w2" || res.Effective[1].ArtifactID != "w3" || res.Effective[2].ArtifactID != "p1" {
		t.Fatalf("effective order wrong: %+v", res.Effective)
	}
	if len(res.Shadowed) != 1 || res.Shadowed[0].ArtifactID != "w1" || res.Shadowed[0].ShadowedByID != "p1" {
		t.Fatalf("shadowed wrong: %+v", res.Shadowed)
	}
}

func TestResolveDeterministicUnderShuffles(t *testing.T) {
	candidates := []ResolvableArtifact{
		candidate("w1", KindSkill, "k1", ScopeWorkspaceDefault, 0, 1),
		candidate("p1", KindSkill, "k1", ScopeProject, 0, 2),
		candidate("w2", KindRule, "k2", ScopeWorkspaceDefault, 0, 1),
		candidate("p2", KindRule, "k3", ScopeProject, 5, 1),
		candidate("w3", KindRule, "k3", ScopeWorkspaceDefault, 0, 9),
		candidate("p3", KindSkill, "k4", ScopeProject, 0, 1),
	}
	base, err := Resolve(candidates)
	if err != nil {
		t.Fatal(err)
	}
	// Multiple rotations of the input must not change the outcome.
	for rot := 1; rot < len(candidates); rot++ {
		rotated := append(append([]ResolvableArtifact{}, candidates[rot:]...), candidates[:rot]...)
		got, err := Resolve(rotated)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Effective) != len(base.Effective) {
			t.Fatalf("rotation %d: effective size changed", rot)
		}
		for i := range got.Effective {
			if got.Effective[i] != base.Effective[i] {
				t.Fatalf("rotation %d: effective[%d] differs: %+v vs %+v", rot, i, got.Effective[i], base.Effective[i])
			}
		}
		if len(got.Shadowed) != len(base.Shadowed) {
			t.Fatalf("rotation %d: shadowed size changed", rot)
		}
	}
}

func TestResolveSameScopeConflictDeterministicWinner(t *testing.T) {
	candidates := []ResolvableArtifact{
		candidate("b-id", KindSkill, "k", ScopeProject, 1, 1),
		candidate("a-id", KindSkill, "k", ScopeProject, 1, 2),
		candidate("c-id", KindSkill, "k", ScopeProject, 9, 1),
	}
	res, err := Resolve(candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts=%d want 1", len(res.Conflicts))
	}
	c := res.Conflicts[0]
	if c.ResolvedByID != "c-id" {
		t.Fatalf("winner=%s want highest precedence c-id", c.ResolvedByID)
	}
	if len(c.ArtifactIDs) != 3 || c.ArtifactIDs[0] != "a-id" {
		t.Fatalf("conflict ids not sorted/complete: %v", c.ArtifactIDs)
	}
	// Precedence tie resolves by artifact id ascending.
	tie := []ResolvableArtifact{
		candidate("b-id", KindSkill, "k", ScopeProject, 4, 1),
		candidate("a-id", KindSkill, "k", ScopeProject, 4, 2),
	}
	res2, err := Resolve(tie)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Effective[0].ArtifactID != "a-id" {
		t.Fatalf("tie winner=%s want a-id", res2.Effective[0].ArtifactID)
	}
}

func TestResolveEffectiveLimitExactAndPlusOne(t *testing.T) {
	build := func(n int) []ResolvableArtifact {
		out := make([]ResolvableArtifact, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, candidate(fmt.Sprintf("id-%d", i), KindSkill, fmt.Sprintf("k%04d", i), ScopeProject, 0, 1))
		}
		return out
	}
	res, err := Resolve(build(MaxEffectiveArtifacts))
	if err != nil {
		t.Fatalf("exactly 2000 effective rejected: %v", err)
	}
	if len(res.Effective) != MaxEffectiveArtifacts {
		t.Fatalf("effective=%d want 2000", len(res.Effective))
	}
	_, err = Resolve(build(MaxEffectiveArtifacts + 1))
	if !errorHasCode(err, ErrCodeEffectiveLimitExceeded) {
		t.Fatalf("2001 effective not rejected: %v", err)
	}
	// Shadowed workspace duplicates do not count against the cap.
	withShadow := append(build(MaxEffectiveArtifacts),
		candidate("extra", KindSkill, "k0000", ScopeWorkspaceDefault, 0, 1))
	if _, err := Resolve(withShadow); err != nil {
		t.Fatalf("2000 effective + shadow rejected: %v", err)
	}
}

func TestResolveRejectsInvalidCandidates(t *testing.T) {
	bad := candidate("x", "workflow", "k", ScopeProject, 0, 1)
	if _, err := Resolve([]ResolvableArtifact{bad}); err == nil {
		t.Fatal("invalid kind accepted")
	}
	badRev := candidate("x", KindSkill, "k", ScopeProject, 0, 0)
	if _, err := Resolve([]ResolvableArtifact{badRev}); err == nil {
		t.Fatal("revision 0 accepted")
	}
}

// TestResolveDeduplicatesExactDuplicateCandidates is the negative oracle for
// candidate identity integrity: repeated identical rows collapse without
// changing the outcome and never manufacture self-conflicts.
func TestResolveDeduplicatesExactDuplicateCandidates(t *testing.T) {
	single := []ResolvableArtifact{
		candidate("w1", KindSkill, "k1", ScopeWorkspaceDefault, 0, 1),
		candidate("p1", KindSkill, "k1", ScopeProject, 0, 2),
		candidate("w2", KindRule, "k2", ScopeWorkspaceDefault, 0, 1),
	}
	doubled := []ResolvableArtifact{single[0], single[1], single[2], single[0], single[1], single[0], single[2], single[1]}
	a, errA := Resolve(single)
	b, errB := Resolve(doubled)
	if errA != nil || errB != nil {
		t.Fatalf("resolve errors: %v / %v", errA, errB)
	}
	if !reflect.DeepEqual(a.Effective, b.Effective) || !reflect.DeepEqual(a.Shadowed, b.Shadowed) || !reflect.DeepEqual(a.Conflicts, b.Conflicts) {
		t.Fatalf("exact duplicates changed the outcome:\n%+v\n%+v", a, b)
	}
	// A lone artifact repeated never conflicts with itself.
	lone := []ResolvableArtifact{single[0], single[0], single[0]}
	res, err := Resolve(lone)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 0 || len(res.Effective) != 1 {
		t.Fatalf("repeated lone artifact self-conflicted: %+v", res)
	}
}

// TestResolveRejectsInconsistentDuplicateIDs is the negative oracle for
// candidate identity integrity: the same artifact id with ANY differing
// summary field is inconsistent data and rejected, so Resolve output is
// always internally valid (sorted, unique, crosscheckable provenance).
func TestResolveRejectsInconsistentDuplicateIDs(t *testing.T) {
	base := candidate("p1", KindSkill, "k/1", ScopeProject, 0, 1)
	mutations := map[string]func(*ResolvableArtifact){
		"key":        func(c *ResolvableArtifact) { c.Key = "other/key" },
		"kind":       func(c *ResolvableArtifact) { c.Kind = KindRule },
		"scope":      func(c *ResolvableArtifact) { c.Scope = ScopeWorkspaceDefault },
		"precedence": func(c *ResolvableArtifact) { c.Precedence = 9 },
		"revision":   func(c *ResolvableArtifact) { c.Revision = 7 },
		"digest":     func(c *ResolvableArtifact) { c.Digest = "sha256:" + strings.Repeat("f", 64) },
	}
	for name, mutate := range mutations {
		conflicting := base
		mutate(&conflicting)
		for _, order := range [][]ResolvableArtifact{
			{base, conflicting},
			{conflicting, base},
		} {
			if _, err := Resolve(order); err == nil {
				t.Errorf("%s inconsistent duplicate id accepted (%v first)", name, order[0].Key)
			}
		}
	}
}

// TestProtocolConflictScopeMustMatchEffectiveWinner is the negative oracle
// for conflict/winner scope agreement: direct ResolvedByID acceptance
// requires the conflict scope to equal the effective winner's source scope;
// opposite-scope displacement is legal only through the validated shadowed
// branch.
func TestProtocolConflictScopeMustMatchEffectiveWinner(t *testing.T) {
	artifact := func(scope Scope) ProtocolArtifact {
		return ProtocolArtifact{
			ArtifactID: "a-id", Kind: KindSkill, Key: "k", Title: "T",
			Revision: 1, SourceScope: scope, ContentType: ContentTypeMarkdown,
			Content: "body", Metadata: map[string]any{},
			Digest: "sha256:" + strings.Repeat("0", 64),
		}
	}
	conflict := func(scope Scope, resolvedBy string) KeyConflict {
		return KeyConflict{
			Kind: KindSkill, Key: "k", Scope: scope,
			ArtifactIDs: []string{"a-id", "b-id"}, ResolvedByID: resolvedBy,
		}
	}
	build := func(winnerScope Scope, c KeyConflict, shadowed []ShadowedArtifact) *Protocol {
		return &Protocol{
			Project: "proj", ProtocolRevision: "snap-1",
			Artifacts: []ProtocolArtifact{artifact(winnerScope)},
			Conflicts: []KeyConflict{c}, Shadowed: shadowed,
		}
	}

	// Consistent: workspace-default conflict won by the workspace-default
	// effective winner (direct acceptance, scopes agree).
	ok := build(ScopeWorkspaceDefault, conflict(ScopeWorkspaceDefault, "a-id"), nil)
	if err := ok.Validate(); err != nil {
		t.Fatalf("scope-matched direct acceptance rejected: %v", err)
	}
	if _, err := ok.EncodeBundle(); err != nil {
		t.Fatalf("scope-matched direct acceptance not encodable: %v", err)
	}
	// Consistent: project conflict won by the project effective winner.
	okProject := build(ScopeProject, conflict(ScopeProject, "a-id"), nil)
	if err := okProject.Validate(); err != nil {
		t.Fatalf("project scope-matched direct acceptance rejected: %v", err)
	}
	// Mismatch: workspace-default conflict claiming a project-scope
	// effective winner by ID — rejected even though the IDs match.
	mismatch := build(ScopeProject, conflict(ScopeWorkspaceDefault, "a-id"), nil)
	if err := mismatch.Validate(); err == nil {
		t.Fatal("workspace conflict with project-scope winner id accepted")
	}
	if _, err := mismatch.EncodeBundle(); err == nil {
		t.Fatal("scope-mismatched conflict encoded")
	}
	// Mirror mismatch: project conflict claiming a workspace-scope winner.
	mirror := build(ScopeWorkspaceDefault, conflict(ScopeProject, "a-id"), nil)
	if err := mirror.Validate(); err == nil {
		t.Fatal("project conflict with workspace-scope winner id accepted")
	}
	// Legitimate opposite-scope displacement: workspace-default conflict
	// loser displaced by the project winner, explained by a shadowed entry.
	displaced := build(ScopeProject, conflict(ScopeWorkspaceDefault, "b-id"), []ShadowedArtifact{{
		ArtifactID: "b-id", Kind: KindSkill, Key: "k", Revision: 1, ShadowedByID: "a-id",
	}})
	if err := displaced.Validate(); err != nil {
		t.Fatalf("shadowed-explained displacement rejected: %v", err)
	}
	// Displacement without the shadowed explanation is rejected.
	unexplained := build(ScopeProject, conflict(ScopeWorkspaceDefault, "b-id"), nil)
	if err := unexplained.Validate(); err == nil {
		t.Fatal("unexplained opposite-scope displacement accepted")
	}
}

func smallProtocol(content string) *Protocol {
	return &Protocol{
		Project:          "proj",
		ProtocolRevision: "snap-1",
		GeneratedAt:      time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
		Artifacts: []ProtocolArtifact{{
			ArtifactID:  "a-1",
			Kind:        KindSkill,
			Key:         "k/1",
			Title:       "T",
			Revision:    1,
			SourceScope: ScopeProject,
			ContentType: ContentTypeMarkdown,
			Content:     content,
			Metadata:    map[string]any{"z": 1, "a": 2},
			Digest:      "sha256:" + strings.Repeat("0", 64),
		}},
	}
}

func TestProtocolBundleCanonicalAndStable(t *testing.T) {
	p := smallProtocol("hello")
	b1, err := p.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	// Metadata map ordering cannot affect bundle bytes/etag: rebuild with a
	// differently-ordered map.
	p2 := smallProtocol("hello")
	p2.Artifacts[0].Metadata = map[string]any{"a": 2, "z": 1}
	b2, err := p2.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	if string(b1.Canonical) != string(b2.Canonical) || b1.ETag != b2.ETag {
		t.Fatalf("bundle unstable across metadata map order:\n%s\n%s", b1.Canonical, b2.Canonical)
	}
	if b1.BytesLen != len(b1.Canonical) {
		t.Fatalf("BytesLen=%d len=%d", b1.BytesLen, len(b1.Canonical))
	}
	// Canonical form is sorted: "a" before "z" inside metadata.
	if !strings.Contains(string(b1.Canonical), `"metadata":{"a":2,"z":1}`) {
		t.Fatalf("metadata not sorted in bundle: %s", b1.Canonical)
	}
}

// paddedProtocol builds a snapshot whose canonical bundle size is exactly
// targetBytes by distributing ASCII padding across artifacts, each staying
// within the per-artifact content cap.
func paddedProtocol(targetBytes int, extra int) (*Protocol, error) {
	const artifacts = 5
	p := &Protocol{Project: "proj", ProtocolRevision: "snap-1"}
	for i := 0; i < artifacts; i++ {
		p.Artifacts = append(p.Artifacts, ProtocolArtifact{
			ArtifactID: fmt.Sprintf("a-%d", i), Kind: KindSkill,
			Key: fmt.Sprintf("k/%d", i), Title: "T", Revision: 1,
			SourceScope: ScopeProject, ContentType: ContentTypeMarkdown,
			Content: "a", Metadata: map[string]any{},
			Digest: "sha256:" + strings.Repeat("0", 64),
		})
	}
	probe, err := p.EncodeBundle()
	if err != nil {
		return nil, err
	}
	need := targetBytes + extra - probe.BytesLen
	if need < 0 {
		return nil, fmt.Errorf("probe %d already exceeds target %d", probe.BytesLen, targetBytes+extra)
	}
	per := need / artifacts
	rem := need % artifacts
	for i := range p.Artifacts {
		pad := per
		if i == artifacts-1 {
			pad += rem
		}
		content := strings.Repeat("a", 1+pad)
		if len(content) > MaxArtifactContentBytes {
			return nil, fmt.Errorf("artifact %d content %d exceeds cap", i, len(content))
		}
		p.Artifacts[i].Content = content
	}
	return p, nil
}

func TestProtocolBundleExactLimitAndPlusOne(t *testing.T) {
	// Bundle size is affine in ASCII content length; pad across artifacts so
	// each stays under the 1 MiB per-artifact content cap.
	exact, err := paddedProtocol(MaxProtocolBundleBytes, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := exact.EncodeBundle()
	if err != nil {
		t.Fatalf("exactly 4MiB bundle rejected: %v", err)
	}
	if b.BytesLen != MaxProtocolBundleBytes {
		t.Fatalf("bundle bytes=%d want %d", b.BytesLen, MaxProtocolBundleBytes)
	}
	over, err := paddedProtocol(MaxProtocolBundleBytes, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := over.EncodeBundle(); !errorHasCode(err, ErrCodeProtocolTooLarge) {
		t.Fatalf("4MiB+1 accepted: %v", err)
	}
}

// TestResolveConflictOrderDeterministic is the negative oracle for conflict
// ordering: multiple conflicts across kinds, keys and scopes MUST come out
// sorted (kind, key, scope) regardless of candidate input order.
func TestResolveConflictOrderDeterministic(t *testing.T) {
	build := func() []ResolvableArtifact {
		return []ResolvableArtifact{
			candidate("z1", KindRule, "b/key", ScopeWorkspaceDefault, 1, 1),
			candidate("z2", KindRule, "b/key", ScopeWorkspaceDefault, 2, 1),
			candidate("y1", KindSkill, "a/key", ScopeProject, 1, 1),
			candidate("y2", KindSkill, "a/key", ScopeProject, 1, 2),
			candidate("y3", KindSkill, "a/key", ScopeProject, 9, 1),
			candidate("x1", KindRule, "a/key", ScopeWorkspaceDefault, 1, 1),
			candidate("x2", KindRule, "a/key", ScopeWorkspaceDefault, 1, 1),
			candidate("w1", KindSkill, "a/key", ScopeWorkspaceDefault, 1, 1),
			candidate("w2", KindSkill, "a/key", ScopeWorkspaceDefault, 1, 1),
		}
	}
	base, err := Resolve(build())
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Conflicts) != 4 {
		t.Fatalf("conflicts=%d want 4 (rule/b-key-ws, rule/a-key-ws, skill/a-key-project, skill/a-key-ws)", len(base.Conflicts))
	}
	wantOrder := []struct {
		kind Kind
		key  string
		sc   Scope
	}{
		{KindRule, "a/key", ScopeWorkspaceDefault},
		{KindRule, "b/key", ScopeWorkspaceDefault},
		{KindSkill, "a/key", ScopeProject},
		{KindSkill, "a/key", ScopeWorkspaceDefault},
	}
	for i, w := range wantOrder {
		c := base.Conflicts[i]
		if c.Kind != w.kind || c.Key != w.key || c.Scope != w.sc {
			t.Fatalf("conflict[%d]=(%s,%s,%s) want (%s,%s,%s)", i, c.Kind, c.Key, c.Scope, w.kind, w.key, w.sc)
		}
	}
	// Deterministic under shuffles.
	rng := rand.New(rand.NewSource(11))
	candidates := build()
	for shuffle := 0; shuffle < 12; shuffle++ {
		rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
		got, err := Resolve(candidates)
		if err != nil {
			t.Fatal(err)
		}
		for i := range base.Conflicts {
			a, b := base.Conflicts[i], got.Conflicts[i]
			if a.Kind != b.Kind || a.Key != b.Key || a.Scope != b.Scope ||
				a.ResolvedByID != b.ResolvedByID || !reflect.DeepEqual(a.ArtifactIDs, b.ArtifactIDs) {
				t.Fatalf("shuffle %d: conflict[%d] changed: %+v vs %+v", shuffle, i, a, b)
			}
		}
	}
}

// buildProtocolFromResolution materializes a full Protocol snapshot from a
// Resolve output, preserving the deterministic shadowed/conflict provenance.
func buildProtocolFromResolution(res Resolution) *Protocol {
	p := &Protocol{Project: "proj", ProtocolRevision: "snap-1"}
	for _, e := range res.Effective {
		p.Artifacts = append(p.Artifacts, ProtocolArtifact{
			ArtifactID: e.ArtifactID, Kind: e.Kind, Key: e.Key,
			Title: "T-" + e.ArtifactID, Revision: e.Revision,
			SourceScope: e.Scope, ContentType: ContentTypeMarkdown,
			Content: "body " + e.ArtifactID, Metadata: map[string]any{},
			Digest: e.Digest, Precedence: e.Precedence,
		})
	}
	p.Shadowed = append([]ShadowedArtifact{}, res.Shadowed...)
	p.Conflicts = append([]KeyConflict{}, res.Conflicts...)
	return p
}

// provenanceScenario resolves a snapshot exercising every provenance branch:
// project-over-workspace shadowing, project-scope conflicts, workspace-scope
// conflicts won by the effective artifact, and workspace-scope conflicts
// explained by a shadowed override.
func provenanceScenario(t *testing.T) *Protocol {
	t.Helper()
	candidates := []ResolvableArtifact{
		candidate("w1", KindSkill, "k1", ScopeWorkspaceDefault, 0, 1),
		candidate("p1", KindSkill, "k1", ScopeProject, 0, 2),
		candidate("a-id", KindSkill, "k2", ScopeProject, 1, 1),
		candidate("b-id", KindSkill, "k2", ScopeProject, 5, 1),
		candidate("w2", KindRule, "k3", ScopeWorkspaceDefault, 1, 1),
		candidate("w3", KindRule, "k3", ScopeWorkspaceDefault, 0, 2),
		candidate("w4", KindRule, "k4", ScopeWorkspaceDefault, 3, 1),
		candidate("w5", KindRule, "k4", ScopeWorkspaceDefault, 0, 1),
		candidate("p9", KindRule, "k4", ScopeProject, 0, 1),
	}
	res, err := Resolve(candidates)
	if err != nil {
		t.Fatal(err)
	}
	return buildProtocolFromResolution(res)
}

// TestProtocolBundleShadowedConflictProvenance is the negative oracle for
// bundle provenance: EncodeBundle validates shadowed/conflicts semantically
// (field invariants), crosschecked against the effective set, unique and
// sorted — before any canonical byte is emitted.
func TestProtocolBundleShadowedConflictProvenance(t *testing.T) {
	base := provenanceScenario(t)
	if err := base.Validate(); err != nil {
		t.Fatalf("valid provenance snapshot rejected: %v", err)
	}
	if _, err := base.EncodeBundle(); err != nil {
		t.Fatalf("valid provenance snapshot not encodable: %v", err)
	}
	if len(base.Shadowed) < 2 || len(base.Conflicts) < 3 {
		t.Fatalf("scenario too weak: shadowed=%d conflicts=%d", len(base.Shadowed), len(base.Conflicts))
	}

	copyProtocol := func() *Protocol {
		clone := *base
		clone.Artifacts = append([]ProtocolArtifact{}, base.Artifacts...)
		clone.Shadowed = append([]ShadowedArtifact{}, base.Shadowed...)
		clone.Conflicts = append([]KeyConflict{}, base.Conflicts...)
		for i := range clone.Conflicts {
			clone.Conflicts[i].ArtifactIDs = append([]string{}, base.Conflicts[i].ArtifactIDs...)
		}
		return &clone
	}

	// --- semantic negatives on shadowed entries ---
	unsortedShadow := copyProtocol()
	unsortedShadow.Shadowed[0], unsortedShadow.Shadowed[1] = unsortedShadow.Shadowed[1], unsortedShadow.Shadowed[0]
	dupShadow := copyProtocol()
	dupShadow.Shadowed = append(dupShadow.Shadowed, dupShadow.Shadowed[0])
	selfShadow := copyProtocol()
	selfShadow.Shadowed[0].ShadowedByID = selfShadow.Shadowed[0].ArtifactID
	mismatchShadow := copyProtocol()
	mismatchShadow.Shadowed[0].ShadowedByID = "nonexistent-id"
	orphanShadow := copyProtocol()
	orphanShadow.Shadowed[0].Key = "zz/no-winner"
	zeroRevShadow := copyProtocol()
	zeroRevShadow.Shadowed[0].Revision = 0

	// --- semantic negatives on conflicts ---
	singleConflict := copyProtocol()
	singleConflict.Conflicts[0].ArtifactIDs = singleConflict.Conflicts[0].ArtifactIDs[:1]
	unsortedIDs := copyProtocol()
	unsortedIDs.Conflicts[0].ArtifactIDs = []string{"z-id", "a-id"}
	dupIDs := copyProtocol()
	dupIDs.Conflicts[0].ArtifactIDs = []string{"a-id", "a-id"}
	outsiderWinner := copyProtocol()
	outsiderWinner.Conflicts[0].ResolvedByID = "outsider-id"
	orphanConflict := copyProtocol()
	orphanConflict.Conflicts[0].Key = "zz/no-effective"
	unsortedConflicts := copyProtocol()
	unsortedConflicts.Conflicts[0], unsortedConflicts.Conflicts[len(unsortedConflicts.Conflicts)-1] =
		unsortedConflicts.Conflicts[len(unsortedConflicts.Conflicts)-1], unsortedConflicts.Conflicts[0]
	dupConflicts := copyProtocol()
	dupConflicts.Conflicts = append(dupConflicts.Conflicts, dupConflicts.Conflicts[0])
	badScopeConflict := copyProtocol()
	badScopeConflict.Conflicts[0].Scope = Scope("tenant")
	// Workspace conflict whose winner lost the key to a project override
	// WITHOUT the shadowed provenance explaining it.
	unexplained := copyProtocol()
	for i := range unexplained.Conflicts {
		if unexplained.Conflicts[i].Scope == ScopeWorkspaceDefault {
			unexplained.Conflicts[i].Key = "k4"
			unexplained.Conflicts[i].Kind = KindRule
			break
		}
	}
	unexplained.Shadowed = nil

	// --- crosscheck negatives against the effective set ---
	// A project-scope conflict whose winner is not the effective artifact.
	projectMismatch := copyProtocol()
	for i := range projectMismatch.Conflicts {
		if projectMismatch.Conflicts[i].Scope == ScopeProject {
			projectMismatch.Conflicts[i].ResolvedByID = projectMismatch.Conflicts[i].ArtifactIDs[0]
			break
		}
	}
	// An effective artifact shadowed winner disappears: shadowed entry loses
	// its effective counterpart.
	lostWinner := copyProtocol()
	lostWinner.Artifacts = lostWinner.Artifacts[1:]
	// Effective artifacts must be sorted by (kind,key).
	unsortedEffective := copyProtocol()
	unsortedEffective.Artifacts[0], unsortedEffective.Artifacts[len(unsortedEffective.Artifacts)-1] =
		unsortedEffective.Artifacts[len(unsortedEffective.Artifacts)-1], unsortedEffective.Artifacts[0]

	for name, p := range map[string]*Protocol{
		"unsorted-shadowed":       unsortedShadow,
		"duplicate-shadowed":      dupShadow,
		"self-shadow":             selfShadow,
		"mismatch-shadowed-by":    mismatchShadow,
		"orphan-shadowed":         orphanShadow,
		"zero-revision-shadowed":  zeroRevShadow,
		"single-id-conflict":      singleConflict,
		"unsorted-conflict-ids":   unsortedIDs,
		"duplicate-conflict-ids":  dupIDs,
		"outsider-resolved-by":    outsiderWinner,
		"orphan-conflict":         orphanConflict,
		"unsorted-conflicts":      unsortedConflicts,
		"duplicate-conflicts":     dupConflicts,
		"bad-conflict-scope":      badScopeConflict,
		"unexplained-ws-conflict": unexplained,
		"project-winner-mismatch": projectMismatch,
		"lost-effective-winner":   lostWinner,
		"unsorted-effective":      unsortedEffective,
	} {
		if err := p.Validate(); err == nil {
			t.Errorf("%s accepted by Validate", name)
		}
		if _, err := p.EncodeBundle(); err == nil {
			t.Errorf("%s accepted by EncodeBundle (bytes were emitted)", name)
		}
	}
	// The base snapshot remains untouched by the mutations.
	if err := base.Validate(); err != nil {
		t.Fatalf("base snapshot corrupted by negative cases: %v", err)
	}
}

// TestProtocolBundleETagStableAcrossGeneratedAt is the negative oracle for
// the content ETag: regenerating the same snapshot at a different time MUST
// NOT change the canonical bytes, ETag or digest.
func TestProtocolBundleETagStableAcrossGeneratedAt(t *testing.T) {
	p1 := smallProtocol("hello")
	p1.GeneratedAt = time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	p2 := smallProtocol("hello")
	p2.GeneratedAt = time.Date(2027, 12, 31, 23, 59, 59, 999999999, time.UTC)
	b1, err := p1.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	b2, err := p2.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	if b1.ETag != b2.ETag || b1.Digest != b2.Digest || string(b1.Canonical) != string(b2.Canonical) {
		t.Fatalf("bundle changed with generated_at:\netag1=%s etag2=%s\n%s\n%s", b1.ETag, b2.ETag, b1.Canonical, b2.Canonical)
	}
	if strings.Contains(string(b1.Canonical), "generated_at") {
		t.Fatalf("generated_at leaked into canonical bundle bytes: %s", b1.Canonical)
	}
	// Different snapshot content still changes the ETag.
	p3 := smallProtocol("different")
	b3, err := p3.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	if b1.ETag == b3.ETag {
		t.Fatal("etag collision across different content")
	}
}

// TestEncodeBundleValidatesBeforeOutput is the negative oracle for bundle
// validation: count, per-artifact and provider-binding validation MUST
// reject before any canonical byte is produced.
func TestEncodeBundleValidatesBeforeOutput(t *testing.T) {
	// 2001 artifacts rejected outright.
	over := smallProtocol("x")
	over.Artifacts = nil
	for i := 0; i < MaxEffectiveArtifacts+1; i++ {
		over.Artifacts = append(over.Artifacts, ProtocolArtifact{
			ArtifactID: fmt.Sprintf("id-%04d", i), Kind: KindSkill,
			Key: fmt.Sprintf("k%04d", i), Title: "T", Revision: 1,
			SourceScope: ScopeProject, ContentType: ContentTypeMarkdown,
			Content: "x", Metadata: map[string]any{},
			Digest: "sha256:" + strings.Repeat("0", 64),
		})
	}
	if _, err := over.EncodeBundle(); !errorHasCode(err, ErrCodeEffectiveLimitExceeded) {
		t.Fatalf("2001-artifact bundle not rejected: %v", err)
	}

	// Invalid per-artifact content (oversize) rejected before output.
	big := smallProtocol(strings.Repeat("a", MaxArtifactContentBytes+1))
	if _, err := big.EncodeBundle(); !errorHasCode(err, ErrCodeContentTooLarge) {
		t.Fatalf("oversize per-artifact content not rejected: %v", err)
	}
	// Empty per-artifact content rejected.
	empty := smallProtocol("")
	if _, err := empty.EncodeBundle(); err == nil {
		t.Fatal("empty per-artifact content accepted in bundle")
	}
	// Invalid metadata rejected.
	badMeta := smallProtocol("x")
	badMeta.Artifacts[0].Metadata = map[string]any{"a": math.Inf(1)}
	if _, err := badMeta.EncodeBundle(); err == nil {
		t.Fatal("non-finite metadata accepted in bundle")
	}
	// Duplicate effective keys rejected.
	dup := smallProtocol("x")
	dup.Artifacts = append(dup.Artifacts, dup.Artifacts[0])
	if _, err := dup.EncodeBundle(); err == nil {
		t.Fatal("duplicate effective keys accepted in bundle")
	}
	// Missing protocol revision rejected.
	noRev := smallProtocol("x")
	noRev.ProtocolRevision = ""
	if _, err := noRev.EncodeBundle(); err == nil {
		t.Fatal("missing protocol revision accepted in bundle")
	}
}

// TestProtocolProviderBindingSanitization covers the sanitized provider
// binding: validation rejects secret-shaped/control-laden values, and the
// binding participates in the bundle hash and size accounting.
func TestProtocolProviderBindingSanitization(t *testing.T) {
	valid := ProviderBinding{
		ProviderID: "openai-compatible-main", Model: "text-embedding-3-small",
		Dimension: 1536, BindingRevision: 2, Generation: 3,
		ReindexState: "complete", Health: ProviderHealthHealthy,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid binding rejected: %v", err)
	}
	for name, mutate := range map[string]func(*ProviderBinding){
		"provider-id-empty":   func(b *ProviderBinding) { b.ProviderID = "" },
		"provider-id-space":   func(b *ProviderBinding) { b.ProviderID = "has space" },
		"provider-id-newline": func(b *ProviderBinding) { b.ProviderID = "id\nwith" },
		"provider-id-utf8":    func(b *ProviderBinding) { b.ProviderID = "café" },
		"provider-id-long":    func(b *ProviderBinding) { b.ProviderID = strings.Repeat("p", 129) },
		"model-empty":         func(b *ProviderBinding) { b.Model = "" },
		"model-control":       func(b *ProviderBinding) { b.Model = "mo\x01del" },
		"dimension-zero":      func(b *ProviderBinding) { b.Dimension = 0 },
		"dimension-negative":  func(b *ProviderBinding) { b.Dimension = -1 },
		"binding-rev-zero":    func(b *ProviderBinding) { b.BindingRevision = 0 },
		"generation-zero":     func(b *ProviderBinding) { b.Generation = 0 },
		"reindex-empty":       func(b *ProviderBinding) { b.ReindexState = "" },
		"reindex-control":     func(b *ProviderBinding) { b.ReindexState = "in progress" },
		"health-unknown":      func(b *ProviderBinding) { b.Health = "flaky" },
		"health-empty":        func(b *ProviderBinding) { b.Health = "" },
	} {
		b := valid
		mutate(&b)
		if err := b.Validate(); err == nil {
			t.Errorf("%s mutation accepted", name)
		}
	}
	// Invalid binding poisons the protocol and the bundle.
	p := smallProtocol("x")
	p.ProviderBinding = &valid
	if err := p.Validate(); err != nil {
		t.Fatalf("protocol with valid binding rejected: %v", err)
	}
	bad := *p // value copy: must not alias p's binding pointer
	bad.ProviderBinding = &ProviderBinding{
		ProviderID: "p", Model: "m", Dimension: 8, BindingRevision: 1,
		Generation: 1, ReindexState: "idle", Health: "flaky",
	}
	if err := bad.Validate(); err == nil {
		t.Fatal("protocol with invalid binding accepted")
	}
	if _, err := bad.EncodeBundle(); err == nil {
		t.Fatal("bundle with unsanitized binding accepted")
	}
	// p still carries the valid binding.
	if err := p.Validate(); err != nil {
		t.Fatalf("p corrupted by bad copy: %v", err)
	}
	// The binding participates in the bundle hash (different binding =>
	// different ETag) and in size accounting (bytes count toward 4 MiB).
	bNone, err := smallProtocol("x").EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	bBound, err := p.EncodeBundle()
	if err != nil {
		t.Fatal(err)
	}
	if bNone.ETag == bBound.ETag || bNone.BytesLen == bBound.BytesLen {
		t.Fatalf("provider binding not hashed/counted: etags %s vs %s, bytes %d vs %d",
			bNone.ETag, bBound.ETag, bNone.BytesLen, bBound.BytesLen)
	}
	if !strings.Contains(string(bBound.Canonical), `"provider_binding"`) {
		t.Fatalf("provider_binding missing from bundle bytes: %s", bBound.Canonical)
	}
	if strings.Contains(string(bNone.Canonical), `"provider_binding":{`) {
		t.Fatalf("nil binding encoded as object: %s", bNone.Canonical)
	}
}
