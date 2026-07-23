package common

import (
	"fmt"
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
			name: "missing authored origin",
			mutate: func(c *Corpus) {
				c.Identity.Origin = ""
			},
			wantErr: "origin",
		},
		{
			name: "missing repository license",
			mutate: func(c *Corpus) {
				c.Identity.License = ""
			},
			wantErr: "license",
		},
		{
			name: "missing privacy review identity",
			mutate: func(c *Corpus) {
				c.Identity.PrivacyReview = ""
			},
			wantErr: "privacy_review",
		},
		{
			name: "too few records",
			mutate: func(c *Corpus) {
				c.Records = c.Records[:25]
			},
			wantErr: "at least 26 records",
		},
		{
			name: "duplicate record ID",
			mutate: func(c *Corpus) {
				c.Records[1].ID = c.Records[0].ID
			},
			wantErr: "record_id",
		},
		{
			name: "unresolved record provenance",
			mutate: func(c *Corpus) {
				c.Records[2].SourceEpisodeID = "missing-episode"
			},
			wantErr: "source_episode_id",
		},
		{
			name: "incomplete lifecycle matrix",
			mutate: func(c *Corpus) {
				for i := range c.Records {
					c.Records[i].Lifecycle = "active"
				}
			},
			wantErr: "lifecycle matrix",
		},
		{
			name: "insufficient cross-project collision pairs",
			mutate: func(c *Corpus) {
				c.Records[3].TopicKey = "unique-topic"
				c.Records[3].Content = "unique-content"
			},
			wantErr: "collision matrix",
		},
		{
			name: "too few immutable queries",
			mutate: func(c *Corpus) {
				c.Queries = c.Queries[:19]
			},
			wantErr: "at least 20 queries",
		},
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
				c.Queries[1].ID = c.Queries[0].ID
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
			name: "unresolved evidence reference",
			mutate: func(c *Corpus) {
				c.Queries[0].Labels.Relevant[0] = EvidenceRef{EpisodeID: "missing-record"}
			},
			wantErr: "unresolved",
		},
		{
			name: "future authority claimed as current success",
			mutate: func(c *Corpus) {
				c.Queries[0].Authority["privacy"] = CapabilityExecutedCurrentPath
			},
			wantErr: "not_executed_capability",
		},
		{
			name: "unknown future authority claimed as current success",
			mutate: func(c *Corpus) {
				c.Queries[0].Authority["future-vector-filter"] = CapabilityExecutedCurrentPath
			},
			wantErr: "not_executed_capability",
		},
		{
			name: "insufficient ranking coverage",
			mutate: func(c *Corpus) {
				c.Queries[0].Coverage = []string{CoverageFilterCollision}
			},
			wantErr: "ranking coverage",
		},
		{
			name: "incomplete query class matrix",
			mutate: func(c *Corpus) {
				for i := range c.Queries {
					c.Queries[i].QueryClass = "single-term-lexical"
				}
			},
			wantErr: "query class matrix",
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
	records := make([]CorpusRecord, 26)
	lifecycles := []string{"active", "archived", "soft_deleted", "superseded", "replacement", "stale_deletion_candidate"}
	types := []string{"decision", "bugfix", "discovery", "config"}
	for i := range records {
		project := "project-a"
		sourceEpisodeID := ""
		if i%2 == 1 {
			project = "project-b"
		}
		kind := "episode"
		if i >= 2 {
			kind = "fact"
			if project == "project-a" {
				sourceEpisodeID = "record-00"
			} else {
				sourceEpisodeID = "record-01"
			}
		}
		principalID := "principal-a"
		if project == "project-b" {
			principalID = "principal-b"
		}
		records[i] = CorpusRecord{
			ID:                fmt.Sprintf("record-%02d", i),
			Project:           project,
			Type:              types[i%len(types)],
			Kind:              kind,
			Scope:             []string{"project", "shared", "personal"}[i%3],
			Privacy:           []string{"project", "shared", "private"}[i%3],
			PrincipalID:       principalID,
			TopicKey:          fmt.Sprintf("topic-%02d", i),
			Content:           fmt.Sprintf("synthetic content %02d", i),
			Lifecycle:         lifecycles[i%len(lifecycles)],
			RecordedAt:        "2026-07-01T00:00:00Z",
			ValidFrom:         "2026-07-01T00:00:00Z",
			SourceEpisodeID:   sourceEpisodeID,
			DerivationID:      "synthetic-derivation",
			DerivationVersion: "v1",
		}
	}
	records[1].TopicKey = records[0].TopicKey
	records[1].Content = records[0].Content
	records[3].TopicKey = records[2].TopicKey
	records[3].Content = records[2].Content

	queries := make([]CorpusQuery, 20)
	queryClasses := []string{
		"exact-topic", "single-term-lexical", "multi-term-lexical", "project-filter",
		"type-filter", "scope-filter", "temporal-as-of", "collision", "evidence", "no-answer",
	}
	for i := range queries {
		coverage := CoverageRanking
		switch {
		case i >= 4 && i < 11:
			coverage = CoverageFilterCollision
		case i >= 11 && i < 15:
			coverage = CoverageLifecycleTemporal
		case i >= 15 && i < 18:
			coverage = CoverageProvenance
		case i >= 18:
			coverage = CoverageNoAnswer
		}
		isNoAnswer := i >= 18
		relevant := []EvidenceRef{{EpisodeID: "record-00"}}
		if isNoAnswer {
			relevant = nil
		}
		queries[i] = CorpusQuery{
			ID:           fmt.Sprintf("query-%03d", i),
			Text:         fmt.Sprintf("synthetic retrieval query %03d", i),
			ProfileClass: "lexical-fast",
			QueryClass:   queryClasses[i%len(queryClasses)],
			Split:        "held-out",
			Coverage:     []string{coverage},
			Authority: map[string]string{
				"project":    CapabilityExecutedCurrentPath,
				"type":       CapabilityExecutedCurrentPath,
				"scope":      CapabilityExecutedCurrentPath,
				"temporal":   CapabilityExecutedCurrentPath,
				"privacy":    CapabilityNotExecuted,
				"lifecycle":  CapabilityNotExecuted,
				"provenance": CapabilityNotExecuted,
			},
			Labels: EvidenceLabels{
				Relevant:        relevant,
				HardNegativeIDs: []string{"record-02"},
				NoAnswer:        &isNoAnswer,
				Temporal: &TemporalLabels{
					ValidAt:             "2026-07-01T00:00:00Z",
					EligibleEvidenceIDs: []string{"record-00"},
				},
				Isolation: &IsolationLabels{
					PrincipalProject: "project-a",
					EligibleIDs:      []string{"record-00"},
					ExcludedIDs:      []string{"record-01"},
				},
			},
		}
	}
	return Corpus{
		SchemaVersion: "cortex.retrieval-corpus/v1",
		Version:       "2026-07-22.1",
		Identity: CorpusIdentity{
			Origin:        "cortex-authored-synthetic",
			License:       "repository-license",
			PrivacyReview: "synthetic-no-personal-data",
		},
		Records:     records,
		Queries:     queries,
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
