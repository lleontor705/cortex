package vectorhydration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func testManifest(t *testing.T) Manifest {
	schedule, _ := Schedule(42, CampaignID, "phase", "run")
	return Manifest{SchemaVersion: SchemaVersion, Campaign: CampaignManifest{AmendmentVersion, CampaignID}, Phase: PhaseManifest{PhaseSchemaVersion, "phase"}, Run: RunManifest{RunSchemaVersion, "run"}, SourceMachine: SourceMachineManifest{SourceSchemaVersion, "machine", "windows", "amd64", "cpu"}, SourceCommit: "0123456789abcdef0123456789abcdef01234567", BenchmarkPackage: BenchmarkPackage, LegacyBenchmark: LegacyBenchmark, BatchBenchmark: BatchBenchmark, Seed: 42, Schedule: schedule}
}
func TestManifestRejectsInvalidV2Bindings(t *testing.T) {
	checks := []struct {
		name string
		edit func(*Manifest)
	}{
		{"short commit", func(m *Manifest) { m.SourceCommit = "0123456789abcdef" }},
		{"uppercase commit", func(m *Manifest) { m.SourceCommit = "0123456789ABCDEF0123456789abcdef01234567" }},
		{"zero commit", func(m *Manifest) { m.SourceCommit = "0000000000000000000000000000000000000000" }},
		{"unsafe package", func(m *Manifest) { m.BenchmarkPackage = "../internal/store/sqlite" }},
		{"unsafe benchmark", func(m *Manifest) { m.LegacyBenchmark = "Benchmark/legacy" }},
		{"duplicate benchmarks", func(m *Manifest) { m.BatchBenchmark = m.LegacyBenchmark }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			m := testManifest(t)
			check.edit(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("invalid v2 binding accepted")
			}
		})
	}
}
func TestManifestRejectsV1AndUnknownSchemas(t *testing.T) {
	for _, schema := range []string{"vec-bench-stat/v1", "vec-bench-stat/v3", "unknown"} {
		t.Run(schema, func(t *testing.T) {
			m := testManifest(t)
			m.SchemaVersion = schema
			if err := m.Validate(); err == nil {
				t.Fatal("unsupported schema accepted")
			}
		})
	}
}
func TestManifestValidationAndCanonicalHash(t *testing.T) {
	m := testManifest(t)
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	a, err := m.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.CanonicalJSON()
	if string(a) != string(b) {
		t.Fatal("canonical JSON is unstable")
	}
	h, err := m.SHA256()
	if err != nil || len(h) != 64 {
		t.Fatalf("hash: %q %v", h, err)
	}
}
func TestManifestRejectsScheduleShape(t *testing.T) {
	m := testManifest(t)
	m.Schedule[1], m.Schedule[2] = m.Schedule[2], m.Schedule[1]
	if err := m.Validate(); err == nil {
		t.Fatal("out-of-order schedule accepted")
	}
	m = testManifest(t)
	m.Schedule = append(m.Schedule, m.Schedule[0])
	if err := m.Validate(); err == nil {
		t.Fatal("extra schedule entry accepted")
	}
}
func TestManifestJSONIngestionIsStrict(t *testing.T) {
	b, err := json.Marshal(testManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	checks := []string{
		`{"schema_version":"vec-bench-stat/v2","unknown":true}`,
		`{"schema_version":"vec-bench-stat/v2","campaign":{"version":"1.0.0-draft.1","id":"VEC-BENCH-STAT","extra":true}}`,
		`{"schema_version":"vec-bench-stat/v2","schema_version":"vec-bench-stat/v2"}`,
		`{"schema_version":"vec-bench-stat/v2","campaign":{"version":"1.0.0-draft.1","id":"VEC-BENCH-STAT","id":"duplicate"}}`,
		`{"schema_version":"vec-bench-stat/v2","schedule":[{"entry":1,"entry":2}]}`,
		string(b) + ` {}`,
	}
	for _, input := range checks {
		var got Manifest
		if err := json.Unmarshal([]byte(input), &got); err == nil {
			t.Fatalf("accepted invalid manifest JSON: %s", input)
		}
	}
	var got Manifest
	if err := json.Unmarshal(append(b, []byte(" \t\n")...), &got); err != nil {
		t.Fatalf("rejected trailing whitespace: %v", err)
	}
}
func TestManifestDigestMatchesIndependentSHA256(t *testing.T) {
	m := testManifest(t)
	b, err := m.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := sha256.Sum256(b)
	want := hex.EncodeToString(wantBytes[:])
	got, err := m.SHA256()
	if err != nil || got != want {
		t.Fatalf("digest %q, want independent digest %q", got, want)
	}
	m.SourceCommit = strings.Repeat("f", 40)
	changed, err := m.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if changed == got {
		t.Fatal("changing source_commit did not change digest")
	}
}
