package projectprotocol

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Property-based invariants (deterministic PRNG so failures reproduce).
// Run count is bounded to keep the suite sub-second.

const propertyRuns = 120

func TestPropertyCanonicalStableAcrossMapOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var randomValue func(depth int) any
	randomValue = func(depth int) any {
		switch rng.Intn(6) {
		case 0:
			return rng.Intn(1000) - 500
		case 1:
			return rng.Float64()*2 - 1
		case 2:
			return strings.Repeat("w", rng.Intn(8)) + "é"
		case 3:
			return rng.Intn(2) == 0
		case 4:
			return nil
		default:
			if depth >= 2 {
				return "leaf"
			}
			if rng.Intn(2) == 0 {
				n := rng.Intn(4)
				arr := make([]any, n)
				for i := range arr {
					arr[i] = randomValue(depth + 1)
				}
				return arr
			}
			n := rng.Intn(5)
			m := make(map[string]any, n)
			for i := 0; i < n; i++ {
				m[string(rune('a'+rng.Intn(6)))+strings.Repeat("k", rng.Intn(3))] = randomValue(depth + 1)
			}
			return m
		}
	}
	for run := 0; run < propertyRuns; run++ {
		// Build one map, then produce a shuffled-key reconstruction of the
		// same data (different insertion order, same content).
		n := 1 + rng.Intn(8)
		original := make(map[string]any, n)
		keys := make([]string, 0, n)
		for i := 0; i < n; i++ {
			k := strings.Repeat(string(rune('a'+rng.Intn(26))), 1+rng.Intn(3)) + strings.Repeat("é", rng.Intn(2))
			if _, exists := original[k]; exists {
				continue
			}
			keys = append(keys, k)
			original[k] = randomValue(0)
		}
		shuffled := make(map[string]any, len(original))
		order := append([]string{}, keys...)
		rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		for _, k := range order {
			shuffled[k] = original[k]
		}
		c1, err1 := CanonicalJSON(original)
		c2, err2 := CanonicalJSON(shuffled)
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("run %d: error asymmetry %v vs %v", run, err1, err2)
		}
		if err1 != nil {
			continue
		}
		if !bytes.Equal(c1, c2) {
			t.Fatalf("run %d: canonical bytes differ across key order:\n%s\n%s", run, c1, c2)
		}
		// Metadata path enforces the same stability under its limit.
		if len(c1) <= MaxArtifactMetadataBytes {
			d1, err := CanonicalizeMetadataMap(original)
			if err != nil {
				t.Fatalf("run %d: metadata map rejected: %v", run, err)
			}
			d2, err := CanonicalizeMetadataMap(shuffled)
			if err != nil {
				t.Fatalf("run %d: shuffled metadata rejected: %v", run, err)
			}
			if !bytes.Equal(d1, d2) {
				t.Fatalf("run %d: metadata canonical differs", run)
			}
		}
	}
}

func TestPropertyCanonicalRoundtripFixpoint(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for run := 0; run < propertyRuns; run++ {
		m := map[string]any{
			"n": rng.Intn(100000),
			"f": rng.Float64() * 1e6,
			"s": "text" + strings.Repeat("é", rng.Intn(4)),
			"b": rng.Intn(2) == 0,
			"l": []any{rng.Intn(100), nil, "x"},
		}
		first, err := CanonicalizeMetadataMap(m)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		decoded, err := DecodeCanonicalRaw(first)
		if err != nil {
			t.Fatalf("run %d: decode: %v", run, err)
		}
		second, err := CanonicalJSON(decoded)
		if err != nil {
			t.Fatalf("run %d: re-encode: %v", run, err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("run %d: not a fixpoint:\n%s\n%s", run, first, second)
		}
		obj, ok := decoded.(map[string]any)
		if !ok || len(obj) != 5 {
			t.Fatalf("run %d: roundtrip lost shape", run)
		}
	}
}

var keyRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]*$`)

func TestPropertyKeyValidatorMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	runes := []rune{'a', 'Z', '9', '.', '_', '/', '-', ' ', 'é', 0x00}
	for run := 0; run < propertyRuns; run++ {
		n := 1 + rng.Intn(6)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteRune(runes[rng.Intn(len(runes))])
		}
		key := sb.String()
		err := ValidateKey(key)
		oracle := keyRegex.MatchString(key) && len([]rune(key)) <= MaxKeyRunes
		if (err == nil) != oracle {
			t.Fatalf("run %d key=%q: validator=%v oracle=%v", run, key, err, oracle)
		}
	}
}

func TestPropertyResolverDeterministicAndCapped(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	for run := 0; run < propertyRuns; run++ {
		n := rng.Intn(40)
		candidates := make([]ResolvableArtifact, 0, n)
		for i := 0; i < n; i++ {
			candidates = append(candidates, ResolvableArtifact{
				ArtifactID: fmtID(i),
				Kind:       []Kind{KindSkill, KindRule}[rng.Intn(2)],
				Key:        "k" + string(rune('a'+rng.Intn(6))),
				Scope:      []Scope{ScopeProject, ScopeWorkspaceDefault}[rng.Intn(2)],
				Precedence: int32(rng.Intn(3)),
				Revision:   int64(1 + rng.Intn(5)),
				Digest:     "sha256:" + strings.Repeat("0", 64),
			})
		}
		a, errA := Resolve(candidates)
		// Shuffle and re-resolve.
		rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
		b, errB := Resolve(candidates)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("run %d: error asymmetry", run)
		}
		if errA != nil {
			continue
		}
		if len(a.Effective) != len(b.Effective) {
			t.Fatalf("run %d: effective size changed under shuffle", run)
		}
		for i := range a.Effective {
			if a.Effective[i] != b.Effective[i] {
				t.Fatalf("run %d: effective[%d] changed under shuffle", run, i)
			}
		}
		// Invariants: sorted, unique, capped.
		if len(a.Effective) > MaxEffectiveArtifacts {
			t.Fatalf("run %d: cap exceeded", run)
		}
		seen := map[artifactKey]struct{}{}
		for i, e := range a.Effective {
			ak := artifactKey{kind: e.Kind, key: e.Key}
			if _, dup := seen[ak]; dup {
				t.Fatalf("run %d: duplicate effective key", run)
			}
			seen[ak] = struct{}{}
			if i > 0 {
				prev := a.Effective[i-1]
				if prev.Kind > e.Kind || (prev.Kind == e.Kind && prev.Key >= e.Key) {
					t.Fatalf("run %d: effective not sorted by (kind,key)", run)
				}
			}
		}
	}
}

func TestPropertyLimitWriterNeverExceedsLimit(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for run := 0; run < propertyRuns; run++ {
		limit := int64(rng.Intn(64))
		w := NewLimitWriter(limit)
		var accepted []byte
		for write := 0; write < 8; write++ {
			p := make([]byte, rng.Intn(24))
			rng.Read(p)
			n, err := w.Write(p)
			if err != nil {
				if n != 0 || w.Bytes() != nil {
					t.Fatalf("run %d: failed write exposed partial output", run)
				}
				break
			}
			accepted = append(accepted, p...)
			if w.Count() > limit {
				t.Fatalf("run %d: count %d > limit %d", run, w.Count(), limit)
			}
		}
		if !w.Failed() {
			if !bytes.Equal(w.Bytes(), accepted) {
				t.Fatalf("run %d: accepted bytes mismatch", run)
			}
			if w.Count() != int64(len(accepted)) {
				t.Fatalf("run %d: count mismatch", run)
			}
		}
	}
}

func TestPropertyDigestStableUnderSortedKeyRebuild(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	for run := 0; run < propertyRuns; run++ {
		n := rng.Intn(6) + 1
		m := make(map[string]any, n)
		for i := 0; i < n; i++ {
			m[string(rune('A'+rng.Intn(20)))] = rng.Intn(1000)
		}
		d1, b1, err := CanonicalDigest(m)
		if err != nil {
			t.Fatal(err)
		}
		// Rebuild via sorted key order (canonical traversal).
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ordered := make(map[string]any, len(m))
		for _, k := range keys {
			ordered[k] = m[k]
		}
		d2, b2, err := CanonicalDigest(ordered)
		if err != nil {
			t.Fatal(err)
		}
		if d1 != d2 || !bytes.Equal(b1, b2) {
			t.Fatalf("run %d: digest unstable", run)
		}
	}
}

// TestPropertyResolutionFullyDeterministic extends the shuffle property to
// the WHOLE resolution output: effective, shadowed and conflicts must be
// byte-identical (not just same-sized) under input permutation.
func TestPropertyResolutionFullyDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for run := 0; run < propertyRuns; run++ {
		n := rng.Intn(30)
		candidates := make([]ResolvableArtifact, 0, n)
		for i := 0; i < n; i++ {
			candidates = append(candidates, ResolvableArtifact{
				ArtifactID: fmtID(i),
				Kind:       []Kind{KindSkill, KindRule}[rng.Intn(2)],
				Key:        "k" + string(rune('a'+rng.Intn(5))),
				Scope:      []Scope{ScopeProject, ScopeWorkspaceDefault}[rng.Intn(2)],
				Precedence: int32(rng.Intn(3)),
				Revision:   int64(1 + rng.Intn(5)),
				Digest:     "sha256:" + strings.Repeat("0", 64),
			})
		}
		a, errA := Resolve(candidates)
		rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
		b, errB := Resolve(candidates)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("run %d: error asymmetry %v vs %v", run, errA, errB)
		}
		if errA != nil {
			continue
		}
		if !reflect.DeepEqual(a.Effective, b.Effective) {
			t.Fatalf("run %d: effective changed under shuffle", run)
		}
		if !reflect.DeepEqual(a.Shadowed, b.Shadowed) {
			t.Fatalf("run %d: shadowed changed under shuffle", run)
		}
		if !reflect.DeepEqual(a.Conflicts, b.Conflicts) {
			t.Fatalf("run %d: conflicts changed under shuffle:\n%+v\n%+v", run, a.Conflicts, b.Conflicts)
		}
		// Conflicts are sorted by (kind, key, scope).
		for i := 1; i < len(a.Conflicts); i++ {
			prev, cur := a.Conflicts[i-1], a.Conflicts[i]
			if prev.Kind > cur.Kind ||
				(prev.Kind == cur.Kind && prev.Key > cur.Key) ||
				(prev.Kind == cur.Kind && prev.Key == cur.Key && prev.Scope >= cur.Scope) {
				t.Fatalf("run %d: conflicts not sorted at %d", run, i)
			}
		}
	}
}

// TestPropertySurrogateScanMatchesStdlibOracle generates random JSON string
// bodies from escape-token soup and compares against the stdlib decoder
// oracle: encoding/json maps unpaired surrogate escapes to U+FFFD. The
// corpus contains no literal U+FFFD, so whenever the stdlib-decoded value
// contains U+FFFD our scanner MUST reject; whenever it does not, our
// decoder MUST accept with a canonical fixpoint.
func TestPropertySurrogateScanMatchesStdlibOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	tokens := []string{
		`\`, `\\`, `\\\`, `\u`, `\uD800`, `\udc00`, `\uD83D`, `\ude00`,
		`\u0041`, `\n`, `\"`, `/`, `uD800`, `D83D`, `x`, `0`, `é`, `😀`,
	}
	for run := 0; run < propertyRuns; run++ {
		n := rng.Intn(10)
		var body strings.Builder
		for i := 0; i < n; i++ {
			body.WriteString(tokens[rng.Intn(len(tokens))])
		}
		raw := []byte(`{"k":"` + body.String() + `"}`)
		var std map[string]any
		stdErr := json.Unmarshal(raw, &std)
		got, ourErr := CanonicalizeMetadata(raw)
		if stdErr != nil {
			// Malformed JSON must be rejected by us too.
			if ourErr == nil {
				t.Fatalf("run %d: stdlib rejected %q but we accepted: %s", run, raw, got)
			}
			continue
		}
		decoded := std["k"].(string)
		if strings.ContainsRune(decoded, 0xFFFD) {
			if ourErr == nil {
				t.Fatalf("run %d: stdlib decoded U+FFFD from %q but we accepted: %s", run, raw, got)
			}
			continue
		}
		if ourErr != nil {
			t.Fatalf("run %d: we rejected FFFD-free input %q: %v", run, raw, ourErr)
		}
		// Fixpoint on acceptance.
		v, err := DecodeCanonicalRaw(got)
		if err != nil {
			t.Fatalf("run %d: decode canonical: %v", run, err)
		}
		again, err := CanonicalJSON(v)
		if err != nil {
			t.Fatalf("run %d: re-encode: %v", run, err)
		}
		if !bytes.Equal(got, again) {
			t.Fatalf("run %d: not a fixpoint:\n%s\n%s", run, got, again)
		}
	}
}

// TestPropertyEscapedGoStringRoundtrip generates arbitrary valid-UTF-8 Go
// strings (never containing U+FFFD), JSON-encodes them with the stdlib and
// requires our canonical path to accept and roundtrip the value.
func TestPropertyEscapedGoStringRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	runes := []rune{'a', 'Z', '0', '"', '\\', '/', '\n', '\t', 0x00, 0x1f, 'é', '😀', '中', 0x7f, ' '}
	for run := 0; run < propertyRuns; run++ {
		n := rng.Intn(24)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteRune(runes[rng.Intn(len(runes))])
		}
		s := sb.String()
		enc, err := json.Marshal(map[string]any{"k": s})
		if err != nil {
			t.Fatal(err)
		}
		got, err := CanonicalizeMetadata(enc)
		if err != nil {
			t.Fatalf("run %d: stdlib-encoded string rejected: %q: %v", run, s, err)
		}
		v, err := DecodeCanonicalRaw(got)
		if err != nil {
			t.Fatalf("run %d: decode: %v", run, err)
		}
		if v.(map[string]any)["k"].(string) != s {
			t.Fatalf("run %d: roundtrip changed value: %q", run, s)
		}
	}
}

// TestPropertyArtifactETagDerivation: for randomly generated valid artifact
// states the canonical ETag is a deterministic function of the covered state
// (independent of rebuilds and timestamps), changes under any covered-field
// flip, verifies after assignment, and stops verifying after mutation.
func TestPropertyArtifactETagDerivation(t *testing.T) {
	rng := rand.New(rand.NewSource(10))
	randomArtifact := func() Artifact {
		a := Artifact{
			ID:      "id-" + fmtID(rng.Intn(1000)),
			Project: []string{"", "proj-" + fmtID(rng.Intn(50))}[rng.Intn(2)],
			Kind:    []Kind{KindSkill, KindRule}[rng.Intn(2)],
			Key:     "k/" + fmtID(rng.Intn(100)),
			Title:   "T" + fmtID(rng.Intn(100)),
			Scope:   ScopeProject, Status: StatusActive,
			Precedence:     int32(rng.Intn(10)),
			LatestRevision: int64(1 + rng.Intn(9)),
		}
		if a.Project == "" {
			a.Scope = ScopeWorkspaceDefault
		}
		if rng.Intn(2) == 0 {
			r := int64(1 + rng.Intn(int(a.LatestRevision)))
			a.ActiveRevision = &r
		}
		a.ActivationRevision = int64(rng.Intn(5))
		return withCanonicalETag(a)
	}
	flipCovered := func(a *Artifact) string {
		switch rng.Intn(7) {
		case 0:
			a.Status = StatusDraft
			return "status"
		case 1:
			a.Title += "x"
			return "title"
		case 2:
			a.LatestRevision++
			return "latest_revision"
		case 3:
			a.Precedence++
			return "precedence"
		case 4:
			a.ActivationRevision++
			return "activation_revision"
		case 5:
			if a.ActiveRevision != nil {
				a.ActiveRevision = nil
			} else {
				r := a.LatestRevision
				a.ActiveRevision = &r
			}
			return "active_revision"
		default:
			now := timeNowUTC()
			a.Status = StatusDeleted
			a.DeletedAt = &now
			a.DeletedBy = "u1"
			a.DeleteReason = "r"
			return "delete_provenance"
		}
	}
	for run := 0; run < propertyRuns; run++ {
		a := randomArtifact()
		first, err := a.CanonicalETag()
		if err != nil {
			t.Fatalf("run %d: derive: %v", run, err)
		}
		rebuilt, err := randomArtifactETagRebuild(a)
		if err != nil {
			t.Fatalf("run %d: rebuild: %v", run, err)
		}
		if first != rebuilt {
			t.Fatalf("run %d: derivation not a pure function of state", run)
		}
		if !a.VerifyETag() {
			t.Fatalf("run %d: assigned etag does not verify", run)
		}
		mutated := a
		field := flipCovered(&mutated)
		if mutated.VerifyETag() {
			t.Fatalf("run %d: flipping %s kept the etag valid", run, field)
		}
		flipped, err := mutated.CanonicalETag()
		if err != nil {
			t.Fatalf("run %d: derive mutated: %v", run, err)
		}
		if flipped == first {
			t.Fatalf("run %d: flipping %s collided etags", run, field)
		}
	}
}

// randomArtifactETagRebuild re-derives the etag of a through a fresh value
// copy to prove the derivation reads state, not cached identity.
func randomArtifactETagRebuild(a Artifact) (string, error) {
	fresh := a
	return fresh.CanonicalETag()
}

// TestPropertyBundleProvenanceCrosscheck: a Protocol assembled from a random
// Resolve output always validates, and every targeted provenance corruption
// (winner rewrite, id-list corruption, orphaned provenance, order shuffling)
// is rejected by Validate before any bundle byte is emitted.
func TestPropertyBundleProvenanceCrosscheck(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for run := 0; run < propertyRuns; run++ {
		n := rng.Intn(30)
		candidates := make([]ResolvableArtifact, 0, n)
		for i := 0; i < n; i++ {
			candidates = append(candidates, ResolvableArtifact{
				ArtifactID: fmtID(1000 + i),
				Kind:       []Kind{KindSkill, KindRule}[rng.Intn(2)],
				Key:        "k" + string(rune('a'+rune(rng.Intn(5)))),
				Scope:      []Scope{ScopeProject, ScopeWorkspaceDefault}[rng.Intn(2)],
				Precedence: int32(rng.Intn(3)),
				Revision:   int64(1 + rng.Intn(5)),
				Digest:     "sha256:" + strings.Repeat("0", 64),
			})
		}
		res, err := Resolve(candidates)
		if err != nil {
			continue // invalid random kind/scope combos are skipped
		}
		p := buildProtocolFromResolution(res)
		if err := p.Validate(); err != nil {
			t.Fatalf("run %d: valid resolution output rejected: %v", run, err)
		}
		if _, err := p.EncodeBundle(); err != nil {
			t.Fatalf("run %d: valid resolution output not encodable: %v", run, err)
		}

		corrupt := *p
		corrupt.Artifacts = append([]ProtocolArtifact{}, p.Artifacts...)
		corrupt.Shadowed = append([]ShadowedArtifact{}, p.Shadowed...)
		corrupt.Conflicts = append([]KeyConflict{}, p.Conflicts...)
		for i := range corrupt.Conflicts {
			corrupt.Conflicts[i].ArtifactIDs = append([]string{}, p.Conflicts[i].ArtifactIDs...)
		}
		mode := rng.Intn(4)
		var corrupted bool
		switch {
		case mode == 0 && len(corrupt.Shadowed) > 0:
			i := rng.Intn(len(corrupt.Shadowed))
			corrupt.Shadowed[i].ShadowedByID = "corrupt-id"
			corrupted = true
		case mode == 1 && len(corrupt.Conflicts) > 0:
			i := rng.Intn(len(corrupt.Conflicts))
			corrupt.Conflicts[i].ResolvedByID = "corrupt-id"
			corrupted = true
		case mode == 2 && len(corrupt.Artifacts) > 0 && (len(corrupt.Shadowed) > 0 || len(corrupt.Conflicts) > 0):
			// Remove the effective artifact whose (kind,key) is referenced
			// by some provenance entry, orphaning it. Prefer shadowed
			// references, else conflict references.
			var kind Kind
			var key string
			if len(corrupt.Shadowed) > 0 {
				ref := corrupt.Shadowed[rng.Intn(len(corrupt.Shadowed))]
				kind, key = ref.Kind, ref.Key
			} else {
				ref := corrupt.Conflicts[rng.Intn(len(corrupt.Conflicts))]
				kind, key = ref.Kind, ref.Key
			}
			for j, art := range corrupt.Artifacts {
				if art.Kind == kind && art.Key == key {
					corrupt.Artifacts = append(corrupt.Artifacts[:j], corrupt.Artifacts[j+1:]...)
					break
				}
			}
			corrupted = true
		case mode == 3 && len(corrupt.Shadowed) > 1:
			i := rng.Intn(len(corrupt.Shadowed) - 1)
			corrupt.Shadowed[i], corrupt.Shadowed[i+1] = corrupt.Shadowed[i+1], corrupt.Shadowed[i]
			corrupted = true
		}
		if !corrupted {
			continue
		}
		if err := corrupt.Validate(); err == nil {
			t.Fatalf("run %d mode %d: corrupted provenance accepted", run, mode)
		}
		if _, err := corrupt.EncodeBundle(); err == nil {
			t.Fatalf("run %d mode %d: corrupted provenance encoded", run, mode)
		}
	}
}

// TestPropertyResolveDuplicateIDIntegrity: exact duplicate candidate rows
// never change the resolution outcome; any same-id field mutation is
// rejected; and every successful Resolve output is internally valid — the
// materialized protocol passes full provenance validation.
func TestPropertyResolveDuplicateIDIntegrity(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	for run := 0; run < propertyRuns; run++ {
		n := 1 + rng.Intn(20)
		candidates := make([]ResolvableArtifact, 0, n)
		for i := 0; i < n; i++ {
			candidates = append(candidates, ResolvableArtifact{
				ArtifactID: fmtID(i),
				Kind:       []Kind{KindSkill, KindRule}[rng.Intn(2)],
				Key:        "k" + string(rune('a'+rng.Intn(5))),
				Scope:      []Scope{ScopeProject, ScopeWorkspaceDefault}[rng.Intn(2)],
				Precedence: int32(rng.Intn(3)),
				Revision:   int64(1 + rng.Intn(5)),
				Digest:     "sha256:" + strings.Repeat("0", 64),
			})
		}
		base, errBase := Resolve(candidates)

		// Inject 1..3 exact duplicates of random candidates, shuffled in.
		withDups := append([]ResolvableArtifact{}, candidates...)
		for d := 0; d < 1+rng.Intn(3); d++ {
			withDups = append(withDups, candidates[rng.Intn(len(candidates))])
		}
		rng.Shuffle(len(withDups), func(i, j int) { withDups[i], withDups[j] = withDups[j], withDups[i] })
		dup, errDup := Resolve(withDups)
		if (errBase == nil) != (errDup == nil) {
			t.Fatalf("run %d: error asymmetry %v vs %v", run, errBase, errDup)
		}
		if errBase != nil {
			continue
		}
		if !reflect.DeepEqual(base.Effective, dup.Effective) ||
			!reflect.DeepEqual(base.Shadowed, dup.Shadowed) ||
			!reflect.DeepEqual(base.Conflicts, dup.Conflicts) {
			t.Fatalf("run %d: exact duplicates changed the outcome", run)
		}
		// Internal validity: Resolve output always survives full protocol
		// provenance validation (sorted, unique, crosschecked).
		p := buildProtocolFromResolution(dup)
		if err := p.Validate(); err != nil {
			t.Fatalf("run %d: resolve output not internally valid: %v", run, err)
		}

		// Any single-field mutation of a repeated id is rejected.
		orig := candidates[rng.Intn(len(candidates))]
		mut := orig
		switch rng.Intn(6) {
		case 0:
			mut.Key = orig.Key + "x"
		case 1:
			if orig.Kind == KindSkill {
				mut.Kind = KindRule
			} else {
				mut.Kind = KindSkill
			}
		case 2:
			if orig.Scope == ScopeProject {
				mut.Scope = ScopeWorkspaceDefault
			} else {
				mut.Scope = ScopeProject
			}
		case 3:
			mut.Precedence++
		case 4:
			mut.Revision++
		default:
			mut.Digest = "sha256:" + strings.Repeat("f", 64)
		}
		injected := append(append([]ResolvableArtifact{}, candidates...), mut)
		if _, err := Resolve(injected); err == nil {
			t.Fatalf("run %d: inconsistent duplicate id accepted", run)
		}
	}
}

func fmtID(i int) string {
	const digits = "0123456789abcdef"
	if i == 0 {
		return "id-0"
	}
	var sb strings.Builder
	sb.WriteString("id-")
	for i > 0 {
		sb.WriteByte(digits[i%16])
		i /= 16
	}
	return sb.String()
}
