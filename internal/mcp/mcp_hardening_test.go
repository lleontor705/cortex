package mcp

// W6.2 MCP Hardening Tests — REQ-MCPH-001/002/003
//
// These tests pin the hardening fixes that W6.2 delivers:
//
//  1. handleCapturePassive defaults to a VALID source so passive observations
//     actually persist without an explicit source argument (REQ-MCPH-001).
//  2. handleCapturePassive classifies save errors via the ValidationError
//     taxonomy and SURFACES real failures instead of swallowing them as
//     duplicates (REQ-MCPH-002).
//  3. The validation error message in the store lists all accepted types
//     including session_summary and passive (REQ-MCPH-003).
//  4. handleSave treats a dedup skip (ClassDedupSkipped) as success, not error
//     (REQ-MCPH-002 / REQ-FOUND-003).
//  5. Server instructions and tool descriptions are Cortex-native with no
//     legacy-compatibility framing (REQ-MCPH-003).

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	sessionstore "github.com/lleontor705/cortex/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// --- REQ-MCPH-001: passive persists with default source ---------------------

// TestHandleCapturePassive_DefaultSourcePersists verifies that
// handleCapturePassive WITHOUT an explicit "source" argument still persists
// every extracted learning. The handler must default to a VALID source
// ("auto"), not the invalid "mcp-passive" that was silently rejected.
func TestHandleCapturePassive_DefaultSourcePersists(t *testing.T) {
	stores := setupTestStores(t)

	content := "## Key Learnings:\n" +
		"1. Always validate input arguments before persisting to the database\n" +
		"2. Use context cancellation to prevent goroutine leaks on shutdown\n"

	// NO explicit source — handler must default to a valid source.
	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"content": content,
		"project": "demo",
	})
	text := resultText(result)

	if result.IsError {
		t.Fatalf("expected success, got IsError: %q", text)
	}
	if !strings.Contains(text, "saved=2") {
		t.Fatalf("expected saved=2 (default source must be valid), got %q", text)
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

// --- REQ-MCPH-002: real errors surfaced, not swallowed ----------------------

// TestHandleCapturePassive_RealErrorSurfaced verifies that a genuine
// persistence failure is NOT silently swallowed as a "duplicate". The handler
// must surface the failure in its response and report a non-zero failures
// count.
func TestHandleCapturePassive_RealErrorSurfaced(t *testing.T) {
	// Create a store backed by a CLOSED database to force real SQL errors.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// Provide both Observations and Sessions so the handler doesn't nil-panic;
	// both use the closed DB so every query fails.
	brokenStores := &Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     sessionstore.NewStore(db),
	}

	content := "## Key Learnings:\n1. This learning will fail to persist due to closed DB connection\n"

	result := callTool(t, handleCapturePassive(brokenStores), map[string]interface{}{
		"content": content,
		"project": "demo",
		"source":  "auto",
	})
	text := resultText(result)

	// The handler MUST surface the failure — either as IsError=true OR as a
	// response that includes a failures count > 0.
	if result.IsError {
		// Acceptable: handler returned an error response.
		return
	}
	if !strings.Contains(text, "failed=") {
		t.Errorf("expected 'failed=' in response for real error, got %q", text)
	}
	// The response must NOT claim the learning was saved or deduped.
	if strings.Contains(text, "saved=1") {
		t.Errorf("real error was miscounted as saved: %q", text)
	}
}

// TestHandleCapturePassive_CorrectCounting verifies the extracted/saved/
// duplicates/failed counts are accurate when a mix of outcomes occurs.
func TestHandleCapturePassive_CorrectCounting(t *testing.T) {
	stores := setupTestStores(t)

	content := "## Key Learnings:\n" +
		"1. First learning that will save successfully into the store\n" +
		"2. Second learning that will also save successfully into the store\n"

	result := callTool(t, handleCapturePassive(stores), map[string]interface{}{
		"content": content,
		"project": "demo",
		"source":  "auto",
	})
	text := resultText(result)

	// Must report correct counts for the first successful capture.
	if !strings.Contains(text, "extracted=2") {
		t.Errorf("expected extracted=2, got %q", text)
	}
	if !strings.Contains(text, "saved=2") {
		t.Errorf("expected saved=2, got %q", text)
	}
	if !strings.Contains(text, "failed=0") {
		t.Errorf("expected failed=0 for all-successful capture, got %q", text)
	}
}

// --- REQ-MCPH-002: store dedup returns ClassDedupSkipped --------------------

// TestStore_ManualDedupReturnsClassDedupSkipped verifies that saving a
// duplicate "manual" observation returns a ValidationError with Code
// ClassDedupSkipped, so callers can classify the outcome via errors.As
// instead of guessing.
func TestStore_ManualDedupReturnsClassDedupSkipped(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "dedup-sess", "demo")

	obs := &domain.Observation{
		SessionID: "dedup-sess",
		Type:      domain.TypeManual,
		Title:     "Dedup Test",
		Content:   "Content that will be saved twice as manual type",
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}

	// First save: success.
	if err := stores.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("first save failed: %v", err)
	}

	// Second save of the same manual content: must return ClassDedupSkipped.
	dup := &domain.Observation{
		SessionID: "dedup-sess",
		Type:      domain.TypeManual,
		Title:     "Dedup Test",
		Content:   "Content that will be saved twice as manual type",
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}
	err := stores.Observations.Save(context.Background(), dup)
	if err == nil {
		t.Fatal("expected ClassDedupSkipped error for duplicate manual save, got nil")
	}
	if !domain.IsClass(err, domain.ClassDedupSkipped) {
		t.Errorf("expected ClassDedupSkipped, got %v (not classifiable as dedup_skipped)", err)
	}
}

// TestStore_NonDuplicateManualSaveReturnsNil verifies that a manual save that
// is NOT a duplicate returns nil (not a dedup classification).
func TestStore_NonDuplicateManualSaveReturnsNil(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "nodup-sess", "demo")

	obs := &domain.Observation{
		SessionID: "nodup-sess",
		Type:      domain.TypeManual,
		Title:     "Unique Content A",
		Content:   "First unique observation content",
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}
	if err := stores.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("first save: %v", err)
	}

	obs2 := &domain.Observation{
		SessionID: "nodup-sess",
		Type:      domain.TypeManual,
		Title:     "Unique Content B",
		Content:   "Second different observation content",
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}
	err := stores.Observations.Save(context.Background(), obs2)
	if err != nil {
		t.Errorf("non-duplicate save should return nil, got: %v", err)
	}
	if domain.IsClass(err, domain.ClassDedupSkipped) {
		t.Error("non-duplicate save was misclassified as dedup_skipped")
	}
}

// --- REQ-MCPH-002: handleSave treats dedup as success -----------------------

// TestHandleSave_DedupIsSuccess verifies that handleSave treats a
// ClassDedupSkipped outcome as success, not failure. The response must not
// carry IsError=true for a legitimate dedup skip.
func TestHandleSave_DedupIsSuccess(t *testing.T) {
	stores := setupTestStores(t)

	args := map[string]interface{}{
		"title":   "Dedup Via Handler",
		"content": "Manual content saved twice through cortex_save handler",
		"type":    "manual",
		"project": "demo",
	}

	// First save: success.
	result1 := callTool(t, handleSave(stores), args)
	if result1.IsError {
		t.Fatalf("first save failed: %q", resultText(result1))
	}

	// Second identical save: dedup — must still be success (IsError=false).
	result2 := callTool(t, handleSave(stores), args)
	if result2.IsError {
		t.Fatalf("dedup save returned IsError=true: %q", resultText(result2))
	}
}

// --- REQ-MCPH-003: validation error message lists all types -----------------

// TestValidationErrorMessageListsAllTypes verifies the store's validation
// error message for invalid types lists ALL accepted types, including
// session_summary and passive. A stale message that omits these types is a
// REQ-MCPH-003 violation.
func TestValidationErrorMessageListsAllTypes(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "msg-sess", "demo")

	obs := &domain.Observation{
		SessionID: "msg-sess",
		Type:      "totally-bogus-type",
		Title:     "Bogus",
		Content:   "Trigger validation error",
		Project:   "demo",
		Scope:     domain.ScopeProject,
	}

	err := stores.Observations.Save(context.Background(), obs)
	if err == nil {
		t.Fatal("expected validation error for bogus type")
	}
	msg := strings.ToLower(err.Error())
	for _, want := range []string{"session_summary", "passive"} {
		if !strings.Contains(msg, want) {
			t.Errorf("validation error message missing %q: %s", want, err.Error())
		}
	}
}

// --- REQ-MCPH-003: Cortex-native instructions scan --------------------------

// TestServerInstructionsNoStaleToolCounts verifies that the server instructions
// don't carry stale tool counts or references to removed tool names.
func TestServerInstructionsNoStaleToolCounts(t *testing.T) {
	lower := strings.ToLower(serverInstructions)
	// Must not reference removed legacy names.
	for _, legacy := range removedLegacyNames {
		if strings.Contains(lower, legacy) {
			t.Errorf("serverInstructions references removed legacy name %q", legacy)
		}
	}
	// Must reference at least the core tools.
	for _, core := range []string{"cortex_save", "cortex_search", "cortex_context", "cortex_session_summary"} {
		if !strings.Contains(lower, core) {
			t.Errorf("serverInstructions missing core tool reference %q", core)
		}
	}
}
