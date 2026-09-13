package privacy_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func TestProtectText(t *testing.T) {
	tests := []struct {
		name            string
		input           string
		want            string
		wantDisposition privacy.Disposition
		wantMarkers     int
		wantErr         error
	}{
		{name: "plain text unchanged", input: "Hello world, cortex privacy foundation", want: "Hello world, cortex privacy foundation", wantDisposition: privacy.DispositionUnchanged, wantMarkers: 0},
		{name: "single exact marker redaction", input: "Connect using <private>super_secret_pw</private> on port 5432", want: "Connect using [REDACTED] on port 5432", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "case insensitive ascii markers", input: "Key: <PRIVATE>secret1</PRIVATE> and Token: <Private>secret2</pRiVaTe>", want: "Key: [REDACTED] and Token: [REDACTED]", wantDisposition: privacy.DispositionRedacted, wantMarkers: 2},
		{name: "adjacent markers with public content", input: "Data: <private>one</private><private>two</private> end", want: "Data: [REDACTED][REDACTED] end", wantDisposition: privacy.DispositionRedacted, wantMarkers: 2},
		{name: "empty marker with residual public content", input: "Public prefix <private></private> public suffix", want: "Public prefix [REDACTED] public suffix", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "unrelated tags and less-than untouched", input: "Check <div>, <privacy>, <priv>, and a < b", want: "Check <div>, <privacy>, <priv>, and a < b", wantDisposition: privacy.DispositionUnchanged, wantMarkers: 0},
		{name: "attribute bearing marker rejected", input: "Check <private attr=\"x\">secret</private>", wantErr: privacy.ErrInvalidMarker},
		{name: "attribute bearing opening tag alone rejected", input: "Check <private kind=x> text", wantErr: privacy.ErrInvalidMarker},
		{name: "whitespace altered opening marker rejected", input: "Check < private>secret</private>", wantErr: privacy.ErrInvalidMarker},
		{name: "whitespace altered closing marker rejected", input: "Check <private>secret</ private>", wantErr: privacy.ErrInvalidMarker},
		{name: "whitespace altered closing marker separated rejected", input: "Check < / private >", wantErr: privacy.ErrInvalidMarker},
		{name: "dangling marker without closing angle bracket", input: "Check <private unclosed", wantErr: privacy.ErrInvalidMarker},
		{name: "dangling closing marker without closing angle bracket", input: "Check </private unclosed", wantErr: privacy.ErrInvalidMarker},
		{name: "empty string fails residual validation", input: "", wantErr: privacy.ErrRequiredEmpty},
		{name: "whitespace only fails residual validation", input: "   \t\n  ", wantErr: privacy.ErrRequiredEmpty},
		{name: "entirely private content fails residual validation before placeholder", input: "<private>sensitive payload</private>", wantErr: privacy.ErrRequiredEmpty},
		{name: "private content surrounded only by whitespace fails residual validation", input: "  \t <private>only secret</private> \n ", wantErr: privacy.ErrRequiredEmpty},
		{name: "multiple private blocks with only whitespace residual", input: "<private>part1</private>   <private>part2</private>", wantErr: privacy.ErrRequiredEmpty},
		{name: "unclosed opening marker rejected", input: "Public text <private>unclosed secret", wantErr: privacy.ErrInvalidMarker},
		{name: "stray closing marker rejected", input: "Public text </private> without opening", wantErr: privacy.ErrInvalidMarker},
		{name: "nested marker rejected", input: "Public <private>outer <private>inner</private></private> text", wantErr: privacy.ErrInvalidMarker},
		{name: "reversed marker order rejected", input: "Public </private><private>reversed</private> text", wantErr: privacy.ErrInvalidMarker},
		{name: "invalid utf8 byte sequence rejected", input: "bad \xff\xfe text", wantErr: privacy.ErrInvalidUTF8},
		{name: "invalid utf8 truncated sequence rejected", input: "bad \xc3\x28 text", wantErr: privacy.ErrInvalidUTF8},
		{name: "invalid utf8 unexpected continuation rejected", input: "bad \x80 text", wantErr: privacy.ErrInvalidUTF8},
		{name: "invalid utf8 overlong encoding rejected", input: "bad \xc0\xaf text", wantErr: privacy.ErrInvalidUTF8},
		{name: "multiline body literal newline preserved", input: "Header line 1\n<private>secret line a\nsecret line b</private>\nFooter line 3\n", want: "Header line 1\n[REDACTED]\nFooter line 3\n", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "multiline body windows newline preserved", input: "Line1\r\n<private>secret</private>\r\nLine2", want: "Line1\r\n[REDACTED]\r\nLine2", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "multiline whitespace only residual rejected", input: "\n  \n<private>secret\nlines</private>\n \t \n", wantErr: privacy.ErrRequiredEmpty},
		{name: "literal public redacted placeholder alone passes", input: "[REDACTED]", want: "[REDACTED]", wantDisposition: privacy.DispositionUnchanged, wantMarkers: 0},
		{name: "literal placeholder with marker generates second placeholder", input: "[REDACTED] <private>secret</private>", want: "[REDACTED] [REDACTED]", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "literal placeholder inside private tag fails required residual", input: "<private>[REDACTED]</private>", wantErr: privacy.ErrRequiredEmpty},
		{name: "literal placeholder inside marker with external public residual", input: "Prefix <private>[REDACTED]</private> suffix", want: "Prefix [REDACTED] suffix", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "raw utf8 cyrillic and emoji with marker", input: "Привет <private>пароль_123</private> мир 🔑", want: "Привет [REDACTED] мир 🔑", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "raw utf8 cjk multilingual with marker", input: "用户数据 <private>机密密钥</private> 完毕", want: "用户数据 [REDACTED] 完毕", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
		{name: "raw utf8 multiline body with marker", input: "Строка 1: старт\n<private>пароль\nвторая строка</private>\nСтрока 3: финиш", want: "Строка 1: старт\n[REDACTED]\nСтрока 3: финиш", wantDisposition: privacy.DispositionRedacted, wantMarkers: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := privacy.ProtectText(tt.input)
			if tt.wantErr != nil {
				if err == nil || !errors.Is(err, tt.wantErr) {
					t.Fatalf("ProtectText() error = %v, want %v", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sensitive") {
					t.Fatalf("ProtectText() leaked payload in error: %v", err)
				}
				var privErr *privacy.Error
				if errors.As(err, &privErr) {
					if privErr.Code == "" {
						t.Fatalf("ProtectText() returned error with empty Code: %v", err)
					}
				}
				return
			}
			if err != nil || got.ProtectedValue != tt.want {
				t.Fatalf("ProtectText() = %q, %v, want %q, nil", got.ProtectedValue, err, tt.want)
			}
			if got.Disposition != tt.wantDisposition {
				t.Fatalf("ProtectText() disposition = %q, want %q", got.Disposition, tt.wantDisposition)
			}
			if got.MarkerCount != tt.wantMarkers {
				t.Fatalf("ProtectText() marker count = %d, want %d", got.MarkerCount, tt.wantMarkers)
			}
		})
	}

	t.Run("safe typed error metadata contract and canary", func(t *testing.T) {
		const canary = "canary-secret-payload-987"
		_, err := privacy.ProtectText("Public text <private>" + canary)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var privErr *privacy.Error
		if !errors.As(err, &privErr) {
			t.Fatalf("expected *privacy.Error, got %T", err)
		}
		if privErr.Code != privacy.ErrCodeInvalidMarker || privErr.Position != 12 {
			t.Fatalf("expected ErrCodeInvalidMarker at 12, got %q at %d", privErr.Code, privErr.Position)
		}
		if strings.Contains(err.Error(), canary) || strings.Contains(privErr.Field, canary) {
			t.Fatalf("error leaked canary: %s", err.Error())
		}
		data, mErr := json.Marshal(privErr)
		if mErr != nil || strings.Contains(string(data), canary) {
			t.Fatalf("json marshal leaked canary or errored: %v, %s", mErr, string(data))
		}
		txtData, tErr := privErr.MarshalText()
		if tErr != nil || strings.Contains(string(txtData), canary) {
			t.Fatalf("marshal text leaked canary or errored: %v, %s", tErr, string(txtData))
		}

		// Multibyte position calculation: "Запрос " is 7 runes and 13 UTF-8 bytes.
		_, err = privacy.ProtectText("Запрос <private>unclosed")
		if errors.As(err, &privErr) && privErr.Position != 13 {
			t.Fatalf("expected byte position 13 for multibyte text, got %d", privErr.Position)
		}

		// Field name containing private marker sanitized to [REDACTED]
		_, err = privacy.ProtectField("<private>canary-field</private>", "<private>unclosed", true)
		if errors.As(err, &privErr) {
			if privErr.Field != privacy.RedactedPlaceholder {
				t.Fatalf("expected field to be sanitized to %q, got %q", privacy.RedactedPlaceholder, privErr.Field)
			}
			if strings.Contains(privErr.Error(), "canary-field") {
				t.Fatalf("field canary leaked: %s", privErr.Error())
			}
		}

		// Field name containing malformed UTF-8 sanitized
		if _, err = privacy.ProtectField("bad\xfffield", "content", true); err != nil {
			t.Fatalf("expected no error for valid content with sanitized field, got %v", err)
		}

		// Nil error receiver contract
		var nilErr *privacy.Error
		if nilErr.Error() != "privacy: validation failed" {
			t.Fatalf("unexpected nil error string: %s", nilErr.Error())
		}
		if nData, _ := nilErr.MarshalJSON(); string(nData) != "null" {
			t.Fatalf("unexpected nil JSON: %s", string(nData))
		}
		if nTxt, _ := nilErr.MarshalText(); string(nTxt) != "privacy: validation failed" {
			t.Fatalf("unexpected nil text: %s", string(nTxt))
		}
		if nilErr.Is(privacy.ErrInvalidMarker) {
			t.Fatal("nil error should not match target")
		}

		// ProtectString helper check
		ps, pErr := privacy.ProtectString("Public <private>secret</private>")
		if pErr != nil || ps != "Public [REDACTED]" {
			t.Fatalf("ProtectString failed: %q, %v", ps, pErr)
		}
	})

	t.Run("protect optional text generated vs literal placeholder", func(t *testing.T) {
		got, err := privacy.ProtectOptionalText("<private>optional secret</private>")
		if err != nil || got.ProtectedValue != "[REDACTED]" {
			t.Fatalf("ProtectOptionalText failed: %+v, %v", got, err)
		}
		if got.Disposition != privacy.DispositionRedacted || got.MarkerCount != 1 || got.PublicResidualPresent {
			t.Fatalf("unexpected optional text result: %+v", got)
		}

		gotLit, err := privacy.ProtectOptionalText("[REDACTED]")
		if err != nil || gotLit.ProtectedValue != "[REDACTED]" {
			t.Fatalf("literal placeholder in optional failed: %+v, %v", gotLit, err)
		}
		if gotLit.Disposition != privacy.DispositionUnchanged || gotLit.MarkerCount != 0 || !gotLit.PublicResidualPresent {
			t.Fatalf("unexpected literal optional text result: %+v", gotLit)
		}

		gotClean, err := privacy.ProtectOptionalText("")
		if err != nil || gotClean.ProtectedValue != "" || gotClean.Disposition != privacy.DispositionUnchanged {
			t.Fatalf("unexpected clean optional: %+v", gotClean)
		}
	})
}

func TestProtectEnvelope(t *testing.T) {
	t.Run("protect envelope value with clean metadata", func(t *testing.T) {
		meta := map[string]string{"env": "prod", "service": "indexer"}
		env := privacy.Envelope{Content: "Observation: <private>secret-pw</private> updated", Metadata: meta}
		got, err := privacy.ProtectEnvelope(env)
		if err != nil || got.Content != "Observation: [REDACTED] updated" {
			t.Fatalf("ProtectEnvelope() = %+v, %v", got, err)
		}
		if got.Metadata["env"] != "prod" || got.Metadata["service"] != "indexer" {
			t.Fatalf("unexpected metadata: %+v", got.Metadata)
		}
		if got.Disposition != privacy.DispositionRedacted || got.MarkerCount != 1 {
			t.Fatalf("unexpected disposition/marker count: %+v", got)
		}
		got.Metadata["env"] = "mutated"
		if meta["env"] != "prod" {
			t.Fatalf("caller metadata was mutated after modification of result")
		}
	})

	t.Run("protect envelope pointer and method call", func(t *testing.T) {
		env := &privacy.Envelope{
			Content:  "API call with <PRIVATE>bearer-token</PRIVATE> succeeded",
			Metadata: map[string]string{"trace_id": "abc-123"},
		}
		got, err := privacy.ProtectEnvelope(env)
		if err != nil || got.Content != "API call with [REDACTED] succeeded" {
			t.Fatalf("ProtectEnvelope(ptr) = %+v, %v", got, err)
		}
		if got.Disposition != privacy.DispositionRedacted || got.MarkerCount != 1 {
			t.Fatalf("unexpected disposition/marker count: %+v", got)
		}

		envVal := privacy.Envelope{Content: "Run <private>cmd</private> safely", Metadata: map[string]string{"k": "v"}}
		gotVal, err := envVal.Protect()
		if err != nil || gotVal.Content != "Run [REDACTED] safely" {
			t.Fatalf("env.Protect() = %+v, %v", gotVal, err)
		}
		if gotVal.Disposition != privacy.DispositionRedacted || gotVal.MarkerCount != 1 {
			t.Fatalf("unexpected disposition/marker count on env.Protect: %+v", gotVal)
		}

		cleanEnv := privacy.Envelope{Content: "Plain public prose", Metadata: nil}
		gotClean, err := cleanEnv.Protect()
		if err != nil || gotClean.Content != "Plain public prose" {
			t.Fatalf("clean envelope failed: %+v, %v", gotClean, err)
		}
		if gotClean.Disposition != privacy.DispositionUnchanged || gotClean.MarkerCount != 0 {
			t.Fatalf("unexpected disposition/count for clean envelope: %+v", gotClean)
		}
	})

	t.Run("no caller mutation on content failure", func(t *testing.T) {
		meta := map[string]string{"key": "original_value"}
		originalContent := "<private>no public content</private>"
		env := &privacy.Envelope{Content: originalContent, Metadata: meta}

		_, err := privacy.ProtectEnvelope(env)
		if err == nil || !errors.Is(err, privacy.ErrRequiredEmpty) {
			t.Fatalf("expected ErrRequiredEmpty, got %v", err)
		}
		if env.Content != originalContent {
			t.Fatalf("caller content was mutated on failure: %q", env.Content)
		}
		if meta["key"] != "original_value" || len(meta) != 1 {
			t.Fatalf("caller metadata was mutated on failure: %+v", meta)
		}
	})

	t.Run("nil envelope pointer rejected", func(t *testing.T) {
		var nilEnv *privacy.Envelope
		if _, err := privacy.ProtectEnvelope(nilEnv); err == nil || !errors.Is(err, privacy.ErrNilEnvelope) {
			t.Fatalf("expected ErrNilEnvelope, got %v", err)
		}
	})

	t.Run("multi-field named envelope happy path", func(t *testing.T) {
		meta := map[string]string{"env": "prod", "trace": "tr-100"}
		fields := map[string]string{"f_sec": "<private>field-secret</private> rest", "f_clean": "plain"}
		named := []privacy.NamedField{
			{Name: "req_sec", Value: "prefix <private>sec-1</private> suffix", Required: true},
			{Name: "opt_sec", Value: "<private>only-secret</private>", Required: false},
			{Name: "literal_res", Value: "[REDACTED]", Required: true},
		}
		env := privacy.Envelope{Content: "Log <private>c-sec</private> ok", Fields: fields, NamedFields: named, Metadata: meta}

		got, err := privacy.ProtectEnvelope(env)
		if err != nil || got.Content != "Log [REDACTED] ok" {
			t.Fatalf("ProtectEnvelope content failed: %+v, %v", got, err)
		}
		if got.Fields["f_sec"] != "[REDACTED] rest" || got.Fields["f_clean"] != "plain" {
			t.Fatalf("unexpected Fields: %+v", got.Fields)
		}
		if got.NamedFields[0].Value != "prefix [REDACTED] suffix" || got.NamedFields[1].Value != "[REDACTED]" || got.NamedFields[2].Value != "[REDACTED]" {
			t.Fatalf("unexpected NamedFields: %+v", got.NamedFields)
		}
		if got.Disposition != privacy.DispositionRedacted || got.MarkerCount != 4 {
			t.Fatalf("expected redacted with 4 markers, got %q, %d", got.Disposition, got.MarkerCount)
		}

		// Deep copy no-mutation verification
		got.Fields["f_sec"], got.NamedFields[0].Value, got.Metadata["env"] = "tampered", "tampered", "tampered"
		if fields["f_sec"] != "<private>field-secret</private> rest" || named[0].Value != "prefix <private>sec-1</private> suffix" || meta["env"] != "prod" {
			t.Fatal("caller Fields, NamedFields, or Metadata was mutated")
		}
	})

	t.Run("multi-field named envelope error and no caller mutation", func(t *testing.T) {
		const canary = "named-field-canary-fail-821"
		origNamed := []privacy.NamedField{
			{Name: "good_field", Value: "Public text", Required: true},
			{Name: "bad_field", Value: "<private>" + canary, Required: true},
		}
		env := &privacy.Envelope{Content: "Safe content", Fields: map[string]string{"k": "v"}, NamedFields: origNamed}

		_, err := privacy.ProtectEnvelope(env)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var privErr *privacy.Error
		if !errors.As(err, &privErr) || privErr.Field != "bad_field" {
			t.Fatalf("expected *privacy.Error with field bad_field, got %v", err)
		}
		if strings.Contains(err.Error(), canary) || strings.Contains(privErr.Field, canary) {
			t.Fatalf("error leaked canary: %s", err.Error())
		}
		if env.NamedFields[1].Value != "<private>"+canary || env.Content != "Safe content" {
			t.Fatalf("caller Envelope was mutated on error: %+v", env)
		}

		// Map field error
		envMap := &privacy.Envelope{Content: "Safe", Fields: map[string]string{"map_bad": "<private>unclosed"}}
		if _, err = privacy.ProtectEnvelope(envMap); err == nil || !errors.As(err, &privErr) || privErr.Field != "map_bad" {
			t.Fatalf("expected error on map_bad, got %v", err)
		}
	})

	t.Run("duplicate named fields and safe canary", func(t *testing.T) {
		const canary = "dup-canary-name-63a"
		named := []privacy.NamedField{
			{Name: canary, Value: "Valid public 1", Required: true},
			{Name: canary, Value: "Valid public 2", Required: true},
		}
		env := &privacy.Envelope{NamedFields: named}

		got, err := privacy.ProtectEnvelope(env)
		if got != nil {
			t.Errorf("expected nil envelope on duplicate named fields, got %v", got)
		}
		if err == nil {
			t.Fatal("expected error on duplicate named fields, got nil")
		}
		var privErr *privacy.Error
		if !errors.As(err, &privErr) || privErr.Code != privacy.ErrCodeValidation || privErr.Field != "named_fields" {
			t.Fatalf("expected ErrCodeValidation on named_fields, got %v", err)
		}
		if strings.Contains(err.Error(), canary) || strings.Contains(privErr.Field, canary) {
			t.Fatalf("error leaked canary: %s", err.Error())
		}
		data, _ := json.Marshal(privErr)
		if strings.Contains(string(data), canary) {
			t.Fatalf("json.Marshal leaked canary: %s", string(data))
		}
		if named[0].Value != "Valid public 1" || named[1].Value != "Valid public 2" {
			t.Fatal("caller named fields mutated on duplicate error")
		}

		// ProtectNamedFields standalone test
		if res, nfErr := privacy.ProtectNamedFields(named...); res != nil || nfErr == nil {
			t.Fatalf("ProtectNamedFields should fail on duplicates: %v, %v", res, nfErr)
		}
	})
}

func TestMetadata(t *testing.T) {
	t.Run("metadata validation passes for clean entries", func(t *testing.T) {
		meta := map[string]string{"owner": "admin", "region": "us-east-1"}
		if err := privacy.ValidateMetadata(meta); err != nil {
			t.Fatalf("unexpected error for clean metadata: %v", err)
		}
		if err := privacy.ValidateMetadata(nil); err != nil {
			t.Fatalf("unexpected error for nil metadata: %v", err)
		}
	})

	t.Run("metadata rejection on private markers in values", func(t *testing.T) {
		badValues := []string{
			"<private>token123</private>",
			"<PRIVATE>TOKEN</PRIVATE>",
			"prefix <Private>val</pRiVaTe> suffix",
			"unclosed <private>secret",
			"stray </private> marker",
			"<private attr=\"x\">",
			"< private>val</private>",
			"<private",
			"</private",
			"prefix </ private> suffix",
		}
		for _, bad := range badValues {
			meta := map[string]string{"key": bad}
			err := privacy.ValidateMetadata(meta)
			if err == nil || !errors.Is(err, privacy.ErrMetadataRejected) {
				t.Fatalf("ValidateMetadata(%q) expected ErrMetadataRejected, got %v", bad, err)
			}
			if !errors.Is(err, privacy.ErrInvalidMarker) {
				t.Fatalf("ValidateMetadata(%q) expected ErrInvalidMarker, got %v", bad, err)
			}
			if strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("metadata error leaked payload: %v", err)
			}
		}
	})

	t.Run("metadata rejection on private markers in keys", func(t *testing.T) {
		meta := map[string]string{"<private>key</private>": "valid_value"}
		if err := privacy.ValidateMetadata(meta); err == nil || !errors.Is(err, privacy.ErrMetadataRejected) {
			t.Fatalf("expected ErrMetadataRejected for key with markers, got %v", err)
		}
	})

	t.Run("metadata rejection prevents envelope protection without mutating caller", func(t *testing.T) {
		meta := map[string]string{"auth": "<private>api-key</private>", "repo": "cortex"}
		content := "Valid public content before redaction"
		env := &privacy.Envelope{Content: content, Metadata: meta}

		_, err := privacy.ProtectEnvelope(env)
		if err == nil || !errors.Is(err, privacy.ErrMetadataRejected) {
			t.Fatalf("ProtectEnvelope expected ErrMetadataRejected, got %v", err)
		}
		if env.Content != content {
			t.Fatalf("caller content was mutated on failure: %q", env.Content)
		}
		if env.Metadata["auth"] != "<private>api-key</private>" || env.Metadata["repo"] != "cortex" {
			t.Fatalf("caller metadata was mutated on failure: %+v", env.Metadata)
		}
	})

	t.Run("metadata invalid utf8 rejected", func(t *testing.T) {
		meta := map[string]string{"valid_key": "bad \xff\xfe value"}
		if err := privacy.ValidateMetadata(meta); err == nil || !errors.Is(err, privacy.ErrInvalidUTF8) {
			t.Fatalf("expected ErrInvalidUTF8, got %v", err)
		}
	})

	t.Run("metadata safe canary labels in keys and values", func(t *testing.T) {
		const canaryKey, canaryVal = "canary-meta-key-55d", "canary-meta-val-99e"
		canaryTests := []struct {
			name     string
			meta     map[string]string
			wantCode privacy.ErrorCode
		}{
			{"marker in key", map[string]string{"<private>" + canaryKey + "</private>": "val"}, privacy.ErrCodeInvalidMarker},
			{"marker in value", map[string]string{canaryKey: "<private>" + canaryVal + "</private>"}, privacy.ErrCodeInvalidMarker},
			{"invalid utf8 in key", map[string]string{canaryKey + "\xff": "val"}, privacy.ErrCodeInvalidUTF8},
			{"invalid utf8 in value", map[string]string{canaryKey: canaryVal + "\xff"}, privacy.ErrCodeInvalidUTF8},
		}
		for _, tc := range canaryTests {
			t.Run(tc.name, func(t *testing.T) {
				err := privacy.ValidateMetadata(tc.meta)
				if err == nil {
					t.Fatalf("expected error for %s, got nil", tc.name)
				}
				var privErr *privacy.Error
				if !errors.As(err, &privErr) || privErr.Code != tc.wantCode || privErr.Field != "metadata" {
					t.Fatalf("expected %q on metadata, got %v", tc.wantCode, err)
				}
				if strings.Contains(err.Error(), canaryKey) || strings.Contains(err.Error(), canaryVal) {
					t.Fatalf("err.Error() leaked canary: %s", err.Error())
				}
				if strings.Contains(privErr.Field, canaryKey) || strings.Contains(privErr.Field, canaryVal) {
					t.Fatalf("privErr.Field leaked canary: %s", privErr.Field)
				}
				data, _ := json.Marshal(privErr)
				if strings.Contains(string(data), canaryKey) || strings.Contains(string(data), canaryVal) {
					t.Fatalf("json.Marshal leaked canary: %s", string(data))
				}
			})
		}
	})
}
