// Package cortex captures retrieval evidence from the currently shipped Cortex
// application path without reimplementing search, filtering, or ranking.
package cortex

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/lleontor705/cortex/bench/common"
	"github.com/lleontor705/cortex/internal/domain"
)

// Query identifies one immutable baseline query and its effective production
// search inputs.
type Query struct {
	ID                      string               `json:"id"`
	Text                    string               `json:"text"`
	Options                 domain.SearchOptions `json:"options"`
	UnsupportedCapabilities []string             `json:"unsupported_capabilities,omitempty"`
}

// BaselineRun contains per-query current-production traces and any correctness
// failures that block use of the run as baseline evidence.
type BaselineRun struct {
	Queries            []QueryTrace `json:"queries"`
	BlockingFailures   []string     `json:"blocking_failures"`
	IncompleteEvidence []string     `json:"incomplete_evidence"`
}

// QueryTrace records effective inputs, ranked identities, performance samples,
// and a safe error string for one production search invocation.
type QueryTrace struct {
	QueryID        string            `json:"query_id"`
	EffectiveInput EffectiveInput    `json:"effective_input"`
	Ranked         []RankedResult    `json:"ranked"`
	Capabilities   []CapabilityTrace `json:"capabilities,omitempty"`
	Latency        LatencySample     `json:"latency"`
	Resources      ResourceSample    `json:"resources"`
	Error          string            `json:"error,omitempty"`
}

// CapabilityNotExecuted identifies a labelled authority field that the current
// production search path cannot execute without broadening its semantics.
const CapabilityNotExecuted = "not_executed_capability"

// CapabilityTrace records an explicitly labelled field that was not executed
// by the current production retrieval path.
type CapabilityTrace struct {
	Field  string `json:"field"`
	Status string `json:"status"`
}

// EffectiveInput is the exact supported filter and execution input passed to
// the current production Search store.
type EffectiveInput struct {
	Query       string     `json:"query"`
	Project     string     `json:"project"`
	Type        string     `json:"type,omitempty"`
	Scope       string     `json:"scope,omitempty"`
	Limit       int        `json:"limit"`
	FusionK     float64    `json:"fusion_k,omitempty"`
	GraphExpand bool       `json:"graph_expand"`
	AsOf        *time.Time `json:"as_of,omitempty"`
}

// RankedResult binds a corpus-stable label to the current production database
// identity and observed ranking evidence.
type RankedResult struct {
	StableID  string  `json:"stable_id"`
	CurrentID int64   `json:"current_id"`
	Project   string  `json:"project"`
	Position  int     `json:"position"`
	Score     float64 `json:"score"`
	Strategy  string  `json:"strategy"`
}

// LatencySample records wall-clock search latency in a serialization-friendly
// unit.
type LatencySample struct {
	Unit        string `json:"unit"`
	Nanoseconds int64  `json:"nanoseconds"`
}

// ResourceSample records process heap state and allocations observed across a
// query. It is evidence, not a performance gate.
type ResourceSample struct {
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
}

// RunCurrentProductionBaseline executes queries through BenchStores.App's
// current production Search store. stableIDs maps current SQLite identities to
// immutable corpus labels; it does not participate in search or ranking.
func RunCurrentProductionBaseline(ctx context.Context, stores *common.BenchStores, stableIDs map[int64]string, queries []Query) (BaselineRun, error) {
	if stores == nil || stores.App == nil || stores.App.Stores == nil || stores.App.Stores.Search == nil {
		return BaselineRun{}, fmt.Errorf("current production search path is unavailable")
	}

	run := BaselineRun{Queries: make([]QueryTrace, 0, len(queries))}
	for i, query := range queries {
		if strings.TrimSpace(query.ID) == "" {
			return run, fmt.Errorf("queries[%d].id is required", i)
		}

		options := query.Options
		options.Query = query.Text
		trace := QueryTrace{
			QueryID: query.ID,
			EffectiveInput: EffectiveInput{
				Query:       query.Text,
				Project:     options.Project,
				Type:        options.Type,
				Scope:       options.Scope,
				Limit:       options.Limit,
				FusionK:     options.FusionK,
				GraphExpand: options.GraphExpand,
				AsOf:        options.AsOf,
			},
			Ranked:       []RankedResult{},
			Capabilities: notExecutedCapabilities(query.UnsupportedCapabilities),
		}

		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		started := time.Now()
		results, searchErr := stores.App.Stores.Search.Search(ctx, query.Text, options)
		trace.Latency = LatencySample{Unit: "nanoseconds", Nanoseconds: time.Since(started).Nanoseconds()}
		runtime.ReadMemStats(&after)
		trace.Resources = ResourceSample{
			HeapAllocBytes:  after.HeapAlloc,
			TotalAllocBytes: allocationDelta(before.TotalAlloc, after.TotalAlloc),
		}

		if searchErr != nil {
			trace.Error = searchErr.Error()
			run.Queries = append(run.Queries, trace)
			continue
		}

		for position, result := range results {
			stableID := stableIDs[result.ID]
			if stableID == "" {
				stableID = "current:" + strconv.FormatInt(result.ID, 10)
			}
			trace.Ranked = append(trace.Ranked, RankedResult{
				StableID:  stableID,
				CurrentID: result.ID,
				Project:   result.Project,
				Position:  position + 1,
				Score:     result.Rank,
				Strategy:  result.ScoreBreakdown.Strategy,
			})
		}

		run.BlockingFailures = append(run.BlockingFailures, detectProjectLeakage(trace)...)
		run.IncompleteEvidence = append(run.IncompleteEvidence, detectIncompleteStableIDs(trace)...)
		run.Queries = append(run.Queries, trace)
	}

	if len(run.BlockingFailures) > 0 {
		return run, fmt.Errorf("baseline blocked by %d project isolation violation(s)", len(run.BlockingFailures))
	}
	if len(run.IncompleteEvidence) > 0 {
		return run, fmt.Errorf("baseline evidence incomplete: %d result(s) lack immutable corpus stable IDs", len(run.IncompleteEvidence))
	}
	return run, nil
}

func notExecutedCapabilities(fields []string) []CapabilityTrace {
	if len(fields) == 0 {
		return nil
	}

	capabilities := make([]CapabilityTrace, 0, len(fields))
	for _, field := range fields {
		capabilities = append(capabilities, CapabilityTrace{Field: field, Status: CapabilityNotExecuted})
	}
	return capabilities
}

func detectProjectLeakage(trace QueryTrace) []string {
	if trace.EffectiveInput.Project == "" {
		return nil
	}

	failures := make([]string, 0)
	for _, result := range trace.Ranked {
		if result.Project != trace.EffectiveInput.Project {
			failures = append(failures, fmt.Sprintf(
				"query %q leaked stable ID %q/current ID %d from project %q into project %q",
				trace.QueryID,
				result.StableID,
				result.CurrentID,
				result.Project,
				trace.EffectiveInput.Project,
			))
		}
	}
	return failures
}

func detectIncompleteStableIDs(trace QueryTrace) []string {
	findings := make([]string, 0)
	for _, result := range trace.Ranked {
		if strings.HasPrefix(result.StableID, "current:") {
			findings = append(findings, fmt.Sprintf(
				"query %q returned current ID %d without an immutable corpus stable ID",
				trace.QueryID,
				result.CurrentID,
			))
		}
	}
	return findings
}

func allocationDelta(before, after uint64) uint64 {
	if after < before {
		return 0
	}
	return after - before
}
