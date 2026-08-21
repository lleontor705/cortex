package vectorhydration

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func analysisInput(t *testing.T, ratio float64) AnalysisInput {
	t.Helper()
	m := testManifest(t)
	results := make([]Result, 0, len(m.Schedule)*2)
	base := time.Unix(100, 0).UTC()
	for i, e := range m.Schedule {
		for _, measurement := range []Measurement{MeasurementLegacy, MeasurementBatch} {
			ns := 100.0
			if measurement == MeasurementLegacy {
				ns = (6.0 + ratio*float64(e.Block)) * 100
			}
			seq := i*2 + 1
			if (e.Order == OrderAB && measurement == MeasurementBatch) || (e.Order == OrderBA && measurement == MeasurementLegacy) {
				seq++
			}
			pid := len(results) + 1
			results = append(results, Result{Cell: e.Cell, Block: e.Block, Order: e.Order, BlockID: e.BlockID, RunID: e.RunID, Sequence: seq, Measurement: measurement, PID: pid, ProcessIdentity: fmt.Sprintf("fake:%d", pid), StartedAt: base, FinishedAt: base.Add(time.Nanosecond), ExitCode: 0, NsPerOp: ns, AllocsPerOp: 2, StdoutSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", StderrSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"})
			results[len(results)-1].Request.Env = []string{fmt.Sprintf("GOMAXPROCS=%d", e.Cell)}
			results[len(results)-1].Request.Identity = sealedRequestIdentity(m, e, measurement, seq)
		}
	}
	raw := map[string][]byte{}
	for i := range results {
		if results[i].Measurement == MeasurementLegacy {
			results[i].Benchmark = m.LegacyBenchmark
		} else {
			results[i].Benchmark = m.BatchBenchmark
		}
		results[i].BytesPerOp = 3
		data := []byte(fmt.Sprintf("%s-8 1 %g ns/op 3 B/op 2 allocs/op\n", results[i].Benchmark, results[i].NsPerOp))
		results[i].StdoutSHA256 = hashBytes(data)
		stdoutName := fmt.Sprintf("stdout-%03d", i)
		stderrName := fmt.Sprintf("stderr-%03d", i)
		results[i].RawStdout = stdoutName
		raw[stdoutName] = data
		results[i].StderrSHA256 = hashBytes(nil)
		results[i].RawStderr = stderrName
		raw[stderrName] = nil
	}
	artifacts := map[string]GateArtifact{}
	for _, name := range []string{"semantic", "query", "allocation"} {
		b, err := json.Marshal(gateArtifactJSON{SchemaVersion: gateSchemaVersion, CampaignID: CampaignID, AmendmentVersion: AmendmentVersion, ManifestDigest: m.ManifestDigest(), SourceCommit: m.SourceCommit, PhaseID: m.Phase.ID, TestBinarySHA256: strings.Repeat("c", 64), GateName: name, Command: "reference-gate --" + name, Exit: 0, Result: "PASS"})
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = GateArtifact{Content: b, SHA256: hashBytes(b)}
	}
	trusted := map[string]string{}
	for name, artifact := range artifacts {
		trusted[name] = artifact.SHA256
	}
	registry := TrustRegistry{SchemaVersion: trustSchemaVersion, CampaignID: CampaignID, AmendmentVersion: AmendmentVersion, PhaseID: m.Phase.ID, SourceCommit: m.SourceCommit, TestBinarySHA256: strings.Repeat("c", 64), Gates: trusted}
	binaryPath := t.TempDir() + "/benchmark"
	binaryData := []byte("prepared benchmark")
	if err := os.WriteFile(binaryPath, binaryData, 0600); err != nil {
		t.Fatal(err)
	}
	registry.TestBinarySHA256 = hashBytes(binaryData)
	for name := range artifacts {
		var g gateArtifactJSON
		if err := json.Unmarshal(artifacts[name].Content, &g); err != nil {
			t.Fatal(err)
		}
		g.TestBinarySHA256 = registry.TestBinarySHA256
		b, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = GateArtifact{Content: b, SHA256: hashBytes(b)}
		trusted[name] = artifacts[name].SHA256
		registry.Gates[name] = artifacts[name].SHA256
	}
	rb, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	return AnalysisInput{Manifest: m, Results: results, Raw: raw, Binary: BenchmarkBinary{SourceCommit: m.SourceCommit, BinaryPath: binaryPath, SHA256: registry.TestBinarySHA256}, Gates: ExternalGates{artifacts: artifacts, trustedSHA256: trusted, registry: registry, registrySHA256: hashBytes(rb)}}
}

func TestAnalyzeGoldenAndSeedReproducibility(t *testing.T) {
	in := analysisInput(t, .01)
	a, b := Analyze(in), Analyze(in)
	if a.Status != "PASS" || len(a.Cells) != 3 {
		t.Fatalf("golden report: %#v", a)
	}
	if a.Seed != in.Manifest.Seed || a.Resamples != BootstrapResamples || a.GuardMargin != GuardMargin {
		t.Fatalf("protocol fields: %#v", a)
	}
	for i := range a.Cells {
		if a.Cells[i] != b.Cells[i] {
			t.Fatalf("non-deterministic cell %d: %#v %#v", i, a.Cells[i], b.Cells[i])
		}
	}
}

func TestAnalyzeEqualityAndBelowThreshold(t *testing.T) {
	in := analysisInput(t, 0)
	if got := Analyze(in); got.Status != "FAIL" {
		t.Fatalf("degenerate equality must fail closed: %#v", got)
	}
	in = analysisInput(t, .01)
	for i := range in.Results {
		if in.Results[i].Cell == 4 {
			if in.Results[i].Measurement == MeasurementLegacy {
				in.Results[i].NsPerOp = 509
			} else {
				in.Results[i].NsPerOp = 100
			}
		}
	}
	if got := Analyze(in); got.Status != "FAIL" {
		t.Fatalf("one cell below must fail: %#v", got)
	}
}

func TestAnalyzeRejectsInvalidRetentionAndProcess(t *testing.T) {
	tests := []func(*AnalysisInput){
		func(in *AnalysisInput) { in.Results = in.Results[:len(in.Results)-1] },
		func(in *AnalysisInput) { in.Results[0].Failed = true },
		func(in *AnalysisInput) { in.Results[0].FinishedAt = in.Results[0].StartedAt },
		func(in *AnalysisInput) { in.Results[0].NsPerOp = math.NaN() },
	}
	for _, mutate := range tests {
		in := analysisInput(t, .01)
		mutate(&in)
		if got := Analyze(in); got.Status != "FAIL" {
			t.Fatalf("invalid input accepted: %#v", got)
		}
	}
}

func TestAnalyzeSealsGOMAXPROCSAndRequestIdentity(t *testing.T) {
	for _, mutate := range []func(*AnalysisInput){
		func(in *AnalysisInput) { in.Results[0].Request.Env = []string{"GOMAXPROCS=1", "gomaxprocs=1"} },
		func(in *AnalysisInput) { in.Results[0].Request.Env = []string{"GOMAXPROCS=2"} },
		func(in *AnalysisInput) { in.Results[0].Request.Identity = "forged" },
	} {
		in := analysisInput(t, .01)
		mutate(&in)
		if got := Analyze(in); got.Status != "FAIL" {
			t.Fatalf("accepted unsealed request: %#v", got)
		}
	}
}

func TestAnalyzeRequiresExternalGates(t *testing.T) {
	in := analysisInput(t, .01)
	in.Gates.artifacts = nil
	if got := Analyze(in); got.Status != "BLOCKED" {
		t.Fatalf("missing gates: %#v", got)
	}
}

func TestAnalyzeRejectsNonzeroGateExit(t *testing.T) {
	for _, exit := range []int{-1, 1} {
		in := analysisInput(t, .01)
		var artifact gateArtifactJSON
		if err := json.Unmarshal(in.Gates.artifacts["semantic"].Content, &artifact); err != nil {
			t.Fatal(err)
		}
		artifact.Exit = exit
		content, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		in.Gates.artifacts["semantic"] = GateArtifact{Content: content, SHA256: hashBytes(content)}
		in.Gates.trustedSHA256["semantic"] = hashBytes(content)
		in.Gates.registry.Gates["semantic"] = hashBytes(content)
		got := Analyze(in)
		if got.Status != "BLOCKED" {
			t.Fatalf("exit %d accepted: %#v", exit, got)
		}
		if len(got.Reasons) != 1 || got.Reasons[0] != "invalid or missing gate artifact: semantic" {
			t.Fatalf("exit %d reached the wrong validation class: %#v", exit, got.Reasons)
		}
	}
}

func TestAnalyzeRejectsReusedAndUnreferencedRaw(t *testing.T) {
	tests := []func(*AnalysisInput){
		func(in *AnalysisInput) { in.Results[1].RawStdout = in.Results[0].RawStdout },
		func(in *AnalysisInput) { in.Raw["padding.stdout"] = []byte("padding") },
	}
	for _, mutate := range tests {
		in := analysisInput(t, .01)
		mutate(&in)
		if got := Analyze(in); got.Status != "FAIL" {
			t.Fatalf("invalid raw consumption accepted: %#v", got)
		}
	}
}

func TestAnalyzeReplayRejectsHyphenatedUnexpectedBenchmark(t *testing.T) {
	in := analysisInput(t, .01)
	result := in.Results[0]
	unexpected := append(append([]byte(nil), in.Raw[result.RawStdout]...), []byte(fmt.Sprintf("BenchmarkUnexpected/foo-bar-8 1 %g ns/op %g B/op %g allocs/op\n", result.NsPerOp, result.BytesPerOp, result.AllocsPerOp))...)
	in.Raw[result.RawStdout] = unexpected
	in.Results[0].StdoutSHA256 = hashBytes(unexpected)
	if got := Analyze(in); got.Status != "FAIL" {
		t.Fatalf("hyphenated unexpected benchmark accepted during replay: %#v", got)
	}
}

func TestAnalyzeReplayRejectsTerminalZeroBenchmarkSuffix(t *testing.T) {
	in := analysisInput(t, .01)
	result := in.Results[0]
	zero := []byte(fmt.Sprintf("%s-0 1 %g ns/op %g B/op %g allocs/op\n", result.Benchmark, result.NsPerOp, result.BytesPerOp, result.AllocsPerOp))
	in.Raw[result.RawStdout] = zero
	in.Results[0].StdoutSHA256 = hashBytes(zero)
	if got := Analyze(in); got.Status != "FAIL" {
		t.Fatalf("terminal -0 benchmark accepted during replay: %#v", got)
	}
}

func TestBCaGoldenVectors(t *testing.T) {
	vectors := [][]float64{
		{0.7, 1.1, 1.8, 2.4, 3.2, 3.9, 4.6, 5.4, 6.1, 6.8, 7.5, 8.3, 9.0, 9.7, 10.4, 11.2, 11.9, 12.6, 13.3, 14.1},
		{0.2, 0.3, 0.4, 0.6, 0.8, 1.1, 1.5, 2.0, 2.7, 3.6, 4.8, 6.3, 8.1, 10.4, 13.2, 16.7, 21.1, 26.8, 34.0, 43.2},
		{4.91, 4.95, 4.98, 5.01, 5.03, 5.05, 5.07, 5.08, 5.09, 5.095, 5.099, 5.101, 5.105, 5.11, 5.12, 5.14, 5.17, 5.21, 5.28, 5.36},
	}
	want := []float64{4.25, 1.3, 5.065}
	for i, v := range vectors {
		if got := bcaLower(v, uint64(700+i)); math.Abs(got-want[i]) > 1e-12 {
			t.Fatalf("vector %d: got %.17g want %.17g", i, got, want[i])
		}
	}
}

func TestBCaDegenerateAndGateBindingFailClosed(t *testing.T) {
	flat := make([]float64, PairedBlocksPerCell)
	for i := range flat {
		flat[i] = 5.1
	}
	if got := bcaLower(flat, 701); !math.IsNaN(got) {
		t.Fatalf("degenerate BCa must be NaN, got %v", got)
	}
	in := analysisInput(t, .01)
	var forged gateArtifactJSON
	if err := json.Unmarshal(in.Gates.artifacts["semantic"].Content, &forged); err != nil {
		t.Fatal(err)
	}
	forged.PhaseID = "other-phase"
	b, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	in.Gates.artifacts["semantic"] = GateArtifact{Content: b, SHA256: hashBytes(b)}
	in.Gates.trustedSHA256["semantic"] = hashBytes(b)
	in.Gates.registry.Gates["semantic"] = hashBytes(b)
	in.Gates.registry.PhaseID = in.Manifest.Phase.ID
	got := Analyze(in)
	if got.Status != "BLOCKED" {
		t.Fatalf("mismatched gate accepted: %#v", got)
	}
	if len(got.Reasons) != 1 || got.Reasons[0] != "invalid or missing gate artifact: semantic" {
		t.Fatalf("mismatched gate reached the wrong validation class: %#v", got.Reasons)
	}
}
