package projectprotocol

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestCanonicalizeMetadataExactLimit(t *testing.T) {
	// Canonical form {"k":"<pad>"} has fixed overhead; pad to exactly 64KiB.
	overhead := len(`{"k":""}`)
	pad := MaxArtifactMetadataBytes - overhead
	raw := []byte(`{"k":"` + strings.Repeat("x", pad) + `"}`)
	got, err := CanonicalizeMetadata(raw)
	if err != nil {
		t.Fatalf("exactly 64KiB canonical metadata rejected: %v", err)
	}
	if len(got) != MaxArtifactMetadataBytes {
		t.Fatalf("canonical length=%d want %d", len(got), MaxArtifactMetadataBytes)
	}
	// +1 canonical byte must reject without truncation.
	rawPlus := []byte(`{"k":"` + strings.Repeat("x", pad+1) + `"}`)
	if _, err := CanonicalizeMetadata(rawPlus); !errorHasCode(err, ErrCodeMetadataTooLarge) {
		t.Fatalf("64KiB+1 accepted or wrong code: %v", err)
	}
}

func TestCanonicalizeMetadataEquivalence(t *testing.T) {
	// Whitespace and key-order changes canonicalize identically.
	a := []byte(`{"alpha":1,"beta":"béta","gamma":[1,2,{"z":true,"a":null}]}`)
	b := []byte(`{ "gamma" : [ 1 , 2 , { "a" : null , "z" : true } ] , "beta" : "béta" , "alpha" : 1 }`)
	ca, err := CanonicalizeMetadata(a)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	cb, err := CanonicalizeMetadata(b)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if !bytes.Equal(ca, cb) {
		t.Fatalf("canonical forms differ:\n%s\n%s", ca, cb)
	}
	// Roundtrip: decoding the canonical form and re-encoding is a fixpoint.
	v, err := DecodeCanonicalRaw(ca)
	if err != nil {
		t.Fatalf("decode canonical: %v", err)
	}
	again, err := CanonicalJSON(v)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if !bytes.Equal(ca, again) {
		t.Fatalf("canonical not a fixpoint:\n%s\n%s", ca, again)
	}
}

func TestCanonicalizeMetadataDuplicateKeys(t *testing.T) {
	for _, raw := range []string{
		`{"a":1,"a":2}`,
		`{ "a" : 1 , "a" : 1 }`,
		`{"outer":{"nested":1,"nested":2}}`,
		`{"a":1,"b":{"x":1,"x":2},"c":3}`,
		`{"a":1,"a":1,"a":1}`,
	} {
		if _, err := CanonicalizeMetadata([]byte(raw)); !errorHasCode(err, ErrCodeDuplicateMetadataKey) {
			t.Errorf("duplicate keys in %s: got %v want duplicate_metadata_key", raw, err)
		}
	}
}

func TestCanonicalizeMetadataInvalidUTF8AndSurrogates(t *testing.T) {
	if _, err := CanonicalizeMetadata([]byte("{\"a\":\"\xff\"}")); !errorHasCode(err, ErrCodeInvalidUTF8) {
		t.Errorf("invalid UTF-8 metadata: %v", err)
	}
	// CESU-8 encoding of a lone high surrogate is invalid UTF-8.
	loneSurrogate := string([]byte{0xED, 0xA0, 0x80})
	if _, err := CanonicalizeMetadata([]byte("{\"a\":\"" + loneSurrogate + "\"}")); !errorHasCode(err, ErrCodeInvalidUTF8) {
		t.Errorf("literal lone surrogate: %v", err)
	}
	if _, err := CanonicalizeMetadata([]byte(`{"a":"\ud800"}`)); err == nil {
		t.Error("lone high surrogate escape accepted")
	}
	if _, err := CanonicalizeMetadata([]byte(`{"a":"\udc00"}`)); err == nil {
		t.Error("lone low surrogate escape accepted")
	}
	got, err := CanonicalizeMetadata([]byte(`{"a":"\ud800\udc00"}`))
	if err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
	// The pair decodes to U+10000 and is emitted as literal UTF-8.
	if !bytes.Contains(got, []byte("𐀀")) {
		t.Fatalf("surrogate pair not canonicalized to literal rune: %s", got)
	}
}

// TestEscapedBackslashSurrogateLiteralAccepted is the regression oracle for
// the raw surrogate pre-scan: valid JSON whose TEXT contains an escaped
// backslash followed by surrogate-looking hex (e.g. the six characters
// \uD800) MUST be accepted. Only genuine unpaired \uXXXX escapes are
// rejected.
func TestEscapedBackslashSurrogateLiteralAccepted(t *testing.T) {
	valid := []string{
		`{"k":"\\uD800"}`,        // literal text \uD800
		`{"k":"\\udFFF"}`,        // lowercase hex literal
		`{"k":"\\\\uD800"}`,      // two escaped backslashes + text uD800
		`{"k":"a\\uD800b"}`,      // embedded in longer text
		`{"\\uD800":1}`,          // literal text as object key
		`{"k":"\\uD800\\uDC00"}`, // two literal texts, not a pair
		`{"k":"\\ude00"}`,
	}
	for _, raw := range valid {
		got, err := CanonicalizeMetadata([]byte(raw))
		if err != nil {
			t.Errorf("valid escaped-backslash surrogate text rejected: %s: %v", raw, err)
			continue
		}
		// The canonical form must preserve the literal text: backslash
		// re-escaped as \\ followed by the plain uXXXX characters.
		if raw == `{"k":"\\uD800"}` && !bytes.Contains(got, []byte(`\\uD800`)) {
			t.Errorf("literal text not preserved canonically: %s", got)
		}
	}
	invalid := []string{
		`{"k":"\uD800"}`,        // genuine lone high surrogate escape
		`{"k":"\uDC00"}`,        // genuine lone low surrogate escape
		`{"k":"\uD800x"}`,       // high followed by non-escape
		`{"k":"\uD800\\udc00"}`, // high followed by escaped-backslash text, not an escape pair
		`{"k":"\\\uD800"}`,      // escaped backslash + genuine lone high escape
		`{"k":"\uD83D"}`,        // BMP emoji high half alone
	}
	for _, raw := range invalid {
		if _, err := CanonicalizeMetadata([]byte(raw)); err == nil {
			t.Errorf("genuine unpaired surrogate escape accepted: %s", raw)
		}
	}
	// A genuine valid pair still round-trips to the composed rune.
	got, err := CanonicalizeMetadata([]byte(`{"k":"\ud83d\ude00"}`))
	if err != nil {
		t.Fatalf("valid surrogate pair rejected: %v", err)
	}
	if !bytes.Contains(got, []byte("😀")) {
		t.Fatalf("surrogate pair not canonicalized to literal rune: %s", got)
	}
	// Mixed: escaped-backslash text BEFORE a genuine valid pair.
	mixed, err := CanonicalizeMetadata([]byte(`{"k":"\\uD800\ud83d\ude00"}`))
	if err != nil {
		t.Fatalf("mixed literal text + valid pair rejected: %v", err)
	}
	if !bytes.Contains(mixed, []byte("😀")) || !bytes.Contains(mixed, []byte(`\\uD800`)) {
		t.Fatalf("mixed canonical wrong: %s", mixed)
	}
	// Mixed: escaped-backslash text BEFORE a genuine lone escape.
	if _, err := CanonicalizeMetadata([]byte(`{"k":"\\uD800\ud800"}`)); err == nil {
		t.Fatal("lone escape after literal text accepted")
	}
}

func TestCanonicalizeMetadataNonFiniteNumbers(t *testing.T) {
	if _, err := CanonicalizeMetadataMap(map[string]any{"a": math.Inf(1)}); err == nil {
		t.Error("+Inf accepted")
	}
	if _, err := CanonicalizeMetadataMap(map[string]any{"a": math.NaN()}); err == nil {
		t.Error("NaN accepted")
	}
	if _, err := CanonicalizeMetadataMap(map[string]any{"a": math.Inf(-1)}); err == nil {
		t.Error("-Inf accepted")
	}
	// A literal that overflows float64 is non-finite and must be rejected.
	if _, err := CanonicalizeMetadata([]byte(`{"a":1e400}`)); err == nil {
		t.Error("1e400 accepted")
	}
	// Same for the encode path via json.Number.
	if _, err := CanonicalJSON(map[string]any{"a": json.Number("1e400")}); err == nil {
		t.Error("json.Number 1e400 accepted")
	}
}

func TestCanonicalizeMetadataEscapedControls(t *testing.T) {
	raw := []byte("{\"a\":\"\\u0001\\u0002\\u0007\\b\\n\\u001f\"}")
	got, err := CanonicalizeMetadata(raw)
	if err != nil {
		t.Fatalf("escaped controls rejected: %v", err)
	}
	// Control characters re-appear as minimal \u00xx escapes in canonical form.
	want := []byte("{\"a\":\"\\u0001\\u0002\\u0007\\b\\n\\u001f\"}")
	if !bytes.Equal(got, want) {
		t.Fatalf("escaped controls canonical mismatch:\n got %s\nwant %s", got, want)
	}
	// Equivalent map-built value canonicalizes identically.
	m := map[string]any{"a": "\x01\x02\a\b\n\x1f"}
	gotMap, err := CanonicalizeMetadataMap(m)
	if err != nil {
		t.Fatalf("map path: %v", err)
	}
	if !bytes.Equal(got, gotMap) {
		t.Fatalf("raw and map paths disagree:\n raw  %s\n map  %s", got, gotMap)
	}
}

func TestCanonicalizeMetadataMultibyteStability(t *testing.T) {
	rawEscaped := []byte(`{"k":"clé"}`)
	rawLiteral := []byte("{\"k\":\"clé\"}")
	a, err := CanonicalizeMetadata(rawEscaped)
	if err != nil {
		t.Fatalf("escaped: %v", err)
	}
	b, err := CanonicalizeMetadata(rawLiteral)
	if err != nil {
		t.Fatalf("literal: %v", err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("escaped and literal multibyte differ: %s vs %s", a, b)
	}
	if !bytes.Contains(a, []byte("clé")) {
		t.Fatalf("multibyte rune re-escaped in canonical form: %s", a)
	}
}

func TestCanonicalMapOrderStability(t *testing.T) {
	// Insertion order must not influence canonical bytes or digests.
	m1 := map[string]any{}
	m2 := map[string]any{}
	keys := []string{"z", "a", "m", "é", "0", "~", "zz", "aa"}
	for i, k := range keys {
		m1[k] = i
	}
	for i := len(keys) - 1; i >= 0; i-- {
		m2[keys[i]] = i
	}
	c1, err := CanonicalJSON(m1)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := CanonicalJSON(m2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c1, c2) {
		t.Fatalf("map order changed canonical bytes: %s vs %s", c1, c2)
	}
	// Keys sort in UTF-8 byte order.
	decoded, err := DecodeCanonicalRaw(c1)
	if err != nil {
		t.Fatal(err)
	}
	obj := decoded.(map[string]any)
	if len(obj) != len(keys) {
		t.Fatalf("key count changed: %d", len(obj))
	}
}

func TestCanonicalNumberNormalization(t *testing.T) {
	cases := []struct{ in, want string }{
		{`0`, `0`},
		{`-0`, `0`},
		{`1.0`, `1`},
		{`1.50`, `1.5`},
		{`1e2`, `100`},
		{`1E+2`, `100`},
		{`-1.5e0`, `-1.5`},
		{`123`, `123`},
		{`9007199254740993`, `9007199254740993`}, // >2^53 integer kept literally
	}
	for _, tc := range cases {
		got, err := CanonicalJSON(map[string]any{"n": json.Number(tc.in)})
		if err != nil {
			t.Errorf("number %s: %v", tc.in, err)
			continue
		}
		want := `{"n":` + tc.want + `}`
		if string(got) != want {
			t.Errorf("number %s canonicalized to %s want %s", tc.in, got, want)
		}
	}
	// Float64 values agree with the normalized literal path.
	lit, _ := CanonicalJSON(map[string]any{"n": json.Number("1.0")})
	fl, _ := CanonicalJSON(map[string]any{"n": 1.0})
	if !bytes.Equal(lit, fl) {
		t.Errorf("literal and float64 paths disagree: %s vs %s", lit, fl)
	}
}

func TestCanonicalRejectsMalformedInput(t *testing.T) {
	for _, raw := range []string{
		``,
		`[1,2]`, // root must be object for metadata
		`"str"`,
		`42`,
		`{"a":1} {"b":2}`, // trailing data
		`{"a":1,}`,
		`{"a":nope}`,
		`{"a":01}`,
		`{"a":.5}`,
		`{"a":1.}`,
	} {
		if _, err := CanonicalizeMetadata([]byte(raw)); err == nil {
			t.Errorf("malformed metadata accepted: %q", raw)
		}
	}
	// Nested containers are fine as values.
	if _, err := CanonicalizeMetadata([]byte(`{"a":[1,{"b":[null,true,false]}]}`)); err != nil {
		t.Errorf("nested value rejected: %v", err)
	}
}

func TestCanonicalUnsupportedTypes(t *testing.T) {
	type custom struct{ X int }
	if _, err := CanonicalJSON(map[string]any{"a": custom{1}}); !errorHasCode(err, ErrCodeUnsupportedType) {
		t.Errorf("struct accepted: %v", err)
	}
	if _, err := CanonicalJSON(map[string]any{"a": []byte("hi")}); !errorHasCode(err, ErrCodeUnsupportedType) {
		t.Errorf("raw []byte accepted: %v", err)
	}
}

func TestDigestAndETagStability(t *testing.T) {
	m1 := map[string]any{"alpha": "one", "beta": []any{1, 2, 3}}
	m2 := map[string]any{"beta": []any{1, 2, 3}, "alpha": "one"}
	d1, b1, err := CanonicalDigest(m1)
	if err != nil {
		t.Fatal(err)
	}
	d2, b2, err := CanonicalDigest(m2)
	if err != nil {
		t.Fatal(err)
	}
	if d1 != d2 || !bytes.Equal(b1, b2) {
		t.Fatalf("digest/bytes differ across map order: %s vs %s", d1, d2)
	}
	if !strings.HasPrefix(d1, "sha256:") || len(d1) != len("sha256:")+64 {
		t.Fatalf("digest format wrong: %s", d1)
	}
	etag := ETag(b1)
	if !strings.HasPrefix(etag, `"`) || !strings.HasSuffix(etag, `"`) {
		t.Fatalf("etag not quoted: %s", etag)
	}
	if etag == ETag([]byte("different")) {
		t.Fatal("etag collision on different bytes")
	}
}
