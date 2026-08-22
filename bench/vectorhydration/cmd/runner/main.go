// Command runner is the deliberately small, strict entry point for collection.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lleontor705/cortex/bench/vectorhydration"
)

type collectArgs struct {
	manifest, repo, goTool, sourcePin, out string
}

type runnerDeps struct {
	prepare            func(context.Context, vectorhydration.Manifest, string, string, vectorhydration.Executor) (vectorhydration.BenchmarkBinary, error)
	collect            func(context.Context, vectorhydration.Manifest, vectorhydration.CollectorConfig) error
	executor           vectorhydration.Executor
	loadGates          func(string, string, map[string]string) (vectorhydration.ExternalGates, error)
	analyzePublication func(string, vectorhydration.ExternalGates, vectorhydration.BenchmarkBinary) (vectorhydration.AnalysisReport, error)
	stderr             io.Writer
}

func main() {
	os.Exit(mainExitCode(os.Args[1:], &runnerDeps{stderr: os.Stderr}))
}

func mainExitCode(argv []string, deps *runnerDeps) int {
	if deps == nil {
		deps = &runnerDeps{}
	}
	if err := run(argv, deps); err != nil {
		stderr := deps.stderr
		if stderr == nil {
			stderr = os.Stderr
		}
		_, _ = fmt.Fprintln(stderr, err)
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			return coded.ExitCode()
		}
		return 2
	}
	return 0
}

func run(argv []string, deps *runnerDeps) error {
	if deps == nil {
		deps = &runnerDeps{}
	}
	if len(argv) == 0 {
		return errors.New("usage: runner collect --manifest FILE --repo DIR --go FILE --source-pin COMMIT --out DIR")
	}
	if argv[0] == "analyze" {
		return runAnalyze(argv[1:], deps)
	}
	if argv[0] != "collect" {
		return errors.New("usage: runner collect --manifest FILE --repo DIR --go FILE --source-pin COMMIT --out DIR")
	}
	args, err := parseCollectArgs(argv[1:])
	if err != nil {
		return err
	}
	manifest, err := loadManifest(args.manifest)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	if args.sourcePin != manifest.SourceCommit {
		return errors.New("source pin must exactly match manifest source_commit")
	}
	if err := refuseExisting(args.out); err != nil {
		return err
	}
	binaryPath := filepath.Clean(args.out) + ".benchmark"
	if err := refuseExisting(binaryPath); err != nil {
		return fmt.Errorf("benchmark binary: %w", err)
	}
	if deps.prepare == nil {
		deps.prepare = vectorhydration.PrepareBenchmarkBinary
	}
	if deps.collect == nil {
		deps.collect = vectorhydration.Collect
	}
	base := deps.executor
	if base == nil {
		base = vectorhydration.OSExecutor{}
	}
	executor := goExecutor{tool: args.goTool, base: base}
	binary, err := deps.prepare(context.Background(), manifest, args.repo, binaryPath, executor)
	if err != nil {
		return fmt.Errorf("prepare benchmark binary: %w", err)
	}
	if binary.BinaryPath == "" {
		return errors.New("prepare benchmark binary returned an empty path")
	}
	if err := deps.collect(context.Background(), manifest, vectorhydration.CollectorConfig{
		OutputDir: args.out, Measurements: []vectorhydration.Measurement{vectorhydration.MeasurementLegacy, vectorhydration.MeasurementBatch},
		Command: commandFor(binary.BinaryPath, manifest), Executor: executor,
	}); err != nil {
		return fmt.Errorf("collect: %w", err)
	}
	return nil
}

func parseCollectArgs(argv []string) (collectArgs, error) {
	var out collectArgs
	seen := map[string]bool{}
	values := map[string]*string{"--manifest": &out.manifest, "--repo": &out.repo, "--go": &out.goTool, "--source-pin": &out.sourcePin, "--out": &out.out}
	for i := 0; i < len(argv); i++ {
		name := argv[i]
		value, ok := values[name]
		if !ok {
			return out, fmt.Errorf("unknown flag or positional argument %q", name)
		}
		if seen[name] {
			return out, fmt.Errorf("duplicate flag %q", name)
		}
		seen[name] = true
		if i+1 >= len(argv) || strings.TrimSpace(argv[i+1]) == "" || strings.HasPrefix(argv[i+1], "-") {
			return out, fmt.Errorf("flag %s requires a non-empty value", name)
		}
		i++
		*value = argv[i]
	}
	for name, value := range values {
		if !seen[name] {
			return out, fmt.Errorf("missing required flag %s", name)
		}
		if strings.TrimSpace(*value) == "" {
			return out, fmt.Errorf("flag %s requires a non-empty value", name)
		}
	}
	return out, nil
}

func loadManifest(path string) (vectorhydration.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return vectorhydration.Manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var manifest vectorhydration.Manifest
	if err := dec.Decode(&manifest); err != nil {
		return manifest, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return manifest, errors.New("manifest must contain exactly one JSON value")
		}
		return manifest, err
	}
	if err := manifest.Validate(); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func refuseExisting(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("output already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect output: %w", err)
	}
	return nil
}

func commandFor(binary string, manifest vectorhydration.Manifest) vectorhydration.Command {
	return func(entry vectorhydration.ScheduleEntry, measurement vectorhydration.Measurement) (vectorhydration.ProcessRequest, error) {
		benchmark, err := benchmarkName(manifest, measurement)
		if err != nil {
			return vectorhydration.ProcessRequest{}, err
		}
		return vectorhydration.ProcessRequest{Executable: binary, Args: []string{"-test.run=^$", "-test.bench=^" + benchmark + "$", "-test.benchmem", "-test.count=1"}}, nil
	}
}

func benchmarkName(m vectorhydration.Manifest, measurement vectorhydration.Measurement) (string, error) {
	if measurement == vectorhydration.MeasurementLegacy {
		return m.LegacyBenchmark, nil
	}
	if measurement == vectorhydration.MeasurementBatch {
		return m.BatchBenchmark, nil
	}
	return "", fmt.Errorf("unknown measurement %q", measurement)
}

type goExecutor struct {
	tool string
	base vectorhydration.Executor
}

func (e goExecutor) Execute(ctx context.Context, req vectorhydration.ProcessRequest) vectorhydration.Execution {
	if req.Executable == "go" {
		req.Executable = e.tool
	}
	return e.base.Execute(ctx, req)
}
