package mcp

// Behavioral characterization tests for memory MCP handlers
// that are not already covered by tools_test.go or integration_test.go.
//
// This file is part of SDD change coverage-70-and-lint (task 1.3 G2). It only
// authors tests in this reserved file; it does NOT modify production code nor
// any other test file. Oracles used: result.IsError, stable semantic text
// fragments, decoded payloads, and database effects (via the store layer).
//
// These are characterization tests: the production behavior already exists, so
// strict-TDD RED is demonstrated by a reversible, production-safe assertion
// flip (a deliberately wrong oracle fails, the correct oracle passes) rather
// than by mutating production code (which this task forbids).

import (
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
)

// --- cortex_context ------------------------------------------------------

func TestHandleContext_EmptyStore(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleContext(stores), map[string]interface{}{})
	text := resultText(result)

	// No sessions and no observations -> dedicated empty-state message.
	if !strings.Contains(text, "No previous session memories found.") {
		t.Fatalf("expected empty-state message, got %q", text)
	}
}

func TestHandleContext_Populated(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Context Obs", "demo", "s1")

	// Seed a prompt so the "Recent Prompts" section is exercised.
	if err := stores.Prompts.Save(context.Background(), &domain.Prompt{
		Content:   "How do I configure FTS5",
		Project:   "demo",
		SessionID: "s1",
	}); err != nil {
		t.Fatalf("seed prompt: %v", err)
	}

	result := callTool(t, handleContext(stores), map[string]interface{}{
		"project": "demo",
	})
	text := resultText(result)

	for _, want := range []string{
		"## Recent Sessions",
		"## Recent Prompts",
		"## Recent Observations",
		"Memory stats:",
		"Context Obs",
		"How do I configure FTS5",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected context output to contain %q, got %q", want, text)
		}
	}
}

// --- cortex_session_summary ----------------------------------------------

// TestHandleSessionSummary_PersistsSessionSummaryType verifies the FIXED
// behavior: handleSessionSummary saves with Type "session_summary", which is
// now part of the observation store's allowed type set. The save MUST succeed
// (IsError=false) and a database row with Type "session_summary" MUST persist.
//
// Previously this was a known production defect: "session_summary" was rejected
// by the validation switch, so cortex_session_summary always failed to persist.
// (SDD change cortex-v2-independent-platform, W1.1 type-registry fix; specs
// REQ-FOUND-002 and REQ-MCPH-001.)
func TestHandleSessionSummary_PersistsSessionSummaryType(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleSessionSummary(stores), map[string]interface{}{
		"content": "## Goal\nShip coverage\n\n## Accomplished\n- Added tests",
		"project": "demo",
	})
	text := resultText(result)

	// FIX: the session_summary type is now accepted, so the save succeeds.
	if result.IsError {
		t.Fatalf("expected success (session_summary is a valid type), got IsError %q", text)
	}
	if !strings.Contains(text, "Session summary saved") {
		t.Errorf("expected 'Session summary saved' confirmation, got %q", text)
	}

	// Database effect: exactly one session_summary observation persisted.
	list, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	var count int
	for _, o := range list {
		if o.Type == "session_summary" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 session_summary observation persisted, got %d", count)
	}
}

// --- cortex_get_observation ----------------------------------------------

func TestHandleGetObservation_Success(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "Gettable Obs", "demo", "s1")

	result := callTool(t, handleGetObservation(stores), map[string]interface{}{
		"id": float64(obs.ID),
	})
	text := resultText(result)

	for _, want := range []string{"Gettable Obs", "Content for Gettable Obs", "Session:", "Scope:", "Created:"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected get output to contain %q, got %q", want, text)
		}
	}

	// Database effect: implicit access recording bumps the importance access count.
	score, err := stores.Scoring.GetScore(context.Background(), obs.ID)
	if err != nil {
		t.Fatalf("get score: %v", err)
	}
	if score.AccessCount != 1 {
		t.Errorf("expected access_count=1 after get, got %d", score.AccessCount)
	}
}

func TestHandleGetObservation_MissingID(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleGetObservation(stores), map[string]interface{}{})
	if !result.IsError {
		t.Fatalf("expected IsError for missing id, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "id must be a positive integer") {
		t.Errorf("expected 'id must be a positive integer', got %q", resultText(result))
	}
}

func TestHandleGetObservation_NotFound(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleGetObservation(stores), map[string]interface{}{
		"id": float64(999999),
	})
	if !result.IsError {
		t.Fatalf("expected IsError for missing observation, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "not found") {
		t.Errorf("expected 'not found', got %q", resultText(result))
	}
}

// --- cortex_save_prompt -------------------------------------------------

func TestHandleSavePrompt(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleSavePrompt(stores), map[string]interface{}{
		"content":    "How do I add an MCP tool?",
		"project":    "demo",
		"session_id": "s1",
	})
	text := resultText(result)

	if !strings.Contains(text, "Prompt saved:") {
		t.Fatalf("expected 'Prompt saved:' confirmation, got %q", text)
	}

	// Database effect: prompt persisted and retrievable via the prompt store.
	prompts, err := stores.Prompts.List(context.Background(), "demo", 10)
	if err != nil {
		t.Fatalf("list prompts: %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt persisted, got %d", len(prompts))
	}
	if !strings.Contains(prompts[0].Content, "How do I add an MCP tool?") {
		t.Errorf("unexpected prompt content: %q", prompts[0].Content)
	}
}

// --- cortex_update -------------------------------------------------------

func TestHandleUpdate_Success(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "Original Title", "demo", "s1")

	result := callTool(t, handleUpdate(stores), map[string]interface{}{
		"id":      float64(obs.ID),
		"title":   "Updated Title",
		"content": "Updated content body",
	})
	text := resultText(result)

	if !strings.Contains(text, "Memory updated:") {
		t.Fatalf("expected 'Memory updated:' confirmation, got %q", text)
	}

	// Database effect: the persisted observation reflects the new title.
	got, err := stores.Observations.GetByID(context.Background(), obs.ID)
	if err != nil {
		t.Fatalf("get observation: %v", err)
	}
	if got.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got %q", got.Title)
	}
	if got.Content != "Updated content body" {
		t.Errorf("expected updated content, got %q", got.Content)
	}
}

func TestHandleUpdate_MissingID(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleUpdate(stores), map[string]interface{}{
		"title": "anything",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for missing id, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "id must be a positive integer") {
		t.Errorf("expected 'id must be a positive integer', got %q", resultText(result))
	}
}

func TestHandleUpdate_NoFields(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "Keep Me", "demo", "s1")

	// Only id provided -> nothing to update.
	result := callTool(t, handleUpdate(stores), map[string]interface{}{
		"id": float64(obs.ID),
	})
	if !result.IsError {
		t.Fatalf("expected IsError when no updatable field provided, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "provide at least one field to update") {
		t.Errorf("expected guidance to provide a field, got %q", resultText(result))
	}
}

func TestHandleUpdate_NotFound(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleUpdate(stores), map[string]interface{}{
		"id":    float64(999999),
		"title": "ghost",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for missing observation, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Failed to find memory") {
		t.Errorf("expected 'Failed to find memory', got %q", resultText(result))
	}
}

// --- cortex_suggest_topic_key --------------------------------------------

func TestHandleSuggestTopicKey(t *testing.T) {
	cases := []struct {
		name        string
		args        map[string]interface{}
		wantPrefix  string
		wantErr     bool
		wantErrFrag string
	}{
		{
			name:       "architecture family from type",
			args:       map[string]interface{}{"type": "architecture", "title": "Auth Design"},
			wantPrefix: "architecture/",
		},
		{
			name:       "bug family inferred from title keyword",
			args:       map[string]interface{}{"title": "Fixed N+1 query in user list"},
			wantPrefix: "bug/",
		},
		{
			name:       "decision family inferred from content keyword",
			args:       map[string]interface{}{"title": "Token strategy", "content": "We decided to choose JWT for auth"},
			wantPrefix: "decision/",
		},
		{
			name:        "empty input rejected",
			args:        map[string]interface{}{},
			wantErr:     true,
			wantErrFrag: "provide title or content",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, handleSuggestTopicKey(), tc.args)
			text := resultText(result)

			if tc.wantErr {
				if !result.IsError {
					t.Fatalf("expected IsError, got %q", text)
				}
				if !strings.Contains(text, tc.wantErrFrag) {
					t.Errorf("expected error fragment %q, got %q", tc.wantErrFrag, text)
				}
				return
			}

			if !strings.HasPrefix(text, "Suggested topic_key:") {
				t.Fatalf("expected 'Suggested topic_key:' prefix, got %q", text)
			}
			if !strings.Contains(text, tc.wantPrefix) {
				t.Errorf("expected topic family prefix %q in %q", tc.wantPrefix, text)
			}
		})
	}
}

// --- cortex_session_start / cortex_session_end ---------------------------

func TestHandleSessionStart(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleSessionStart(stores), map[string]interface{}{
		"id":      "sess-start-1",
		"project": "demo",
	})
	text := resultText(result)

	if !strings.Contains(text, `Session "sess-start-1" started for project "demo"`) {
		t.Fatalf("expected start confirmation, got %q", text)
	}

	// Database effect: session was created and is retrievable.
	sess, err := stores.Sessions.GetByID(context.Background(), "sess-start-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Project != "demo" {
		t.Errorf("expected project 'demo', got %q", sess.Project)
	}
}

func TestHandleSessionStart_DefaultDirectory(t *testing.T) {
	stores := setupTestStores(t)

	// Omit directory -> handler defaults to ".".
	result := callTool(t, handleSessionStart(stores), map[string]interface{}{
		"id":      "sess-default-dir",
		"project": "demo",
	})
	if result.IsError {
		t.Fatalf("did not expect error, got %q", resultText(result))
	}

	sess, err := stores.Sessions.GetByID(context.Background(), "sess-default-dir")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.Directory != "." {
		t.Errorf("expected default directory '.', got %q", sess.Directory)
	}
}

func TestHandleSessionEnd_Success(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "sess-end-1", "demo")

	result := callTool(t, handleSessionEnd(stores), map[string]interface{}{
		"id":      "sess-end-1",
		"summary": "Shipped coverage tests",
	})
	text := resultText(result)

	if !strings.Contains(text, `Session "sess-end-1" completed`) {
		t.Fatalf("expected completion confirmation, got %q", text)
	}

	// Database effect: session is marked ended and summary stored.
	sess, err := stores.Sessions.GetByID(context.Background(), "sess-end-1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess.EndedAt == nil {
		t.Error("expected EndedAt to be set after end")
	}
	if sess.Summary != "Shipped coverage tests" {
		t.Errorf("expected summary persisted, got %q", sess.Summary)
	}
}

func TestHandleSessionEnd_NotFound(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleSessionEnd(stores), map[string]interface{}{
		"id": "does-not-exist",
	})
	if !result.IsError {
		t.Fatalf("expected IsError when ending missing session, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "Failed to end session") {
		t.Errorf("expected 'Failed to end session', got %q", resultText(result))
	}
}

// --- cortex_stats --------------------------------------------------------

func TestHandleStats(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	saveObs(t, stores, "Stat Obs", "demo", "s1")

	result := callTool(t, handleStats(stores), map[string]interface{}{})
	text := resultText(result)

	for _, want := range []string{"Memory System Stats:", "Sessions:", "Observations:", "Projects:", "demo"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected stats output to contain %q, got %q", want, text)
		}
	}
}

// --- cortex_timeline / cortex_revision_history (edge cases) --------------

func TestHandleTimeline_MissingID(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleTimeline(stores), map[string]interface{}{})
	if !result.IsError {
		t.Fatalf("expected IsError for missing observation_id, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "observation_id must be a positive integer") {
		t.Errorf("expected 'observation_id must be a positive integer', got %q", resultText(result))
	}
}

func TestHandleTimeline_NotFound(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleTimeline(stores), map[string]interface{}{
		"observation_id": float64(999999),
	})
	if !result.IsError {
		t.Fatalf("expected IsError for missing observation, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "not found") {
		t.Errorf("expected 'not found', got %q", resultText(result))
	}
}

func TestHandleRevisionHistory_MissingID(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleRevisionHistory(stores), map[string]interface{}{})
	if !result.IsError {
		t.Fatalf("expected IsError for missing observation_id, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "observation_id must be a positive integer") {
		t.Errorf("expected 'observation_id must be a positive integer', got %q", resultText(result))
	}
}

func TestHandleRevisionHistory_NoHistory(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "demo")
	obs := saveObs(t, stores, "No Revisions", "demo", "s1")

	// An observation that was never updated has no revision snapshots.
	result := callTool(t, handleRevisionHistory(stores), map[string]interface{}{
		"observation_id": float64(obs.ID),
	})
	text := resultText(result)
	if text != "[]" {
		t.Errorf("expected '[]' for empty history, got %q", text)
	}
}

// --- cortex_capture_passive (extraction + cleaning) ----------------------

func TestHandleCapturePassive_EmptyContent(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"project": "demo",
	})
	if !result.IsError {
		t.Fatalf("expected IsError for empty content, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "content is required") {
		t.Errorf("expected 'content is required', got %q", resultText(result))
	}
}

// TestHandleCapturePassive_PersistsPassiveType verifies the type-registry fix
// through the capture-passive handler path: handleCapturePassive saves each
// learning with Type "passive", which is now part of the observation store's
// allowed type set, so every extracted learning persists.
//
// This test passes an explicit valid source ("auto"). The handler's DEFAULT
// source was previously the invalid "mcp-passive" (not in the allowed source
// set), but W6.2 fixed the default to domain.SourceAuto ("auto"). The
// explicit source here is redundant now that the default is valid, but kept
// for historical clarity. The store-level test below proves both new types
// persist unconditionally.
//
// (SDD change cortex-v2-independent-platform, W1.1 type-registry fix + W6.2
// source-default fix.)
func TestHandleCapturePassive_PersistsPassiveType(t *testing.T) {
	stores := setupTestStores(t)

	content := "## Key Learnings:\n" +
		"1. Always wrap database writes in explicit transactions\n" +
		"2. Validate incoming input before persisting any state changes\n"

	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"content": content,
		"project": "demo",
		// Explicit valid source (redundant since W6.2 default is also "auto").
		"source": "auto",
	})
	text := resultText(result)

	if !strings.Contains(text, "Passive capture complete:") {
		t.Fatalf("expected capture summary, got %q", text)
	}
	// Extraction works.
	if !strings.Contains(text, "extracted=2") {
		t.Errorf("expected extracted=2 (extraction works), got %q", text)
	}
	// FIX: both learnings now persist (saved=2) because 'passive' is accepted.
	if !strings.Contains(text, "saved=2") {
		t.Errorf("expected saved=2 (passive type is now accepted), got %q", text)
	}

	// Database effect: two passive observations persisted.
	list, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	var count int
	for _, o := range list {
		if o.Type == "passive" {
			count++
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 passive observations persisted, got %d", count)
	}
}

// TestStore_NewTypesAccepted_BogusTypeRejected is the criterion-3 regression
// test for the type-registry fix (REQ-FOUND-002). It pins three invariants at
// the store layer:
//
//  1. The newly-accepted types (session_summary, passive) Save successfully and
//     return the REAL outcome (nil error), never a masked type-rejection.
//  2. A genuinely invalid type is STILL rejected with a ValidationError whose
//     message is "type must be one of ...". This proves the switch boundary is
//     correct and the registry was not made over-permissive.
//  3. Real persistence failures cannot be masked as type-rejections:
//     validateObservation() is a pure pre-check that runs BEFORE any DB access,
//     so it returns a ValidationError ONLY for actual type violations. A real
//     DB error (e.g. database locked) originates from the transaction path
//     (memory store: insert observation: %w) and is structurally incapable of
//     being turned into a "type must be one of" error by the validation switch.
//
// (SDD change cortex-v2-independent-platform, W1.1 type-registry fix.)
func TestStore_NewTypesAccepted_BogusTypeRejected(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "reg-sess", "demo")

	// Each newly-accepted type must save successfully and persist a row.
	for _, tc := range []struct {
		name, typ string
	}{
		{"session_summary", domain.TypeSessionSummary},
		{"passive", domain.TypePassive},
	} {
		t.Run(tc.name+"_persists", func(t *testing.T) {
			obs := &domain.Observation{
				SessionID: "reg-sess",
				Type:      tc.typ,
				Title:     "Reg " + tc.name,
				Content:   "Regression content for " + tc.name,
				Project:   "demo",
				Scope:     domain.ScopeProject,
			}
			// Valid new type: MUST NOT be masked as a type-rejection. The save
			// returns the real outcome (nil on success).
			if err := stores.Observations.Save(context.Background(), obs); err != nil {
				t.Fatalf("expected %q to be accepted, got masked/rejected error: %v", tc.typ, err)
			}
			if obs.ID == 0 {
				t.Fatalf("expected a persisted row (nonzero ID) for %q", tc.typ)
			}

			// Database effect: the row exists with the exact type stored.
			got, err := stores.Observations.GetByID(context.Background(), obs.ID)
			if err != nil {
				t.Fatalf("get observation: %v", err)
			}
			if got.Type != tc.typ {
				t.Errorf("expected stored type %q, got %q", tc.typ, got.Type)
			}
		})
	}

	// A genuinely invalid type MUST still be rejected by the validation switch.
	// This is the negative control proving the registry is not over-permissive.
	t.Run("bogus_type_rejected_with_validation_error", func(t *testing.T) {
		obs := &domain.Observation{
			SessionID: "reg-sess",
			Type:      "totally-bogus-type",
			Title:     "Should Not Persist",
			Content:   "This must be rejected by the validation switch",
			Project:   "demo",
			Scope:     domain.ScopeProject,
		}
		err := stores.Observations.Save(context.Background(), obs)
		if err == nil {
			t.Fatal("expected a ValidationError for a bogus type, got nil")
		}
		// The real type-rejection error surfaces verbatim — not masked.
		if !strings.Contains(err.Error(), "type must be one of") {
			t.Fatalf("expected 'type must be one of' rejection, got %q", err.Error())
		}
		// No row may have been persisted for the bogus type.
		list, lerr := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
		if lerr != nil {
			t.Fatalf("list observations: %v", lerr)
		}
		for _, o := range list {
			if o.Type == "totally-bogus-type" {
				t.Error("a bogus-type observation must not be persisted")
			}
		}
	})
}

func TestHandleCapturePassive_SpanishHeaderExtracts(t *testing.T) {
	stores := setupTestStores(t)

	content := "## Aprendizajes Clave:\n" +
		"1. Siempre usar transacciones al escribir en la base de datos principal\n"

	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"content": content,
		"project": "demo",
	})
	text := resultText(result)

	if !strings.Contains(text, "Passive capture complete:") {
		t.Fatalf("expected capture summary, got %q", text)
	}
	// Spanish header is recognized and the item is extracted.
	if !strings.Contains(text, "extracted=1") {
		t.Errorf("expected extracted=1 for Spanish header, got %q", text)
	}
}

func TestHandleCapturePassive_NoLearningsSection(t *testing.T) {
	stores := setupTestStores(t)

	content := "Some narrative text without any structured learnings header at all."

	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"content": content,
		"project": "demo",
	})
	text := resultText(result)

	if !strings.Contains(text, "extracted=0") || !strings.Contains(text, "saved=0") {
		t.Errorf("expected extracted=0 saved=0 when no section present, got %q", text)
	}
}

// Direct unit tests for the pure extraction/cleaning helpers are decoupled from
// the persistence regression coverage above and directly cover extraction
// behavior (English/Spanish headers, numbered vs bullet fallback, markdown
// stripping, and the minimum-length/word gate).

func TestExtractLearnings(t *testing.T) {
	cases := []struct {
		name     string
		text     string
		wantLen  int
		wantFrag string
	}{
		{
			name:     "english numbered",
			text:     "## Key Learnings:\n1. Always wrap database writes in explicit transactions\n",
			wantLen:  1,
			wantFrag: "explicit transactions",
		},
		{
			name:     "spanish aprendizajes clave",
			text:     "## Aprendizajes Clave:\n1. Siempre usar transacciones al escribir en la base de datos\n",
			wantLen:  1,
			wantFrag: "transacciones",
		},
		{
			name: "bullet fallback",
			text: "## Learnings:\n" +
				"- Always validate input before persisting any state changes to disk\n",
			wantLen:  1,
			wantFrag: "validate input",
		},
		{
			name:    "no header yields nothing",
			text:    "Plain narrative without any learnings section header.",
			wantLen: 0,
		},
		{
			name:    "too short is filtered",
			text:    "## Key Learnings:\n1. tiny\n",
			wantLen: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractLearnings(tc.text)
			if len(got) != tc.wantLen {
				t.Fatalf("expected %d learnings, got %d (%v)", tc.wantLen, len(got), got)
			}
			if tc.wantFrag != "" {
				if !strings.Contains(strings.Join(got, " "), tc.wantFrag) {
					t.Errorf("expected fragment %q in %v", tc.wantFrag, got)
				}
			}
		})
	}
}

func TestExtractLearnings_StripsMarkdown(t *testing.T) {
	text := "## Key Learnings:\n1. Use **bold** and `code` markers carefully when writing output\n"
	got := extractLearnings(text)
	if len(got) != 1 {
		t.Fatalf("expected 1 learning, got %d", len(got))
	}
	if strings.Contains(got[0], "**") {
		t.Errorf("bold markdown not stripped: %q", got[0])
	}
	if strings.Contains(got[0], "`") {
		t.Errorf("inline-code markdown not stripped: %q", got[0])
	}
}

func TestCleanMarkdown(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Use **bold** here", "Use bold here"},
		{"Use `code` here", "Use code here"},
		{"Use *italic* here", "Use italic here"},
		{"Collapse   extra\t whitespace", "Collapse extra whitespace"},
	}
	for _, tc := range cases {
		if got := cleanMarkdown(tc.in); got != tc.want {
			t.Errorf("cleanMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
