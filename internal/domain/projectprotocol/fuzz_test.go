package projectprotocol

import (
	"bytes"
	"strconv"
	"testing"
)

// Native Go fuzz targets. In normal `go test` runs the seed corpus executes
// as unit tests; bounded fuzzing runs during verification with -fuzztime.

func FuzzCanonicalizeMetadata(f *testing.F) {
	seeds := []string{
		`{}`,
		`{"a":1}`,
		`{"a":1,"a":2}`,
		`{"z":1,"a":{"b":[1,2,{"c":null}]}}`,
		`{"k":"clé"}`,
		"{\"ctrl\":\"\x01\x02\"}",
		`{"n":1e400}`,
		`{"n":-0}`,
		`{"s":"\ud800"}`,
		`{"s":"\ud800\udc00"}`,
		`{"s":"\\ud800"}`,
		`{"s":"\\ud800\\udc00"}`,
		`{"s":"\\\ud800"}`,
		`{"s":"\\ud800\ud83d\ude00"}`,
		`{"\\ud800":1}`,
		`[1,2]`,
		`{"a":1} trailing`,
		"\xff\xfe",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		canonical, err := CanonicalizeMetadata(raw)
		if err != nil {
			return // every rejection is a valid outcome; no panic is the invariant
		}
		// Invariants on success:
		if len(canonical) > MaxArtifactMetadataBytes {
			t.Fatalf("canonical exceeds limit: %d", len(canonical))
		}
		if !bytes.HasPrefix(canonical, []byte("{")) {
			t.Fatalf("canonical root not an object: %s", canonical)
		}
		// Fixpoint: decode(encode(x)) re-encodes identically.
		decoded, err := DecodeCanonicalRaw(canonical)
		if err != nil {
			t.Fatalf("canonical not decodable: %v", err)
		}
		again, err := CanonicalJSON(decoded)
		if err != nil {
			t.Fatalf("canonical not re-encodable: %v", err)
		}
		if !bytes.Equal(canonical, again) {
			t.Fatalf("not a fixpoint:\n%s\n%s", canonical, again)
		}
	})
}

func FuzzValidateKey(f *testing.F) {
	seeds := []string{"a", "a/b", "A", "-x", "a.b_c-d", "", "9", "a b", "aé"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, key string) {
		err := ValidateKey(key)
		// Oracle: length and charset rules must agree with the validator's
		// boolean outcome; no panic on any input.
		runes := []rune(key)
		validLen := len(runes) >= 1 && len(runes) <= MaxKeyRunes
		if err != nil && validLen {
			// If rejected purely by charset that's fine; only ensure the
			// empty and oversize cases are always rejected.
			t.Logf("key %q rejected (charset)", key)
		}
		if err == nil && !validLen {
			t.Fatalf("key %q accepted despite invalid length", key)
		}
	})
}

func FuzzResolve(f *testing.F) {
	f.Add([]byte{0x01, 0x61, 0x00, 0x02, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		// Interpret fuzz bytes as candidate tuples: kind(1) key(1) scope(1)
		// precedence(1) revision(1). IDs carry the tuple index so duplicate
		// semantics are exercised deterministically by the property suite.
		var candidates []ResolvableArtifact
		for i := 0; i+5 <= len(data); i += 5 {
			var kind Kind
			switch data[i] % 3 {
			case 0:
				kind = KindSkill
			case 1:
				kind = KindRule
			default:
				kind = Kind("other") // intentionally invalid sometimes
			}
			scope := ScopeWorkspaceDefault
			if data[i+2]%2 == 1 {
				scope = ScopeProject
			}
			candidates = append(candidates, ResolvableArtifact{
				ArtifactID: "id-" + strconv.Itoa(i) + "-" + string(rune('a'+data[i]%26)),
				Kind:       kind,
				Key:        "k" + string(rune('a'+data[i+1]%26)),
				Scope:      scope,
				Precedence: int32(data[i+3] % 4),
				Revision:   int64(1 + data[i+4]%5),
				Digest:     "sha256:0000",
			})
		}
		res, err := Resolve(candidates)
		if err != nil {
			return
		}
		if len(res.Effective) > MaxEffectiveArtifacts {
			t.Fatalf("cap exceeded: %d", len(res.Effective))
		}
		for i := 1; i < len(res.Effective); i++ {
			prev, cur := res.Effective[i-1], res.Effective[i]
			if prev.Kind > cur.Kind || (prev.Kind == cur.Kind && prev.Key >= cur.Key) {
				t.Fatalf("effective not sorted at %d", i)
			}
		}
		// Internal validity invariant: every successful Resolve output is
		// directly consumable — the materialized protocol passes the full
		// sorted/unique/crosschecked provenance validation.
		p := buildProtocolFromResolution(res)
		if err := p.Validate(); err != nil {
			t.Fatalf("resolve output not internally valid: %v", err)
		}
	})
}
