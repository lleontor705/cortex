package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/vectorhydration"
)

func analyzeCLIArgs(dir, binary, report string) []string {
	return []string{"analyze", "--publication", dir, "--registry", filepath.Join(dir, "registry"), "--registry-sha256", strings.Repeat("a", 64), "--semantic", filepath.Join(dir, "semantic"), "--query", filepath.Join(dir, "query"), "--allocation", filepath.Join(dir, "allocation"), "--binary", binary, "--report", report}
}

func writeAnalyzeFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir, binary := t.TempDir(), ""
	m := testManifest(t)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "input-manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	binary = filepath.Join(dir, "benchmark")
	if err = os.WriteFile(binary, []byte("binary"), 0700); err != nil {
		t.Fatal(err)
	}
	return dir, binary, filepath.Join(dir, "report.json")
}

func TestAnalyzeRequiresExactFlags(t *testing.T) {
	valid := []string{"--publication", "p", "--registry", "r", "--registry-sha256", "s", "--semantic", "s", "--query", "q", "--allocation", "a", "--binary", "b", "--report", "o"}
	for _, args := range [][]string{{}, valid[:len(valid)-2], append(append([]string{}, valid...), "extra"), append(append([]string{}, valid...), "--report", "x")} {
		if _, err := parseAnalyzeArgs(args); err == nil {
			t.Fatalf("accepted invalid args %q", args)
		}
	}
}

func TestAnalyzeCLIContractsAndStatusExit(t *testing.T) {
	for _, status := range []string{"PASS", "FAIL", "BLOCKED", "INCONCLUSIVE"} {
		t.Run(status, func(t *testing.T) {
			dir, binary, reportPath := writeAnalyzeFixture(t)
			var gotRegistry, gotDigest string
			var gotPaths map[string]string
			var gotPublication string
			var gotBinary vectorhydration.BenchmarkBinary
			deps := &runnerDeps{loadGates: func(registry, digest string, paths map[string]string) (vectorhydration.ExternalGates, error) {
				gotRegistry, gotDigest, gotPaths = registry, digest, paths
				return vectorhydration.ExternalGates{}, nil
			}, analyzePublication: func(publication string, _ vectorhydration.ExternalGates, binary vectorhydration.BenchmarkBinary) (vectorhydration.AnalysisReport, error) {
				gotPublication, gotBinary = publication, binary
				return vectorhydration.AnalysisReport{Status: status, Reasons: []string{"reason"}}, nil
			}}
			args := analyzeCLIArgs(dir, binary, reportPath)
			wantCode := 0
			if status != "PASS" {
				wantCode = 1
			}
			if got := mainExitCode(args, deps); got != wantCode {
				t.Fatalf("exit=%d want %d", got, wantCode)
			}
			if gotRegistry != filepath.Join(dir, "registry") || gotDigest != strings.Repeat("a", 64) || !reflect.DeepEqual(gotPaths, map[string]string{"semantic": filepath.Join(dir, "semantic"), "query": filepath.Join(dir, "query"), "allocation": filepath.Join(dir, "allocation")}) {
				t.Fatalf("gate inputs incorrect: %q %q %#v", gotRegistry, gotDigest, gotPaths)
			}
			wantHash := sha256.Sum256([]byte("binary"))
			if gotPublication != dir || gotBinary.SourceCommit != testManifest(t).SourceCommit || gotBinary.BinaryPath != binary || gotBinary.SHA256 != hex.EncodeToString(wantHash[:]) {
				t.Fatalf("analyzer inputs incorrect: %q %#v", gotPublication, gotBinary)
			}
			want, _ := json.MarshalIndent(vectorhydration.AnalysisReport{Status: status, Reasons: []string{"reason"}}, "", "  ")
			want = append(want, '\n')
			got, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("non-canonical report: %q", got)
			}
		})
	}
}

func TestAnalyzeErrorsDoNotPublish(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, string, string)
		deps  func() *runnerDeps
	}{
		{"manifest load", func(t *testing.T, d, _ string) { _ = os.Remove(filepath.Join(d, "input-manifest.json")) }, func() *runnerDeps { return &runnerDeps{} }},
		{"binary hash", func(_ *testing.T, _, b string) { _ = os.Remove(b) }, func() *runnerDeps { return &runnerDeps{} }},
		{"gate", func(_ *testing.T, _, _ string) {}, func() *runnerDeps {
			return &runnerDeps{loadGates: func(string, string, map[string]string) (vectorhydration.ExternalGates, error) {
				return vectorhydration.ExternalGates{}, errors.New("gate")
			}}
		}},
		{"analyzer", func(_ *testing.T, _, _ string) {}, func() *runnerDeps {
			return &runnerDeps{loadGates: func(string, string, map[string]string) (vectorhydration.ExternalGates, error) {
				return vectorhydration.ExternalGates{}, nil
			}, analyzePublication: func(string, vectorhydration.ExternalGates, vectorhydration.BenchmarkBinary) (vectorhydration.AnalysisReport, error) {
				return vectorhydration.AnalysisReport{}, errors.New("analyzer")
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, binary, report := writeAnalyzeFixture(t)
			tc.setup(t, dir, binary)
			if mainExitCode(analyzeCLIArgs(dir, binary, report), tc.deps()) == 0 {
				t.Fatal("error returned zero")
			}
			if _, err := os.Lstat(report); !os.IsNotExist(err) {
				t.Fatalf("report exists: %v", err)
			}
		})
	}
}

func TestAnalyzeRejectsSymlinkBinary(t *testing.T) {
	dir, binary, report := writeAnalyzeFixture(t)
	link := filepath.Join(dir, "target")
	if err := os.Rename(binary, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link, binary); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	called := false
	deps := &runnerDeps{loadGates: func(string, string, map[string]string) (vectorhydration.ExternalGates, error) {
		called = true
		return vectorhydration.ExternalGates{}, nil
	}}
	if mainExitCode(analyzeCLIArgs(dir, binary, report), deps) == 0 || called {
		t.Fatal("symlink reached dependencies")
	}
}

func TestAnalyzeRacePreservesReportAndExistingReportRefuses(t *testing.T) {
	dir, binary, report := writeAnalyzeFixture(t)
	deps := &runnerDeps{loadGates: func(string, string, map[string]string) (vectorhydration.ExternalGates, error) {
		return vectorhydration.ExternalGates{}, nil
	}, analyzePublication: func(string, vectorhydration.ExternalGates, vectorhydration.BenchmarkBinary) (vectorhydration.AnalysisReport, error) {
		if err := os.WriteFile(report, []byte("raced"), 0600); err != nil {
			t.Fatal(err)
		}
		return vectorhydration.AnalysisReport{Status: "PASS"}, nil
	}}
	if mainExitCode(analyzeCLIArgs(dir, binary, report), deps) == 0 {
		t.Fatal("race was accepted")
	}
	got, _ := os.ReadFile(report)
	if string(got) != "raced" {
		t.Fatalf("race report overwritten: %q", got)
	}
	if err := os.WriteFile(report, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	deps.loadGates = func(string, string, map[string]string) (vectorhydration.ExternalGates, error) {
		return vectorhydration.ExternalGates{}, errors.New("must not load")
	}
	if mainExitCode(analyzeCLIArgs(dir, binary, report), deps) == 0 {
		t.Fatal("existing report accepted")
	}
	got, _ = os.ReadFile(report)
	if string(got) != "keep" {
		t.Fatalf("existing report overwritten: %q", got)
	}
}
