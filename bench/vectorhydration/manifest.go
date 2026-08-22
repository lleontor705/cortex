package vectorhydration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	SchemaVersion       = "vec-bench-stat/v2"
	CampaignID          = "VEC-BENCH-STAT"
	AmendmentVersion    = "1.0.0-draft.1"
	CampaignVersion     = AmendmentVersion
	PhaseSchemaVersion  = "1.0.0"
	RunSchemaVersion    = "1.0.0"
	SourceSchemaVersion = "1.0.0"
	GuardMargin         = 0.10
	LCBThreshold        = 5.10
	PairedBlocksPerCell = 20
	BenchmarkPackage    = "./internal/store/sqlite"
	LegacyBenchmark     = "BenchmarkHydrateLegacyGetByID_N100"
	BatchBenchmark      = "BenchmarkHydrateBatchGetByIDs_N100"
	maxJSONNestingDepth = 1024
)

var RequiredCells = [...]int{1, 2, 4}

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	idPattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*$`)
)

type CampaignManifest struct {
	Version string `json:"version"`
	ID      string `json:"id"`
}
type PhaseManifest struct {
	Version string `json:"version"`
	ID      string `json:"id"`
}
type RunManifest struct {
	Version string `json:"version"`
	ID      string `json:"id"`
}
type SourceMachineManifest struct {
	Version string `json:"version"`
	ID      string `json:"id"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	CPU     string `json:"cpu"`
}
type Manifest struct {
	SchemaVersion    string                `json:"schema_version"`
	Campaign         CampaignManifest      `json:"campaign"`
	Phase            PhaseManifest         `json:"phase"`
	Run              RunManifest           `json:"run"`
	SourceMachine    SourceMachineManifest `json:"source_machine"`
	SourceCommit     string                `json:"source_commit"`
	BenchmarkPackage string                `json:"benchmark_package"`
	LegacyBenchmark  string                `json:"legacy_benchmark"`
	BatchBenchmark   string                `json:"batch_benchmark"`
	Seed             uint64                `json:"seed"`
	Schedule         []ScheduleEntry       `json:"schedule"`
}

func (m *Manifest) UnmarshalJSON(data []byte) error {
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var decoded struct {
		SchemaVersion    string                `json:"schema_version"`
		Campaign         CampaignManifest      `json:"campaign"`
		Phase            PhaseManifest         `json:"phase"`
		Run              RunManifest           `json:"run"`
		SourceMachine    SourceMachineManifest `json:"source_machine"`
		SourceCommit     string                `json:"source_commit"`
		BenchmarkPackage string                `json:"benchmark_package"`
		LegacyBenchmark  string                `json:"legacy_benchmark"`
		BatchBenchmark   string                `json:"batch_benchmark"`
		Seed             uint64                `json:"seed"`
		Schedule         []ScheduleEntry       `json:"schedule"`
	}
	if err := dec.Decode(&decoded); err != nil {
		return err
	}
	if err := ensureJSONEOF(dec); err != nil {
		return err
	}
	*m = Manifest{
		SchemaVersion: decoded.SchemaVersion, Campaign: decoded.Campaign,
		Phase: decoded.Phase, Run: decoded.Run, SourceMachine: decoded.SourceMachine,
		SourceCommit: decoded.SourceCommit, BenchmarkPackage: decoded.BenchmarkPackage,
		LegacyBenchmark: decoded.LegacyBenchmark, BatchBenchmark: decoded.BatchBenchmark,
		Seed: decoded.Seed, Schedule: decoded.Schedule,
	}
	return nil
}
func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("manifest JSON must contain exactly one value")
		}
		return err
	}
	return nil
}
func rejectDuplicateJSONFields(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(dec); err != nil {
		return err
	}
	return ensureJSONEOF(dec)
}

type jsonContainer struct {
	kind json.Delim
	seen map[string]struct{}
}

func walkJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	stack := make([]jsonContainer, 0, 8)
	push := func(delim json.Delim) error {
		if len(stack) >= maxJSONNestingDepth {
			return fmt.Errorf("manifest JSON nesting exceeds %d levels", maxJSONNestingDepth)
		}
		stack = append(stack, jsonContainer{kind: delim, seen: map[string]struct{}{}})
		return nil
	}
	if delim, ok := token.(json.Delim); ok {
		if delim != '{' && delim != '[' {
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
		if err := push(delim); err != nil {
			return err
		}
	}
	for len(stack) > 0 {
		frame := &stack[len(stack)-1]
		if !dec.More() {
			closing, err := dec.Token()
			if err != nil {
				return err
			}
			want := json.Delim(']')
			if frame.kind == '{' {
				want = '}'
			}
			if closing != want {
				return fmt.Errorf("unexpected JSON delimiter %q", closing)
			}
			stack = stack[:len(stack)-1]
			continue
		}
		if frame.kind == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("manifest JSON object key is not a string")
			}
			if _, exists := frame.seen[name]; exists {
				return fmt.Errorf("duplicate manifest JSON field %q", name)
			}
			frame.seen[name] = struct{}{}
		}
		value, err := dec.Token()
		if err != nil {
			return err
		}
		if delim, ok := value.(json.Delim); ok {
			if delim != '{' && delim != '[' {
				return fmt.Errorf("unexpected JSON delimiter %q", delim)
			}
			if err := push(delim); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion || m.Seed == 0 {
		return fmt.Errorf("schema_version %q and non-zero seed are required", m.SchemaVersion)
	}
	if m.Campaign.ID != CampaignID || m.Campaign.Version != AmendmentVersion {
		return fmt.Errorf("campaign must be %q at amendment %q", CampaignID, AmendmentVersion)
	}
	if !commitPattern.MatchString(m.SourceCommit) || m.SourceCommit == strings.Repeat("0", 40) {
		return fmt.Errorf("source_commit must be a non-zero lowercase 40-hex Git commit")
	}
	if m.BenchmarkPackage != BenchmarkPackage {
		return fmt.Errorf("benchmark_package must be %q", BenchmarkPackage)
	}
	if m.LegacyBenchmark != LegacyBenchmark || m.BatchBenchmark != BatchBenchmark {
		return fmt.Errorf("legacy_benchmark and batch_benchmark must identify the registered benchmarks")
	}
	if m.LegacyBenchmark == m.BatchBenchmark {
		return fmt.Errorf("legacy_benchmark and batch_benchmark must be distinct")
	}
	if err := validIdentity("campaign", m.Campaign.Version, m.Campaign.ID); err != nil {
		return err
	}
	if err := validIdentity("phase", m.Phase.Version, m.Phase.ID); err != nil {
		return err
	}
	if err := validIdentity("run", m.Run.Version, m.Run.ID); err != nil {
		return err
	}
	if err := validIdentity("source_machine", m.SourceMachine.Version, m.SourceMachine.ID); err != nil {
		return err
	}
	for name, value := range map[string]string{"os": m.SourceMachine.OS, "arch": m.SourceMachine.Arch, "cpu": m.SourceMachine.CPU} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("source_machine.%s is required", name)
		}
	}
	want, err := Schedule(m.Seed, m.Campaign.ID, m.Phase.ID, m.Run.ID)
	if err != nil {
		return err
	}
	if len(m.Schedule) != len(want) {
		return fmt.Errorf("schedule must contain exactly %d entries", len(want))
	}
	for i := range want {
		if m.Schedule[i] != want[i] {
			return fmt.Errorf("schedule[%d] is missing, extra, or out of order", i)
		}
	}
	return nil
}
func validIdentity(name, version, id string) error {
	if strings.TrimSpace(version) == "" || strings.TrimSpace(id) == "" {
		return fmt.Errorf("%s version and id are required", name)
	}
	if name == "campaign" && version != CampaignVersion {
		return fmt.Errorf("campaign version must be %q", CampaignVersion)
	}
	expected := map[string]string{"phase": PhaseSchemaVersion, "run": RunSchemaVersion, "source_machine": SourceSchemaVersion}
	if want, ok := expected[name]; ok && version != want {
		return fmt.Errorf("%s version must be %q", name, want)
	}
	if name != "campaign" && !versionPattern.MatchString(version) {
		return fmt.Errorf("%s version has invalid format", name)
	}
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%s id has invalid format", name)
	}
	return nil
}
func (m Manifest) CanonicalJSON() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	return append(b, '\n'), nil
}
func (m Manifest) SHA256() (string, error) {
	b, err := m.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
