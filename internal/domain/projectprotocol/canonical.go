package projectprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"unicode/utf8"
)

// This file implements the canonical JSON encoding used for metadata
// validation, revision digests and effective protocol bundles.
//
// Canonical form guarantees:
//   - object keys sorted by UTF-8 byte order at every nesting level, so map
//     iteration order can never change the encoding;
//   - compact output (no insignificant whitespace);
//   - minimal string escaping: only ", \ and control characters below 0x20
//     are escaped (short forms \b \f \n \r \t, else \u00xx lowercase); all
//     other runes are emitted as literal UTF-8, so HTML characters are never
//     escaped;
//   - numbers normalized: integer literals keep their (already canonical by
//     JSON grammar) literal form; float literals and float64 values use the
//     shortest round-trip representation, mirroring encoding/json;
//   - non-finite floats (NaN, ±Inf, and literals that overflow float64) are
//     rejected;
//   - invalid UTF-8 strings are rejected;
//   - duplicate object keys are rejected on the raw-input decode path (Go
//     maps cannot represent them, so the map path is duplicate-free by
//     construction).
//
// The encoder is implemented in-repo with no third-party dependency.

const lowerHex = "0123456789abcdef"

// CanonicalJSON encodes v into its deterministic canonical JSON form.
// Accepted value types: nil, bool, string, json.Number, float32/float64,
// signed/unsigned integers, []any, map[string]any and json.RawMessage.
// Other types are rejected as unsupported to avoid encoding ambiguity.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeCanonical streams the canonical encoding of v into w. Writing is
// token-granular so a LimitWriter can abort exactly at the limit boundary.
func writeCanonical(w io.Writer, v any) error {
	switch t := v.(type) {
	case nil:
		_, err := w.Write([]byte("null"))
		return err
	case bool:
		if t {
			_, err := w.Write([]byte("true"))
			return err
		}
		_, err := w.Write([]byte("false"))
		return err
	case string:
		return writeCanonicalString(w, t)
	case json.Number:
		lit, err := canonicalNumberLiteral(string(t))
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(lit))
		return err
	case float64:
		lit, err := canonicalFloat(t)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(lit))
		return err
	case float32:
		lit, err := canonicalFloat(float64(t))
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(lit))
		return err
	case int:
		return writeInt(w, int64(t))
	case int8:
		return writeInt(w, int64(t))
	case int16:
		return writeInt(w, int64(t))
	case int32:
		return writeInt(w, int64(t))
	case int64:
		return writeInt(w, t)
	case uint:
		return writeUint(w, uint64(t))
	case uint8:
		return writeUint(w, uint64(t))
	case uint16:
		return writeUint(w, uint64(t))
	case uint32:
		return writeUint(w, uint64(t))
	case uint64:
		return writeUint(w, t)
	case []any:
		if err := writeByte(w, '['); err != nil {
			return err
		}
		for i, item := range t {
			if i > 0 {
				if err := writeByte(w, ','); err != nil {
					return err
				}
			}
			if err := writeCanonical(w, item); err != nil {
				return err
			}
		}
		return writeByte(w, ']')
	case map[string]any:
		if err := writeByte(w, '{'); err != nil {
			return err
		}
		if t != nil {
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i > 0 {
					if err := writeByte(w, ','); err != nil {
						return err
					}
				}
				if err := writeCanonicalString(w, k); err != nil {
					return err
				}
				if err := writeByte(w, ':'); err != nil {
					return err
				}
				if err := writeCanonical(w, t[k]); err != nil {
					return err
				}
			}
		}
		return writeByte(w, '}')
	case json.RawMessage:
		decoded, err := DecodeCanonicalRaw([]byte(t))
		if err != nil {
			return err
		}
		return writeCanonical(w, decoded)
	default:
		return &Error{
			Code:    ErrCodeUnsupportedType,
			Message: fmt.Sprintf("unsupported canonical value type %T", v),
		}
	}
}

func writeByte(w io.Writer, b byte) error {
	_, err := w.Write([]byte{b})
	return err
}

func writeInt(w io.Writer, v int64) error {
	_, err := w.Write([]byte(strconv.FormatInt(v, 10)))
	return err
}

func writeUint(w io.Writer, v uint64) error {
	_, err := w.Write([]byte(strconv.FormatUint(v, 10)))
	return err
}

// writeCanonicalString emits a minimally escaped JSON string. The input must
// be valid UTF-8; invalid strings are rejected, never sanitized.
func writeCanonicalString(w io.Writer, s string) error {
	if !utf8.ValidString(s) {
		return ErrInvalidUTF8
	}
	if _, err := w.Write([]byte{'"'}); err != nil {
		return err
	}
	start := 0
	for i, r := range s {
		if r >= 0x20 && r != '"' && r != '\\' {
			continue
		}
		if start < i {
			if _, err := w.Write([]byte(s[start:i])); err != nil {
				return err
			}
		}
		var err error
		switch r {
		case '"':
			_, err = w.Write([]byte(`\"`))
		case '\\':
			_, err = w.Write([]byte(`\\`))
		case '\b':
			_, err = w.Write([]byte(`\b`))
		case '\f':
			_, err = w.Write([]byte(`\f`))
		case '\n':
			_, err = w.Write([]byte(`\n`))
		case '\r':
			_, err = w.Write([]byte(`\r`))
		case '\t':
			_, err = w.Write([]byte(`\t`))
		default:
			esc := []byte{'\\', 'u', '0', '0', lowerHex[(r>>4)&0xf], lowerHex[r&0xf]}
			_, err = w.Write(esc)
		}
		if err != nil {
			return err
		}
		start = i + utf8.RuneLen(r)
	}
	if start < len(s) {
		if _, err := w.Write([]byte(s[start:])); err != nil {
			return err
		}
	}
	_, err := w.Write([]byte{'"'})
	return err
}

// validNumberLiteral reports whether s satisfies the JSON number grammar:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?
func validNumberLiteral(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] == '0':
		i++
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for i < len(s) && isASCIIDigit(s[i]) {
			i++
		}
	default:
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if i >= len(s) || !isASCIIDigit(s[i]) {
			return false
		}
		for i < len(s) && isASCIIDigit(s[i]) {
			i++
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if i >= len(s) || !isASCIIDigit(s[i]) {
			return false
		}
		for i < len(s) && isASCIIDigit(s[i]) {
			i++
		}
	}
	return i == len(s)
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

// canonicalNumberLiteral normalizes a numeric literal. Integer literals are
// already canonical by grammar except "-0", normalized to "0". Float-syntax
// literals are normalized through float64; literals that overflow to an
// infinity are rejected as non-finite.
func canonicalNumberLiteral(lit string) (string, error) {
	if !validNumberLiteral(lit) {
		return "", &Error{Code: ErrCodeValidation, Message: "invalid JSON number literal"}
	}
	if isIntegerLiteral(lit) {
		if lit == "-0" {
			return "0", nil
		}
		return lit, nil
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return "", &Error{Code: ErrCodeValidation, Message: "non-finite or unrepresentable number"}
	}
	return canonicalFloat(f)
}

func isIntegerLiteral(lit string) bool {
	for i := 0; i < len(lit); i++ {
		switch lit[i] {
		case '.', 'e', 'E':
			return false
		}
	}
	return true
}

// canonicalFloat formats f with the shortest round-trip representation,
// mirroring encoding/json's float encoding (including the e-09 → e-9
// exponent cleanup) so canonical bytes agree with stdlib output.
func canonicalFloat(f float64) (string, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "", &Error{Code: ErrCodeValidation, Message: "non-finite numbers are not allowed"}
	}
	abs := math.Abs(f)
	format := byte('f')
	if abs != 0 {
		if abs < 1e-6 || abs >= 1e21 {
			format = 'e'
		}
	}
	b := strconv.AppendFloat(nil, f, format, -1, 64)
	if format == 'e' {
		// Clean up e-09 to e-9 exactly as encoding/json does.
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return string(b), nil
}

// DecodeCanonicalRaw decodes raw JSON bytes into canonical-ready values:
// duplicate object keys are rejected, trailing data is rejected, numbers are
// preserved as json.Number, and invalid UTF-8 input is rejected before any
// decoding (Go's decoder would otherwise silently replace it with U+FFFD).
// Unpaired \uD800-\uDFFF escapes are rejected because they decode to U+FFFD
// and would silently alias distinct inputs.
func DecodeCanonicalRaw(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, &Error{Code: ErrCodeValidation, Message: "empty JSON input"}
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalidUTF8
	}
	if err := rejectLoneSurrogateEscapes(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := decodeValue(dec)
	if err != nil {
		return nil, AsError(err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, &Error{Code: ErrCodeValidation, Message: "multiple JSON values"}
		}
		return nil, AsError(err)
	}
	return value, nil
}

// decodeValue recursively decodes one JSON value with duplicate-key
// detection. Numbers arrive as json.Number via the decoder's UseNumber.
func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := make(map[string]any)
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, &Error{Code: ErrCodeValidation, Message: "malformed JSON object"}
				}
				if _, dup := m[key]; dup {
					return nil, &Error{
						Code:    ErrCodeDuplicateMetadataKey,
						Message: "duplicate object key",
						Detail:  "metadata keys must be unique",
					}
				}
				val, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				m[key] = val
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return nil, err
			}
			return m, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return nil, err
			}
			return arr, nil
		}
		return nil, &Error{Code: ErrCodeValidation, Message: "malformed JSON"}
	case string:
		return t, nil
	case bool:
		return t, nil
	case json.Number:
		return t, nil
	case nil:
		return nil, nil
	default:
		return nil, &Error{Code: ErrCodeValidation, Message: "malformed JSON"}
	}
}

// rejectLoneSurrogateEscapes scans raw JSON text for ACTUAL \uXXXX escape
// sequences in the surrogate range. The scan is escape-aware: it tracks
// string boundaries and backslash runs so that the six literal characters
// \uD800 produced by an ESCAPED backslash (JSON source "\\uD800") are treated
// as plain text, not an escape. Only a genuine unpaired surrogate escape —
// one whose initiating backslash is not itself escaped — is rejected, because
// Go's decoder would turn it into U+FFFD and silently alias distinct inputs.
func rejectLoneSurrogateEscapes(raw []byte) error {
	const (
		stateText    = false
		stateString  = true
		backslash    = '\\'
		quote        = '"'
		escapeMarker = 'u'
	)
	inString := stateText
	i := 0
	for i < len(raw) {
		c := raw[i]
		if !inString {
			if c == quote {
				inString = stateString
			}
			i++
			continue
		}
		switch c {
		case quote:
			inString = stateText
			i++
			continue
		case backslash:
		default:
			i++
			continue
		}
		// Count the run of consecutive backslashes starting at i.
		run := 0
		for i+run < len(raw) && raw[i+run] == backslash {
			run++
		}
		if i+run >= len(raw) {
			// Unterminated escape: malformed JSON, rejected by the decoder.
			return nil
		}
		if run%2 == 0 {
			// Even run: escaped backslashes followed by one literal byte
			// (e.g. "\\uD800" is the literal text \uD800). Not an escape.
			i += run + 1
			continue
		}
		// Odd run: the final backslash initiates an escape sequence.
		marker := raw[i+run]
		next := i + run + 1
		if marker != escapeMarker {
			// Simple escape (", \, /, b, f, n, r, t) or invalid (the
			// decoder rejects invalid ones); no surrogate concerns.
			i = next
			continue
		}
		if next+4 > len(raw) || !isHexRun(raw[next:next+4]) {
			// Malformed \u escape: rejected by the decoder.
			return nil
		}
		r, _ := parseHex4(raw[next : next+4])
		switch {
		case r >= 0xDC00 && r <= 0xDFFF:
			return &Error{Code: ErrCodeValidation, Message: "unpaired low surrogate escape"}
		case r >= 0xD800 && r <= 0xDBFF:
			rest := raw[next+4:]
			if len(rest) < 6 || rest[0] != backslash || rest[1] != escapeMarker || !isHexRun(rest[2:6]) {
				return &Error{Code: ErrCodeValidation, Message: "unpaired high surrogate escape"}
			}
			lo, ok := parseHex4(rest[2:6])
			if !ok || lo < 0xDC00 || lo > 0xDFFF {
				return &Error{Code: ErrCodeValidation, Message: "unpaired high surrogate escape"}
			}
			i = next + 4 + 6 // skip both halves of the valid pair
			continue
		}
		i = next + 4
	}
	return nil
}

func isHexRun(b []byte) bool {
	for _, c := range b {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

func parseHex4(b []byte) (int, bool) {
	v := 0
	for _, c := range b {
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + int(c-'0')
		case c >= 'a' && c <= 'f':
			v = v*16 + int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v = v*16 + int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return v, true
}

// CanonicalizeMetadata validates raw JSON metadata and returns its canonical
// bytes. The root must be a JSON object; duplicate keys, invalid UTF-8,
// unpaired surrogates and non-finite numbers are rejected. The canonical
// byte length must not exceed MaxArtifactMetadataBytes: exactly at the limit
// is accepted, one byte more is rejected without truncation.
func CanonicalizeMetadata(raw []byte) ([]byte, error) {
	value, err := DecodeCanonicalRaw(raw)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, &Error{Code: ErrCodeValidation, Message: "metadata root must be a JSON object"}
	}
	return encodeBounded(value, MaxArtifactMetadataBytes, ErrCodeMetadataTooLarge)
}

// CanonicalizeMetadataMap validates an in-memory metadata object and returns
// its canonical bytes under the same rules and limit as CanonicalizeMetadata.
func CanonicalizeMetadataMap(m map[string]any) ([]byte, error) {
	if m == nil {
		m = map[string]any{}
	}
	if err := validateCanonicalValue(m); err != nil {
		return nil, err
	}
	return encodeBounded(m, MaxArtifactMetadataBytes, ErrCodeMetadataTooLarge)
}

// encodeBounded streams the canonical encoding through a LimitWriter so the
// abort happens exactly at limit+1 with no partial output.
func encodeBounded(v any, limit int64, code ErrorCode) ([]byte, error) {
	lw := NewLimitWriter(limit)
	if err := writeCanonical(lw, v); err != nil {
		if lw.Failed() {
			return nil, NewLimitError(code, limit)
		}
		return nil, AsError(err)
	}
	out := lw.Bytes()
	if out == nil {
		return nil, NewLimitError(code, limit)
	}
	return out, nil
}

// validateCanonicalValue walks v and rejects values the encoder cannot
// represent deterministically (unsupported types, invalid UTF-8, non-finite
// floats) before any bytes are emitted.
func validateCanonicalValue(v any) error {
	switch t := v.(type) {
	case nil, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return nil
	case float32:
		_, err := canonicalFloat(float64(t))
		return err
	case float64:
		_, err := canonicalFloat(t)
		return err
	case json.Number:
		_, err := canonicalNumberLiteral(string(t))
		return err
	case string:
		if !utf8.ValidString(t) {
			return ErrInvalidUTF8
		}
		return nil
	case []any:
		for _, item := range t {
			if err := validateCanonicalValue(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for k, val := range t {
			if !utf8.ValidString(k) {
				return ErrInvalidUTF8
			}
			if err := validateCanonicalValue(val); err != nil {
				return err
			}
		}
		return nil
	case json.RawMessage:
		_, err := DecodeCanonicalRaw([]byte(t))
		return err
	default:
		return &Error{
			Code:    ErrCodeUnsupportedType,
			Message: fmt.Sprintf("unsupported canonical value type %T", v),
		}
	}
}

// CanonicalDigest returns the canonical bytes of v and their "sha256:<hex>"
// digest. Digests computed this way are stable across map iteration order.
func CanonicalDigest(v any) (string, []byte, error) {
	canonical, err := CanonicalJSON(v)
	if err != nil {
		return "", nil, err
	}
	return DigestHex(canonical), canonical, nil
}

// DigestHex returns the "sha256:<hex>" digest of bytes.
func DigestHex(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ETag returns the opaque quoted ETag for canonical bytes (a quoted sha256
// hex string), as used for artifact ETags and protocol conditional requests.
func ETag(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}
