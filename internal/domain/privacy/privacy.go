package privacy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// RedactedPlaceholder is the deterministic string that replaces private markers and content.
const (
	RedactedPlaceholder = "[REDACTED]"
	openTagLen          = 9  // len("<private>")
	closeTagLen         = 10 // len("</private>")
)

// Disposition represents the redaction outcome.
type Disposition string

const (
	DispositionUnchanged  Disposition = "unchanged"
	DispositionRedacted   Disposition = "redacted"
	DispositionUnmodified Disposition = "unchanged" // alias for compatibility
)

// TextResult holds the protected output and provenance metadata.
type TextResult struct {
	ProtectedValue        string
	Text                  string // convenience alias for ProtectedValue
	PublicResidualPresent bool
	Disposition           Disposition
	MarkerCount           int
}

func (r TextResult) String() string { return r.ProtectedValue }

// ErrorCode identifies the stable classification of privacy errors.
type ErrorCode string

const (
	ErrCodeValidation         ErrorCode = "validation"
	ErrCodeInvalidMarker      ErrorCode = "privacy_invalid_marker"
	ErrCodeRequiredEmpty      ErrorCode = "privacy_required_empty"
	ErrCodeUnclosedMarker     ErrorCode = "privacy_invalid_marker"
	ErrCodeStrayClosingMarker ErrorCode = "privacy_invalid_marker"
	ErrCodeNestedMarker       ErrorCode = "privacy_invalid_marker"
	ErrCodeNoPublicContent    ErrorCode = "privacy_required_empty"
	ErrCodeInvalidUTF8        ErrorCode = "invalid_utf8"
	ErrCodeMetadataRejected   ErrorCode = "privacy_invalid_marker"
	ErrCodeNilEnvelope        ErrorCode = "nil_envelope"
)

// Error is a typed, payload-free privacy domain error that prevents leaking secrets.
type Error struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	Field       string    `json:"field,omitempty"`
	Position    int       `json:"position,omitempty"`
	MarkerCount int       `json:"marker_count,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return "privacy: validation failed"
	}
	var sb strings.Builder
	sb.WriteString("privacy: " + string(e.Code))
	if msg := sanitizeField(e.Message); msg != "" {
		sb.WriteString(": " + msg)
	}
	if field := sanitizeField(e.Field); field != "" {
		fmt.Fprintf(&sb, " (field=%s)", field)
	}
	if e.Position >= 0 {
		fmt.Fprintf(&sb, " (position=%d)", e.Position)
	}
	if e.MarkerCount > 0 {
		fmt.Fprintf(&sb, " (markers=%d)", e.MarkerCount)
	}
	return sb.String()
}

func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e != nil && other != nil && e.Code == other.Code
}

type errorDTO struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	Field       string    `json:"field,omitempty"`
	Position    *int      `json:"position,omitempty"`
	MarkerCount int       `json:"marker_count,omitempty"`
}

func (e *Error) MarshalJSON() ([]byte, error) {
	if e == nil {
		return []byte("null"), nil
	}
	d := errorDTO{Code: e.Code, Message: sanitizeField(e.Message), Field: sanitizeField(e.Field), MarkerCount: e.MarkerCount}
	if e.Position >= 0 {
		d.Position = &e.Position
	}
	return json.Marshal(d)
}

func (e *Error) MarshalText() ([]byte, error) {
	if e == nil {
		return []byte("privacy: validation failed"), nil
	}
	return []byte(e.Error()), nil
}

var (
	ErrInvalidMarker      = &Error{Code: ErrCodeInvalidMarker, Message: "invalid or malformed marker syntax", Position: -1}
	ErrRequiredEmpty      = &Error{Code: ErrCodeRequiredEmpty, Message: "residual public content is empty", Position: -1}
	ErrUnclosedMarker     = &Error{Code: ErrCodeInvalidMarker, Message: "opening marker without matching closing marker", Position: -1}
	ErrStrayClosingMarker = &Error{Code: ErrCodeInvalidMarker, Message: "closing marker without opening marker", Position: -1}
	ErrNestedMarker       = &Error{Code: ErrCodeInvalidMarker, Message: "nested marker is not permitted", Position: -1}
	ErrNoPublicContent    = &Error{Code: ErrCodeRequiredEmpty, Message: "residual public content is empty", Position: -1}
	ErrInvalidUTF8        = &Error{Code: ErrCodeInvalidUTF8, Message: "content is not valid UTF-8", Position: -1}
	ErrMetadataRejected   = &Error{Code: ErrCodeInvalidMarker, Message: "metadata contains private markers or is invalid", Position: -1}
	ErrNilEnvelope        = &Error{Code: ErrCodeNilEnvelope, Message: "envelope must not be nil", Position: -1}
)

// NamedField defines a named text field with requirement semantics for envelope protection.
type NamedField struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Required bool   `json:"required"`
}

// Envelope represents an observation, message, or multi-field payload with associated metadata.
type Envelope struct {
	Content     string            `json:"content,omitempty"`
	Fields      map[string]string `json:"fields,omitempty"`
	NamedFields []NamedField      `json:"named_fields,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Disposition Disposition       `json:"disposition,omitempty"`
	MarkerCount int               `json:"marker_count,omitempty"`
}

func (e Envelope) Protect() (Envelope, error) { return ProtectEnvelope(e) }

type markerSpan struct{ start, end int }

type markerType int

const (
	markerNone markerType = iota
	markerExactOpen
	markerExactClose
	markerMalformed
)

func isASCIIEqualFold(s, target string) bool {
	if len(s) != len(target) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c, t := s[i], target[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if t >= 'A' && t <= 'Z' {
			t += 'a' - 'A'
		}
		if c != t {
			return false
		}
	}
	return true
}

func scanTagEnd(text string, from int) int {
	for from < len(text) {
		from++
		if text[from-1] == '>' {
			break
		}
	}
	return from
}

func skipWS(text string, j int) int {
	for j < len(text) && (text[j] == ' ' || text[j] == '\t' || text[j] == '\r' || text[j] == '\n') {
		j++
	}
	return j
}

// classifyTag identifies whether text[i] begins an exact or malformed private marker.
func classifyTag(text string, i int) (markerType, int) {
	if i >= len(text) || text[i] != '<' {
		return markerNone, i
	}
	if i+openTagLen <= len(text) && isASCIIEqualFold(text[i:i+openTagLen], "<private>") {
		return markerExactOpen, i + openTagLen
	}
	if i+closeTagLen <= len(text) && isASCIIEqualFold(text[i:i+closeTagLen], "</private>") {
		return markerExactClose, i + closeTagLen
	}
	for _, prefix := range []string{"<private", "</private"} {
		if i+len(prefix) <= len(text) && isASCIIEqualFold(text[i:i+len(prefix)], prefix) {
			return markerMalformed, scanTagEnd(text, i+len(prefix))
		}
	}
	j := skipWS(text, i+1)
	if j < len(text) && text[j] == '/' {
		j = skipWS(text, j+1)
	}
	const priv = "private"
	if j+len(priv) <= len(text) && isASCIIEqualFold(text[j:j+len(priv)], priv) {
		return markerMalformed, scanTagEnd(text, j+len(priv))
	}
	return markerNone, i
}

func hasPrivateMarker(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '<' {
			if mType, _ := classifyTag(s, i); mType != markerNone {
				return true
			}
		}
	}
	return false
}

func sanitizeField(s string) string {
	if s == "" {
		return ""
	}
	if !utf8.ValidString(s) || hasPrivateMarker(s) {
		return RedactedPlaceholder
	}
	return s
}

// ValidateMetadata verifies metadata contains valid UTF-8 and no private markers or marker-like syntax.
func ValidateMetadata(metadata map[string]string) error {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := metadata[k]
		if !utf8.ValidString(k) || !utf8.ValidString(v) {
			return &Error{Code: ErrCodeInvalidUTF8, Message: "content is not valid UTF-8", Field: "metadata", Position: -1}
		}
		if hasPrivateMarker(k) || hasPrivateMarker(v) {
			return &Error{Code: ErrCodeInvalidMarker, Message: "metadata contains private markers or is invalid", Field: "metadata", Position: -1}
		}
	}
	return nil
}

// parseSpans scans text and extracts valid private spans or returns a typed error.
func parseSpans(text, field string) ([]markerSpan, error) {
	field = sanitizeField(field)
	var spans []markerSpan
	inPrivate, openStart, markerCount := false, -1, 0

	for i := 0; i < len(text); i++ {
		if text[i] != '<' {
			continue
		}
		mType, nextIdx := classifyTag(text, i)
		if mType == markerNone {
			continue
		}
		if inPrivate {
			if mType == markerExactClose {
				spans = append(spans, markerSpan{start: openStart, end: nextIdx})
				inPrivate, openStart, i = false, -1, nextIdx-1
				continue
			}
			if mType == markerExactOpen {
				return nil, &Error{Code: ErrCodeInvalidMarker, Message: "nested marker is not permitted", Field: field, Position: i, MarkerCount: markerCount}
			}
			return nil, &Error{Code: ErrCodeInvalidMarker, Message: "invalid or malformed marker syntax", Field: field, Position: i, MarkerCount: markerCount}
		}

		if mType == markerExactOpen {
			inPrivate, openStart, markerCount, i = true, i, markerCount+1, nextIdx-1
			continue
		}
		if mType == markerExactClose {
			return nil, &Error{Code: ErrCodeInvalidMarker, Message: "closing marker without opening marker", Field: field, Position: i, MarkerCount: markerCount}
		}
		return nil, &Error{Code: ErrCodeInvalidMarker, Message: "invalid or malformed marker syntax", Field: field, Position: i, MarkerCount: markerCount}
	}

	if inPrivate {
		return nil, &Error{Code: ErrCodeInvalidMarker, Message: "opening marker without matching closing marker", Field: field, Position: openStart, MarkerCount: markerCount}
	}
	return spans, nil
}

// ProtectField evaluates prose for a specific field name, enforcing the pinned grammar
// and required-residual presence when required is true.
func ProtectField(field, text string, required bool) (TextResult, error) {
	field = sanitizeField(field)
	if !utf8.ValidString(text) {
		return TextResult{}, &Error{Code: ErrCodeInvalidUTF8, Message: "content is not valid UTF-8", Field: field, Position: -1}
	}

	spans, err := parseSpans(text, field)
	if err != nil {
		return TextResult{}, err
	}

	var residual strings.Builder
	last := 0
	for _, sp := range spans {
		residual.WriteString(text[last:sp.start])
		last = sp.end
	}
	residual.WriteString(text[last:])

	hasResidual := strings.TrimSpace(residual.String()) != ""
	if required && !hasResidual {
		return TextResult{}, &Error{Code: ErrCodeRequiredEmpty, Message: "residual public content is empty", Field: field, Position: -1, MarkerCount: len(spans)}
	}

	if len(spans) == 0 {
		return TextResult{
			ProtectedValue: text, Text: text,
			PublicResidualPresent: hasResidual,
			Disposition:           DispositionUnchanged, MarkerCount: 0,
		}, nil
	}

	var protected strings.Builder
	last = 0
	for _, sp := range spans {
		protected.WriteString(text[last:sp.start])
		protected.WriteString(RedactedPlaceholder)
		last = sp.end
	}
	protected.WriteString(text[last:])
	outStr := protected.String()

	return TextResult{
		ProtectedValue: outStr, Text: outStr,
		PublicResidualPresent: hasResidual,
		Disposition:           DispositionRedacted, MarkerCount: len(spans),
	}, nil
}

// ProtectText redacts private markers from required text using the exact ASCII case-insensitive
// marker grammar. It validates residual public content before inserting placeholders.
func ProtectText(text string) (TextResult, error) {
	return ProtectField("", text, true)
}

// ProtectOptionalText redacts private markers from optional text where residual public
// content is not required to be non-empty.
func ProtectOptionalText(text string) (TextResult, error) {
	return ProtectField("", text, false)
}

// ProtectString is a helper that returns just the protected text string.
func ProtectString(text string) (string, error) {
	res, err := ProtectText(text)
	return res.ProtectedValue, err
}

// ProtectNamedFields evaluates and redacts multiple named fields atomically.
// If any field violates grammar or residual requirements, it returns a typed,
// payload-free error naming the failing field, and no field is modified or returned.
func ProtectNamedFields(fields ...NamedField) (map[string]TextResult, error) {
	results := make(map[string]TextResult, len(fields))
	for _, f := range fields {
		if _, ok := results[f.Name]; ok {
			return nil, &Error{Code: ErrCodeValidation, Message: "duplicate named field", Field: "named_fields", Position: -1}
		}
		res, err := ProtectField(f.Name, f.Value, f.Required)
		if err != nil {
			return nil, err
		}
		results[f.Name] = res
	}
	return results, nil
}

// ProtectFields evaluates and redacts a map of named fields atomically, treating all as required.
func ProtectFields(fields map[string]string) (map[string]TextResult, error) {
	if fields == nil {
		return nil, nil
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	results := make(map[string]TextResult, len(fields))
	for _, k := range keys {
		res, err := ProtectField(k, fields[k], true)
		if err != nil {
			return nil, err
		}
		results[k] = res
	}
	return results, nil
}

// ProtectEnvelope protects an Envelope by validating metadata and redacting content and named fields.
// All fields are evaluated atomically: on any failure, no fields are modified and caller input is never mutated.
func ProtectEnvelope[T Envelope | *Envelope](env T) (T, error) {
	var zero T
	var isPtr bool
	var in Envelope
	switch v := any(env).(type) {
	case *Envelope:
		if v == nil {
			return zero, ErrNilEnvelope
		}
		in, isPtr = *v, true
	case Envelope:
		in = v
	}
	if err := ValidateMetadata(in.Metadata); err != nil {
		return zero, err
	}
	totalMarkers, disposition := 0, DispositionUnchanged
	var contentRes TextResult
	if in.Content != "" || (len(in.Fields) == 0 && len(in.NamedFields) == 0) {
		var err error
		if contentRes, err = ProtectField("content", in.Content, true); err != nil {
			return zero, err
		}
		totalMarkers += contentRes.MarkerCount
		if contentRes.Disposition == DispositionRedacted {
			disposition = DispositionRedacted
		}
	}
	var clonedFields map[string]string
	if len(in.Fields) > 0 {
		fieldResults, err := ProtectFields(in.Fields)
		if err != nil {
			return zero, err
		}
		clonedFields = make(map[string]string, len(fieldResults))
		for k, res := range fieldResults {
			clonedFields[k] = res.ProtectedValue
			totalMarkers += res.MarkerCount
			if res.Disposition == DispositionRedacted {
				disposition = DispositionRedacted
			}
		}
	}
	var clonedNamedFields []NamedField
	if len(in.NamedFields) > 0 {
		seen := make(map[string]struct{}, len(in.NamedFields))
		for _, nf := range in.NamedFields {
			if _, ok := seen[nf.Name]; ok {
				return zero, &Error{Code: ErrCodeValidation, Message: "duplicate named field", Field: "named_fields", Position: -1}
			}
			seen[nf.Name] = struct{}{}
		}
		clonedNamedFields = make([]NamedField, len(in.NamedFields))
		for idx, nf := range in.NamedFields {
			res, err := ProtectField(nf.Name, nf.Value, nf.Required)
			if err != nil {
				return zero, err
			}
			clonedNamedFields[idx] = NamedField{Name: nf.Name, Value: res.ProtectedValue, Required: nf.Required}
			totalMarkers += res.MarkerCount
			if res.Disposition == DispositionRedacted {
				disposition = DispositionRedacted
			}
		}
	}
	var clonedMeta map[string]string
	if in.Metadata != nil {
		clonedMeta = make(map[string]string, len(in.Metadata))
		for k, val := range in.Metadata {
			clonedMeta[k] = val
		}
	}
	out := Envelope{
		Content: contentRes.ProtectedValue, Fields: clonedFields,
		NamedFields: clonedNamedFields, Metadata: clonedMeta,
		Disposition: disposition, MarkerCount: totalMarkers,
	}
	if isPtr {
		return any(&out).(T), nil
	}
	return any(out).(T), nil
}
