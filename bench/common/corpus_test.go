package common

import (
	"strings"
	"testing"
)

func TestCorpusValidateVersionedContract(t *testing.T) {
	corpus := validCorpus()

	if err := corpus.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestCorpusValidateRejectsIncompleteLabels(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Corpus)
		wantErr string
	}{
		{
			name: "missing schema version",
			mutate: func(c *Corpus) {
				c.SchemaVersion = ""
			},
			wantErr: "schema_version",
		},
		{
			name: "missing corpus version",
			mutate: func(c *Corpus) {
				c.Version = ""
			},
			wantErr: "version",
		},
		{
			name: "missing immutable query ID",
			mutate: func(c *Corpus) {
				c.Queries[0].ID = ""
			},
			wantErr: "query_id",
		},
		{
			name: "duplicate immutable query ID",
			mutate: func(c *Corpus) {
				c.Queries = append(c.Queries, c.Queries[0])
			},
			wantErr: "query_id",
		},
		{
			name: "missing profile class",
			mutate: func(c *Corpus) {
				c.Queries[0].ProfileClass = ""
			},
			wantErr: "profile_class",
		},
		{
			name: "missing query class",
			mutate: func(c *Corpus) {
				c.Queries[0].QueryClass = ""
			},
			wantErr: "query_class",
		},
		{
			name: "missing relevant evidence",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.Relevant = nil
			},
			wantErr: "relevant",
		},
		{
			name: "evidence missing immutable IDs and span",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.Relevant[0] = EvidenceRef{}
			},
			wantErr: "evidence",
		},
		{
			name: "evidence span missing episode ID",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.Relevant[0] = EvidenceRef{Span: &EvidenceSpan{StartByte: 1, EndByte: 2}}
			},
			wantErr: "episode_id",
		},
		{
			name: "missing hard negative labels",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.HardNegativeIDs = nil
			},
			wantErr: "hard_negative_ids",
		},
		{
			name: "missing no answer label",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.NoAnswer = nil
			},
			wantErr: "no_answer",
		},
		{
			name: "missing temporal labels",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.Temporal = nil
			},
			wantErr: "temporal",
		},
		{
			name: "missing isolation labels",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.Isolation = nil
			},
			wantErr: "isolation",
		},
		{
			name: "missing split policy",
			mutate: func(c *Corpus) {
				c.SplitPolicy = SplitPolicy{}
			},
			wantErr: "split_policy",
		},
		{
			name: "missing build metadata",
			mutate: func(c *Corpus) {
				c.Build = BuildMetadata{}
			},
			wantErr: "build",
		},
		{
			name: "missing hardware metadata",
			mutate: func(c *Corpus) {
				c.Hardware = HardwareMetadata{}
			},
			wantErr: "hardware",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			corpus := validCorpus()
			tt.mutate(&corpus)

			err := corpus.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %q, want field %q", err, tt.wantErr)
			}
		})
	}
}

func TestEvidenceRefValidateAcceptedForms(t *testing.T) {
	tests := []struct {
		name string
		ref  EvidenceRef
	}{
		{name: "episode ID", ref: EvidenceRef{EpisodeID: "episode-001"}},
		{name: "fact ID", ref: EvidenceRef{FactID: "fact-001"}},
		{name: "episode evidence span", ref: EvidenceRef{Span: &EvidenceSpan{EpisodeID: "episode-001", StartByte: 0, EndByte: 8}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.ref.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func validCorpus() Corpus {
	noAnswer := false
	return Corpus{
		SchemaVersion: "cortex.retrieval-corpus/v1",
		Version:       "2026-07-22.1",
		Queries: []CorpusQuery{{
			ID:           "query-001",
			Text:         "Which decision is currently valid?",
			ProfileClass: "temporal-as-of",
			QueryClass:   "single-hop",
			Split:        "held-out",
			Labels: EvidenceLabels{
				Relevant:        []EvidenceRef{{EpisodeID: "episode-001"}, {FactID: "fact-001"}},
				HardNegativeIDs: []string{"fact-stale-001"},
				NoAnswer:        &noAnswer,
				Temporal: &TemporalLabels{
					ValidAt:             "2026-07-01T00:00:00Z",
					EligibleEvidenceIDs: []string{"episode-001", "fact-001"},
				},
				Isolation: &IsolationLabels{
					PrincipalProject: "project-a",
					EligibleIDs:      []string{"episode-001", "fact-001"},
					ExcludedIDs:      []string{"project-b-fact-001"},
				},
			},
		}},
		SplitPolicy: SplitPolicy{Version: "v1", Strategy: "fixed-by-query-id"},
		Build:       BuildMetadata{Commit: "0123456789abcdef", Dirty: false},
		Hardware: HardwareMetadata{
			ProfileID: "ci-linux-amd64-v1",
			OS:        "linux",
			Arch:      "amd64",
			CPU:       "example-cpu",
			MemoryMB:  8192,
		},
	}
}
