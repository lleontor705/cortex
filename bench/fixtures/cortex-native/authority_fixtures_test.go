package cortexnative

import (
	"bufio"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/bench/common"
)

const (
	authorityCorpusSchemaVersion = "cortex.retrieval-corpus/v1"
	authorityCorpusVersion       = "2026-07-22.1"
)

var authorityDimensions = []string{
	"project", "record_kind", "type", "scope", "privacy", "owner", "tags",
	"classification", "confidence", "source", "lifecycle", "temporal", "provenance",
}

type authorityFixture struct {
	CorpusSchemaVersion string                    `json:"corpus_schema_version"`
	CorpusVersion       string                    `json:"corpus_version"`
	ID                  string                    `json:"id"`
	Class               string                    `json:"class"`
	Query               authorityQuery            `json:"query"`
	Records             []authorityRecord         `json:"records"`
	DimensionLabels     []authorityDimensionLabel `json:"dimension_labels"`
	Labels              authorityLabels           `json:"labels"`
	ExpectedErrors      []string                  `json:"expected_errors"`
}

type authorityQuery struct {
	ID               string `json:"id"`
	Profile          string `json:"profile"`
	PrincipalProject string `json:"principal_project"`
	PrincipalSubject string `json:"principal_subject"`
	ValidAt          string `json:"valid_at"`
}

type authorityRecord struct {
	ID              string            `json:"id"`
	RecordKind      string            `json:"record_kind"`
	Project         string            `json:"project"`
	Type            string            `json:"type"`
	Scope           string            `json:"scope"`
	Privacy         string            `json:"privacy"`
	Owner           string            `json:"owner,omitempty"`
	Tags            []string          `json:"tags"`
	Classification  map[string]string `json:"classification"`
	RegistryVersion string            `json:"registry_version"`
	Confidence      *confidenceLabel  `json:"confidence,omitempty"`
	Source          string            `json:"source"`
	Lifecycle       lifecycleLabel    `json:"lifecycle"`
	Temporal        temporalLabel     `json:"temporal"`
	Provenance      *provenanceLabel  `json:"provenance,omitempty"`
	Supersedes      string            `json:"supersedes,omitempty"`
}

type confidenceLabel struct {
	Applies bool     `json:"applies"`
	Value   *float64 `json:"value"`
}

type lifecycleLabel struct {
	State  string           `json:"state"`
	Events []lifecycleEvent `json:"events"`
}

type lifecycleEvent struct {
	Type        string `json:"type"`
	RecordedAt  string `json:"recorded_at"`
	EffectiveAt string `json:"effective_at"`
}

type temporalLabel struct {
	RecordedAt string `json:"recorded_at"`
	ValidFrom  string `json:"valid_from"`
	ValidUntil string `json:"valid_until,omitempty"`
}

type provenanceLabel struct {
	Citations         []common.EvidenceSpan `json:"citations"`
	DerivationID      string                `json:"derivation_id"`
	DerivationVersion string                `json:"derivation_version"`
}

type authorityDimensionLabel struct {
	Dimension   string   `json:"dimension"`
	Class       string   `json:"class"`
	EvidenceIDs []string `json:"evidence_ids"`
}

type authorityLabels struct {
	Relevant        []common.EvidenceRef `json:"relevant"`
	HardNegativeIDs []string             `json:"hard_negative_ids"`
	NoAnswer        *bool                `json:"no_answer"`
	Eligibility     eligibilityLabels    `json:"eligibility"`
}

type eligibilityLabels struct {
	Ordinary []string `json:"ordinary"`
	Archive  []string `json:"archive"`
	AsOf     []string `json:"as_of"`
}

func TestAuthorityFixtures(t *testing.T) {
	fixtures := readAuthorityFixtures(t, "authority.jsonl")
	if len(fixtures) == 0 {
		t.Fatal("authority fixtures are empty")
	}

	classesByDimension := make(map[string]map[string]bool, len(authorityDimensions))
	registeredTypes := make(map[string]bool)
	lifecycleStates := make(map[string]bool)
	stableIDs := make(map[string]bool)
	var sawMissingConfidence, sawZeroConfidence, sawRestored bool

	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			validateAuthorityFixture(t, fixture)
		})

		for _, label := range fixture.DimensionLabels {
			if classesByDimension[label.Dimension] == nil {
				classesByDimension[label.Dimension] = make(map[string]bool)
			}
			classesByDimension[label.Dimension][label.Class] = true
		}
		for _, record := range fixture.Records {
			if stableIDs[record.ID] {
				t.Errorf("stable record ID %q is reused across fixtures", record.ID)
			}
			stableIDs[record.ID] = true
			registeredTypes[record.Type] = true
			lifecycleStates[record.Lifecycle.State] = true
			if record.Confidence == nil {
				sawMissingConfidence = true
			} else if record.Confidence.Applies && record.Confidence.Value != nil && *record.Confidence.Value == 0 {
				sawZeroConfidence = true
			}
			for _, event := range record.Lifecycle.Events {
				if event.Type == "restore" {
					sawRestored = true
				}
			}
		}
	}

	for _, dimension := range authorityDimensions {
		for _, class := range []string{"positive", "negative", "boundary"} {
			if !classesByDimension[dimension][class] {
				t.Errorf("dimension %q has no %s label", dimension, class)
			}
		}
	}
	for _, typ := range []string{"bugfix", "decision", "discovery", "config"} {
		if !registeredTypes[typ] {
			t.Errorf("registered type %q is not represented", typ)
		}
	}
	for _, state := range []string{"active", "archived", "soft_deleted", "superseded"} {
		if !lifecycleStates[state] {
			t.Errorf("lifecycle state %q is not represented", state)
		}
	}
	if !sawRestored {
		t.Error("restored lifecycle transition is not represented")
	}
	if !sawMissingConfidence || !sawZeroConfidence {
		t.Errorf("confidence labels must distinguish missing (%t) from explicit zero (%t)", sawMissingConfidence, sawZeroConfidence)
	}
}

func readAuthorityFixtures(t *testing.T, path string) []authorityFixture {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open authority fixtures: %v", err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close authority fixtures: %v", err)
		}
	})

	var fixtures []authorityFixture
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		var fixture authorityFixture
		if err := json.Unmarshal(scanner.Bytes(), &fixture); err != nil {
			t.Fatalf("decode authority fixture line %d: %v", line, err)
		}
		fixtures = append(fixtures, fixture)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan authority fixtures: %v", err)
	}
	return fixtures
}

func validateAuthorityFixture(t *testing.T, fixture authorityFixture) {
	t.Helper()

	if fixture.CorpusSchemaVersion != authorityCorpusSchemaVersion || fixture.CorpusVersion != authorityCorpusVersion {
		t.Fatalf("corpus version = %q/%q, want %q/%q", fixture.CorpusSchemaVersion, fixture.CorpusVersion, authorityCorpusSchemaVersion, authorityCorpusVersion)
	}
	if fixture.ID == "" || fixture.Query.ID == "" {
		t.Fatal("stable fixture and query IDs are required")
	}
	if !slices.Contains([]string{"positive", "negative", "boundary"}, fixture.Class) {
		t.Fatalf("class %q is not registered", fixture.Class)
	}
	if fixture.Query.Profile == "" || fixture.Query.PrincipalProject == "" || fixture.Query.PrincipalSubject == "" {
		t.Fatal("query profile, project, and subject are required")
	}
	if _, err := time.Parse(time.RFC3339, fixture.Query.ValidAt); err != nil {
		t.Fatalf("query valid_at: %v", err)
	}
	if fixture.Class == "negative" && len(fixture.ExpectedErrors) == 0 {
		t.Fatal("negative fixture requires stable expected_errors")
	}
	if fixture.Class != "negative" && len(fixture.ExpectedErrors) != 0 {
		t.Fatal("only negative fixtures may have expected_errors")
	}

	records := make(map[string]authorityRecord, len(fixture.Records))
	for _, record := range fixture.Records {
		if record.ID == "" {
			t.Fatal("record stable ID is required")
		}
		if _, duplicate := records[record.ID]; duplicate {
			t.Fatalf("record stable ID %q is duplicated", record.ID)
		}
		records[record.ID] = record
		validateRecord(t, fixture, record)
	}
	for _, record := range fixture.Records {
		validateLineage(t, records, record)
	}

	seenDimensions := make(map[string]bool, len(fixture.DimensionLabels))
	for _, label := range fixture.DimensionLabels {
		if !slices.Contains(authorityDimensions, label.Dimension) {
			t.Errorf("dimension %q is not registered", label.Dimension)
		}
		if label.Class != fixture.Class {
			t.Errorf("dimension %q class = %q, want fixture class %q", label.Dimension, label.Class, fixture.Class)
		}
		if seenDimensions[label.Dimension] {
			t.Errorf("dimension %q is labelled more than once", label.Dimension)
		}
		seenDimensions[label.Dimension] = true
		if len(label.EvidenceIDs) == 0 {
			t.Errorf("dimension %q has no stable evidence IDs", label.Dimension)
		}
		validateIDs(t, records, label.EvidenceIDs, "dimension evidence")
	}
	for _, dimension := range authorityDimensions {
		if !seenDimensions[dimension] {
			t.Errorf("dimension %q has no fixture label", dimension)
		}
	}

	if fixture.Labels.Relevant == nil || fixture.Labels.HardNegativeIDs == nil || fixture.Labels.NoAnswer == nil {
		t.Fatal("relevant, hard-negative, and no-answer labels must be explicit")
	}
	if fixture.Labels.Eligibility.Ordinary == nil || fixture.Labels.Eligibility.Archive == nil || fixture.Labels.Eligibility.AsOf == nil {
		t.Fatal("ordinary, archive, and as-of eligibility labels must be explicit")
	}
	for _, evidence := range fixture.Labels.Relevant {
		if err := evidence.Validate(); err != nil {
			t.Errorf("relevant evidence: %v", err)
			continue
		}
		id := evidence.EpisodeID
		if evidence.FactID != "" {
			id = evidence.FactID
		}
		if evidence.Span != nil {
			id = evidence.Span.EpisodeID
		}
		validateIDs(t, records, []string{id}, "relevant evidence")
	}
	validateIDs(t, records, fixture.Labels.HardNegativeIDs, "hard negative")
	validateIDs(t, records, fixture.Labels.Eligibility.Ordinary, "ordinary eligibility")
	validateIDs(t, records, fixture.Labels.Eligibility.Archive, "archive eligibility")
	validateIDs(t, records, fixture.Labels.Eligibility.AsOf, "as-of eligibility")
}

func validateRecord(t *testing.T, fixture authorityFixture, record authorityRecord) {
	t.Helper()

	if fixture.Class != "negative" {
		for field, value := range map[string]string{
			"record_kind": record.RecordKind, "project": record.Project, "type": record.Type,
			"scope": record.Scope, "privacy": record.Privacy, "registry_version": record.RegistryVersion,
			"source": record.Source, "lifecycle.state": record.Lifecycle.State,
		} {
			if value == "" {
				t.Errorf("record %q: %s is required", record.ID, field)
			}
		}
		if record.Confidence == nil {
			t.Errorf("record %q: confidence must be explicit", record.ID)
		}
	}
	if record.Scope == "personal" || record.Privacy == "private" {
		if record.Owner == "" {
			t.Errorf("record %q: owner is required for personal/private authority", record.ID)
		}
	}
	if record.Scope == "personal" && record.Privacy == "shared" {
		t.Errorf("record %q: personal/shared is invalid", record.ID)
	}
	if !slices.IsSorted(record.Tags) || hasDuplicates(record.Tags) {
		t.Errorf("record %q: tags must be normalized, unique, and sorted", record.ID)
	}
	for _, tag := range record.Tags {
		if tag != strings.ToLower(strings.TrimSpace(tag)) {
			t.Errorf("record %q: tag %q is not normalized", record.ID, tag)
		}
	}
	for key, value := range record.Classification {
		if key != strings.ToLower(strings.TrimSpace(key)) || value != strings.ToLower(strings.TrimSpace(value)) {
			t.Errorf("record %q: classification %q=%q is not normalized", record.ID, key, value)
		}
	}
	if record.Confidence != nil {
		if record.Confidence.Applies && record.Confidence.Value == nil {
			t.Errorf("record %q: applicable confidence requires a value", record.ID)
		}
		if !record.Confidence.Applies && record.Confidence.Value != nil {
			t.Errorf("record %q: non-applicable confidence must omit value", record.ID)
		}
		if record.Confidence.Value != nil && (*record.Confidence.Value < 0 || *record.Confidence.Value > 1) {
			t.Errorf("record %q: confidence must be in [0,1]", record.ID)
		}
	}
	validateTime(t, record.ID+" recorded_at", record.Temporal.RecordedAt)
	validFrom := validateTime(t, record.ID+" valid_from", record.Temporal.ValidFrom)
	if record.Temporal.ValidUntil != "" {
		validUntil := validateTime(t, record.ID+" valid_until", record.Temporal.ValidUntil)
		if !validUntil.After(validFrom) {
			t.Errorf("record %q: valid_until must be after valid_from", record.ID)
		}
	}
	for _, event := range record.Lifecycle.Events {
		validateTime(t, record.ID+" lifecycle recorded_at", event.RecordedAt)
		validateTime(t, record.ID+" lifecycle effective_at", event.EffectiveAt)
	}
	if record.RecordKind == "derived" {
		if record.Provenance == nil || len(record.Provenance.Citations) == 0 || record.Provenance.DerivationID == "" || record.Provenance.DerivationVersion == "" {
			t.Errorf("record %q: derived record requires episode citations and derivation identity/version", record.ID)
		}
	}
}

func validateLineage(t *testing.T, records map[string]authorityRecord, record authorityRecord) {
	t.Helper()

	if record.Supersedes != "" {
		if _, ok := records[record.Supersedes]; !ok {
			t.Errorf("record %q supersedes unknown stable ID %q", record.ID, record.Supersedes)
		}
	}
	if record.Provenance == nil {
		return
	}
	for _, citation := range record.Provenance.Citations {
		if err := (common.EvidenceRef{Span: &citation}).Validate(); err != nil {
			t.Errorf("record %q citation: %v", record.ID, err)
			continue
		}
		source, ok := records[citation.EpisodeID]
		if !ok {
			t.Errorf("record %q cites unknown episode %q", record.ID, citation.EpisodeID)
			continue
		}
		if source.RecordKind != "episode" {
			t.Errorf("record %q citation %q is not an episode", record.ID, citation.EpisodeID)
		}
		if source.Project != record.Project {
			t.Errorf("record %q citation %q crosses project authority", record.ID, citation.EpisodeID)
		}
	}
}

func validateIDs(t *testing.T, records map[string]authorityRecord, ids []string, label string) {
	t.Helper()
	for _, id := range ids {
		if _, ok := records[id]; !ok {
			t.Errorf("%s ID %q does not identify a fixture record", label, id)
		}
	}
}

func validateTime(t *testing.T, field, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Errorf("%s: %v", field, err)
	}
	return parsed
}

func hasDuplicates(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return true
		}
	}
	return false
}
