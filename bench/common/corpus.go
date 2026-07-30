package common

import "fmt"

const (
	// CapabilityExecutedCurrentPath marks an authority dimension enforced by the
	// production retrieval path exercised by this corpus.
	CapabilityExecutedCurrentPath = "executed_current_path"
	// CapabilityNotExecuted prevents future authority labels from being reported
	// as current-path conformance evidence.
	CapabilityNotExecuted = "not_executed_capability"

	CoverageRanking           = "ranking"
	CoverageFilterCollision   = "filter_collision"
	CoverageLifecycleTemporal = "lifecycle_temporal"
	CoverageProvenance        = "provenance"
	CoverageNoAnswer          = "no_answer"
)

// Corpus is the versioned, reproducible input contract for retrieval evaluation.
type Corpus struct {
	SchemaVersion string           `json:"schema_version"`
	Version       string           `json:"version"`
	Identity      CorpusIdentity   `json:"identity"`
	Records       []CorpusRecord   `json:"records"`
	Queries       []CorpusQuery    `json:"queries"`
	SplitPolicy   SplitPolicy      `json:"split_policy"`
	Build         BuildMetadata    `json:"build"`
	Hardware      HardwareMetadata `json:"hardware"`
}

// CorpusIdentity records the authorship, redistribution, and privacy basis of
// the immutable benchmark input.
type CorpusIdentity struct {
	Origin        string `json:"origin"`
	License       string `json:"license"`
	PrivacyReview string `json:"privacy_review"`
}

// CorpusRecord is the labelled retrieval truth ingested by a baseline run.
type CorpusRecord struct {
	ID                string `json:"id"`
	Project           string `json:"project"`
	Type              string `json:"type"`
	Kind              string `json:"kind"`
	Scope             string `json:"scope"`
	Privacy           string `json:"privacy"`
	PrincipalID       string `json:"principal_id,omitempty"`
	TopicKey          string `json:"topic_key"`
	Content           string `json:"content"`
	Lifecycle         string `json:"lifecycle"`
	RecordedAt        string `json:"recorded_at"`
	ValidFrom         string `json:"valid_from"`
	ValidUntil        string `json:"valid_until,omitempty"`
	SourceEpisodeID   string `json:"source_episode_id,omitempty"`
	DerivationID      string `json:"derivation_id,omitempty"`
	DerivationVersion string `json:"derivation_version,omitempty"`
}

// CorpusQuery identifies one immutable query and its complete evidence labels.
type CorpusQuery struct {
	ID           string            `json:"id"`
	Text         string            `json:"text"`
	ProfileClass string            `json:"profile_class"`
	QueryClass   string            `json:"query_class"`
	Split        string            `json:"split"`
	Coverage     []string          `json:"coverage"`
	Authority    map[string]string `json:"authority"`
	Labels       EvidenceLabels    `json:"labels"`
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
	if c.Identity.Origin == "" {
		return fmt.Errorf("identity origin is required")
	}
	if c.Identity.License == "" {
		return fmt.Errorf("identity license is required")
	}
	if c.Identity.PrivacyReview == "" {
		return fmt.Errorf("identity privacy_review is required")
	}
	if len(c.Records) < 26 {
		return fmt.Errorf("at least 26 records are required")
	}
	if len(c.Queries) < 20 {
		return fmt.Errorf("at least 20 queries are required")
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

	records, err := validateCorpusRecords(c.Records)
	if err != nil {
		return err
	}

	queryIDs := make(map[string]struct{}, len(c.Queries))
	coverage := make(map[string]int)
	queryClasses := make(map[string]struct{})
	noAnswerCount := 0
	for i, query := range c.Queries {
		if err := query.validate(records); err != nil {
			return fmt.Errorf("queries[%d]: %w", i, err)
		}
		if _, duplicate := queryIDs[query.ID]; duplicate {
			return fmt.Errorf("queries[%d].query_id %q is duplicated", i, query.ID)
		}
		queryIDs[query.ID] = struct{}{}
		queryClasses[query.QueryClass] = struct{}{}
		for _, class := range query.Coverage {
			coverage[class]++
		}
		if *query.Labels.NoAnswer {
			noAnswerCount++
		}
	}
	if coverage[CoverageRanking] < 4 {
		return fmt.Errorf("ranking coverage requires at least 4 queries")
	}
	if coverage[CoverageFilterCollision] < 7 {
		return fmt.Errorf("filter collision coverage requires at least 7 queries")
	}
	if coverage[CoverageLifecycleTemporal] < 4 {
		return fmt.Errorf("lifecycle temporal coverage requires at least 4 queries")
	}
	if coverage[CoverageProvenance] < 3 {
		return fmt.Errorf("provenance coverage requires at least 3 queries")
	}
	if coverage[CoverageNoAnswer] < 2 || noAnswerCount < 2 {
		return fmt.Errorf("no-answer coverage requires at least 2 labelled queries")
	}
	if !containsAll(queryClasses,
		"exact-topic", "single-term-lexical", "multi-term-lexical", "project-filter",
		"type-filter", "scope-filter", "temporal-as-of", "collision", "evidence", "no-answer",
	) {
		return fmt.Errorf("query class matrix is incomplete")
	}

	return nil
}

func (q CorpusQuery) validate(records map[string]CorpusRecord) error {
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
	if len(q.Coverage) == 0 {
		return fmt.Errorf("coverage is required")
	}
	if err := validateAuthority(q.Authority); err != nil {
		return err
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
		if id := evidence.recordID(); !recordExists(records, id) {
			return fmt.Errorf("labels.relevant[%d] has unresolved record %q", i, id)
		}
	}
	if q.Labels.HardNegativeIDs == nil {
		return fmt.Errorf("labels.hard_negative_ids is required")
	}
	for i, id := range q.Labels.HardNegativeIDs {
		if id == "" {
			return fmt.Errorf("labels.hard_negative_ids[%d] is empty", i)
		}
		if !recordExists(records, id) {
			return fmt.Errorf("labels.hard_negative_ids[%d] has unresolved record %q", i, id)
		}
	}
	if q.Labels.Temporal == nil || q.Labels.Temporal.ValidAt == "" || q.Labels.Temporal.EligibleEvidenceIDs == nil {
		return fmt.Errorf("labels.temporal valid_at and eligible_evidence_ids are required")
	}
	if q.Labels.Isolation == nil || q.Labels.Isolation.PrincipalProject == "" || q.Labels.Isolation.EligibleIDs == nil || q.Labels.Isolation.ExcludedIDs == nil {
		return fmt.Errorf("labels.isolation principal_project, eligible_ids, and excluded_ids are required")
	}
	for _, labelledIDs := range [][]string{
		q.Labels.Temporal.EligibleEvidenceIDs,
		q.Labels.Isolation.EligibleIDs,
		q.Labels.Isolation.ExcludedIDs,
	} {
		for _, id := range labelledIDs {
			if !recordExists(records, id) {
				return fmt.Errorf("labels contain unresolved record %q", id)
			}
		}
	}
	return nil
}

func validateCorpusRecords(input []CorpusRecord) (map[string]CorpusRecord, error) {
	records := make(map[string]CorpusRecord, len(input))
	projects := make(map[string]struct{})
	types := make(map[string]struct{})
	kinds := make(map[string]struct{})
	scopes := make(map[string]struct{})
	privacy := make(map[string]struct{})
	lifecycles := make(map[string]struct{})
	contents := make(map[string]string)
	topics := make(map[string]string)
	contentCollisions := make(map[string]struct{})
	topicCollisions := make(map[string]struct{})
	privatePrincipals := make(map[string]struct{})

	for i, record := range input {
		if err := record.validate(); err != nil {
			return nil, fmt.Errorf("records[%d]: %w", i, err)
		}
		if _, duplicate := records[record.ID]; duplicate {
			return nil, fmt.Errorf("records[%d].record_id %q is duplicated", i, record.ID)
		}
		records[record.ID] = record
		projects[record.Project] = struct{}{}
		types[record.Type] = struct{}{}
		kinds[record.Kind] = struct{}{}
		scopes[record.Scope] = struct{}{}
		privacy[record.Privacy] = struct{}{}
		lifecycles[record.Lifecycle] = struct{}{}
		if record.Privacy == "private" {
			privatePrincipals[record.PrincipalID] = struct{}{}
		}
		if project, ok := contents[record.Content]; ok && project != record.Project {
			contentCollisions[record.Content] = struct{}{}
		} else {
			contents[record.Content] = record.Project
		}
		if project, ok := topics[record.TopicKey]; ok && project != record.Project {
			topicCollisions[record.TopicKey] = struct{}{}
		} else {
			topics[record.TopicKey] = record.Project
		}
	}

	for i, record := range input {
		if record.SourceEpisodeID == "" {
			continue
		}
		source, ok := records[record.SourceEpisodeID]
		if !ok {
			return nil, fmt.Errorf("records[%d].source_episode_id %q is unresolved", i, record.SourceEpisodeID)
		}
		if source.Kind != "episode" || source.Project != record.Project {
			return nil, fmt.Errorf("records[%d].source_episode_id must reference a same-project episode", i)
		}
	}

	if len(projects) < 2 || len(contentCollisions) < 2 || len(topicCollisions) < 2 {
		return nil, fmt.Errorf("project collision matrix requires two projects with two matching contents and topic keys")
	}
	if !containsAll(types, "decision", "bugfix", "discovery", "config") {
		return nil, fmt.Errorf("type matrix requires decision, bugfix, discovery, and config")
	}
	if !containsAll(kinds, "episode", "fact") {
		return nil, fmt.Errorf("record kind matrix requires episode and fact")
	}
	if !containsAll(scopes, "project", "shared", "personal") || !containsAll(privacy, "project", "shared", "private") {
		return nil, fmt.Errorf("scope/privacy matrix requires project, shared, personal, and private records")
	}
	if len(privatePrincipals) < 2 {
		return nil, fmt.Errorf("privacy matrix requires private records for distinct principals")
	}
	if !containsAll(lifecycles, "active", "archived", "soft_deleted", "superseded", "replacement", "stale_deletion_candidate") {
		return nil, fmt.Errorf("lifecycle matrix is incomplete")
	}
	return records, nil
}

func (r CorpusRecord) validate() error {
	if r.ID == "" || r.Project == "" || r.Type == "" || r.Kind == "" || r.Scope == "" || r.Privacy == "" {
		return fmt.Errorf("record_id, project, type, kind, scope, and privacy are required")
	}
	if r.TopicKey == "" || r.Content == "" || r.Lifecycle == "" || r.RecordedAt == "" || r.ValidFrom == "" {
		return fmt.Errorf("topic_key, content, lifecycle, recorded_at, and valid_from are required")
	}
	if r.Privacy == "private" && r.PrincipalID == "" {
		return fmt.Errorf("principal_id is required for private records")
	}
	if r.Kind == "fact" && (r.SourceEpisodeID == "" || r.DerivationID == "" || r.DerivationVersion == "") {
		return fmt.Errorf("fact source_episode_id, derivation_id, and derivation_version are required")
	}
	return nil
}

func validateAuthority(authority map[string]string) error {
	current := []string{"project", "type", "scope", "temporal"}
	future := []string{"privacy", "lifecycle", "provenance"}
	for _, dimension := range current {
		if authority[dimension] != CapabilityExecutedCurrentPath {
			return fmt.Errorf("authority %q must be %q", dimension, CapabilityExecutedCurrentPath)
		}
	}
	for _, dimension := range future {
		if authority[dimension] != CapabilityNotExecuted {
			return fmt.Errorf("authority %q must be %q", dimension, CapabilityNotExecuted)
		}
	}
	for dimension, status := range authority {
		if !contains(current, dimension) && status != CapabilityNotExecuted {
			return fmt.Errorf("future authority %q must be %q", dimension, CapabilityNotExecuted)
		}
	}
	return nil
}

func (r EvidenceRef) recordID() string {
	switch {
	case r.EpisodeID != "":
		return r.EpisodeID
	case r.FactID != "":
		return r.FactID
	case r.Span != nil:
		return r.Span.EpisodeID
	default:
		return ""
	}
}

func recordExists(records map[string]CorpusRecord, id string) bool {
	_, ok := records[id]
	return ok
}

func containsAll(values map[string]struct{}, required ...string) bool {
	for _, value := range required {
		if _, ok := values[value]; !ok {
			return false
		}
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
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
