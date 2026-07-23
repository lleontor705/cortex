// Command baseline validates and materializes Cortex-native retrieval evidence.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lleontor705/cortex/bench/common"
	benchcortex "github.com/lleontor705/cortex/bench/cortex"
)

const approvalInputSchema = "cortex.baseline-approval-input/v1"

type runCommand struct {
	Root, RunID, OutputDir, Input, Seed, ProtocolVersion string
}

type verifyCommand struct{ Root string }

type reproCommand struct {
	Runs     []string
	Protocol string
	Output   string
}

type approveInputCommand struct {
	Root, Output string
}

type approvalInput struct {
	SchemaVersion  string                  `json:"schema_version"`
	Commit         string                  `json:"commit"`
	BinarySHA256   string                  `json:"binary_sha256"`
	CorpusSHA256   string                  `json:"corpus_sha256"`
	ProtocolSHA256 string                  `json:"protocol_sha256"`
	GoVersion      string                  `json:"go_version"`
	GOOS           string                  `json:"goos"`
	GOARCH         string                  `json:"goarch"`
	Hardware       common.HardwareMetadata `json:"hardware"`
}

type commandDependencies struct {
	verify       func(verifyCommand) error
	run          func(context.Context, runCommand) error
	repro        func(reproCommand) error
	approveInput func(approveInputCommand) error
	runEvidence  func(context.Context, benchcortex.EvidenceRunRequest) (common.IndependentRun, error)
}

func main() {
	deps := productionDependencies()
	if err := execute(context.Background(), os.Args[1:], os.Stdout, deps); err != nil {
		fmt.Fprintln(os.Stderr, "baseline:", err)
		os.Exit(1)
	}
}

func productionDependencies() commandDependencies {
	deps := commandDependencies{runEvidence: benchcortex.RunEvidence}
	deps.verify = verifyBundle
	deps.run = func(ctx context.Context, command runCommand) error {
		return executeEvidenceRun(ctx, command, deps.runEvidence)
	}
	deps.repro = createRepro
	deps.approveInput = createApprovalInput
	return deps
}

func execute(ctx context.Context, args []string, stdout io.Writer, deps commandDependencies) error {
	if len(args) == 0 {
		return fmt.Errorf("subcommand is required: verify|run|repro|approve-input\n%s", usageText())
	}
	switch args[0] {
	case "help", "-h", "--help":
		_, err := io.WriteString(stdout, usageText())
		return err
	case "verify":
		command, err := parseVerify(args[1:])
		if err != nil {
			return err
		}
		return requireDependency(deps.verify, "verify")(command)
	case "run":
		command, err := parseRun(args[1:])
		if err != nil {
			return err
		}
		if deps.run == nil {
			return fmt.Errorf("run command is unavailable")
		}
		return deps.run(ctx, command)
	case "repro":
		command, err := parseRepro(args[1:])
		if err != nil {
			return err
		}
		return requireDependency(deps.repro, "repro")(command)
	case "approve-input":
		command, err := parseApproveInput(args[1:])
		if err != nil {
			return err
		}
		return requireDependency(deps.approveInput, "approve-input")(command)
	default:
		return fmt.Errorf("unknown subcommand %q: expected verify|run|repro|approve-input", args[0])
	}
}

func requireDependency[T any](fn func(T) error, name string) func(T) error {
	if fn != nil {
		return fn
	}
	return func(T) error { return fmt.Errorf("%s command is unavailable", name) }
}

func parseVerify(args []string) (verifyCommand, error) {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "evidence root")
	if err := flags.Parse(args); err != nil {
		return verifyCommand{}, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*root) == "" {
		return verifyCommand{}, fmt.Errorf("verify requires --root")
	}
	return verifyCommand{Root: *root}, nil
}

func parseRun(args []string) (runCommand, error) {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "evidence root")
	runID := flags.String("run-id", "", "unique run ID")
	out := flags.String("out", "", "new output directory")
	input := flags.String("input", "", "approved identity input (default <root>/approval-input.json)")
	seed := flags.String("seed", "42", "registered deterministic seed")
	version := flags.String("protocol-version", "1.0.0", "registered protocol version")
	if err := flags.Parse(args); err != nil {
		return runCommand{}, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*root) == "" || strings.TrimSpace(*runID) == "" || strings.TrimSpace(*out) == "" {
		return runCommand{}, fmt.Errorf("run requires --root, --run-id, and --out")
	}
	if strings.Contains(*runID, ",") || strings.TrimSpace(*seed) == "" || strings.TrimSpace(*version) == "" {
		return runCommand{}, fmt.Errorf("run requires one run ID and non-empty deterministic seed/protocol version")
	}
	if strings.TrimSpace(*input) == "" {
		*input = filepath.Join(*root, "approval-input.json")
	}
	return runCommand{Root: *root, RunID: *runID, OutputDir: *out, Input: *input, Seed: *seed, ProtocolVersion: *version}, nil
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func parseRepro(args []string) (reproCommand, error) {
	flags := flag.NewFlagSet("repro", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var runs stringList
	flags.Var(&runs, "run", "independent-run.json or sibling report.json (repeat exactly twice)")
	protocol := flags.String("protocol", "", "approved reproducibility protocol JSON")
	out := flags.String("out", "", "new repro JSON file")
	if err := flags.Parse(args); err != nil {
		return reproCommand{}, err
	}
	if flags.NArg() != 0 || len(runs) != 2 || strings.TrimSpace(*protocol) == "" || strings.TrimSpace(*out) == "" {
		return reproCommand{}, fmt.Errorf("repro requires exactly two --run values, --protocol, and --out")
	}
	return reproCommand{Runs: runs, Protocol: *protocol, Output: *out}, nil
}

func parseApproveInput(args []string) (approveInputCommand, error) {
	flags := flag.NewFlagSet("approve-input", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "evidence root")
	out := flags.String("out", "", "new approval-input template")
	if err := flags.Parse(args); err != nil {
		return approveInputCommand{}, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*root) == "" || strings.TrimSpace(*out) == "" {
		return approveInputCommand{}, fmt.Errorf("approve-input requires --root and --out")
	}
	return approveInputCommand{Root: *root, Output: *out}, nil
}

func verifyBundle(command verifyCommand) error {
	request, err := benchcortex.NewEvidenceRunRequest(command.Root, "validation-only", "validation-only", "42", "validation-only", benchcortex.EvidenceIdentity{})
	if err != nil {
		return err
	}
	if request.Corpus.Build.Dirty {
		return fmt.Errorf("corpus declares a dirty build")
	}
	var protocol map[string]any
	if err := readJSON(filepath.Join(command.Root, "protocol.json"), &protocol); err != nil {
		return err
	}
	if strings.TrimSpace(fmt.Sprint(protocol["protocol_version"])) == "" {
		return fmt.Errorf("protocol_version is required")
	}
	return nil
}

func executeEvidenceRun(ctx context.Context, command runCommand, runEvidence func(context.Context, benchcortex.EvidenceRunRequest) (common.IndependentRun, error)) error {
	if runEvidence == nil {
		return fmt.Errorf("evidence runner is unavailable")
	}
	repository, err := commandOutput("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if err := requireOutputOutsideRepository(repository, command.OutputDir); err != nil {
		return err
	}
	if err := requireCleanBuild(repository); err != nil {
		return err
	}
	request, err := benchcortex.NewEvidenceRunRequest(command.Root, command.OutputDir, command.RunID, command.Seed, command.ProtocolVersion, benchcortex.EvidenceIdentity{})
	if err != nil {
		return err
	}
	var approved approvalInput
	if err := readJSON(command.Input, &approved); err != nil {
		return err
	}
	if _, err := marshalApprovalInput(approved); err != nil {
		return fmt.Errorf("validate approval input: %w", err)
	}
	if approved.GoVersion != runtime.Version() || approved.GOOS != runtime.GOOS || approved.GOARCH != runtime.GOARCH {
		return fmt.Errorf("approved build environment does not match current process")
	}
	current, err := currentIdentity(command.Root, approved.Hardware)
	if err != nil {
		return err
	}
	if current.Commit != approved.Commit {
		return fmt.Errorf("commit mismatch: approved %s, current %s", approved.Commit, current.Commit)
	}
	if current.BinarySHA256 != approved.BinarySHA256 {
		return fmt.Errorf("binary hash mismatch: approved %s, current %s", approved.BinarySHA256, current.BinarySHA256)
	}
	request.Identity = benchcortex.EvidenceIdentity{
		Commit: approved.Commit, BinarySHA256: approved.BinarySHA256, CorpusSHA256: approved.CorpusSHA256,
		ProtocolSHA256: approved.ProtocolSHA256, Hardware: approved.Hardware,
	}
	if err := benchcortex.ValidateEvidenceIdentity(request); err != nil {
		return err
	}
	_, err = runEvidence(ctx, request)
	return err
}

func requireOutputOutsideRepository(repository, output string) error {
	repositoryPath, err := canonicalPath(repository)
	if err != nil {
		return fmt.Errorf("resolve repository path: %w", err)
	}
	outputPath, err := canonicalPath(output)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	relative, err := filepath.Rel(repositoryPath, outputPath)
	if err != nil {
		return fmt.Errorf("compare repository and output paths: %w", err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("representative output must be staged outside repository %s", repositoryPath)
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cursor := absolute
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(cursor)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", err
		}
		suffix = append(suffix, filepath.Base(cursor))
		cursor = parent
	}
}

func createRepro(command reproCommand) error {
	runs := make([]common.IndependentRun, len(command.Runs))
	for i, path := range command.Runs {
		if filepath.Base(path) == "report.json" {
			path = filepath.Join(filepath.Dir(path), "independent-run.json")
		}
		if err := readJSON(path, &runs[i]); err != nil {
			return fmt.Errorf("run %d: %w", i+1, err)
		}
	}
	var protocol common.ReproProtocol
	if err := readJSON(command.Protocol, &protocol); err != nil {
		return err
	}
	analysis, err := common.AnalyzeReproducibility(runs, protocol)
	if err != nil {
		return err
	}
	encoded, err := common.MarshalReproAnalysis(analysis)
	if err != nil {
		return err
	}
	return writeNewFile(command.Output, encoded)
}

func createApprovalInput(command approveInputCommand) error {
	if err := verifyBundle(verifyCommand{Root: command.Root}); err != nil {
		return err
	}
	request, err := benchcortex.NewEvidenceRunRequest(command.Root, "validation-only", "validation-only", "42", "validation-only", benchcortex.EvidenceIdentity{})
	if err != nil {
		return err
	}
	identity, err := currentIdentity(command.Root, request.Corpus.Hardware)
	if err != nil {
		return err
	}
	input := approvalInput{
		SchemaVersion: approvalInputSchema, Commit: identity.Commit, BinarySHA256: identity.BinarySHA256,
		CorpusSHA256: identity.CorpusSHA256, ProtocolSHA256: identity.ProtocolSHA256,
		GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Hardware: identity.Hardware,
	}
	encoded, err := marshalApprovalInput(input)
	if err != nil {
		return err
	}
	return writeNewFile(command.Output, encoded)
}

func marshalApprovalInput(input approvalInput) ([]byte, error) {
	if input.SchemaVersion != approvalInputSchema || !validCommit(input.Commit) || !validSHA256(input.BinarySHA256) ||
		!validSHA256(input.CorpusSHA256) || !validSHA256(input.ProtocolSHA256) {
		return nil, fmt.Errorf("approval input identity is incomplete")
	}
	encoded, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func currentIdentity(root string, hardware common.HardwareMetadata) (benchcortex.EvidenceIdentity, error) {
	commit, err := commandOutput("git", "rev-parse", "HEAD")
	if err != nil {
		return benchcortex.EvidenceIdentity{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return benchcortex.EvidenceIdentity{}, err
	}
	binaryHash, err := fileSHA256(executable)
	if err != nil {
		return benchcortex.EvidenceIdentity{}, err
	}
	corpusHash, err := fileSHA256(filepath.Join(root, "corpus.json"))
	if err != nil {
		return benchcortex.EvidenceIdentity{}, err
	}
	protocolHash, err := fileSHA256(filepath.Join(root, "protocol.json"))
	if err != nil {
		return benchcortex.EvidenceIdentity{}, err
	}
	return benchcortex.EvidenceIdentity{Commit: commit, BinarySHA256: binaryHash, CorpusSHA256: corpusHash, ProtocolSHA256: protocolHash, Hardware: hardware}, nil
}

func requireCleanBuild(repository string) error {
	status, err := commandOutput("git", "-C", repository, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("dirty build refused; commit or remove all changes before representative execution:\n%s", status)
	}
	return nil
}

func commandOutput(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

func readJSON(path string, target any) error {
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeNewFile(path string, contents []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("output path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("refusing to overwrite %s", path)
		}
		return err
	}
	defer file.Close()
	if _, err := file.Write(contents); err != nil {
		return err
	}
	return file.Sync()
}

func fileSHA256(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:]), nil
}

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validCommit(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && (len(decoded) == 20 || len(decoded) == 32)
}

func usageText() string {
	return `Portable Cortex baseline evidence command

Usage (PowerShell, cmd.exe, and bash; GNU Make is not required):
  go run ./bench/cortex/cmd/baseline verify --root <bundle>
  go run ./bench/cortex/cmd/baseline run --root <bundle> --run-id run-001 --out <new-dir-outside-repository>
  go run ./bench/cortex/cmd/baseline repro --run <run-001/report.json> --run <run-002/report.json> --protocol <approved-repro.json> --out <new-file>
  go run ./bench/cortex/cmd/baseline approve-input --root <bundle> --out <new-template.json>

Run reads <bundle>/approval-input.json, requires output staging outside the repository, invokes exactly one fresh process/database evidence run, and never overwrites output.
Invoke run a second time with a distinct run ID and output directory for reproducibility.
approve-input validates the bundle and emits identity inputs only; it does not execute evidence or record human approval.
`
}
