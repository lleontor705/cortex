package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/bench/common"
	benchcortex "github.com/lleontor705/cortex/bench/cortex"
)

func TestCLIRequiresSupportedSubcommand(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{nil, {"unknown"}} {
		var stdout bytes.Buffer
		err := execute(context.Background(), args, &stdout, commandDependencies{})
		if err == nil || !strings.Contains(err.Error(), "verify|run|repro|approve-input") {
			t.Fatalf("execute(%q) error = %v, want supported subcommands", args, err)
		}
	}
}

func TestCLIHelpAndUnavailableDependencies(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if err := execute(context.Background(), []string{"help"}, &stdout, commandDependencies{}); err != nil {
		t.Fatalf("execute(help) error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Portable Cortex baseline evidence command") {
		t.Fatalf("help output = %q", stdout.String())
	}
	for _, args := range [][]string{
		{"verify", "--root", "bundle"},
		{"run", "--root", "bundle", "--run-id", "run-001", "--out", "output"},
		{"repro", "--run", "one", "--run", "two", "--protocol", "protocol", "--out", "output"},
		{"approve-input", "--root", "bundle", "--out", "output"},
	} {
		if err := execute(context.Background(), args, io.Discard, commandDependencies{}); err == nil || !strings.Contains(err.Error(), "unavailable") {
			t.Fatalf("execute(%q) error = %v, want unavailable", args, err)
		}
	}
	deps := productionDependencies()
	if deps.verify == nil || deps.run == nil || deps.repro == nil || deps.approveInput == nil || deps.runEvidence == nil {
		t.Fatalf("productionDependencies() = %+v, want all commands", deps)
	}
}

func TestCLIRejectsMalformedArguments(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"verify"},
		{"verify", "--root", "bundle", "extra"},
		{"run", "--root", "bundle"},
		{"run", "--root", "bundle", "--run-id", "run", "--out", "output", "--protocol-version", ""},
		{"repro", "--run", "one", "--protocol", "protocol", "--out", "output"},
		{"approve-input", "--root", "bundle"},
		{"approve-input", "--root", "bundle", "--out", "output", "extra"},
	} {
		if err := execute(context.Background(), args, io.Discard, routingDependencies(nil)); err == nil {
			t.Fatalf("execute(%q) accepted malformed arguments", args)
		}
	}
}

func TestCLIRoutesSubcommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "verify", args: []string{"verify", "--root", "bundle"}, want: "verify"},
		{name: "run", args: []string{"run", "--root", "bundle", "--run-id", "run-001", "--out", "output"}, want: "run"},
		{name: "repro", args: []string{"repro", "--run", "one.json", "--run", "two.json", "--protocol", "repro-protocol.json", "--out", "repro.json"}, want: "repro"},
		{name: "approve input", args: []string{"approve-input", "--root", "bundle", "--out", "approval-input.json"}, want: "approve-input"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := ""
			deps := routingDependencies(&called)
			if err := execute(context.Background(), tt.args, &bytes.Buffer{}, deps); err != nil {
				t.Fatalf("execute() error = %v", err)
			}
			if called != tt.want {
				t.Fatalf("called = %q, want %q", called, tt.want)
			}
		})
	}
}

func TestRunAcceptsExactlyOneFreshProcessIdentity(t *testing.T) {
	t.Parallel()

	var requests []runCommand
	deps := routingDependencies(nil)
	deps.run = func(_ context.Context, command runCommand) error {
		requests = append(requests, command)
		return nil
	}

	for _, runID := range []string{"run-001", "run-002"} {
		args := []string{"run", "--root", "bundle", "--run-id", runID, "--out", runID}
		if err := execute(context.Background(), args, &bytes.Buffer{}, deps); err != nil {
			t.Fatalf("execute(%q) error = %v", runID, err)
		}
	}
	if len(requests) != 2 || requests[0].RunID == requests[1].RunID || requests[0].OutputDir == requests[1].OutputDir {
		t.Fatalf("requests = %+v, want two distinct process invocations", requests)
	}
	if err := execute(context.Background(), []string{"run", "--root", "bundle", "--run-id", "run-001,run-002", "--out", "output"}, &bytes.Buffer{}, deps); err == nil {
		t.Fatal("execute() accepted batched run IDs")
	}
}

func TestRunUsesDeterministicProtocolFlags(t *testing.T) {
	t.Parallel()

	var got runCommand
	deps := routingDependencies(nil)
	deps.run = func(_ context.Context, command runCommand) error { got = command; return nil }
	args := []string{"run", "--root", "bundle", "--run-id", "run-001", "--out", "output", "--seed", "7", "--protocol-version", "v2"}
	if err := execute(context.Background(), args, &bytes.Buffer{}, deps); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if got.Seed != "7" || got.ProtocolVersion != "v2" {
		t.Fatalf("run command = %+v, want explicit deterministic flags", got)
	}

	if err := execute(context.Background(), []string{"run", "--root", "bundle", "--run-id", "run-001", "--out", "output", "--seed", ""}, &bytes.Buffer{}, deps); err == nil {
		t.Fatal("execute() accepted an empty seed")
	}
}

func TestArgumentsRemainShellSafe(t *testing.T) {
	t.Parallel()

	root := `C:\evidence bundles\v1 & untouched`
	out := `/tmp/evidence output/$(not-executed);still-literal`
	var got runCommand
	deps := routingDependencies(nil)
	deps.run = func(_ context.Context, command runCommand) error { got = command; return nil }
	args := []string{"run", "--root", root, "--run-id", "run-001", "--out", out}
	if err := execute(context.Background(), args, &bytes.Buffer{}, deps); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if got.Root != root || got.OutputDir != out {
		t.Fatalf("arguments changed: got root %q out %q", got.Root, got.OutputDir)
	}
}

func TestRunStopsBeforeEvidenceOnDirtyBuildOrHashMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		failure error
	}{
		{name: "dirty build", failure: errors.New("dirty build")},
		{name: "hash mismatch", failure: benchcortex.ErrCorpusHashMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calledEvidence := false
			deps := routingDependencies(nil)
			deps.run = func(context.Context, runCommand) error { return tt.failure }
			deps.runEvidence = func(context.Context, benchcortex.EvidenceRunRequest) (common.IndependentRun, error) {
				calledEvidence = true
				return common.IndependentRun{}, nil
			}
			err := execute(context.Background(), []string{"run", "--root", "bundle", "--run-id", "run-001", "--out", "output"}, &bytes.Buffer{}, deps)
			if !errors.Is(err, tt.failure) && !strings.Contains(err.Error(), tt.failure.Error()) {
				t.Fatalf("execute() error = %v, want %v", err, tt.failure)
			}
			if calledEvidence {
				t.Fatal("evidence executed after preflight failure")
			}
		})
	}
}

func TestWriteNewFileRequiresAbsentOutputAndDoesNotOverwrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "result.json")
	if err := writeNewFile(path, []byte("first\n")); err != nil {
		t.Fatalf("writeNewFile() error = %v", err)
	}
	if err := writeNewFile(path, []byte("second\n")); err == nil {
		t.Fatal("writeNewFile() overwrote existing output")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(contents) != "first\n" {
		t.Fatalf("contents = %q, want original output", contents)
	}
}

func TestApproveInputEmitsTemplateWithoutHumanApproval(t *testing.T) {
	t.Parallel()

	template := approvalInput{
		SchemaVersion:  approvalInputSchema,
		Commit:         strings.Repeat("a", 40),
		BinarySHA256:   strings.Repeat("b", 64),
		CorpusSHA256:   strings.Repeat("c", 64),
		ProtocolSHA256: strings.Repeat("d", 64),
	}
	encoded, err := marshalApprovalInput(template)
	if err != nil {
		t.Fatalf("marshalApprovalInput() error = %v", err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), `"approved`) || strings.Contains(strings.ToLower(string(encoded)), `"reviewer`) {
		t.Fatalf("template contains human approval: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"schema_version"`) || !strings.HasSuffix(string(encoded), "\n") {
		t.Fatalf("template is not deterministic JSON: %s", encoded)
	}
}

func TestUsageIsPortableAndDoesNotRequireMake(t *testing.T) {
	t.Parallel()

	text := usageText()
	for _, required := range []string{"PowerShell", "cmd.exe", "bash", "go run ./bench/cortex/cmd/baseline"} {
		if !strings.Contains(text, required) {
			t.Fatalf("usage missing %q", required)
		}
	}
	if !strings.Contains(text, "outside the repository") {
		t.Fatalf("usage omits external output staging requirement: %s", text)
	}
	if strings.Contains(strings.ToLower(text), "\n  make ") {
		t.Fatalf("usage requires GNU Make: %s", text)
	}
}

func TestRunOutputMustBeOutsideRepository(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	inside := filepath.Join(repository, "bench", "evidence", "run-001")
	outside := filepath.Join(filepath.Dir(repository), filepath.Base(repository)+"-staging", "run-001")
	if err := requireOutputOutsideRepository(repository, inside); err == nil {
		t.Fatal("requireOutputOutsideRepository() accepted output inside repository")
	}
	if err := requireOutputOutsideRepository(repository, outside); err != nil {
		t.Fatalf("requireOutputOutsideRepository() rejected external staging: %v", err)
	}
}

func TestRunOutputRejectsExternalSymlinkBackIntoRepository(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	external := t.TempDir()
	link := filepath.Join(external, "repository-link")
	if err := os.Symlink(repository, link); err != nil {
		t.Skipf("filesystem does not permit symlink test: %v", err)
	}
	if err := requireOutputOutsideRepository(repository, filepath.Join(link, "run-001")); err == nil {
		t.Fatal("requireOutputOutsideRepository() accepted symlink back into repository")
	}
}

func TestRequireCleanBuildRejectsUnknownUntrackedFiles(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init")
	tracked := filepath.Join(repository, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(tracked) error = %v", err)
	}
	runGit(t, repository, "add", "tracked.txt")
	runGit(t, repository, "-c", "user.name=Cortex Test", "-c", "user.email=cortex@example.invalid", "commit", "-m", "test fixture")
	if err := requireCleanBuild(repository); err != nil {
		t.Fatalf("requireCleanBuild(clean) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "unknown-user-data"), []byte("preserve me"), 0o600); err != nil {
		t.Fatalf("WriteFile(untracked) error = %v", err)
	}
	if err := requireCleanBuild(repository); err == nil || !strings.Contains(err.Error(), "unknown-user-data") {
		t.Fatalf("requireCleanBuild(dirty) error = %v, want named untracked file", err)
	}
}

func TestVerifyAndApproveInputExecuteAgainstCommittedBundle(t *testing.T) {
	repository := testRepositoryRoot(t)
	root := filepath.Join(repository, "bench", "evidence", "cortex-native", "v1")
	if err := verifyBundle(verifyCommand{Root: root}); err != nil {
		t.Fatalf("verifyBundle() error = %v", err)
	}
	output := filepath.Join(t.TempDir(), "approval-input.json")
	if err := createApprovalInput(approveInputCommand{Root: root, Output: output}); err != nil {
		t.Fatalf("createApprovalInput() error = %v", err)
	}
	var input approvalInput
	if err := readJSON(output, &input); err != nil {
		t.Fatalf("readJSON(approval input) error = %v", err)
	}
	if input.SchemaVersion != approvalInputSchema || !validCommit(input.Commit) || !validSHA256(input.BinarySHA256) {
		t.Fatalf("approval input identity is incomplete: %+v", input)
	}
	if err := createApprovalInput(approveInputCommand{Root: root, Output: output}); err == nil {
		t.Fatal("createApprovalInput() overwrote existing output")
	}
}

func TestCreateReproExecutesAndRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	runPaths := []string{filepath.Join(directory, "run-001.json"), filepath.Join(directory, "run-002.json")}
	for i, path := range runPaths {
		writeTestJSON(t, path, cliIndependentRun("run-00"+string(rune('1'+i))))
	}
	protocolPath := filepath.Join(directory, "protocol.json")
	writeTestJSON(t, protocolPath, common.ReproProtocol{
		Version: "repro-v1", DispersionMethod: "sample_standard_deviation", ApprovedBy: "test-reviewer",
	})
	output := filepath.Join(directory, "repro.json")
	command := reproCommand{Runs: runPaths, Protocol: protocolPath, Output: output}
	if err := createRepro(command); err != nil {
		t.Fatalf("createRepro() error = %v", err)
	}
	if err := createRepro(command); err == nil {
		t.Fatal("createRepro() overwrote existing analysis")
	}
	var analysis common.ReproAnalysis
	if err := readJSON(output, &analysis); err != nil {
		t.Fatalf("readJSON(repro) error = %v", err)
	}
	if !analysis.DeterministicMatch || len(analysis.Runs) != 2 {
		t.Fatalf("repro analysis = %+v, want two deterministic runs", analysis)
	}
}

func TestProductionCommandsReturnActionableInputErrors(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing")
	if err := verifyBundle(verifyCommand{Root: missing}); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("verifyBundle(missing) error = %v", err)
	}
	if err := createApprovalInput(approveInputCommand{Root: missing, Output: filepath.Join(t.TempDir(), "approval.json")}); err == nil {
		t.Fatal("createApprovalInput(missing) error = nil")
	}
	if _, err := currentIdentity(missing, common.HardwareMetadata{}); err == nil {
		t.Fatal("currentIdentity(missing) error = nil")
	}
	if err := createRepro(reproCommand{
		Runs: []string{missing, missing}, Protocol: missing, Output: filepath.Join(t.TempDir(), "repro.json"),
	}); err == nil || !strings.Contains(err.Error(), "run 1") {
		t.Fatalf("createRepro(missing) error = %v", err)
	}
	if _, err := fileSHA256(missing); err == nil {
		t.Fatal("fileSHA256(missing) error = nil")
	}
	if err := writeNewFile("", []byte("invalid")); err == nil {
		t.Fatal("writeNewFile(empty) error = nil")
	}
}

func TestExecuteEvidenceRunRejectsRepositoryOutputBeforeRunner(t *testing.T) {
	called := false
	err := executeEvidenceRun(context.Background(), runCommand{
		Root: "bench/evidence/cortex-native/v1", RunID: "run-001", OutputDir: "bench/evidence/cortex-native/v1/runs/run-001",
	}, func(context.Context, benchcortex.EvidenceRunRequest) (common.IndependentRun, error) {
		called = true
		return common.IndependentRun{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "outside repository") {
		t.Fatalf("executeEvidenceRun() error = %v, want repository-output refusal", err)
	}
	if called {
		t.Fatal("evidence runner called after output-path refusal")
	}
}

func cliIndependentRun(runID string) common.IndependentRun {
	available := true
	report := common.EvidenceReport{
		SchemaVersion: "retrieval-evidence-report/v1", RunID: runID, ReportID: runID + "-report",
		CorpusVersion: "corpus-v1", ProtocolVersion: "protocol-v1",
		Build:    common.BuildMetadata{Commit: strings.Repeat("a", 40)},
		Hardware: common.HardwareMetadata{ProfileID: "hardware-v1", OS: "test", Arch: "test", CPU: "test", MemoryMB: 1024},
		MetricDefinitions: []common.MetricDefinition{{
			Name: "retrieved_count", Unit: "count", Direction: "higher_is_better", Description: "Observed results.",
		}},
		Profiles: []common.ProfileReport{{
			ProfileID: "lexical", ProfileVersion: "v1", QueryClass: "single-hop",
			Metrics: map[string]float64{"retrieved_count": 1}, Latency: common.LatencyReport{P50: 1, P95: 1, P99: 1, Unit: "nanoseconds"},
			Throughput: common.ThroughputReport{QueriesPerSecond: 1, Unit: "queries_per_second"},
		}},
		Queries: []common.QueryReport{{
			QueryID: "query-1", ProfileID: "lexical", ProfileVersion: "v1", QueryClass: "single-hop",
			Metrics: map[string]float64{"retrieved_count": 1}, CurrentOutput: []common.RankedOutput{}, CandidateOutput: []common.RankedOutput{},
		}},
		Resources: common.ResourceReport{
			CPUSeconds: 1, CPUUnit: "seconds", CPUAvailable: &available,
			PeakRSSBytes: 1, PeakRSSUnit: "bytes", PeakRSSAvailable: &available,
			StorageBytes: 1, StorageUnit: "bytes", StorageAvailable: &available,
			IndexBytes: 1, IndexUnit: "bytes", IndexAvailable: &available,
		},
		Uncertainty: common.UncertaintyReport{Method: "test", ConfidenceLevel: 0.95, SampleSize: 1, Notes: "Test fixture."},
		Limitations: []string{"Test fixture only."},
	}
	return common.IndependentRun{
		RunID: runID, Seed: "42", BinarySHA256: strings.Repeat("b", 64), Report: report,
		HeapAllocBytes: 1, TotalAllocBytes: 2, AllocationsAvailable: true,
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func testRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", "..", ".."))
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", repository}, args...)
	if output, err := exec.Command("git", commandArgs...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func routingDependencies(called *string) commandDependencies {
	set := func(name string) {
		if called != nil {
			*called = name
		}
	}
	return commandDependencies{
		verify:       func(verifyCommand) error { set("verify"); return nil },
		run:          func(context.Context, runCommand) error { set("run"); return nil },
		repro:        func(reproCommand) error { set("repro"); return nil },
		approveInput: func(approveInputCommand) error { set("approve-input"); return nil },
		runEvidence: func(context.Context, benchcortex.EvidenceRunRequest) (common.IndependentRun, error) {
			return common.IndependentRun{}, nil
		},
	}
}

func TestRepeatedRunFlagPreservesArgumentOrder(t *testing.T) {
	t.Parallel()

	var got reproCommand
	deps := routingDependencies(nil)
	deps.repro = func(command reproCommand) error { got = command; return nil }
	args := []string{"repro", "--run", "first report.json", "--run", "second;report.json", "--protocol", "protocol.json", "--out", "repro.json"}
	if err := execute(context.Background(), args, &bytes.Buffer{}, deps); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if want := []string{"first report.json", "second;report.json"}; !reflect.DeepEqual(got.Runs, want) {
		t.Fatalf("runs = %q, want %q", got.Runs, want)
	}
}
