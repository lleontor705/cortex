package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lleontor705/cortex/bench/vectorhydration"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type analyzeArgs struct{ publication, registry, registrySHA, semantic, query, allocation, binary, report string }
type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string { return e.err.Error() }
func (e exitError) Unwrap() error { return e.err }
func (e exitError) ExitCode() int { return e.code }

func parseAnalyzeArgs(argv []string) (analyzeArgs, error) {
	var a analyzeArgs
	seen := map[string]bool{}
	values := map[string]*string{"--publication": &a.publication, "--registry": &a.registry, "--registry-sha256": &a.registrySHA, "--semantic": &a.semantic, "--query": &a.query, "--allocation": &a.allocation, "--binary": &a.binary, "--report": &a.report}
	for i := 0; i < len(argv); i++ {
		name, ok := values[argv[i]]
		if !ok {
			return a, fmt.Errorf("unknown flag or positional argument %q", argv[i])
		}
		if seen[argv[i]] {
			return a, fmt.Errorf("duplicate flag %q", argv[i])
		}
		seen[argv[i]] = true
		if i+1 >= len(argv) || strings.TrimSpace(argv[i+1]) == "" || strings.HasPrefix(argv[i+1], "-") {
			return a, fmt.Errorf("flag %s requires a non-empty value", argv[i])
		}
		i++
		*name = argv[i]
	}
	for flag, value := range values {
		if !seen[flag] || strings.TrimSpace(*value) == "" {
			return a, fmt.Errorf("missing required flag %s", flag)
		}
	}
	return a, nil
}

func runAnalyze(argv []string, deps *runnerDeps) error {
	a, err := parseAnalyzeArgs(argv)
	if err != nil {
		return err
	}
	if err = refuseExisting(a.report); err != nil {
		return err
	}
	m, err := loadManifest(filepath.Join(a.publication, "input-manifest.json"))
	if err != nil {
		return fmt.Errorf("load input manifest: %w", err)
	}
	binarySHA, err := hashBinary(a.binary)
	if err != nil {
		return fmt.Errorf("hash binary: %w", err)
	}
	if deps.loadGates == nil {
		deps.loadGates = vectorhydration.LoadExternalGates
	}
	gates, err := deps.loadGates(a.registry, a.registrySHA, map[string]string{"semantic": a.semantic, "query": a.query, "allocation": a.allocation})
	if err != nil {
		return fmt.Errorf("load external gates: %w", err)
	}
	binary := vectorhydration.BenchmarkBinary{SourceCommit: m.SourceCommit, BinaryPath: a.binary, SHA256: binarySHA}
	if deps.analyzePublication == nil {
		deps.analyzePublication = vectorhydration.AnalyzePublication
	}
	report, err := deps.analyzePublication(a.publication, gates, binary)
	if err != nil {
		return fmt.Errorf("analyze publication: %w", err)
	}
	if err = publishReport(a.report, report); err != nil {
		return fmt.Errorf("publish report: %w", err)
	}
	if report.Status != "PASS" {
		return exitError{code: 1, err: fmt.Errorf("analysis status: %s", report.Status)}
	}
	return nil
}

func hashBinary(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("binary must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func publishReport(path string, report vectorhydration.AnalysisReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	parent := filepath.Dir(path)
	if err = os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".report-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Link(tmpPath, path); err != nil {
		return err
	}
	return nil
}
