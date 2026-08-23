package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/mcp/memorycontract"
	"github.com/mark3labs/mcp-go/server"
)

// REQ-MCP-001: cortex_* namespace with no aliases
//
// Every public MCP tool MUST be renamed to a coherent cortex_* name.
// Removed (legacy) tool names MUST return an unknown-tool result with NO alias,
// shim, redirect, or deprecation wrapper.
// A regression suite MUST assert that every removed name returns unknown-tool.

// allRegisteredToolNames returns the names of all tools registered on a server
// created with the full tool set (allowlist=nil).
func allRegisteredToolNames(t *testing.T) []string {
	t.Helper()
	stores := setupTestStores(t)
	srv := NewServer(stores)
	tools := srv.ListTools()
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}

// TestNoMemPrefixedToolsRegistered asserts that NO tool with a mem_ prefix is
// registered when listing all tools. This is the core REQ-MCP-001 regression.
func TestNoMemPrefixedToolsRegistered(t *testing.T) {
	names := allRegisteredToolNames(t)
	for _, name := range names {
		if strings.HasPrefix(name, "mem_") {
			t.Errorf("LEGACY TOOL STILL REGISTERED: %q — all public tools must be cortex_* (REQ-MCP-001)", name)
		}
	}
}

// TestNoBareTemporalPrefixedToolsRegistered asserts that NO tool with a bare
// temporal_ prefix (without cortex_) is registered. Temporal tools are renamed
// to cortex_temporal_*.
func TestNoBareTemporalPrefixedToolsRegistered(t *testing.T) {
	names := allRegisteredToolNames(t)
	for _, name := range names {
		if strings.HasPrefix(name, "temporal_") && !strings.HasPrefix(name, "cortex_temporal_") {
			t.Errorf("BARE TEMPORAL TOOL STILL REGISTERED: %q — must be cortex_temporal_* (REQ-MCP-001)", name)
		}
	}
}

// All removed legacy mem_* names that MUST NOT resolve to any behavior.
var removedLegacyNames = []string{
	"mem_save", "mem_search", "mem_context",
	"mem_session_summary", "mem_session_start", "mem_session_end",
	"mem_get_observation", "mem_suggest_topic_key", "mem_capture_passive",
	"mem_save_prompt", "mem_update", "mem_relate",
	"mem_graph", "mem_score", "mem_search_hybrid",
	"mem_revision_history", "mem_delete", "mem_stats",
	"mem_timeline", "mem_archive", "mem_merge_projects",
	"mem_search_temporal", "mem_consolidate", "mem_project_dna",
}

// TestRemovedLegacyNamesReturnUnknownTool asserts that every removed legacy
// name returns nil from GetTool (i.e. the MCP framework will return an
// unknown-tool result naturally — NO alias, shim, redirect, or wrapper).
func TestRemovedLegacyNamesReturnUnknownTool(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServer(stores)
	for _, name := range removedLegacyNames {
		t.Run(name, func(t *testing.T) {
			tool := srv.GetTool(name)
			if tool != nil {
				t.Errorf("ALIAS/SHIM DETECTED: removed name %q resolved to a tool — "+
					"no alias, shim, redirect, or deprecation wrapper is permitted (REQ-MCP-001)", name)
			}
		})
	}
}

// All bare temporal_* names that MUST NOT resolve to any behavior.
var removedBareTemporalNames = []string{
	"temporal_create_edge", "temporal_create_snapshot",
	"temporal_evaluate_quality", "temporal_evolution_path",
	"temporal_fact_state", "temporal_get_edges",
	"temporal_get_relevant", "temporal_health_check",
	"temporal_record_operation", "temporal_system_metrics",
}

// TestRemovedBareTemporalNamesReturnUnknownTool asserts that every removed bare
// temporal_* name returns nil from GetTool.
func TestRemovedBareTemporalNamesReturnUnknownTool(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServer(stores)
	for _, name := range removedBareTemporalNames {
		t.Run(name, func(t *testing.T) {
			tool := srv.GetTool(name)
			if tool != nil {
				t.Errorf("ALIAS/SHIM DETECTED: removed bare temporal name %q resolved to a tool (REQ-MCP-001)", name)
			}
		})
	}
}

// Tools required in the ordinary agent profile (design Part 2 §9).
var requiredOrdinaryAgentTools = []string{
	"cortex_save", "cortex_search", "cortex_context",
	"cortex_session_summary", "cortex_session_start", "cortex_session_end",
	"cortex_get_observation", "cortex_suggest_topic_key", "cortex_capture_passive",
	"cortex_save_prompt", "cortex_update", "cortex_relate",
	"cortex_graph", "cortex_score", "cortex_search_hybrid",
	"cortex_revision_history", "cortex_graph_relationships", "cortex_graph_path",
	"cortex_handoff", "cortex_get_rules", "cortex_save_rule",
	"cortex_ingest_code", "cortex_get_blast_radius", "cortex_detect_cycles",
	"cortex_analyze_architecture",
}

// The 5 tools required in the admin profile (design Part 2 §9).
var requiredAdminTools = []string{
	"cortex_delete", "cortex_stats", "cortex_timeline",
	"cortex_archive", "cortex_merge_projects",
}

// All cortex_temporal_* tools that MUST be in the temporal profile and MUST NOT
// be in ordinary agent discovery.
var requiredTemporalTools = []string{
	"cortex_temporal_create_edge", "cortex_temporal_create_snapshot",
	"cortex_temporal_evaluate_quality", "cortex_temporal_evolution_path",
	"cortex_temporal_fact_state", "cortex_temporal_get_edges",
	"cortex_temporal_get_relevant", "cortex_temporal_health_check",
	"cortex_temporal_record_operation", "cortex_temporal_system_metrics",
}

// TestOrdinaryAgentProfileHasAllRequiredTools asserts that the ordinary agent
// profile contains every required cortex_* tool (REQ-MCP-002).
func TestOrdinaryAgentProfileHasAllRequiredTools(t *testing.T) {
	for _, name := range requiredOrdinaryAgentTools {
		if !ProfileAgent[name] {
			t.Errorf("ordinary agent profile MISSING required tool: %q (REQ-MCP-002)", name)
		}
	}
}

// TestAdminProfileHasAllRequiredTools asserts that the admin profile contains
// every required cortex_* tool (REQ-MCP-002).
func TestAdminProfileHasAllRequiredTools(t *testing.T) {
	for _, name := range requiredAdminTools {
		if !ProfileAdmin[name] {
			t.Errorf("admin profile MISSING required tool: %q (REQ-MCP-002)", name)
		}
	}
}

// TestTemporalProfileExists asserts that the temporal profile is defined and
// contains every required cortex_temporal_* tool (REQ-MCP-002).
func TestTemporalProfileExists(t *testing.T) {
	temporalProfile, ok := Profiles["temporal"]
	if !ok {
		t.Fatal("temporal profile NOT defined — temporal tools must be in a separate profile, not ordinary agent discovery (REQ-MCP-002)")
	}
	for _, name := range requiredTemporalTools {
		if !temporalProfile[name] {
			t.Errorf("temporal profile MISSING required tool: %q (REQ-MCP-002)", name)
		}
	}
}

// cortex_search_temporal is a point-in-time search tool that is NOT a member of
// the cortex_temporal_* family but, per design (server.go ProfileTemporal), MUST
// live in the temporal profile and MUST NOT appear in ordinary agent or admin
// discovery. These assertions pin that membership explicitly so that removing
// it from the temporal profile or misassigning it to agent/admin is caught.
const searchTemporalTool = "cortex_search_temporal"

// TestSearchTemporalInTemporalProfile asserts that cortex_search_temporal IS a
// member of the temporal profile (REQ-MCP-002). It lives alongside the
// cortex_temporal_* tools even though its name does not carry the temporal_
// suffix, because point-in-time search belongs with temporal tooling.
func TestSearchTemporalInTemporalProfile(t *testing.T) {
	temporalProfile, ok := Profiles["temporal"]
	if !ok {
		t.Fatal("temporal profile NOT defined — cortex_search_temporal must be in a separate profile, not ordinary agent discovery (REQ-MCP-002)")
	}
	if !temporalProfile[searchTemporalTool] {
		t.Errorf("temporal profile MISSING %q — point-in-time search belongs with temporal tools, not ordinary agent (REQ-MCP-002)", searchTemporalTool)
	}
}

// TestSearchTemporalAbsentFromOrdinaryAgent asserts that cortex_search_temporal
// does NOT appear in the ordinary agent profile (REQ-MCP-002).
func TestSearchTemporalAbsentFromOrdinaryAgent(t *testing.T) {
	if ProfileAgent[searchTemporalTool] {
		t.Errorf("%q found in ordinary agent profile — temporal search must NOT appear in ordinary agent discovery (REQ-MCP-002)", searchTemporalTool)
	}
}

// TestSearchTemporalAbsentFromAdmin asserts that cortex_search_temporal does NOT
// appear in the admin profile (REQ-MCP-002).
func TestSearchTemporalAbsentFromAdmin(t *testing.T) {
	if ProfileAdmin[searchTemporalTool] {
		t.Errorf("%q found in admin profile — temporal search belongs in the temporal profile only (REQ-MCP-002)", searchTemporalTool)
	}
}

// TestTemporalToolsAbsentFromOrdinaryAgent asserts that temporal tools do NOT
// appear in the ordinary agent profile (REQ-MCP-002).
func TestTemporalToolsAbsentFromOrdinaryAgent(t *testing.T) {
	for _, name := range requiredTemporalTools {
		if ProfileAgent[name] {
			t.Errorf("temporal tool %q found in ordinary agent profile — temporal tools must NOT appear in ordinary agent discovery (REQ-MCP-002)", name)
		}
	}
}

// TestAdminToolsNotInOrdinaryAgent asserts admin tools are absent from ordinary agent.
func TestAdminToolsNotInOrdinaryAgent(t *testing.T) {
	for _, name := range requiredAdminTools {
		if ProfileAgent[name] {
			t.Errorf("admin tool %q found in ordinary agent profile — profiles must be coherent with no overlap violations (REQ-MCP-002)", name)
		}
	}
}

// TestOrdinaryToolsNotInAdmin asserts ordinary agent tools are absent from admin.
func TestOrdinaryToolsNotInAdmin(t *testing.T) {
	for _, name := range requiredOrdinaryAgentTools {
		if ProfileAdmin[name] {
			t.Errorf("ordinary tool %q found in admin profile — profiles must be coherent (REQ-MCP-002)", name)
		}
	}
}

// TestTemporalToolsNotInAdmin asserts temporal tools are absent from admin.
func TestTemporalToolsNotInAdmin(t *testing.T) {
	temporalProfile, ok := Profiles["temporal"]
	if !ok {
		t.Skip("temporal profile not yet defined")
	}
	for name := range temporalProfile {
		if ProfileAdmin[name] {
			t.Errorf("temporal tool %q found in admin profile — profiles must be coherent (REQ-MCP-002)", name)
		}
	}
}

// TestNoMemPrefixedEntriesInProfiles asserts that NO profile contains any mem_*
// entry — all must be cortex_* (REQ-MCP-001 regression).
func TestNoMemPrefixedEntriesInProfiles(t *testing.T) {
	for profileName, profile := range Profiles {
		for tool := range profile {
			if strings.HasPrefix(tool, "mem_") {
				t.Errorf("profile %q contains legacy mem_* entry %q — must be cortex_* (REQ-MCP-001)", profileName, tool)
			}
		}
	}
}

// TestNoBareTemporalEntriesInProfiles asserts that NO profile contains bare
// temporal_* entries — they must be cortex_temporal_*.
func TestNoBareTemporalEntriesInProfiles(t *testing.T) {
	for profileName, profile := range Profiles {
		for tool := range profile {
			if strings.HasPrefix(tool, "temporal_") && !strings.HasPrefix(tool, "cortex_temporal_") {
				t.Errorf("profile %q contains bare temporal_* entry %q — must be cortex_temporal_*", profileName, tool)
			}
		}
	}
}

// TestServerInstructionsAreCortexNative asserts that server instructions
// reference cortex_* tools and do NOT carry Engram framing (REQ-MCPH-003).
func TestServerInstructionsAreCortexNative(t *testing.T) {
	if strings.Contains(serverInstructions, "mem_") {
		t.Errorf("serverInstructions still references mem_* tool names — must be cortex_* (REQ-MCPH-003)")
	}
	if strings.Contains(strings.ToLower(serverInstructions), "engram") {
		t.Errorf("serverInstructions carries Engram framing — must be Cortex-native (REQ-MCPH-003)")
	}
	if !strings.Contains(serverInstructions, "cortex_") {
		t.Errorf("serverInstructions does not reference any cortex_* tool — must be Cortex-native (REQ-MCPH-003)")
	}
}

// TestServerVersionIsV2 asserts the server reports version 2.0.0.
func TestServerVersionIsV2(t *testing.T) {
	if !strings.HasPrefix(serverVersion, "2.") {
		t.Errorf("serverVersion = %q, expected 2.x (design: version 2.0.0)", serverVersion)
	}
}

// TestCortexNamesActuallyRegistered asserts that the cortex_* tool names ARE
// registered on a full server (the inverse of the removed-name test).
func TestCortexNamesActuallyRegistered(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServer(stores)

	// All ordinary agent tools must be registered (these don't need Metrics/QualityMetrics)
	for _, name := range requiredOrdinaryAgentTools {
		t.Run(name, func(t *testing.T) {
			tool := srv.GetTool(name)
			if tool == nil {
				t.Errorf("cortex_* tool %q is NOT registered — expected it to be discoverable (REQ-MCP-001)", name)
			}
		})
	}
}

// TestAdminNamesRegisteredWithAdminProfile asserts admin tools register when
// the admin profile is selected.
func TestAdminNamesRegisteredWithAdminProfile(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServerWithTools(stores, ProfileAdmin)
	for _, name := range requiredAdminTools {
		tool := srv.GetTool(name)
		if tool == nil {
			t.Errorf("admin tool %q NOT registered with admin profile (REQ-MCP-002)", name)
		}
	}
}

// TestAgentProfileExcludesAdminTools asserts that when the agent profile is
// selected, admin tools are NOT registered.
func TestAgentProfileExcludesAdminTools(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServerWithTools(stores, ProfileAgent)
	for _, name := range requiredAdminTools {
		tool := srv.GetTool(name)
		if tool != nil {
			t.Errorf("admin tool %q registered with agent profile — admin tools must NOT appear in ordinary agent discovery (REQ-MCP-002)", name)
		}
	}
}

// --- R6 / REM-MCP-001: shared memorycontract surface on the local server -----

// normalizedOutputSchema renders a tool's published outputSchema as comparable
// JSON regardless of whether it was set raw or typed.
func normalizedOutputSchema(t *testing.T, tool *server.ServerTool) json.RawMessage {
	t.Helper()
	if tool == nil {
		return nil
	}
	raw := tool.Tool.RawOutputSchema
	if len(raw) == 0 && tool.Tool.OutputSchema.Type != "" {
		encoded, err := json.Marshal(tool.Tool.OutputSchema)
		if err != nil {
			t.Fatalf("marshal output schema: %v", err)
		}
		raw = encoded
	}
	return normalizedJSON(t, raw)
}

// normalizedJSON canonicalizes raw JSON (parse + compact re-marshal with
// sorted keys) so two semantically identical documents compare equal. The
// mcp-go server may re-serialize a raw schema, so both sides of every schema
// comparison must go through this normalization.
func normalizedJSON(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		t.Fatalf("JSON is not valid: %v", err)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		t.Fatalf("renormalize JSON: %v", err)
	}
	return encoded
}

// TestCortexHandoffRegisteredWithContract asserts that tools/list registers
// cortex_handoff with the shared outputSchema, annotations, and input schema
// from internal/mcp/memorycontract (REM-MCP-001, RD6).
func TestCortexHandoffRegisteredWithContract(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServer(stores)

	tool := srv.GetTool(memorycontract.ToolHandoff)
	if tool == nil {
		t.Fatal("cortex_handoff is NOT registered — the local server must publish the durable handoff tool (REM-MCP-001)")
	}
	wantSchema := normalizedJSON(t, memorycontract.WriteOutputSchemaJSON)
	if got := normalizedOutputSchema(t, tool); string(got) != string(wantSchema) {
		t.Errorf("cortex_handoff outputSchema = %s, want the shared memorycontract schema %s", got, wantSchema)
	}
	annotations := tool.Tool.Annotations
	if boolHint(annotations.DestructiveHint) || boolHint(annotations.ReadOnlyHint) || boolHint(annotations.OpenWorldHint) {
		t.Errorf("cortex_handoff annotations = %+v, want read/write non-destructive closed-world", annotations)
	}
	if !boolHint(annotations.IdempotentHint) {
		t.Errorf("cortex_handoff must be annotated idempotent — same key+payload replays (REM-HANDOFF-002)")
	}

	var inputSchema struct {
		Type       string   `json:"type"`
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	rawInput := tool.Tool.RawInputSchema
	if len(rawInput) == 0 {
		encoded, err := json.Marshal(tool.Tool.InputSchema)
		if err != nil {
			t.Fatalf("marshal input schema: %v", err)
		}
		rawInput = encoded
	}
	if err := json.Unmarshal(rawInput, &inputSchema); err != nil {
		t.Fatalf("cortex_handoff input schema is not valid JSON: %v", err)
	}
	if inputSchema.Type != "object" {
		t.Errorf("cortex_handoff input schema type = %q, want object", inputSchema.Type)
	}
	for _, required := range []string{"idempotency_key", "observation"} {
		found := false
		for _, name := range inputSchema.Required {
			if name == required {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("cortex_handoff input schema missing required property %q", required)
		}
	}
	if _, ok := inputSchema.Properties["relation"]; !ok {
		t.Error("cortex_handoff input schema missing optional relation property")
	}
}

// TestCortexSavePublishesSharedOutputSchema asserts cortex_save carries the
// shared structured outputSchema additively, without touching its legacy input
// surface (REM-SAVE-001).
func TestCortexSavePublishesSharedOutputSchema(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServer(stores)

	tool := srv.GetTool(memorycontract.ToolSave)
	if tool == nil {
		t.Fatal("cortex_save is NOT registered")
	}
	wantSchema := normalizedJSON(t, memorycontract.WriteOutputSchemaJSON)
	if got := normalizedOutputSchema(t, tool); string(got) != string(wantSchema) {
		t.Errorf("cortex_save outputSchema = %s, want the shared memorycontract schema %s", got, wantSchema)
	}
	if boolHint(tool.Tool.Annotations.IdempotentHint) {
		t.Errorf("cortex_save annotations = %+v, legacy non-idempotent hint must be preserved", tool.Tool.Annotations)
	}
}

// boolHint dereferences a nullable JSON-LD hint: nil means unset (default).
func boolHint(hint *bool) bool { return hint != nil && *hint }

// TestAllToolsMarshalJSONWithoutSchemaConflict verifies that every tool registered
// in both default and filtered modes can be marshaled to JSON without mcp-go schema conflicts.
func TestAllToolsMarshalJSONWithoutSchemaConflict(t *testing.T) {
	stores := setupTestStores(t)
	srv := NewServer(stores)

	tools := srv.ListTools()
	if len(tools) == 0 {
		t.Fatal("expected registered tools, got 0")
	}

	for _, tool := range tools {
		data, err := json.Marshal(tool.Tool)
		if err != nil {
			t.Fatalf("tool %s failed to marshal JSON: %v", tool.Tool.Name, err)
		}
		if len(data) == 0 {
			t.Errorf("tool %s produced empty JSON", tool.Tool.Name)
		}
	}
}
