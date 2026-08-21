package vectorhydration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeExecutor struct {
	requests []ProcessRequest
	output   []byte
	exit     int
	err      error
	pid      int
}

func (f *fakeExecutor) Execute(_ context.Context, r ProcessRequest) Execution {
	f.requests = append(f.requests, r)
	out := f.output
	if len(r.Args) > 1 && r.Args[1] == string(MeasurementBatch) {
		out = []byte(strings.Replace(string(out), LegacyBenchmark, BatchBenchmark, 1))
	}
	f.pid++
	return Execution{Stdout: out, ExitCode: f.exit, Err: f.err, PID: f.pid, ProcessIdentity: "fake" + string(rune('0'+f.pid%10))}
}

type fakeClock struct{ n int }

func (f *fakeClock) Now() time.Time { f.n++; return time.Unix(int64(f.n), 0).UTC() }

func collectorManifest(t *testing.T) Manifest {
	t.Helper()
	return testManifest(t)
}
func collectorConfig(dir string, f *fakeExecutor) CollectorConfig {
	return CollectorConfig{OutputDir: dir, Executor: f, Clock: &fakeClock{}, Measurements: []Measurement{MeasurementLegacy, MeasurementBatch}, Command: func(e ScheduleEntry, m Measurement) (ProcessRequest, error) {
		return ProcessRequest{Executable: "bench", Args: []string{string(e.Order), string(m)}, Env: []string{"GOMAXPROCS=" + string(rune('0'+e.Cell))}}, nil
	}}
}

func TestCollectorOrderCompletenessAndRawHashes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	f := &fakeExecutor{output: []byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\n")}
	m := collectorManifest(t)
	if err := Collect(context.Background(), m, collectorConfig(dir, f)); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 120 || string(f.requests[0].Args[0]) != string(m.Schedule[0].Order) || string(f.requests[1].Args[0]) != string(m.Schedule[0].Order) {
		t.Fatalf("unexpected invocation order: %#v", f.requests)
	}
	var om outputManifest
	b, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err := json.Unmarshal(b, &om); err != nil {
		t.Fatal(err)
	}
	if om.ResultCount != 120 || len(om.Raw) != 240 {
		t.Fatalf("incomplete output: %#v", om)
	}
	format, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if strings.Contains(string(format), "identity_sha256") {
		t.Fatalf("temporary output format claims external identity binding: %s", format)
	}
	var records []Result
	rb, _ := os.ReadFile(filepath.Join(dir, "records.json"))
	if err := json.Unmarshal(rb, &records); err != nil || len(records) != 120 {
		t.Fatal("records were not persisted")
	}
	for _, record := range records {
		if strings.Contains(record.Request.Identity, "binary:") || strings.Contains(record.Request.Identity, "protocol:") {
			t.Fatalf("request identity claims external identity binding: %q", record.Request.Identity)
		}
	}
	if err := verifyRawHashes(dir, om.Raw); err != nil {
		t.Fatal(err)
	}
}

func TestCollectorUsesManifestBenchmarkIdentity(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	f := &fakeExecutor{output: []byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\n")}
	cfg := collectorConfig(dir, f)
	cfg.Measurements = []Measurement{MeasurementLegacy}
	if err := Collect(context.Background(), collectorManifest(t), cfg); err != nil {
		t.Fatalf("manifest benchmark identity rejected: %v", err)
	}
}

func TestParseBenchmarkRequiresManifestIdentityAndGoSuffix(t *testing.T) {
	if _, _, _, _, err := parseBenchmarkExpected([]byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\n"), LegacyBenchmark); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{
		"BenchmarkHydrateLegacyGetByID_N100/c1-8 1 12 ns/op 3 B/op 2 allocs/op\n",
		"BenchmarkHydrateLegacyGetByID_N100 1 12 ns/op 3 B/op 2 allocs/op\n",
		"BenchmarkHydrateLegacyGetByID_N100-0 1 12 ns/op 3 B/op 2 allocs/op\n",
	} {
		if _, _, _, _, err := parseBenchmarkExpected([]byte(output), LegacyBenchmark); err == nil {
			t.Fatalf("accepted non-manifest benchmark identity %q", output)
		}
	}
}

func TestParseBenchmarkRequiresExactlyOneManifestRecord(t *testing.T) {
	for _, output := range []string{
		"BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\nBenchmarkHydrateLegacyGetByID_N100-8 1 13 ns/op 3 B/op 2 allocs/op\n",
		"BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\nBenchmarkHydrateBatchGetByIDs_N100-8 1 13 ns/op 3 B/op 2 allocs/op\n",
		"BenchmarkUnexpected-8 1 12 ns/op 3 B/op 2 allocs/op\n",
		"BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\nBenchmarkUnexpected/foo-bar-8 1 12 ns/op 3 B/op 2 allocs/op\n",
	} {
		if _, _, _, _, err := parseBenchmarkExpected([]byte(output), LegacyBenchmark); err == nil {
			t.Fatalf("accepted benchmark candidates %q", output)
		}
	}
}

func TestCollectorRejectsHyphenatedUnexpectedBenchmark(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	f := &fakeExecutor{output: []byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\nBenchmarkUnexpected/foo-bar-8 1 12 ns/op 3 B/op 2 allocs/op\n")}
	if err := Collect(context.Background(), collectorManifest(t), collectorConfig(dir, f)); err == nil {
		t.Fatal("hyphenated unexpected benchmark was accepted by collection")
	}
}

func TestCollectorRetainsFailureAndRefusesOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	f := &fakeExecutor{output: []byte("ignored"), exit: 7}
	if err := Collect(context.Background(), collectorManifest(t), collectorConfig(dir, f)); err != nil {
		t.Fatal(err)
	}
	var om outputManifest
	b, _ := os.ReadFile(filepath.Join(dir, "manifest.json"))
	_ = json.Unmarshal(b, &om)
	var records []Result
	b, _ = os.ReadFile(filepath.Join(dir, "records.json"))
	_ = json.Unmarshal(b, &records)
	if len(records) != 120 || !records[0].Failed {
		t.Fatal("failed result was not retained")
	}
	if err := Collect(context.Background(), collectorManifest(t), collectorConfig(dir, &fakeExecutor{})); err == nil {
		t.Fatal("overwrite accepted")
	}
}

func TestCollectorRejectsMalformedAndUnknownOutput(t *testing.T) {
	for _, out := range [][]byte{[]byte("not benchmark"), []byte("BenchmarkNope-8 1 1 ns/op\n")} {
		dir := filepath.Join(t.TempDir(), "out")
		f := &fakeExecutor{output: out}
		if err := Collect(context.Background(), collectorManifest(t), collectorConfig(dir, f)); err == nil {
			t.Fatalf("accepted %q", out)
		}
	}
}

func TestCollectorFailureRemovesRacedCompletionMarker(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	m := collectorManifest(t)
	cfg := collectorConfig(dir, &fakeExecutor{output: []byte("ignored")})
	cfg.Command = func(ScheduleEntry, Measurement) (ProcessRequest, error) {
		if err := os.WriteFile(filepath.Join(dir, ".complete"), []byte("unowned\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return ProcessRequest{}, errors.New("injected command failure")
	}
	if err := Collect(context.Background(), m, cfg); err == nil {
		t.Fatal("injected collection failure was accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("failed collection left output directory, stat error=%v", err)
	}
}

func TestCollectorManifestIsDeterministic(t *testing.T) {
	factory := func(dir string) *fakeExecutor {
		return &fakeExecutor{output: []byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\n")}
	}
	// The two runs use identical sealed inputs and therefore identical manifest bytes.
	d1 := filepath.Join(t.TempDir(), "a")
	d2 := filepath.Join(t.TempDir(), "b")
	m := collectorManifest(t)
	if err := Collect(context.Background(), m, collectorConfig(d1, factory(d1))); err != nil {
		t.Fatal(err)
	}
	if err := Collect(context.Background(), m, collectorConfig(d2, factory(d2))); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(filepath.Join(d1, "manifest.json"))
	b, _ := os.ReadFile(filepath.Join(d2, "manifest.json"))
	if string(a) != string(b) {
		t.Fatal("manifest is not deterministic")
	}
}

func TestConcurrentCollectorsHaveOneExclusiveWinner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	m := collectorManifest(t)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- Collect(context.Background(), m, collectorConfig(dir, &fakeExecutor{output: []byte("BenchmarkHydrateLegacyGetByID_N100-8 1 12 ns/op 3 B/op 2 allocs/op\n")}))
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent publication winners = %d, want 1", winners)
	}
	if err := ValidateOutput(dir); err != nil {
		t.Fatal(err)
	}
}
