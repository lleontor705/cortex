package mcp

import (
	"strings"
	"testing"
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

// The 16 tools required in the ordinary agent profile (design Part 2 §9).
var requiredOrdinaryAgentTools = []string{
	"cortex_save", "cortex_search", "cortex_context",
	"cortex_session_summary", "cortex_session_start", "cortex_session_end",
	"cortex_get_observation", "cortex_suggest_topic_key", "cortex_capture_passive",
	"cortex_save_prompt", "cortex_update", "cortex_relate",
	"cortex_graph", "cortex_score", "cortex_search_hybrid",
	"cortex_revision_history",
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
