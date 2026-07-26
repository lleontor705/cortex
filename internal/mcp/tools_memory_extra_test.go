package mcp

// Behavioral characterization tests for Engram-compatible memory MCP handlers
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

// --- mem_context ---------------------------------------------------------

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

// --- mem_session_summary -------------------------------------------------

// TestHandleSessionSummary_KnownDefect_TypeRejected characterizes a KNOWN
// PRODUCTION DEFECT (see issues_found in the apply contract / issue #46):
// handleSessionSummary saves with Type "session_summary", which is NOT in the
// observation store's allowed type set
// (manual, tool_use, decision, bugfix, pattern, config, discovery, learning).
// As a result mem_session_summary currently ALWAYS fails to persist and returns
// an error result. This test pins current behavior so the eventual fix is forced
// to update it; it is not asserting intended behavior.
func TestHandleSessionSummary_KnownDefect_TypeRejected(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleSessionSummary(stores), map[string]interface{}{
		"content": "## Goal\nShip coverage\n\n## Accomplished\n- Added tests",
		"project": "demo",
	})
	text := resultText(result)

	if !result.IsError {
		t.Fatalf("KNOWN DEFECT: expected IsError because 'session_summary' type is rejected, got success %q", text)
	}
	if !strings.Contains(text, "Failed to save session summary") {
		t.Errorf("expected failure message, got %q", text)
	}
	if !strings.Contains(text, "type must be one of") {
		t.Errorf("expected type-validation rejection, got %q", text)
	}

	// Database effect: nothing was persisted.
	list, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	for _, o := range list {
		if o.Type == "session_summary" {
			t.Error("KNOWN DEFECT: no session_summary observation should exist while the bug is present")
		}
	}
}

// --- mem_get_observation -------------------------------------------------

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
	if !strings.Contains(resultText(result), "id is required") {
		t.Errorf("expected 'id is required', got %q", resultText(result))
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

// --- mem_save_prompt -----------------------------------------------------

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

// --- mem_update ----------------------------------------------------------

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
	if !strings.Contains(resultText(result), "id is required") {
		t.Errorf("expected 'id is required', got %q", resultText(result))
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

// --- mem_suggest_topic_key -----------------------------------------------

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

// --- mem_session_start / mem_session_end ---------------------------------

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

// --- mem_stats -----------------------------------------------------------

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

// --- mem_timeline / mem_revision_history (edge cases) --------------------

func TestHandleTimeline_MissingID(t *testing.T) {
	stores := setupTestStores(t)

	result := callTool(t, handleTimeline(stores), map[string]interface{}{})
	if !result.IsError {
		t.Fatalf("expected IsError for missing observation_id, got %q", resultText(result))
	}
	if !strings.Contains(resultText(result), "observation_id is required") {
		t.Errorf("expected 'observation_id is required', got %q", resultText(result))
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
	if !strings.Contains(resultText(result), "observation_id is required") {
		t.Errorf("expected 'observation_id is required', got %q", resultText(result))
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

// --- mem_capture_passive (extraction + cleaning) -------------------------

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

// TestHandleCapturePassive_ExtractsButKnownDefectDoesNotPersist characterizes a
// KNOWN PRODUCTION DEFECT (see issues_found in the apply contract / issue #46):
// handleCapturePassive saves each learning with Type "passive", which is NOT in
// the observation store's allowed type set, so every Save is rejected. Worse,
// the handler swallows the Save error and counts it as a "duplicate", reporting
// saved=0 while presenting a success result. The extraction logic itself is
// correct (extracted=2); only persistence is broken. This test pins current
// behavior; it is not asserting intended behavior.
func TestHandleCapturePassive_ExtractsButKnownDefectDoesNotPersist(t *testing.T) {
	stores := setupTestStores(t)

	content := "## Key Learnings:\n" +
		"1. Always wrap database writes in explicit transactions\n" +
		"2. Validate incoming input before persisting any state changes\n"

	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"content": content,
		"project": "demo",
	})
	text := resultText(result)

	if !strings.Contains(text, "Passive capture complete:") {
		t.Fatalf("expected capture summary, got %q", text)
	}
	// Extraction works.
	if !strings.Contains(text, "extracted=2") {
		t.Errorf("expected extracted=2 (extraction works), got %q", text)
	}
	// KNOWN DEFECT: nothing is actually persisted (saved=0, masked as duplicates).
	if !strings.Contains(text, "saved=0") {
		t.Errorf("KNOWN DEFECT: expected saved=0 because 'passive' type is rejected, got %q", text)
	}

	list, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	for _, o := range list {
		if o.Type == "passive" {
			t.Error("KNOWN DEFECT: no passive observation should exist while the bug is present")
		}
	}
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

// Direct unit tests for the pure extraction/cleaning helpers. These are
// decoupled from the persistence defect above and give honest coverage of the
// learning-extraction behavior (English/Spanish headers, numbered vs bullet
// fallback, markdown stripping, and the minimum-length/word gate).

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
