package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if strings.Contains(strings.ToLower(text), "\n  make ") {
		t.Fatalf("usage requires GNU Make: %s", text)
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
