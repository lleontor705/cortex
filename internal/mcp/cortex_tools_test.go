package mcp

// T07 (strict-integer-ids): strict positive integer identifier handling
// across local MCP tools.
//
// Coverage:
//   - every identifier schema is published as JSON Schema "integer" with
//     "minimum": 1 (memory, graph, scoring, revision, and temporal tools);
//   - non-identifier numeric fields (weights, confidence, limits, counts)
//     intentionally remain "number";
//   - an invalid-identifier table (fractional, NaN/infinity, string,
//     overflow, zero, negative, missing) is rejected with an MCP error
//     BEFORE any store/domain call -- proven by poisoning the shared
//     *sql.DB: any store access would surface "Failed to ..." store
//     errors instead of the validation message;
//   - hard delete with a fractional ID targets nothing (regression for
//     the float-truncation audit finding cortex_delete(id:1.9) -> ID 1);
//   - valid integral IDs (the JSON transport representation float64(N))
//     preserve behavior.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// --- Schema contracts -------------------------------------------------------

// strictIntegerIDFields is the complete local-MCP identifier inventory:
// every tool argument that names a persisted int64 identifier must be
// published as a strict positive integer.
var strictIntegerIDFields = []struct{ tool, field string }{
	// memory tools
	{"cortex_get_observation", "id"},
	{"cortex_update", "id"},
	{"cortex_delete", "id"},
	{"cortex_timeline", "observation_id"},
	{"cortex_revision_history", "observation_id"},
	// graph / scoring tools
	{"cortex_relate", "from_id"},
	{"cortex_relate", "to_id"},
	{"cortex_graph", "observation_id"},
	{"cortex_graph_relationships", "observation_id"},
	{"cortex_graph_path", "from_id"},
	{"cortex_graph_path", "to_id"},
	{"cortex_score", "observation_id"},
	{"cortex_archive", "observation_id"},
	// temporal tools
	{"cortex_temporal_create_edge", "from_obs_id"},
	{"cortex_temporal_create_edge", "to_obs_id"},
	{"cortex_temporal_get_edges", "observation_id"},
	{"cortex_temporal_get_relevant", "observation_id"},
	{"cortex_temporal_create_snapshot", "root_observation_id"},
	{"cortex_temporal_evolution_path", "edge_id"},
	{"cortex_temporal_fact_state", "observation_id"},
}

// nonIDNumberFields must stay "number" per T07 non-goals (weights,
// confidence, limits, depths, and metric counts are not identifiers).
var nonIDNumberFields = []struct{ tool, field string }{
	{"cortex_relate", "weight"},
	{"cortex_relate", "confidence"},
	{"cortex_timeline", "before"},
	{"cortex_timeline", "after"},
	{"cortex_revision_history", "limit"},
	{"cortex_graph", "depth"},
	{"cortex_graph_path", "max_depth"},
	{"cortex_search_hybrid", "limit"},
	{"cortex_temporal_create_edge", "weight"},
	{"cortex_temporal_create_edge", "confidence"},
	{"cortex_temporal_get_relevant", "depth"},
	{"cortex_temporal_record_operation", "duration_ms"},
	{"cortex_temporal_record_operation", "memory_usage_bytes"},
}

func TestIdentifierSchemasAreStrictIntegers(t *testing.T) {
	stores := setupStoresWithObservability(t)
	srv := NewServerWithTools(stores, nil)

	for _, tc := range strictIntegerIDFields {
		registered := srv.GetTool(tc.tool)
		if registered == nil {
			t.Errorf("tool %s not registered", tc.tool)
			continue
		}
		prop, ok := registered.Tool.InputSchema.Properties[tc.field].(map[string]any)
		if !ok {
			t.Errorf("%s.%s: property missing or not an object (got %#v)",
				tc.tool, tc.field, registered.Tool.InputSchema.Properties[tc.field])
			continue
		}
		if got := prop["type"]; got != "integer" {
			t.Errorf("%s.%s: schema type = %#v, want %q", tc.tool, tc.field, got, "integer")
		}
		if !schemaMinimumEqualsOne(prop["minimum"]) {
			t.Errorf("%s.%s: schema minimum = %#v, want 1", tc.tool, tc.field, prop["minimum"])
		}
		required := false
		for _, r := range registered.Tool.InputSchema.Required {
			if r == tc.field {
				required = true
				break
			}
		}
		if !required {
			t.Errorf("%s.%s: identifier must be a required argument", tc.tool, tc.field)
		}
	}
}

// schemaMinimumEqualsOne accepts the numeric Go representations mcp-go can
// place in a property map (float64 from mcp.Min, ints from manual options).
func schemaMinimumEqualsOne(v any) bool {
	switch n := v.(type) {
	case float64:
		return n == 1
	case int:
		return n == 1
	case int64:
		return n == 1
	default:
		return false
	}
}

func TestNonIdentifierNumericFieldsRemainNumbers(t *testing.T) {
	stores := setupStoresWithObservability(t)
	srv := NewServerWithTools(stores, nil)

	for _, tc := range nonIDNumberFields {
		registered := srv.GetTool(tc.tool)
		if registered == nil {
			t.Errorf("tool %s not registered", tc.tool)
			continue
		}
		prop, ok := registered.Tool.InputSchema.Properties[tc.field].(map[string]any)
		if !ok {
			t.Errorf("%s.%s: property missing", tc.tool, tc.field)
			continue
		}
		if got := prop["type"]; got != "number" {
			t.Errorf("%s.%s: schema type = %#v, want %q (non-identifier)", tc.tool, tc.field, got, "number")
		}
	}
}

// --- Strict rejection before any store call ---------------------------------

// invalidIDValues is the adversarial identifier table shared across tools.
var invalidIDValues = []struct {
	name  string
	value any
	omit  bool // when true, the argument key must be absent entirely
}{
	{"missing", nil, true},
	{"json-null", nil, false},
	{"string", "7", false},
	{"numeric-string", "1.9", false},
	{"bool", true, false},
	{"zero", float64(0), false},
	{"negative", float64(-3), false},
	{"negative-fractional", float64(-1.9), false},
	{"fractional", float64(1.9), false},
	{"sub-unit-fractional", float64(0.5), false},
	{"nan", math.NaN(), false},
	{"positive-infinity", math.Inf(1), false},
	{"negative-infinity", math.Inf(-1), false},
	{"overflow-float", float64(1e19), false},
	{"overflow-2e63", 9223372036854775808.0, false},
	{"object", map[string]any{}, false},
}

func TestInvalidIDsRejectedBeforeStoreCalls(t *testing.T) {
	stores := setupTestStores(t)
	// Poison the shared DB: any store/domain call would fail with a
	// "Failed to ..." message. Validation must reject before that.
	if err := stores.Observations.DB().Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	temporalH, temporalStores := setupTemporalHandler(t)
	if err := temporalStores.Observations.DB().Close(); err != nil {
		t.Fatalf("close temporal db: %v", err)
	}

	// Each case supplies the identifier under test plus every other
	// argument needed to get past non-ID validation. bindsViaJSON marks
	// temporal handlers that bind through BindArguments: malformed JSON
	// values (fractional/string/NaN/overflow) are rejected by the JSON
	// binder itself ("Invalid parameters"), still before any store call.
	type idCase struct {
		tool         string
		key          string
		bindsViaJSON bool
		invoke       func(t *testing.T, args map[string]any) *mcp.CallToolResult
	}

	relateArgs := func(extra map[string]any) map[string]any {
		args := map[string]any{"relation_type": "references"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	cases := []idCase{
		{"cortex_delete_hard", "id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["hard_delete"] = true
			return callTool(t, handleDelete(stores), args)
		}},
		{"cortex_delete_soft", "id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["hard_delete"] = false
			return callTool(t, handleDelete(stores), args)
		}},
		{"cortex_get_observation", "id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleGetObservation(stores), args)
		}},
		{"cortex_update", "id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["title"] = "replacement"
			return callTool(t, handleUpdate(stores), args)
		}},
		{"cortex_timeline", "observation_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleTimeline(stores), args)
		}},
		{"cortex_revision_history", "observation_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleRevisionHistory(stores), args)
		}},
		{"cortex_relate_from", "from_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["to_id"] = float64(1)
			return callTool(t, handleRelate(stores), relateArgs(args))
		}},
		{"cortex_relate_to", "to_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["from_id"] = float64(1)
			return callTool(t, handleRelate(stores), relateArgs(args))
		}},
		{"cortex_graph", "observation_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleGraph(stores), args)
		}},
		{"cortex_graph_relationships", "observation_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleGraphRelationships(stores), args)
		}},
		{"cortex_graph_path_from", "from_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["to_id"] = float64(1)
			return callTool(t, handleGraphPath(stores), args)
		}},
		{"cortex_graph_path_to", "to_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["from_id"] = float64(1)
			return callTool(t, handleGraphPath(stores), args)
		}},
		{"cortex_score", "observation_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleScore(stores), args)
		}},
		{"cortex_archive", "observation_id", false, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTool(t, handleArchive(stores), args)
		}},
		{"cortex_temporal_create_edge_from", "from_obs_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["to_obs_id"] = float64(1)
			args["relation_type"] = "references"
			return callTemporal(t, temporalH.CreateTemporalEdge, args)
		}},
		{"cortex_temporal_create_edge_to", "to_obs_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["from_obs_id"] = float64(1)
			args["relation_type"] = "references"
			return callTemporal(t, temporalH.CreateTemporalEdge, args)
		}},
		{"cortex_temporal_get_edges", "observation_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["at"] = "2025-06-15T00:00:00Z"
			return callTemporal(t, temporalH.GetTemporalEdges, args)
		}},
		{"cortex_temporal_get_relevant", "observation_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["at"] = "2025-06-15T00:00:00Z"
			args["depth"] = float64(1)
			return callTemporal(t, temporalH.GetTemporalRelevant, args)
		}},
		{"cortex_temporal_create_snapshot", "root_observation_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			args["snapshot_key"] = "snap"
			return callTemporal(t, temporalH.CreateTemporalSnapshot, args)
		}},
		{"cortex_temporal_evolution_path", "edge_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTemporal(t, temporalH.GetTemporalEvolutionPath, args)
		}},
		{"cortex_temporal_fact_state", "observation_id", true, func(t *testing.T, args map[string]any) *mcp.CallToolResult {
			return callTemporal(t, temporalH.GetCurrentFactState, args)
		}},
	}

	for _, c := range cases {
		for _, v := range invalidIDValues {
			args := map[string]any{}
			if !v.omit {
				args[c.key] = v.value
			}
			result := c.invoke(t, args)
			if result == nil {
				t.Fatalf("%s/%s: nil result", c.tool, v.name)
			}
			text := resultText(result)
			if !result.IsError {
				t.Errorf("%s with %s id (%#v): expected IsError, got success %q", c.tool, v.name, v.value, text)
				continue
			}
			if strings.Contains(text, "Failed to") {
				t.Errorf("%s with %s id (%#v): reached store layer: %q", c.tool, v.name, v.value, text)
				continue
			}
			// Temporal handlers bind through BindArguments: values the
			// JSON binder itself rejects surface as "Invalid parameters"
			// (still zero store calls); everything else must carry the
			// explicit positive-integer message.
			if c.bindsViaJSON && strings.Contains(text, "Invalid parameters") {
				continue
			}
			if !strings.Contains(text, "positive integer") {
				t.Errorf("%s with %s id (%#v): want positive-integer rejection, got %q", c.tool, v.name, v.value, text)
			}
		}
	}
}

// --- Destructive regression: fractional hard delete -------------------------

func TestHardDeleteFractionalIDTargetsNothing(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "proj")
	saveObs(t, stores, "One", "proj", "s1") // id 1
	saveObs(t, stores, "Two", "proj", "s1") // id 2

	for _, v := range []any{float64(1.9), float64(2.1), "1"} {
		result := callTool(t, handleDelete(stores), map[string]any{"id": v, "hard_delete": true})
		if result == nil || !result.IsError {
			t.Fatalf("cortex_delete(id=%#v, hard_delete=true): expected rejection, got %q", v, resultText(result))
		}
		if !strings.Contains(resultText(result), "positive integer") {
			t.Errorf("cortex_delete(id=%#v): want positive-integer rejection, got %q", v, resultText(result))
		}
	}

	// Both observations must survive the invalid hard-delete attempts.
	for _, id := range []int64{1, 2} {
		if _, err := stores.Observations.GetByID(context.Background(), id); err != nil {
			t.Fatalf("observation %d must survive invalid hard deletes: %v", id, err)
		}
	}
}

// --- Valid integral IDs preserve behavior -----------------------------------

func TestValidIntegerIDsPreserveBehavior(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "proj")
	a := saveObs(t, stores, "Alpha Node", "proj", "s1")
	b := saveObs(t, stores, "Beta Node", "proj", "s1")

	get := callTool(t, handleGetObservation(stores), map[string]any{"id": float64(a.ID)})
	if get.IsError || !strings.Contains(resultText(get), fmt.Sprintf("#%d", a.ID)) {
		t.Errorf("cortex_get_observation(valid id): got %q", resultText(get))
	}

	rel := callTool(t, handleRelate(stores), map[string]any{
		"from_id": float64(a.ID), "to_id": float64(b.ID), "relation_type": "references",
	})
	if rel.IsError || !strings.Contains(resultText(rel), "Relationship created") {
		t.Errorf("cortex_relate(valid ids): got %q", resultText(rel))
	}

	graph := callTool(t, handleGraph(stores), map[string]any{"observation_id": float64(a.ID), "depth": float64(1)})
	if graph.IsError {
		t.Errorf("cortex_graph(valid id): got %q", resultText(graph))
	}

	score := callTool(t, handleScore(stores), map[string]any{"observation_id": float64(a.ID)})
	if score.IsError || !strings.Contains(resultText(score), "score:") {
		t.Errorf("cortex_score(valid id): got %q", resultText(score))
	}

	timeline := callTool(t, handleTimeline(stores), map[string]any{"observation_id": float64(a.ID)})
	if timeline.IsError || !strings.Contains(resultText(timeline), ">>>") {
		t.Errorf("cortex_timeline(valid id): got %q", resultText(timeline))
	}

	rev := callTool(t, handleRevisionHistory(stores), map[string]any{"observation_id": float64(a.ID)})
	if rev.IsError {
		t.Errorf("cortex_revision_history(valid id): got %q", resultText(rev))
	}

	update := callTool(t, handleUpdate(stores), map[string]any{"id": float64(a.ID), "title": "Alpha Updated"})
	if update.IsError {
		t.Errorf("cortex_update(valid id): got %q", resultText(update))
	}

	archive := callTool(t, handleArchive(stores), map[string]any{"observation_id": float64(b.ID)})
	if archive.IsError || !strings.Contains(resultText(archive), "archived") {
		t.Errorf("cortex_archive(valid id): got %q", resultText(archive))
	}

	del := callTool(t, handleDelete(stores), map[string]any{"id": float64(b.ID), "hard_delete": true})
	if del.IsError || !strings.Contains(resultText(del), "permanently deleted") {
		t.Errorf("cortex_delete(valid id, hard): got %q", resultText(del))
	}
}
