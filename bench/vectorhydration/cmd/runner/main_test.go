package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/vectorhydration"
)

func TestCollectRequiresExactFlags(t *testing.T) {
	for _, args := range [][]string{
		{"collect"},
		{"collect", "--manifest", "m", "--repo", "r", "--go", "g", "--source-pin", "p", "--out", "o", "extra"},
		{"collect", "--manifest", "m", "--repo", "r", "--go", "g", "--source-pin", "p", "--out", "o", "--out", "x"},
	} {
		if err := run(args, nil); err == nil {
			t.Fatalf("run(%q) accepted invalid arguments", args)
		}
	}
}

func testManifest(t *testing.T) vectorhydration.Manifest {
	t.Helper()
	m := vectorhydration.Manifest{
		SchemaVersion:    vectorhydration.SchemaVersion,
		Campaign:         vectorhydration.CampaignManifest{Version: vectorhydration.AmendmentVersion, ID: vectorhydration.CampaignID},
		Phase:            vectorhydration.PhaseManifest{Version: vectorhydration.PhaseSchemaVersion, ID: "phase"},
		Run:              vectorhydration.RunManifest{Version: vectorhydration.RunSchemaVersion, ID: "run"},
		SourceMachine:    vectorhydration.SourceMachineManifest{Version: vectorhydration.SourceSchemaVersion, ID: "machine", OS: "windows", Arch: "amd64", CPU: "test"},
		SourceCommit:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		BenchmarkPackage: vectorhydration.BenchmarkPackage,
		LegacyBenchmark:  vectorhydration.LegacyBenchmark,
		BatchBenchmark:   vectorhydration.BatchBenchmark,
		Seed:             1,
	}
	var err error
	m.Schedule, err = vectorhydration.Schedule(m.Seed, m.Campaign.ID, m.Phase.ID, m.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMainExitCodeReportsPrepareAndCollectFailures(t *testing.T) {
	dir := t.TempDir()
	m := testManifest(t)
	path := filepath.Join(dir, "manifest.json")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"collect", "--manifest", path, "--repo", dir, "--go", "go", "--source-pin", m.SourceCommit, "--out", filepath.Join(dir, "out")}
	for name, deps := range map[string]*runnerDeps{
		"prepare": {prepare: func(context.Context, vectorhydration.Manifest, string, string, vectorhydration.Executor) (vectorhydration.BenchmarkBinary, error) {
			return vectorhydration.BenchmarkBinary{}, errors.New("prepare failed")
		}},
		"collect": {prepare: func(context.Context, vectorhydration.Manifest, string, string, vectorhydration.Executor) (vectorhydration.BenchmarkBinary, error) {
			return vectorhydration.BenchmarkBinary{BinaryPath: filepath.Join(dir, "bin")}, nil
		}, collect: func(context.Context, vectorhydration.Manifest, vectorhydration.CollectorConfig) error {
			return errors.New("collect failed")
		}},
	} {
		_ = name
		var stderr bytes.Buffer
		deps.stderr = &stderr
		if code := mainExitCode(args, deps); code != 2 || stderr.Len() == 0 {
			t.Fatalf("exit code=%d stderr=%q", code, stderr.String())
		}
	}
}

type collectingExecutor struct {
	requests []vectorhydration.ProcessRequest
}

func (e *collectingExecutor) Execute(_ context.Context, req vectorhydration.ProcessRequest) vectorhydration.Execution {
	e.requests = append(e.requests, req)
	benchmark := strings.TrimSuffix(strings.TrimPrefix(req.Args[1], "-test.bench=^"), "$")
	return vectorhydration.Execution{Stdout: []byte(benchmark + "-8 1 12 ns/op 3 B/op 2 allocs/op\n"), ExitCode: 0, PID: len(e.requests), ProcessIdentity: "fake-" + strconv.Itoa(len(e.requests))}
}

func TestMainExitCodeRunsActualCollectSchedule(t *testing.T) {
	dir, m := t.TempDir(), testManifest(t)
	manifestPath := filepath.Join(dir, "manifest.json")
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, b, 0600); err != nil {
		t.Fatal(err)
	}
	fake := &collectingExecutor{}
	prepared := filepath.Join(dir, "prepared")
	deps := &runnerDeps{stderr: io.Discard, executor: fake, prepare: func(context.Context, vectorhydration.Manifest, string, string, vectorhydration.Executor) (vectorhydration.BenchmarkBinary, error) {
		return vectorhydration.BenchmarkBinary{BinaryPath: prepared}, nil
	}}
	args := []string{"collect", "--manifest", manifestPath, "--repo", dir, "--go", "go", "--source-pin", m.SourceCommit, "--out", filepath.Join(dir, "out")}
	if code := mainExitCode(args, deps); code != 0 {
		t.Fatalf("exit code=%d", code)
	}
	if len(fake.requests) != 120 {
		t.Fatalf("requests=%d, want 120", len(fake.requests))
	}
	for i, req := range fake.requests {
		entry := m.Schedule[i/2]
		measurement := vectorhydration.MeasurementLegacy
		if entry.Order == vectorhydration.OrderBA {
			measurement = vectorhydration.MeasurementBatch
		}
		if i%2 == 1 {
			if measurement == vectorhydration.MeasurementLegacy {
				measurement = vectorhydration.MeasurementBatch
			} else {
				measurement = vectorhydration.MeasurementLegacy
			}
		}
		benchmark := m.LegacyBenchmark
		if measurement == vectorhydration.MeasurementBatch {
			benchmark = m.BatchBenchmark
		}
		wantArgs := []string{"-test.run=^$", "-test.bench=^" + benchmark + "$", "-test.benchmem", "-test.count=1"}
		if req.Executable != prepared || !slices.Equal(req.Args, wantArgs) {
			t.Fatalf("request %d mismatch: %#v", i, req)
		}
		count, value := 0, ""
		for _, env := range req.Env {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "GOMAXPROCS") {
				count++
				value = parts[1]
			}
		}
		if count != 1 || value != strconv.Itoa(entry.Cell) {
			t.Fatalf("request %d GOMAXPROCS count=%d value=%q", i, count, value)
		}
		if !strings.Contains(req.Identity, "/sequence:"+strconv.Itoa(i+1)) {
			t.Fatalf("request %d identity=%q", i, req.Identity)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "out", ".complete")); err != nil {
		t.Fatalf("publication missing: %v", err)
	}
}

func TestCollectRejectsPinBeforePreparation(t *testing.T) {
	dir := t.TempDir()
	m := testManifest(t)
	path := filepath.Join(dir, "manifest.json")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := run([]string{"collect", "--manifest", path, "--repo", dir, "--go", "go", "--source-pin", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "--out", filepath.Join(dir, "out")}, &runnerDeps{prepare: func(context.Context, vectorhydration.Manifest, string, string, vectorhydration.Executor) (vectorhydration.BenchmarkBinary, error) {
		called = true
		return vectorhydration.BenchmarkBinary{}, nil
	}})
	if err == nil || called {
		t.Fatalf("pin mismatch err=%v called=%v", err, called)
	}
}

func TestCollectRefusesExistingOutputsBeforePreparation(t *testing.T) {
	for _, suffix := range []string{"", ".benchmark"} {
		dir := t.TempDir()
		m := testManifest(t)
		manifestPath := filepath.Join(dir, "manifest.json")
		b, _ := json.Marshal(m)
		if err := os.WriteFile(manifestPath, b, 0600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "out")
		if err := os.WriteFile(out+suffix, []byte("existing"), 0600); err != nil {
			t.Fatal(err)
		}
		prepared, collected := 0, 0
		deps := &runnerDeps{
			prepare: func(context.Context, vectorhydration.Manifest, string, string, vectorhydration.Executor) (vectorhydration.BenchmarkBinary, error) {
				prepared++
				return vectorhydration.BenchmarkBinary{}, nil
			},
			collect: func(context.Context, vectorhydration.Manifest, vectorhydration.CollectorConfig) error {
				collected++
				return nil
			},
		}
		if err := run([]string{"collect", "--manifest", manifestPath, "--repo", dir, "--go", "go", "--source-pin", m.SourceCommit, "--out", out}, deps); err == nil {
			t.Fatal("existing output was accepted")
		}
		if prepared != 0 || collected != 0 {
			t.Fatalf("existing %q invoked prepare=%d collect=%d", suffix, prepared, collected)
		}
	}
}
