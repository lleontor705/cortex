package cortexnative

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type fixtureLine struct {
	Kind   string         `json:"kind"`
	Record *fixtureRecord `json:"record,omitempty"`
	Query  *fixtureQuery  `json:"query,omitempty"`
}

type fixtureRecord struct {
	ID             string            `json:"id"`
	Project        string            `json:"project"`
	TopicKey       string            `json:"topic_key"`
	Content        string            `json:"content"`
	Type           string            `json:"type"`
	Scope          string            `json:"scope"`
	Privacy        string            `json:"privacy"`
	Owner          string            `json:"owner,omitempty"`
	Tags           []string          `json:"tags"`
	Classification map[string]string `json:"classification"`
	Lifecycle      string            `json:"lifecycle"`
	ValidFrom      string            `json:"valid_from"`
	ValidUntil     string            `json:"valid_until,omitempty"`
	StaleIndex     bool              `json:"stale_index,omitempty"`
	BlockingReason string            `json:"blocking_reason,omitempty"`
}

type fixtureQuery struct {
	ID                  string         `json:"id"`
	Principal           string         `json:"principal"`
	Project             string         `json:"project"`
	Filters             fixtureFilters `json:"filters"`
	ExpectedEligibleIDs []string       `json:"expected_eligible_ids"`
	ExpectedBlockingIDs []string       `json:"expected_blocking_ids"`
	HardNegativeIDs     []string       `json:"hard_negative_ids"`
	NoAnswer            bool           `json:"no_answer"`
}

type fixtureFilters struct {
	Type           string            `json:"type,omitempty"`
	Scope          string            `json:"scope,omitempty"`
	Privacy        string            `json:"privacy,omitempty"`
	Tags           []string          `json:"tags,omitempty"`
	Classification map[string]string `json:"classification,omitempty"`
	Lifecycle      string            `json:"lifecycle,omitempty"`
	ValidAt        string            `json:"valid_at,omitempty"`
}

type validationResult struct {
	EligibleByQuery map[string][]string
	BlockingReasons map[string]string
}

func TestCollisionFixtures(t *testing.T) {
	records, queries := loadCollisionFixtures(t, "collision.jsonl")

	first, err := validateCollisionFixtures(records, queries)
	if err != nil {
		t.Fatalf("validateCollisionFixtures() error = %v", err)
	}
	second, err := validateCollisionFixtures(records, queries)
	if err != nil {
		t.Fatalf("second validateCollisionFixtures() error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("validator is not deterministic: first = %#v, second = %#v", first, second)
	}

	a := recordByID(t, records, "project-a-decision")
	b := recordByID(t, records, "project-b-decision")
	if a.Content != b.Content {
		t.Fatalf("collision content differs: project A = %q, project B = %q", a.Content, b.Content)
	}
	if a.TopicKey != b.TopicKey {
		t.Fatalf("collision topic keys differ: project A = %q, project B = %q", a.TopicKey, b.TopicKey)
	}

	wantBlockingReasons := map[string]string{
		"project-b-decision":       "unauthorized_project",
		"project-a-private-other":  "principal_private",
		"project-a-deleted":        "soft_deleted",
		"project-a-archived":       "archived",
		"project-a-stale-deletion": "stale_deleted_index",
		"project-a-superseded":     "superseded",
	}
	for id, reason := range wantBlockingReasons {
		if got := first.BlockingReasons[id]; got != reason {
			t.Errorf("blocking reason for %s = %q, want %q", id, got, reason)
		}
	}

	for queryID, ids := range first.EligibleByQuery {
		for _, id := range ids {
			if recordByID(t, records, id).Project != "project-a" {
				t.Errorf("query %s leaked non-project-A ID %s", queryID, id)
			}
		}
	}

	requiredQueries := []string{
		"all-filter-conjunction",
		"type-exact",
		"scope-privacy-exact",
		"tags-classification-exact",
		"lifecycle-exact",
		"temporal-as-of",
		"empty-normalized-filters",
		"hard-negative-no-answer",
	}
	for _, id := range requiredQueries {
		if _, ok := first.EligibleByQuery[id]; !ok {
			t.Errorf("required filter case %q is missing", id)
		}
	}
}

func loadCollisionFixtures(t *testing.T, path string) ([]fixtureRecord, []fixtureQuery) {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close collision fixtures: %v", err)
		}
	})

	var records []fixtureRecord
	var queries []fixtureQuery
	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		var line fixtureLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("decode line %d: %v", lineNumber, err)
		}
		switch line.Kind {
		case "record":
			if line.Record == nil || line.Query != nil {
				t.Fatalf("line %d must contain exactly one record", lineNumber)
			}
			records = append(records, *line.Record)
		case "query":
			if line.Query == nil || line.Record != nil {
				t.Fatalf("line %d must contain exactly one query", lineNumber)
			}
			queries = append(queries, *line.Query)
		default:
			t.Fatalf("line %d has unsupported kind %q", lineNumber, line.Kind)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan fixture: %v", err)
	}
	return records, queries
}

func validateCollisionFixtures(records []fixtureRecord, queries []fixtureQuery) (validationResult, error) {
	result := validationResult{
		EligibleByQuery: make(map[string][]string, len(queries)),
		BlockingReasons: make(map[string]string),
	}
	recordIndex := make(map[string]fixtureRecord, len(records))
	for _, record := range records {
		if record.ID == "" || record.Project == "" || record.TopicKey == "" || record.Content == "" {
			return validationResult{}, fmt.Errorf("record identity, project, topic_key, and content are required")
		}
		if _, duplicate := recordIndex[record.ID]; duplicate {
			return validationResult{}, fmt.Errorf("duplicate record ID %q", record.ID)
		}
		if _, err := time.Parse(time.RFC3339, record.ValidFrom); err != nil {
			return validationResult{}, fmt.Errorf("record %s valid_from: %w", record.ID, err)
		}
		if record.ValidUntil != "" {
			if _, err := time.Parse(time.RFC3339, record.ValidUntil); err != nil {
				return validationResult{}, fmt.Errorf("record %s valid_until: %w", record.ID, err)
			}
		}
		if record.BlockingReason != "" {
			result.BlockingReasons[record.ID] = record.BlockingReason
		}
		recordIndex[record.ID] = record
	}

	seenQueries := make(map[string]struct{}, len(queries))
	coveredBlocking := make(map[string]struct{})
	for _, query := range queries {
		if query.ID == "" || query.Project == "" || query.Principal == "" {
			return validationResult{}, fmt.Errorf("query identity, project, and principal are required")
		}
		if _, duplicate := seenQueries[query.ID]; duplicate {
			return validationResult{}, fmt.Errorf("duplicate query ID %q", query.ID)
		}
		seenQueries[query.ID] = struct{}{}

		eligible, err := eligibleIDs(records, query)
		if err != nil {
			return validationResult{}, fmt.Errorf("query %s: %w", query.ID, err)
		}
		want := sortedCopy(query.ExpectedEligibleIDs)
		if !reflect.DeepEqual(eligible, want) {
			return validationResult{}, fmt.Errorf("query %s eligible IDs = %v, want %v", query.ID, eligible, want)
		}
		if query.NoAnswer != (len(want) == 0) {
			return validationResult{}, fmt.Errorf("query %s no_answer=%t does not match eligible IDs %v", query.ID, query.NoAnswer, want)
		}
		for _, id := range append(append([]string{}, query.ExpectedBlockingIDs...), query.HardNegativeIDs...) {
			if _, exists := recordIndex[id]; !exists {
				return validationResult{}, fmt.Errorf("query %s labels unknown ID %q", query.ID, id)
			}
			if contains(eligible, id) {
				return validationResult{}, fmt.Errorf("query %s labels eligible ID %q as excluded", query.ID, id)
			}
		}
		for _, id := range query.ExpectedBlockingIDs {
			if recordIndex[id].BlockingReason == "" {
				return validationResult{}, fmt.Errorf("query %s blocking ID %q lacks a blocking_reason", query.ID, id)
			}
			coveredBlocking[id] = struct{}{}
		}
		result.EligibleByQuery[query.ID] = eligible
	}

	for id := range result.BlockingReasons {
		if _, covered := coveredBlocking[id]; !covered {
			return validationResult{}, fmt.Errorf("blocking record %q is not explicitly excluded by any query", id)
		}
	}
	return result, nil
}

func eligibleIDs(records []fixtureRecord, query fixtureQuery) ([]string, error) {
	var validAt time.Time
	if normalized(query.Filters.ValidAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(query.Filters.ValidAt))
		if err != nil {
			return nil, fmt.Errorf("valid_at: %w", err)
		}
		validAt = parsed
	}

	var ids []string
	for _, record := range records {
		if record.Project != query.Project || record.StaleIndex || normalized(record.Lifecycle) != "active" {
			continue
		}
		if normalized(record.Privacy) == "private" && record.Owner != query.Principal {
			continue
		}
		if !matchesOptional(record.Type, query.Filters.Type) ||
			!matchesOptional(record.Scope, query.Filters.Scope) ||
			!matchesOptional(record.Privacy, query.Filters.Privacy) ||
			!matchesOptional(record.Lifecycle, query.Filters.Lifecycle) ||
			!containsAllNormalized(record.Tags, query.Filters.Tags) ||
			!matchesClassification(record.Classification, query.Filters.Classification) {
			continue
		}
		if !validAt.IsZero() && !validAtRecord(record, validAt) {
			continue
		}
		ids = append(ids, record.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

func validAtRecord(record fixtureRecord, at time.Time) bool {
	from, _ := time.Parse(time.RFC3339, record.ValidFrom)
	if at.Before(from) {
		return false
	}
	if record.ValidUntil == "" {
		return true
	}
	until, _ := time.Parse(time.RFC3339, record.ValidUntil)
	return at.Before(until)
}

func matchesOptional(value, filter string) bool {
	filter = normalized(filter)
	return filter == "" || normalized(value) == filter
}

func containsAllNormalized(values, filters []string) bool {
	available := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = normalized(value); value != "" {
			available[value] = struct{}{}
		}
	}
	for _, filter := range filters {
		if filter = normalized(filter); filter != "" {
			if _, ok := available[filter]; !ok {
				return false
			}
		}
	}
	return true
}

func matchesClassification(values, filters map[string]string) bool {
	canonical := make(map[string]string, len(values))
	for key, value := range values {
		if key, value = normalized(key), normalized(value); key != "" && value != "" {
			canonical[key] = value
		}
	}
	for key, value := range filters {
		if key, value = normalized(key), normalized(value); key != "" && value != "" {
			if canonical[key] != value {
				return false
			}
		}
	}
	return true
}

func normalized(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func sortedCopy(values []string) []string {
	copyOfValues := append([]string(nil), values...)
	sort.Strings(copyOfValues)
	return copyOfValues
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func recordByID(t *testing.T, records []fixtureRecord, id string) fixtureRecord {
	t.Helper()
	for _, record := range records {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("record %q not found", id)
	return fixtureRecord{}
}
