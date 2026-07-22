package common

import "fmt"

// Corpus is the versioned, reproducible input contract for retrieval evaluation.
type Corpus struct {
	SchemaVersion string           `json:"schema_version"`
	Version       string           `json:"version"`
	Queries       []CorpusQuery    `json:"queries"`
	SplitPolicy   SplitPolicy      `json:"split_policy"`
	Build         BuildMetadata    `json:"build"`
	Hardware      HardwareMetadata `json:"hardware"`
}

// CorpusQuery identifies one immutable query and its complete evidence labels.
type CorpusQuery struct {
	ID           string         `json:"id"`
	Text         string         `json:"text"`
	ProfileClass string         `json:"profile_class"`
	QueryClass   string         `json:"query_class"`
	Split        string         `json:"split"`
	Labels       EvidenceLabels `json:"labels"`
}

// EvidenceLabels records relevance, negative, abstention, temporal, and isolation truth.
type EvidenceLabels struct {
	Relevant        []EvidenceRef    `json:"relevant"`
	HardNegativeIDs []string         `json:"hard_negative_ids"`
	NoAnswer        *bool            `json:"no_answer"`
	Temporal        *TemporalLabels  `json:"temporal"`
	Isolation       *IsolationLabels `json:"isolation"`
}

// EvidenceRef points to an immutable episode, fact, or byte span within an episode.
type EvidenceRef struct {
	EpisodeID string        `json:"episode_id,omitempty"`
	FactID    string        `json:"fact_id,omitempty"`
	Span      *EvidenceSpan `json:"span,omitempty"`
}

// EvidenceSpan identifies a half-open byte range in an immutable episode.
type EvidenceSpan struct {
	EpisodeID string `json:"episode_id"`
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
}

// TemporalLabels record the query time and exact evidence eligible at that time.
type TemporalLabels struct {
	ValidAt             string   `json:"valid_at"`
	EligibleEvidenceIDs []string `json:"eligible_evidence_ids"`
}

// IsolationLabels record the principal project and exact eligible/excluded ID sets.
type IsolationLabels struct {
	PrincipalProject string   `json:"principal_project"`
	EligibleIDs      []string `json:"eligible_ids"`
	ExcludedIDs      []string `json:"excluded_ids"`
}

// SplitPolicy describes how immutable query IDs are assigned to evaluation splits.
type SplitPolicy struct {
	Version  string `json:"version"`
	Strategy string `json:"strategy"`
}

// BuildMetadata binds corpus evidence to the evaluated source revision.
type BuildMetadata struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty"`
}

// HardwareMetadata describes the representative execution envelope.
type HardwareMetadata struct {
	ProfileID string `json:"profile_id"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPU       string `json:"cpu"`
	MemoryMB  int    `json:"memory_mb"`
}

// Validate rejects corpus data that cannot serve as reproducible release evidence.
func (c Corpus) Validate() error {
	if c.SchemaVersion == "" {
		return fmt.Errorf("schema_version is required")
	}
	if c.Version == "" {
		return fmt.Errorf("version is required")
	}
	if len(c.Queries) == 0 {
		return fmt.Errorf("queries are required")
	}
	if c.SplitPolicy.Version == "" || c.SplitPolicy.Strategy == "" {
		return fmt.Errorf("split_policy version and strategy are required")
	}
	if c.Build.Commit == "" {
		return fmt.Errorf("build commit is required")
	}
	if c.Hardware.ProfileID == "" || c.Hardware.OS == "" || c.Hardware.Arch == "" || c.Hardware.CPU == "" || c.Hardware.MemoryMB <= 0 {
		return fmt.Errorf("hardware profile_id, os, arch, cpu, and positive memory_mb are required")
	}

	queryIDs := make(map[string]struct{}, len(c.Queries))
	for i, query := range c.Queries {
		if err := query.validate(); err != nil {
			return fmt.Errorf("queries[%d]: %w", i, err)
		}
		if _, duplicate := queryIDs[query.ID]; duplicate {
			return fmt.Errorf("queries[%d].query_id %q is duplicated", i, query.ID)
		}
		queryIDs[query.ID] = struct{}{}
	}

	return nil
}

func (q CorpusQuery) validate() error {
	if q.ID == "" {
		return fmt.Errorf("query_id is required")
	}
	if q.Text == "" {
		return fmt.Errorf("query text is required")
	}
	if q.ProfileClass == "" {
		return fmt.Errorf("profile_class is required")
	}
	if q.QueryClass == "" {
		return fmt.Errorf("query_class is required")
	}
	if q.Split == "" {
		return fmt.Errorf("split is required")
	}
	if q.Labels.NoAnswer == nil {
		return fmt.Errorf("labels.no_answer is required")
	}
	if !*q.Labels.NoAnswer && len(q.Labels.Relevant) == 0 {
		return fmt.Errorf("labels.relevant is required when no_answer is false")
	}
	for i, evidence := range q.Labels.Relevant {
		if err := evidence.Validate(); err != nil {
			return fmt.Errorf("labels.relevant[%d].evidence: %w", i, err)
		}
	}
	if q.Labels.HardNegativeIDs == nil {
		return fmt.Errorf("labels.hard_negative_ids is required")
	}
	for i, id := range q.Labels.HardNegativeIDs {
		if id == "" {
			return fmt.Errorf("labels.hard_negative_ids[%d] is empty", i)
		}
	}
	if q.Labels.Temporal == nil || q.Labels.Temporal.ValidAt == "" || q.Labels.Temporal.EligibleEvidenceIDs == nil {
		return fmt.Errorf("labels.temporal valid_at and eligible_evidence_ids are required")
	}
	if q.Labels.Isolation == nil || q.Labels.Isolation.PrincipalProject == "" || q.Labels.Isolation.EligibleIDs == nil || q.Labels.Isolation.ExcludedIDs == nil {
		return fmt.Errorf("labels.isolation principal_project, eligible_ids, and excluded_ids are required")
	}
	return nil
}

// Validate requires exactly one complete immutable evidence locator.
func (r EvidenceRef) Validate() error {
	forms := 0
	if r.EpisodeID != "" {
		forms++
	}
	if r.FactID != "" {
		forms++
	}
	if r.Span != nil {
		forms++
		if r.Span.EpisodeID == "" {
			return fmt.Errorf("span episode_id is required")
		}
		if r.Span.StartByte < 0 || r.Span.EndByte <= r.Span.StartByte {
			return fmt.Errorf("span must be a non-empty half-open byte range")
		}
	}
	if forms != 1 {
		return fmt.Errorf("exactly one episode_id, fact_id, or span is required")
	}
	return nil
}
