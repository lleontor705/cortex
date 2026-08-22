package vectorhydration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	BootstrapResamples = 100000
	AllocationCap      = 25000.0
)

type AnalysisInput struct {
	Manifest Manifest
	Results  []Result
	Raw      map[string][]byte
	Gates    ExternalGates
	Binary   BenchmarkBinary
}
type CellReport struct {
	Cell, SampleSize int
	Median, LCB      float64
	Passed           bool
}
type AnalysisReport struct {
	Status      string // PASS, FAIL, BLOCKED, or INCONCLUSIVE
	Cells       []CellReport
	Seed        uint64
	Resamples   int
	GuardMargin float64
	Reasons     []string
}

func Analyze(in AnalysisInput) AnalysisReport {
	r := AnalysisReport{Status: "INCONCLUSIVE", Seed: in.Manifest.Seed, Resamples: BootstrapResamples, GuardMargin: GuardMargin}
	gateOK, gateReasons := gateResults(in)
	if !gateOK {
		r.Status, r.Reasons = "BLOCKED", gateReasons
		return r
	}
	if err := validatePreparedBinary(in); err != nil {
		r.Status, r.Reasons = "BLOCKED", []string{err.Error()}
		return r
	}
	if err := validateAnalysisInput(in); err != nil {
		r.Status, r.Reasons = "FAIL", []string{err.Error()}
		return r
	}
	byCell := make(map[int][]float64, len(RequiredCells))
	for _, cell := range RequiredCells {
		byCell[cell] = make([]float64, 0, PairedBlocksPerCell)
	}
	for _, e := range in.Manifest.Schedule {
		var legacy, batch Result
		for _, x := range in.Results {
			if x.Cell == e.Cell && x.Block == e.Block && x.Measurement == MeasurementLegacy {
				legacy = x
			}
			if x.Cell == e.Cell && x.Block == e.Block && x.Measurement == MeasurementBatch {
				batch = x
			}
		}
		byCell[e.Cell] = append(byCell[e.Cell], legacy.NsPerOp/batch.NsPerOp)
	}
	r.Cells = make([]CellReport, 0, len(RequiredCells))
	all := gateOK
	for _, cell := range RequiredCells {
		v := byCell[cell]
		median := quantile(v, .5)
		lcb := bcaLower(v, in.Manifest.Seed)
		ok := finite(median) && finite(lcb) && lcb >= LCBThreshold
		r.Cells = append(r.Cells, CellReport{Cell: cell, SampleSize: len(v), Median: median, LCB: lcb, Passed: ok})
		all = all && ok
		if !ok {
			r.Reasons = append(r.Reasons, fmt.Sprintf("c%d LCB is below %.2f or non-finite", cell, LCBThreshold))
		}
	}
	if all {
		r.Status = "PASS"
	} else {
		r.Status = "FAIL"
	}
	return r
}

func validateAnalysisInput(in AnalysisInput) error {
	if err := in.Manifest.Validate(); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if len(in.Results) != len(in.Manifest.Schedule)*2 {
		return fmt.Errorf("results must retain exactly %d paired observations", len(in.Manifest.Schedule)*2)
	}
	seen := make(map[string]bool, len(in.Results))
	seenPID := make(map[int]bool, len(in.Results))
	seenIdentity := make(map[string]bool, len(in.Results))
	if len(in.Raw) == 0 {
		return fmt.Errorf("raw stdout and stderr are required")
	}
	knownHashes := make(map[string]bool)
	rawRefs := make([]rawRef, 0, len(in.Raw))
	for name, data := range in.Raw {
		if filepath.Base(name) != name || strings.TrimSpace(name) == "" {
			return fmt.Errorf("non-canonical raw ref %q", name)
		}
		knownHashes[hashBytes(data)] = true
		rawRefs = append(rawRefs, rawRef{Name: name, SHA256: hashBytes(data)})
	}
	expected := make(map[string]ScheduleEntry, len(in.Manifest.Schedule))
	for _, e := range in.Manifest.Schedule {
		expected[fmt.Sprintf("%d/%d", e.Cell, e.Block)] = e
	}
	for _, x := range in.Results {
		key := fmt.Sprintf("%d/%d/%s", x.Cell, x.Block, x.Measurement)
		benchmark, err := benchmarkIdentity(in.Manifest, x.Measurement)
		if err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("duplicate result %s", key)
		}
		e, exists := expected[fmt.Sprintf("%d/%d", x.Cell, x.Block)]
		if !exists || x.Order != e.Order || x.BlockID != e.BlockID || x.RunID != e.RunID {
			return fmt.Errorf("result identity does not match schedule at %s", key)
		}
		if !hasExactGOMAXPROCS(x.Request.Env, e.Cell) {
			return fmt.Errorf("GOMAXPROCS does not match schedule at %s", key)
		}
		seen[key] = true
		if x.Sequence < 1 || x.Sequence > len(in.Results) {
			return fmt.Errorf("invalid sequence at %s", key)
		}
		position := (e.Block-1)*len(RequiredCells) + indexOfCell(e.Cell)
		wantSeq := position*2 + 1
		if (e.Order == OrderAB && x.Measurement == MeasurementBatch) || (e.Order == OrderBA && x.Measurement == MeasurementLegacy) {
			wantSeq++
		}
		if x.Sequence != wantSeq {
			return fmt.Errorf("sequence is not sealed to schedule at %s", key)
		}
		if x.Request.Identity != sealedRequestIdentity(in.Manifest, e, x.Measurement, x.Sequence) {
			return fmt.Errorf("request identity is not sealed to schedule at %s", key)
		}
		if x.PID <= 0 || strings.TrimSpace(x.ProcessIdentity) == "" {
			return fmt.Errorf("missing process identity at %s", key)
		}
		if seenPID[x.PID] || seenIdentity[x.ProcessIdentity] {
			return fmt.Errorf("duplicate process identity at %s", key)
		}
		seenPID[x.PID], seenIdentity[x.ProcessIdentity] = true, true
		if filepath.Base(x.RawStdout) != x.RawStdout || filepath.Base(x.RawStderr) != x.RawStderr || x.RawStdout == "" || x.RawStderr == "" {
			return fmt.Errorf("non-canonical raw names at %s", key)
		}
		for _, prior := range in.Results {
			if prior.Sequence != x.Sequence && prior.PID == x.PID && prior.PID > 0 {
				return fmt.Errorf("duplicate pid at %s", key)
			}
		}
		if x.Failed || x.ExitCode != 0 || x.BlockID == "" || x.RunID == "" {
			return fmt.Errorf("failed or malformed process at %s", key)
		}
		if x.StartedAt.IsZero() || x.FinishedAt.IsZero() || !x.FinishedAt.After(x.StartedAt) {
			return fmt.Errorf("zero or invalid duration at %s", key)
		}
		if !finite(x.NsPerOp) || x.NsPerOp <= 0 || x.AllocsPerOp <= 0 || !finite(x.AllocsPerOp) || x.AllocsPerOp > AllocationCap {
			return fmt.Errorf("invalid timing or allocation at %s", key)
		}
		for _, h := range []string{x.StdoutSHA256, x.StderrSHA256} {
			b, err := hex.DecodeString(h)
			if err != nil || len(b) != sha256.Size {
				return fmt.Errorf("invalid output hash at %s", key)
			}
		}
		if !knownHashes[x.StdoutSHA256] || !knownHashes[x.StderrSHA256] {
			return fmt.Errorf("retained output hash is not valid at %s", key)
		}
		stdout, ok := in.Raw[x.RawStdout]
		if !ok || hashBytes(stdout) != x.StdoutSHA256 {
			return fmt.Errorf("named raw stdout does not match at %s", key)
		}
		stderr, ok := in.Raw[x.RawStderr]
		if !ok || hashBytes(stderr) != x.StderrSHA256 {
			return fmt.Errorf("named raw stderr does not match at %s", key)
		}
		name, n, bytes, allocs, err := parseBenchmarkExpected(stdout, benchmark)
		if err != nil || name != x.Benchmark || n != x.NsPerOp || bytes != x.BytesPerOp || allocs != x.AllocsPerOp {
			return fmt.Errorf("raw metrics do not match record at %s", key)
		}
	}
	for i := 1; i <= len(in.Results); i++ {
		found := false
		for _, x := range in.Results {
			if x.Sequence == i {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("sequence gap at %d", i)
		}
	}
	for _, e := range in.Manifest.Schedule {
		for _, m := range []Measurement{MeasurementLegacy, MeasurementBatch} {
			key := fmt.Sprintf("%d/%d/%s", e.Cell, e.Block, m)
			if !seen[key] {
				return fmt.Errorf("missing block result %s", key)
			}
		}
	}
	if err := validateRawConsumption(rawRefs, in.Results); err != nil {
		return err
	}
	return nil
}
func indexOfCell(cell int) int {
	for i, c := range RequiredCells {
		if c == cell {
			return i
		}
	}
	return -1
}

func AnalyzePublication(dir string, gates ExternalGates, binary BenchmarkBinary) (AnalysisReport, error) {
	for _, name := range []string{"semantic", "query", "allocation"} {
		a, ok := gates.artifacts[name]
		trusted, trustedOK := gates.trustedSHA256[name]
		if !ok || !trustedOK || trusted != a.SHA256 || a.SHA256 == "" {
			return AnalysisReport{Status: "BLOCKED", Reasons: []string{"gate is not preregistered: " + name}}, fmt.Errorf("untrusted gate %s", name)
		}
	}
	if err := ValidateOutput(dir); err != nil {
		return AnalysisReport{Status: "BLOCKED", Reasons: []string{err.Error()}}, err
	}
	omBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return AnalysisReport{}, err
	}
	var om outputManifest
	if err = json.Unmarshal(omBytes, &om); err != nil {
		return AnalysisReport{}, err
	}
	inputBytes, err := os.ReadFile(filepath.Join(dir, "input-manifest.json"))
	if err != nil {
		return AnalysisReport{}, err
	}
	if hashBytes(inputBytes) != om.InputSHA256 {
		return AnalysisReport{}, fmt.Errorf("input manifest digest mismatch")
	}
	var m Manifest
	if err = json.Unmarshal(inputBytes, &m); err != nil {
		return AnalysisReport{}, err
	}
	if om.Campaign != m.Campaign.ID || om.Phase != m.Phase.ID || om.Run != m.Run.ID {
		return AnalysisReport{}, fmt.Errorf("publication identity does not match input manifest")
	}
	recordBytes, err := os.ReadFile(filepath.Join(dir, "records.json"))
	if err != nil {
		return AnalysisReport{}, err
	}
	if hashBytes(recordBytes) != om.RecordsSHA256 {
		return AnalysisReport{}, fmt.Errorf("records digest mismatch")
	}
	var records []Result
	if err = json.Unmarshal(recordBytes, &records); err != nil {
		return AnalysisReport{}, err
	}
	if len(records) != om.ResultCount {
		return AnalysisReport{}, fmt.Errorf("record count mismatch")
	}
	raw := map[string][]byte{}
	for _, ref := range om.Raw {
		if filepath.Base(ref.Name) != ref.Name {
			return AnalysisReport{}, fmt.Errorf("non-canonical raw ref")
		}
		b, e := os.ReadFile(filepath.Join(dir, "raw", ref.Name))
		if e != nil {
			return AnalysisReport{}, e
		}
		if hashBytes(b) != ref.SHA256 {
			return AnalysisReport{}, fmt.Errorf("raw digest mismatch")
		}
		raw[ref.Name] = b
	}
	for i := range records {
		if records[i].RawStdout == "" {
			return AnalysisReport{}, fmt.Errorf("record %d has no raw stdout ref", i)
		}
		if records[i].RawStderr == "" {
			return AnalysisReport{}, fmt.Errorf("record %d has no raw stderr ref", i)
		}
		b, ok := raw[records[i].RawStdout]
		if !ok {
			return AnalysisReport{}, fmt.Errorf("missing named raw stdout")
		}
		stderr, ok := raw[records[i].RawStderr]
		if !ok {
			return AnalysisReport{}, fmt.Errorf("missing named raw stderr")
		}
		if hashBytes(stderr) != records[i].StderrSHA256 {
			return AnalysisReport{}, fmt.Errorf("raw stderr digest mismatch at %d", i)
		}
		if hashBytes(b) != records[i].StdoutSHA256 {
			return AnalysisReport{}, fmt.Errorf("raw stdout digest mismatch at %d", i)
		}
		benchmark, e := benchmarkIdentity(m, records[i].Measurement)
		if e != nil {
			return AnalysisReport{}, e
		}
		name, n, by, a, e := parseBenchmarkExpected(b, benchmark)
		if e != nil || name != records[i].Benchmark || n != records[i].NsPerOp || by != records[i].BytesPerOp || a != records[i].AllocsPerOp {
			return AnalysisReport{}, fmt.Errorf("raw metrics mismatch at %d", i)
		}
	}
	return Analyze(AnalysisInput{Manifest: m, Results: records, Raw: raw, Gates: gates, Binary: binary}), nil
}
func quantile(v []float64, p float64) float64 {
	x := append([]float64(nil), v...)
	sort.Float64s(x)
	if len(x) == 0 {
		return math.NaN()
	}
	z := p * float64(len(x)-1)
	lo := int(z)
	hi := int(math.Ceil(z))
	return x[lo] + (z-float64(lo))*(x[hi]-x[lo])
}
func normalCDF(x float64) float64 { return .5 * (1 + math.Erf(x/math.Sqrt2)) }
func normalInv(p float64) float64 {
	if p <= 0 || p >= 1 {
		return math.NaN()
	}
	lo, hi := -9.0, 9.0
	for i := 0; i < 80; i++ {
		m := (lo + hi) / 2
		if normalCDF(m) < p {
			lo = m
		} else {
			hi = m
		}
	}
	return (lo + hi) / 2
}
func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
