package vectorhydration

// This file is intentionally an execution-only boundary. It records process
// observations; it does not select, retry, or analyse observations.
import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Measurement string

const (
	MeasurementLegacy Measurement = "legacy"
	MeasurementBatch  Measurement = "batch"
)

type Clock interface{ Now() time.Time }
type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

// Command receives the sealed schedule entry. It must return the complete
// command identity, including environment, for one fresh process.
type Command func(ScheduleEntry, Measurement) (ProcessRequest, error)
type CollectorConfig struct {
	OutputDir    string
	Measurements []Measurement
	Command      Command
	Executor     Executor
	Clock        Clock
}

type Result struct {
	Cell            int            `json:"cell"`
	Block           int            `json:"block"`
	Order           Order          `json:"order"`
	BlockID         string         `json:"block_id"`
	RunID           string         `json:"run_id"`
	Sequence        int            `json:"sequence"`
	Measurement     Measurement    `json:"measurement"`
	Request         ProcessRequest `json:"request"`
	PID             int            `json:"pid"`
	ProcessIdentity string         `json:"process_identity"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
	ExitCode        int            `json:"exit_code"`
	Failed          bool           `json:"failed"`
	Error           string         `json:"error,omitempty"`
	Benchmark       string         `json:"benchmark,omitempty"`
	NsPerOp         float64        `json:"ns_per_op,omitempty"`
	BytesPerOp      float64        `json:"bytes_per_op,omitempty"`
	AllocsPerOp     float64        `json:"allocs_per_op,omitempty"`
	StdoutSHA256    string         `json:"stdout_sha256"`
	StderrSHA256    string         `json:"stderr_sha256"`
	RawStdout       string         `json:"raw_stdout"`
	RawStderr       string         `json:"raw_stderr"`
}

var benchmarkLine = regexp.MustCompile(`(?m)^([^\s]+)-[1-9][0-9]*\s+\d+\s+([0-9]+(?:\.[0-9]+)?)\s+ns/op(?:\s+([0-9]+(?:\.[0-9]+)?)\s+B/op)?(?:\s+([0-9]+(?:\.[0-9]+)?)\s+allocs/op)?\s*$`)

func benchmarkIdentity(manifest Manifest, measurement Measurement) (string, error) {
	switch measurement {
	case MeasurementLegacy:
		return manifest.LegacyBenchmark, nil
	case MeasurementBatch:
		return manifest.BatchBenchmark, nil
	default:
		return "", fmt.Errorf("unknown measurement %q", measurement)
	}
}

func sealedRequestIdentity(manifest Manifest, entry ScheduleEntry, measurement Measurement, sequence int) string {
	return fmt.Sprintf("campaign:%s/phase:%s/run:%s/block:%d/cell:%d/measurement:%s/sequence:%d", manifest.Campaign.ID, manifest.Phase.ID, manifest.Run.ID, entry.Block, entry.Cell, measurement, sequence)
}

func hasExactGOMAXPROCS(env []string, cell int) bool {
	want, count, match := strconv.Itoa(cell), 0, false
	for _, value := range env {
		parts := strings.SplitN(value, "=", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "GOMAXPROCS") {
			count++
			match = match || parts[1] == want
		}
	}
	return count == 1 && match
}

func parseBenchmark(out []byte, known map[string]bool) (string, float64, float64, float64, error) {
	matches := benchmarkLine.FindAllStringSubmatch(string(out), -1)
	if len(matches) != 1 {
		if len(matches) > 1 {
			return "", 0, 0, 0, errors.New("benchmark output must contain exactly one record")
		}
		return "", 0, 0, 0, errors.New("malformed benchmark output")
	}
	m := matches[0]
	name := m[1]
	if !known[name] {
		return "", 0, 0, 0, fmt.Errorf("unknown benchmark %q", name)
	}
	parse := func(s string) float64 {
		if s == "" {
			return 0
		}
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	return name, parse(m[2]), parse(m[3]), parse(m[4]), nil
}
func parseBenchmarkExpected(out []byte, expected string) (string, float64, float64, float64, error) {
	name, n, b, a, err := parseBenchmark(out, map[string]bool{expected: true})
	if err != nil {
		return "", 0, 0, 0, err
	}
	if b == 0 || a == 0 {
		return "", 0, 0, 0, errors.New("benchmark must report B/op and allocs/op")
	}
	return name, n, b, a, nil
}

// Collect executes every schedule entry once for every requested measurement.
// A failed process is retained as a Result and never causes a replacement run.
func Collect(ctx context.Context, manifest Manifest, cfg CollectorConfig) error {
	if err := manifest.Validate(); err != nil {
		return err
	}
	var err error
	if strings.TrimSpace(cfg.OutputDir) == "" || cfg.Command == nil || cfg.Executor == nil {
		return errors.New("output directory, command, and executor are required")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realClock{}
	}
	measurements := cfg.Measurements
	if len(measurements) == 0 {
		measurements = []Measurement{MeasurementLegacy, MeasurementBatch}
	}
	for _, m := range measurements {
		if m != MeasurementLegacy && m != MeasurementBatch {
			return fmt.Errorf("unknown measurement %q", m)
		}
	}
	parent := filepath.Dir(cfg.OutputDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	if err := os.Mkdir(cfg.OutputDir, 0755); err != nil {
		return fmt.Errorf("output is reserved or exists: %w", err)
	}
	stage := cfg.OutputDir
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(stage)
		}
	}()
	rawDir := filepath.Join(stage, "raw")
	if err := os.Mkdir(rawDir, 0755); err != nil {
		return err
	}
	var results []Result
	var raws []rawRef
	seq := 0
	for _, entry := range manifest.Schedule {
		var ordered []Measurement
		switch entry.Order {
		case OrderAB:
			ordered = []Measurement{MeasurementLegacy, MeasurementBatch}
		case OrderBA:
			ordered = []Measurement{MeasurementBatch, MeasurementLegacy}
		default:
			return errors.New("invalid schedule order")
		}
		for _, measurement := range ordered {
			seq++
			req, e := cfg.Command(entry, measurement)
			if e != nil {
				return e
			}
			req.Env, e = effectiveEnv(req.Env, entry.Cell)
			if e != nil {
				return e
			}
			req.Identity = sealedRequestIdentity(manifest, entry, measurement, seq)
			started := clock.Now()
			x := cfg.Executor.Execute(ctx, req)
			finished := clock.Now()
			if x.StartedAt.IsZero() {
				x.StartedAt = started
			}
			if x.FinishedAt.IsZero() {
				x.FinishedAt = finished
			}
			prefix := fmt.Sprintf("%03d-%s-%s", seq, entry.RunID, measurement)
			stdoutName, stderrName := prefix+".stdout", prefix+".stderr"
			for _, raw := range []struct {
				name string
				data []byte
			}{{stdoutName, x.Stdout}, {stderrName, x.Stderr}} {
				if err := os.WriteFile(filepath.Join(rawDir, raw.name), raw.data, 0644); err != nil {
					return err
				}
				raws = append(raws, rawRef{raw.name, hashBytes(raw.data)})
			}
			if x.PID <= 0 || strings.TrimSpace(x.ProcessIdentity) == "" {
				return errors.New("executor must report a process identity")
			}
			r := Result{Cell: entry.Cell, Block: entry.Block, Order: entry.Order, BlockID: entry.BlockID, RunID: entry.RunID, Sequence: seq, Measurement: measurement, Request: req, PID: x.PID, ProcessIdentity: x.ProcessIdentity, StartedAt: x.StartedAt, FinishedAt: x.FinishedAt, ExitCode: x.ExitCode, StdoutSHA256: hashBytes(x.Stdout), StderrSHA256: hashBytes(x.Stderr), RawStdout: stdoutName, RawStderr: stderrName}
			if x.Err != nil || x.ExitCode != 0 {
				r.Failed = true
				if x.Err != nil {
					r.Error = x.Err.Error()
				}
			} else {
				benchmark, err := benchmarkIdentity(manifest, measurement)
				if err != nil {
					return err
				}
				r.Benchmark, r.NsPerOp, r.BytesPerOp, r.AllocsPerOp, e = parseBenchmarkExpected(x.Stdout, benchmark)
				if e != nil {
					return e
				}
			}
			results = append(results, r)
		}
	}
	input, err := manifest.CanonicalJSON()
	if err != nil {
		return err
	}
	om := outputManifest{SchemaVersion: outputManifestVersion, Campaign: manifest.Campaign.ID, Phase: manifest.Phase.ID, Run: manifest.Run.ID, InputSHA256: hashBytes(input), ResultCount: len(results), Raw: raws}
	mb, err := canonical(om)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "manifest.json"), mb, 0644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "input-manifest.json"), input, 0644); err != nil {
		return err
	}
	rb, err := canonical(results)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "records.json"), rb, 0644); err != nil {
		return err
	}
	om.RecordsSHA256 = hashBytes(rb)
	mb, err = canonical(om)
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, "manifest.json"), mb, 0644); err != nil {
		return err
	}
	if err = verifyRawHashes(stage, raws); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(stage, ".complete"), []byte("complete\n"), 0600); err != nil {
		return err
	}
	published = true
	return nil
}
