package vectorhydration

import (
	"encoding/json"
	"strings"
	"testing"
)

func testBinaryIdentity() BinaryIdentity {
	return BinaryIdentity{SchemaVersion: BinaryIdentitySchemaVersion, BinarySHA256: strings.Repeat("a", 64), SourceCommit: strings.Repeat("b", 40), SourceTreeSHA256: strings.Repeat("c", 64), ToolSHA256: strings.Repeat("d", 64), ArgvSHA256: strings.Repeat("e", 64), ToolVersion: "go1.26.5", BuildIdentity: ApprovedBuildIdentity}
}

func testProtocolIdentity() ProtocolIdentity {
	return ProtocolIdentity{SchemaVersion: ProtocolIdentitySchemaVersion, Campaign: CampaignID, Amendment: AmendmentVersion, Phase: QualificationPhase, ManifestSHA256: strings.Repeat("a", 64), ScheduleSHA256: strings.Repeat("b", 64), LegacyMeasurementSHA256: strings.Repeat("c", 64), BatchMeasurementSHA256: strings.Repeat("d", 64), Resamples: 100000, Confidence: .95, LCBThreshold: 5.0, GuardThreshold: 5.10, EnvironmentSHA256: strings.Repeat("e", 64)}
}

func TestIdentityValidationRequiresEveryField(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*BinaryIdentity)
	}{
		{"schema", func(x *BinaryIdentity) { x.SchemaVersion = "" }},
		{"binary digest", func(x *BinaryIdentity) { x.BinarySHA256 = "" }},
		{"commit", func(x *BinaryIdentity) { x.SourceCommit = "" }},
		{"tree digest", func(x *BinaryIdentity) { x.SourceTreeSHA256 = "" }},
		{"tool digest", func(x *BinaryIdentity) { x.ToolSHA256 = "" }},
		{"argv digest", func(x *BinaryIdentity) { x.ArgvSHA256 = "" }},
		{"tool version", func(x *BinaryIdentity) { x.ToolVersion = "" }},
		{"build identity", func(x *BinaryIdentity) { x.BuildIdentity = "" }},
	} {
		t.Run("binary/"+tc.name, func(t *testing.T) {
			x := testBinaryIdentity()
			tc.mutate(&x)
			if x.Validate() == nil {
				t.Fatal("accepted omitted required field")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ProtocolIdentity)
	}{
		{"schema", func(x *ProtocolIdentity) { x.SchemaVersion = "" }}, {"campaign", func(x *ProtocolIdentity) { x.Campaign = "" }},
		{"amendment", func(x *ProtocolIdentity) { x.Amendment = "" }}, {"phase", func(x *ProtocolIdentity) { x.Phase = "" }},
		{"manifest", func(x *ProtocolIdentity) { x.ManifestSHA256 = "" }}, {"schedule", func(x *ProtocolIdentity) { x.ScheduleSHA256 = "" }},
		{"legacy measurement", func(x *ProtocolIdentity) { x.LegacyMeasurementSHA256 = "" }}, {"batch measurement", func(x *ProtocolIdentity) { x.BatchMeasurementSHA256 = "" }},
		{"resamples", func(x *ProtocolIdentity) { x.Resamples = 0 }}, {"confidence", func(x *ProtocolIdentity) { x.Confidence = 0 }},
		{"lcb", func(x *ProtocolIdentity) { x.LCBThreshold = 0 }}, {"guard", func(x *ProtocolIdentity) { x.GuardThreshold = 0 }},
		{"environment", func(x *ProtocolIdentity) { x.EnvironmentSHA256 = "" }},
	} {
		t.Run("protocol/"+tc.name, func(t *testing.T) {
			x := testProtocolIdentity()
			tc.mutate(&x)
			if x.Validate() == nil {
				t.Fatal("accepted omitted required field")
			}
		})
	}
}

func TestDigestFieldsRejectEmptyZeroUppercaseAndMalformed(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("0", 64), strings.Repeat("A", 64), "not-a-digest"} {
		for _, tc := range []struct {
			name   string
			mutate func(*BinaryIdentity, string)
		}{
			{"binary", func(x *BinaryIdentity, v string) { x.BinarySHA256 = v }}, {"tree", func(x *BinaryIdentity, v string) { x.SourceTreeSHA256 = v }},
			{"tool", func(x *BinaryIdentity, v string) { x.ToolSHA256 = v }}, {"argv", func(x *BinaryIdentity, v string) { x.ArgvSHA256 = v }},
		} {
			t.Run("binary/"+tc.name+"/"+value, func(t *testing.T) {
				x := testBinaryIdentity()
				tc.mutate(&x, value)
				if x.Validate() == nil {
					t.Fatal("accepted invalid digest")
				}
			})
		}
		for _, tc := range []struct {
			name   string
			mutate func(*ProtocolIdentity, string)
		}{
			{"manifest", func(x *ProtocolIdentity, v string) { x.ManifestSHA256 = v }}, {"schedule", func(x *ProtocolIdentity, v string) { x.ScheduleSHA256 = v }},
			{"legacy", func(x *ProtocolIdentity, v string) { x.LegacyMeasurementSHA256 = v }}, {"batch", func(x *ProtocolIdentity, v string) { x.BatchMeasurementSHA256 = v }},
			{"environment", func(x *ProtocolIdentity, v string) { x.EnvironmentSHA256 = v }},
		} {
			t.Run("protocol/"+tc.name+"/"+value, func(t *testing.T) {
				x := testProtocolIdentity()
				tc.mutate(&x, value)
				if x.Validate() == nil {
					t.Fatal("accepted invalid digest")
				}
			})
		}
	}
}

func TestIdentityDigestMutationMatrix(t *testing.T) {
	b, baseB := testBinaryIdentity(), testBinaryIdentity()
	bd, _ := b.Digest()
	for _, tc := range []struct {
		name   string
		mutate func(*BinaryIdentity)
	}{
		{"schema", func(x *BinaryIdentity) { x.SchemaVersion = BinaryIdentitySchemaVersion + "-alt" }},
		{"binary", func(x *BinaryIdentity) { x.BinarySHA256 = strings.Repeat("f", 64) }}, {"commit", func(x *BinaryIdentity) { x.SourceCommit = strings.Repeat("a", 40) }},
		{"tree", func(x *BinaryIdentity) { x.SourceTreeSHA256 = strings.Repeat("f", 64) }}, {"tool", func(x *BinaryIdentity) { x.ToolSHA256 = strings.Repeat("f", 64) }},
		{"argv", func(x *BinaryIdentity) { x.ArgvSHA256 = strings.Repeat("f", 64) }}, {"version", func(x *BinaryIdentity) { x.ToolVersion = "go1.25.1" }},
		{"build", func(x *BinaryIdentity) { x.BuildIdentity = ApprovedBuildIdentity + "-alt" }},
	} {
		t.Run("binary/"+tc.name, func(t *testing.T) {
			x := baseB
			tc.mutate(&x)
			if _, err := x.Digest(); err == nil && mustDigest(t, x) == bd {
				t.Fatal("mutation did not change digest")
			}
		})
	}
	p, baseP := testProtocolIdentity(), testProtocolIdentity()
	pd, _ := p.Digest()
	for _, tc := range []struct {
		name   string
		mutate func(*ProtocolIdentity)
	}{
		{"phase", func(x *ProtocolIdentity) { x.Phase = HeldOutPhase }}, {"manifest", func(x *ProtocolIdentity) { x.ManifestSHA256 = strings.Repeat("f", 64) }},
		{"schedule", func(x *ProtocolIdentity) { x.ScheduleSHA256 = strings.Repeat("f", 64) }}, {"legacy", func(x *ProtocolIdentity) { x.LegacyMeasurementSHA256 = strings.Repeat("f", 64) }},
		{"batch", func(x *ProtocolIdentity) { x.BatchMeasurementSHA256 = strings.Repeat("f", 64) }}, {"environment", func(x *ProtocolIdentity) { x.EnvironmentSHA256 = strings.Repeat("f", 64) }},
	} {
		t.Run("protocol/"+tc.name, func(t *testing.T) {
			x := baseP
			tc.mutate(&x)
			if mustDigest(t, x) == pd {
				t.Fatal("mutation did not change digest")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*ProtocolIdentity)
	}{
		{"schema", func(x *ProtocolIdentity) { x.SchemaVersion += "-alt" }}, {"campaign", func(x *ProtocolIdentity) { x.Campaign += "-alt" }}, {"amendment", func(x *ProtocolIdentity) { x.Amendment += "-alt" }},
	} {
		t.Run("protocol/rejected/"+tc.name, func(t *testing.T) {
			x := baseP
			tc.mutate(&x)
			if x.Validate() == nil {
				t.Fatal("accepted immutable drift")
			}
		})
	}
}

func mustDigest(t *testing.T, v interface{ Digest() (string, error) }) string {
	t.Helper()
	d, err := v.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestPublicationBinding(t *testing.T) {
	b, p := testBinaryIdentity(), testProtocolIdentity()
	bd, _ := b.Digest()
	pd, _ := p.Digest()
	if err := (PublicationBinding{BinaryIdentitySHA256: bd, ProtocolIdentitySHA256: pd}).Validate(b, p); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []PublicationBinding{{BinaryIdentitySHA256: "", ProtocolIdentitySHA256: pd}, {BinaryIdentitySHA256: bd, ProtocolIdentitySHA256: strings.Repeat("0", 64)}, {BinaryIdentitySHA256: strings.Repeat("0", 64), ProtocolIdentitySHA256: pd}, {BinaryIdentitySHA256: strings.Repeat("f", 64), ProtocolIdentitySHA256: pd}, {BinaryIdentitySHA256: bd, ProtocolIdentitySHA256: strings.Repeat("f", 64)}} {
		if err := binding.Validate(b, p); err == nil {
			t.Fatal("accepted invalid publication binding")
		}
	}
}

func TestIdentityValidationRejectsPortableAndProtocolDrift(t *testing.T) {
	b := testBinaryIdentity()
	for name, mutate := range map[string]func(*BinaryIdentity){
		"missing tool version": func(x *BinaryIdentity) { x.ToolVersion = "" },
		"path tool version":    func(x *BinaryIdentity) { x.ToolVersion = "go1.26.5 /tmp/tool" },
		"host tool version":    func(x *BinaryIdentity) { x.ToolVersion = "go1.26.5@builder" },
		"user host":            func(x *BinaryIdentity) { x.ToolVersion = "user@host" },
		"username path":        func(x *BinaryIdentity) { x.ToolVersion = "username/path" },
		"shell expansion":      func(x *BinaryIdentity) { x.ToolVersion = "go$VERSION" },
		"separator":            func(x *BinaryIdentity) { x.ToolVersion = "go1.26.5\\tool" },
		"whitespace":           func(x *BinaryIdentity) { x.ToolVersion = " go1.26.5" },
		"pid tool version":     func(x *BinaryIdentity) { x.ToolVersion = "go1.26.5-pid123" },
		"unsupported build":    func(x *BinaryIdentity) { x.BuildIdentity = "linux-amd64" },
		"build path":           func(x *BinaryIdentity) { x.BuildIdentity = "go-test-c-trimpath-v1 /tmp" },
	} {
		t.Run(name, func(t *testing.T) {
			mutate(&b)
			if err := b.Validate(); err == nil {
				t.Fatal("accepted non-portable binary identity")
			}
			b = testBinaryIdentity()
		})
	}

	p := testProtocolIdentity()
	for name, mutate := range map[string]func(*ProtocolIdentity){
		"phase drift":                func(x *ProtocolIdentity) { x.Phase = "phase-1.0.0" },
		"missing legacy measurement": func(x *ProtocolIdentity) { x.LegacyMeasurementSHA256 = "" },
		"missing batch measurement":  func(x *ProtocolIdentity) { x.BatchMeasurementSHA256 = "" },
		"resample drift":             func(x *ProtocolIdentity) { x.Resamples++ },
		"confidence drift":           func(x *ProtocolIdentity) { x.Confidence = .90 },
		"lcb drift":                  func(x *ProtocolIdentity) { x.LCBThreshold = 4.99 },
		"guard drift":                func(x *ProtocolIdentity) { x.GuardThreshold = 5.11 },
	} {
		t.Run(name, func(t *testing.T) {
			x := p
			mutate(&x)
			if err := x.Validate(); err == nil {
				t.Fatal("accepted protocol drift")
			}
		})
	}
}

func TestProtocolIdentityStrictJSON(t *testing.T) {
	p := testProtocolIdentity()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ProtocolIdentity
	for _, bad := range []string{
		string(raw) + " {}",
		`{"schema_version":"` + ProtocolIdentitySchemaVersion + `","unknown":1}`,
		string(raw[:len(raw)-1]) + `,"phase":"qualification"}`,
	} {
		if json.Unmarshal([]byte(bad), &decoded) == nil {
			t.Fatalf("accepted invalid protocol JSON %s", bad)
		}
	}
}

func TestBinaryIdentityStrictJSON(t *testing.T) {
	b := testBinaryIdentity()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var decoded BinaryIdentity
	for _, bad := range []string{string(raw) + " {}", `{"schema_version":"` + BinaryIdentitySchemaVersion + `","unknown":1}`, string(raw[:len(raw)-1]) + `,"schema_version":"` + BinaryIdentitySchemaVersion + `"}`} {
		if json.Unmarshal([]byte(bad), &decoded) == nil {
			t.Fatalf("accepted invalid binary JSON %s", bad)
		}
	}
}
