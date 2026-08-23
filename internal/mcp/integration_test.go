package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/mcp/memorycontract"
	"github.com/lleontor705/cortex/v2/internal/migration"
	"github.com/lleontor705/cortex/v2/internal/store/bundle"
	entitystore "github.com/lleontor705/cortex/v2/internal/store/entity"
	graphstore "github.com/lleontor705/cortex/v2/internal/store/graph"
	"github.com/lleontor705/cortex/v2/internal/store/session"
	sqlitestore "github.com/lleontor705/cortex/v2/internal/store/sqlite"
	"github.com/mark3labs/mcp-go/mcp"
	_ "modernc.org/sqlite"
)

// --- Integration Tests --------------------------------------------------------

func TestIntegration_SaveRelateSearchFlow(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "myproject")

	// 1. Save 3 related observations
	saveHandler := handleSave(stores)

	r1 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "JWT auth middleware",
		"content":    "Implemented JWT authentication middleware for HTTP routes",
		"type":       "decision",
		"project":    "myproject",
		"session_id": "s1",
	})
	if !strings.Contains(resultText(r1), "Memory saved") {
		t.Fatalf("expected save confirmation for obs1, got %q", resultText(r1))
	}

	r2 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "Auth service refactor",
		"content":    "Refactored auth service to separate token validation from user lookup",
		"type":       "decision",
		"project":    "myproject",
		"session_id": "s1",
	})
	if !strings.Contains(resultText(r2), "Memory saved") {
		t.Fatalf("expected save confirmation for obs2, got %q", resultText(r2))
	}

	r3 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "Session token storage",
		"content":    "Added Redis-backed session token storage for auth tokens",
		"type":       "decision",
		"project":    "myproject",
		"session_id": "s1",
	})
	if !strings.Contains(resultText(r3), "Memory saved") {
		t.Fatalf("expected save confirmation for obs3, got %q", resultText(r3))
	}

	// Observation IDs should be 1, 2, 3 (auto-increment)
	obs1ID := float64(1)
	obs2ID := float64(2)
	obs3ID := float64(3)

	// 2. Relate observations: obs1 -> obs2 (references), obs2 -> obs3 (follows)
	relateHandler := handleRelate(stores)

	relResult1 := callTool(t, relateHandler, map[string]interface{}{
		"from_id":       obs1ID,
		"to_id":         obs2ID,
		"relation_type": "references",
	})
	if !strings.Contains(resultText(relResult1), "Relationship created") {
		t.Fatalf("expected relationship created for obs1->obs2, got %q", resultText(relResult1))
	}

	relResult2 := callTool(t, relateHandler, map[string]interface{}{
		"from_id":       obs2ID,
		"to_id":         obs3ID,
		"relation_type": "follows",
	})
	if !strings.Contains(resultText(relResult2), "Relationship created") {
		t.Fatalf("expected relationship created for obs2->obs3, got %q", resultText(relResult2))
	}

	// 3. Search for "auth" -- should find all 3 observations
	searchHandler := handleSearch(stores)
	searchResult := callTool(t, searchHandler, map[string]interface{}{
		"query":   "auth",
		"project": "myproject",
	})
	searchText := resultText(searchResult)
	if !strings.Contains(searchText, "JWT auth middleware") {
		t.Errorf("search for 'auth' should find 'JWT auth middleware', got %q", searchText)
	}
	if !strings.Contains(searchText, "Auth service refactor") {
		t.Errorf("search for 'auth' should find 'Auth service refactor', got %q", searchText)
	}
	if !strings.Contains(searchText, "Session token storage") {
		t.Errorf("search for 'auth' should find 'Session token storage', got %q", searchText)
	}

	// 4. Get graph from obs1 with depth 2 -- should find obs2 and obs3
	graphHandler := handleGraph(stores)
	graphResult := callTool(t, graphHandler, map[string]interface{}{
		"observation_id": obs1ID,
		"depth":          float64(2),
	})
	graphText := resultText(graphResult)
	if !strings.Contains(graphText, "Auth service refactor") {
		t.Errorf("graph traversal from obs1 depth 2 should find obs2, got %q", graphText)
	}
	if !strings.Contains(graphText, "Session token storage") {
		t.Errorf("graph traversal from obs1 depth 2 should find obs3, got %q", graphText)
	}

	// 5. Score obs1 -- should return a score
	scoreHandler := handleScore(stores)
	scoreResult := callTool(t, scoreHandler, map[string]interface{}{
		"observation_id": obs1ID,
	})
	scoreText := resultText(scoreResult)
	if !strings.Contains(scoreText, "score:") {
		t.Errorf("expected score info for obs1, got %q", scoreText)
	}
}

func TestIntegration_TopicKeyUpsertWithHistory(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "myproject")

	saveHandler := handleSave(stores)

	// 1. Save observation with topic_key "architecture/auth"
	r1 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "Auth architecture v1",
		"content":    "Using session-based auth with cookie storage",
		"type":       "decision",
		"project":    "myproject",
		"session_id": "s1",
		"topic_key":  "architecture/auth",
	})
	if !strings.Contains(resultText(r1), "Memory saved") {
		t.Fatalf("expected save confirmation, got %q", resultText(r1))
	}

	// 2. Save again with same topic_key but different content -- should upsert
	r2 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "Auth architecture v2",
		"content":    "Switched to JWT-based auth with refresh tokens",
		"type":       "decision",
		"project":    "myproject",
		"session_id": "s1",
		"topic_key":  "architecture/auth",
	})
	if !strings.Contains(resultText(r2), "Memory saved") {
		t.Fatalf("expected save confirmation for upsert, got %q", resultText(r2))
	}

	// 3. Call handleRevisionHistory -- should show 1 revision with the original content
	revHandler := handleRevisionHistory(stores)
	revResult := callTool(t, revHandler, map[string]interface{}{
		"observation_id": float64(1),
		"limit":          float64(10),
	})

	var history []map[string]interface{}
	if err := json.Unmarshal([]byte(resultText(revResult)), &history); err != nil {
		t.Fatalf("expected JSON revision history, got error: %v (text: %q)", err, resultText(revResult))
	}
	if len(history) < 1 {
		t.Fatalf("expected at least 1 revision entry, got %d", len(history))
	}

	// The revision should contain the original title
	foundOriginal := false
	for _, entry := range history {
		if title, ok := entry["title"].(string); ok && title == "Auth architecture v1" {
			foundOriginal = true
			break
		}
	}
	if !foundOriginal {
		t.Errorf("expected revision history to contain original title 'Auth architecture v1', got %v", history)
	}
}

func TestIntegration_DeduplicationFlow(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "myproject")

	saveHandler := handleSave(stores)

	// 1. Save observation with type "manual" (dedup only fires for "manual" type)
	r1 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "Database migration pattern",
		"content":    "Always use transactional migrations with up/down support",
		"type":       "manual",
		"project":    "myproject",
		"session_id": "s1",
	})
	if !strings.Contains(resultText(r1), "Memory saved") {
		t.Fatalf("expected save confirmation, got %q", resultText(r1))
	}

	// 2. Save same observation again (same title, content, project)
	r2 := callTool(t, saveHandler, map[string]interface{}{
		"title":      "Database migration pattern",
		"content":    "Always use transactional migrations with up/down support",
		"type":       "manual",
		"project":    "myproject",
		"session_id": "s1",
	})
	// The second save should still succeed (dedup bumps duplicate_count)
	if !strings.Contains(resultText(r2), "Memory saved") {
		t.Fatalf("expected save confirmation for duplicate, got %q", resultText(r2))
	}

	// 3. Search for the content -- should find only 1 observation (dedup worked)
	searchHandler := handleSearch(stores)
	searchResult := callTool(t, searchHandler, map[string]interface{}{
		"query":   "Database migration pattern",
		"project": "myproject",
	})
	searchText := resultText(searchResult)

	// Count occurrences of the title in search results
	count := strings.Count(searchText, "Database migration pattern")
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence of 'Database migration pattern' in search results (dedup), got %d.\nFull result: %s", count, searchText)
	}
}

// --- R6 / REM-MCP-001: local handoff receipts + UoW, frozen save text ---------

// handoffSessionID is the preexisting local session every handoff test uses.
// The handoff handler requires a preexisting session and validates it before
// any mutation, so the harness seeds it here (REM-HANDOFF-001).
const handoffSessionID = "mcp-handoff-session"

// setupHandoffStores opens a temp-file SQLite database with the full embedded
// v2 baseline (including handoff_receipts, REM-MIGRATION-001) and returns a
// local bundle wired with the SQLite UnitOfWork — the transactional primitive
// the handoff executor enlists on. MCP tests only: no store package is edited.
func setupHandoffStores(t *testing.T) (*Stores, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp-handoff.db")
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys=ON&_pragma=busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(8)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	baseline, err := migration.NewV2Baseline()
	if err != nil {
		t.Fatal(err)
	}
	if err := baseline.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply v2 baseline: %v", err)
	}

	stores := &Stores{
		Observations: sqlitestore.NewStore(db),
		Sessions:     session.NewStore(db),
		Graph:        graphstore.NewStore(db),
		UnitOfWork:   bundle.NewSQLiteUnitOfWork(db, domain.DefaultBusyRetryConfig()),
	}
	if err := stores.Sessions.Create(context.Background(), &domain.Session{
		ID: handoffSessionID, Project: "handoff-demo", Directory: ".",
	}); err != nil {
		t.Fatalf("seed handoff session: %v", err)
	}
	return stores, db
}

// handoffReceiptRow reads the durable receipt ledger for (scope, key).
func handoffReceiptRow(t *testing.T, db *sql.DB, scope, key string) (count int, state, initialStatus string) {
	t.Helper()
	if err := db.QueryRow(
		`SELECT count(*), coalesce(max(state),''), coalesce(max(initial_status),'') FROM handoff_receipts WHERE scope=? AND key=?`,
		scope, key,
	).Scan(&count, &state, &initialStatus); err != nil {
		t.Fatal(err)
	}
	return count, state, initialStatus
}

// handoffCountObservations counts live observations matching exact content.
func handoffCountObservations(t *testing.T, db *sql.DB, content string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM observations WHERE content=? AND deleted_at IS NULL`, content).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// structuredSave decodes a success structuredContent payload.
func structuredSave(t *testing.T, result *mcp.CallToolResult) memorycontract.SaveStructured {
	t.Helper()
	if result == nil || result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}
	payload, ok := result.StructuredContent.(memorycontract.SaveStructured)
	if !ok {
		t.Fatalf("structuredContent = %#v, want memorycontract.SaveStructured", result.StructuredContent)
	}
	return payload
}

// structuredError decodes an isError structuredContent payload.
func structuredError(t *testing.T, result *mcp.CallToolResult) memorycontract.ErrorStructured {
	t.Helper()
	if result == nil || !result.IsError {
		t.Fatalf("expected isError result, got %+v", result)
	}
	payload, ok := result.StructuredContent.(memorycontract.ErrorStructured)
	if !ok {
		t.Fatalf("structuredContent = %#v, want memorycontract.ErrorStructured", result.StructuredContent)
	}
	return payload
}

// handoffArgs builds cortex_handoff tool arguments against the seeded
// preexisting session.
func handoffArgs(key, content string, relation map[string]any) map[string]any {
	args := map[string]any{
		"idempotency_key": key,
		"observation": map[string]any{
			"title":      "Durable handoff",
			"content":    content,
			"type":       "decision",
			"project":    "handoff-demo",
			"session_id": handoffSessionID,
			"tags":       []any{"handoff"},
			"confidence": 0.9,
		},
	}
	if relation != nil {
		args["relation"] = relation
	}
	return args
}

// TestCortexHandoffLocalLifecycle covers the local handoff contract end to end
// through handleHandoff: create commits observation + receipt on the UnitOfWork
// transaction, identical replay returns the same local ref, a differing payload
// under the same key conflicts without mutating anything, and the local
// namespace rejects public_id relation targets (REM-MCP-001, REM-HANDOFF-001/002).
func TestCortexHandoffLocalLifecycle(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)
	const scope = "local/project:handoff-demo"

	t.Run("created_commits_observation_and_receipt_exactly_once", func(t *testing.T) {
		result := callTool(t, handler, handoffArgs("k1", "payload one", nil))
		payload := structuredSave(t, result)
		if payload.Status != string(domain.WriteStatusCreated) {
			t.Fatalf("status = %q, want created", payload.Status)
		}
		if payload.ObservationRef.LocalID == nil || *payload.ObservationRef.LocalID <= 0 {
			t.Fatalf("observation_ref = %+v, want a positive local_id", payload.ObservationRef)
		}
		if payload.ObservationRef.PublicID != nil {
			t.Fatalf("local namespace must return local_id only, got public_id %q", *payload.ObservationRef.PublicID)
		}
		if count, state, initial := handoffReceiptRow(t, db, scope, "k1"); count != 1 || state != "committed" || initial != "created" {
			t.Fatalf("receipt = (count=%d state=%s initial=%s), want one committed/created receipt", count, state, initial)
		}
		if got := handoffCountObservations(t, db, "payload one"); got != 1 {
			t.Fatalf("observations for payload one = %d, want 1", got)
		}
		if !strings.Contains(resultText(result), "Handoff recorded:") {
			t.Fatalf("text = %q, want handoff confirmation", resultText(result))
		}
	})

	t.Run("identical_replay_returns_same_local_ref_without_rematerialization", func(t *testing.T) {
		first := structuredSave(t, callTool(t, handler, handoffArgs("k2", "replay me", nil)))
		second := structuredSave(t, callTool(t, handler, handoffArgs("k2", "replay me", nil)))
		if first.Status != string(domain.WriteStatusCreated) || second.Status != string(domain.WriteStatusReplayed) {
			t.Fatalf("statuses = %q/%q, want created/replayed", first.Status, second.Status)
		}
		if second.ObservationRef.LocalID == nil || first.ObservationRef.LocalID == nil ||
			*second.ObservationRef.LocalID != *first.ObservationRef.LocalID {
			t.Fatalf("refs = %+v -> %+v, want the same local_id", first.ObservationRef, second.ObservationRef)
		}
		if count, state, _ := handoffReceiptRow(t, db, scope, "k2"); count != 1 || state != "committed" {
			t.Fatalf("receipt after replay = (count=%d state=%s), want still one committed receipt", count, state)
		}
		if got := handoffCountObservations(t, db, "replay me"); got != 1 {
			t.Fatalf("observations after replay = %d, want 1 (no re-materialization)", got)
		}
	})

	t.Run("same_key_different_payload_conflicts_and_mutates_nothing", func(t *testing.T) {
		result := callTool(t, handler, handoffArgs("k1", "payload two", nil))
		payload := structuredError(t, result)
		if payload.Error.Code != memorycontract.CodeConflict {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeConflict)
		}
		if count, state, initial := handoffReceiptRow(t, db, scope, "k1"); count != 1 || state != "committed" || initial != "created" {
			t.Fatalf("receipt after conflict = (count=%d state=%s initial=%s), want original receipt untouched", count, state, initial)
		}
		if got := handoffCountObservations(t, db, "payload two"); got != 0 {
			t.Fatalf("conflicting payload materialized %d observations, want 0", got)
		}
	})

	t.Run("public_id_relation_target_rejected_in_local_namespace", func(t *testing.T) {
		result := callTool(t, handler, handoffArgs("k3", "public target", map[string]any{
			"target": map[string]any{"public_id": "0b6a2d64-6c50-49da-b0e4-1a3d11b17a0c"},
			"type":   "references",
		}))
		payload := structuredError(t, result)
		if payload.Error.Code != memorycontract.CodeValidation {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
		}
		if !strings.Contains(payload.Error.Message, "local_id") {
			t.Fatalf("message = %q, want local-namespace rejection", payload.Error.Message)
		}
		if count, _, _ := handoffReceiptRow(t, db, scope, "k3"); count != 0 {
			t.Fatalf("rejected request left %d receipts, want 0", count)
		}
	})

	t.Run("local_id_relation_target_creates_edge_atomically", func(t *testing.T) {
		target := structuredSave(t, callTool(t, handleSave(stores), map[string]any{
			"title": "Handoff target", "content": "relation target body", "project": "handoff-demo",
		}))
		targetID := target.ObservationRef.LocalID

		result := callTool(t, handler, handoffArgs("k4", "with relation", map[string]any{
			"target": map[string]any{"local_id": float64(*targetID)},
			"type":   "references",
		}))
		payload := structuredSave(t, result)
		newID := payload.ObservationRef.LocalID
		if newID == nil || *newID == *targetID {
			t.Fatalf("handoff ref = %+v, want a new observation distinct from target %d", payload.ObservationRef, *targetID)
		}
		var edges int
		if err := db.QueryRow(
			`SELECT count(*) FROM edges WHERE from_obs_id=? AND to_obs_id=? AND relation_type='references'`,
			*newID, *targetID,
		).Scan(&edges); err != nil {
			t.Fatal(err)
		}
		if edges != 1 {
			t.Fatalf("edges = %d, want exactly 1 relation edge", edges)
		}
	})

	t.Run("unwired_bundle_reports_unavailable", func(t *testing.T) {
		result := callTool(t, handleHandoff(&Stores{}), handoffArgs("k5", "no bundle", nil))
		payload := structuredError(t, result)
		if payload.Error.Code != memorycontract.CodeUnavailable {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeUnavailable)
		}
	})

	t.Run("missing_key_reports_validation", func(t *testing.T) {
		args := handoffArgs("", "no key", nil)
		payload := structuredError(t, callTool(t, handler, args))
		if payload.Error.Code != memorycontract.CodeValidation {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
		}
	})

	t.Run("missing_session_id_fails_closed_before_any_mutation", func(t *testing.T) {
		args := handoffArgs("k6", "no session provided", nil)
		delete(args["observation"].(map[string]any), "session_id")
		sessionsBefore := handoffCountSessions(t, db)
		payload := structuredError(t, callTool(t, handler, args))
		if payload.Error.Code != memorycontract.CodeValidation {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
		}
		if !strings.Contains(payload.Error.Message, "existing session") {
			t.Fatalf("message = %q, want preexisting-session requirement", payload.Error.Message)
		}
		if count, _, _ := handoffReceiptRow(t, db, scope, "k6"); count != 0 {
			t.Fatalf("rejected handoff left %d receipts, want 0", count)
		}
		if got := handoffCountObservations(t, db, "no session provided"); got != 0 {
			t.Fatalf("rejected handoff materialized %d observations, want 0", got)
		}
		if sessions := handoffCountSessions(t, db); sessions != sessionsBefore {
			t.Fatalf("sessions = %d, want unchanged %d (no orphan creation outside the atomic unit)", sessions, sessionsBefore)
		}
	})

	t.Run("nonexistent_session_id_fails_closed_without_orphan", func(t *testing.T) {
		args := handoffArgs("k7", "ghost session", nil)
		args["observation"].(map[string]any)["session_id"] = "ghost-session"
		sessionsBefore := handoffCountSessions(t, db)
		payload := structuredError(t, callTool(t, handler, args))
		if payload.Error.Code != memorycontract.CodeValidation {
			t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
		}
		if !strings.Contains(payload.Error.Message, "existing session") {
			t.Fatalf("message = %q, want preexisting-session requirement", payload.Error.Message)
		}
		if count, _, _ := handoffReceiptRow(t, db, scope, "k7"); count != 0 {
			t.Fatalf("rejected handoff left %d receipts, want 0", count)
		}
		if got := handoffCountObservations(t, db, "ghost session"); got != 0 {
			t.Fatalf("rejected handoff materialized %d observations, want 0", got)
		}
		if sessions := handoffCountSessions(t, db); sessions != sessionsBefore {
			t.Fatalf("sessions = %d, want unchanged %d (no ghost session row created)", sessions, sessionsBefore)
		}
		var ghost int
		if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE id=?`, "ghost-session").Scan(&ghost); err != nil {
			t.Fatal(err)
		}
		if ghost != 0 {
			t.Fatalf("ghost session row was created (%d rows)", ghost)
		}
	})
}

// handoffCountSessions counts session rows (orphan detection).
func handoffCountSessions(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// TestCortexHandoffLocalIDValidation pins the relation target lowering: every
// non-finite, fractional, zero, negative, or out-of-int64-range local_id is
// rejected as validation BEFORE any conversion or persistence.
func TestCortexHandoffLocalIDValidation(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)

	for _, v := range []float64{
		1.5, -1.5, // fractional
		math.NaN(),   // not-a-number
		math.Inf(1),  // positive infinity
		math.Inf(-1), // negative infinity
		0, -3,        // non-positive integers
		9.223372036854776e18, // 2^63: first float above MaxInt64
		1e300,                // absurdly large finite
	} {
		args := handoffArgs(fmt.Sprintf("lid-%v", v), "local id probe", map[string]any{
			"target": map[string]any{"local_id": v},
			"type":   "references",
		})
		payload := structuredError(t, callTool(t, handler, args))
		if payload.Error.Code != memorycontract.CodeValidation {
			t.Fatalf("local_id %v: error code = %q, want %q", v, payload.Error.Code, memorycontract.CodeValidation)
		}
		if !strings.Contains(payload.Error.Message, "positive integer") {
			t.Fatalf("local_id %v: message = %q, want positive-integer rejection", v, payload.Error.Message)
		}
	}
	if got := handoffCountObservations(t, db, "local id probe"); got != 0 {
		t.Fatalf("rejected probes materialized %d observations, want 0", got)
	}
}

// TestCortexHandoffRelationArgumentContract pins the relation argument shape
// on the local runtime: a present-but-non-object relation is validation
// (never silently omitted) and the target must set exactly one namespace —
// both or neither fail before any persistence (review R7 fix 2).
func TestCortexHandoffRelationArgumentContract(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)

	cases := []struct {
		name     string
		relation any
		message  string
	}{
		{"relation not an object", "references", "relation must be an object"},
		{"target with both namespaces", map[string]any{
			"target": map[string]any{"local_id": float64(3), "public_id": "00000000-0000-0000-0000-0000000000ab"},
			"type":   "references",
		}, "exactly one of local_id or public_id"},
		{"target with neither namespace", map[string]any{
			"target": map[string]any{},
			"type":   "references",
		}, "exactly one of local_id or public_id"},
		{"local_id not a number", map[string]any{
			"target": map[string]any{"local_id": "7"},
			"type":   "references",
		}, "positive integer"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := handoffArgs("rel-contract-"+tc.name, "relation contract probe", nil)
			args["relation"] = tc.relation
			payload := structuredError(t, callTool(t, handler, args))
			if payload.Error.Code != memorycontract.CodeValidation {
				t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeValidation)
			}
			if !strings.Contains(payload.Error.Message, tc.message) {
				t.Fatalf("message = %q, want it to contain %q", payload.Error.Message, tc.message)
			}
		})
	}
	if got := handoffCountObservations(t, db, "relation contract probe"); got != 0 {
		t.Fatalf("rejected relation shapes materialized %d observations, want 0", got)
	}
	var receipts int
	if err := db.QueryRow(`SELECT count(*) FROM handoff_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatalf("rejected relation shapes left %d receipts, want 0", receipts)
	}
}

// TestCortexSaveProductionWiringPropagatesStatusFromTransaction runs handleSave
// on a production-like bundle (entity participant + UnitOfWork wired) and
// asserts the structured status is the REAL classification decided inside the
// committing transaction — topic upsert reports updated, dedup replay reports
// replayed with the frozen legacy text bytes (REM-SAVE-001).
func TestCortexSaveProductionWiringPropagatesStatusFromTransaction(t *testing.T) {
	stores, db := setupHandoffStores(t)
	stores.Entities = entitystore.NewStore(db) // flips save onto the UoW production path
	handler := handleSave(stores)

	// Topic-key upsert: first save creates, second save updates the same row.
	first := callTool(t, handler, map[string]any{
		"title": "Prod wiring auth", "content": "session cookies", "type": "manual",
		"project": "prodwired", "session_id": handoffSessionID, "topic_key": "architecture/auth",
	})
	if got, want := resultText(first), `Memory saved: "Prod wiring auth" (manual)`; got != want {
		t.Fatalf("production topic save text:\n got=%q\nwant=%q", got, want)
	}
	firstPayload := structuredSave(t, first)
	if firstPayload.Status != string(domain.WriteStatusCreated) {
		t.Fatalf("first status = %q, want created", firstPayload.Status)
	}

	second := callTool(t, handler, map[string]any{
		"title": "Prod wiring auth v2", "content": "jwt with refresh", "type": "manual",
		"project": "prodwired", "session_id": handoffSessionID, "topic_key": "architecture/auth",
	})
	if got, want := resultText(second), `Memory saved: "Prod wiring auth v2" (manual)`; got != want {
		t.Fatalf("production upsert text:\n got=%q\nwant=%q", got, want)
	}
	secondPayload := structuredSave(t, second)
	if secondPayload.Status != string(domain.WriteStatusUpdated) {
		t.Fatalf("upsert status = %q, want %q (propagated from the committing transaction)", secondPayload.Status, domain.WriteStatusUpdated)
	}
	if firstPayload.ObservationRef.LocalID == nil || secondPayload.ObservationRef.LocalID == nil ||
		*firstPayload.ObservationRef.LocalID != *secondPayload.ObservationRef.LocalID {
		t.Fatalf("topic upsert refs = %+v -> %+v, want the same local_id", firstPayload.ObservationRef, secondPayload.ObservationRef)
	}

	// Dedup replay on the production path: identical manual re-save (no
	// topic_key) reports replayed with the frozen duplicate-skipped bytes.
	dedupFirst := callTool(t, handler, map[string]any{
		"title": "Prod wiring dedup", "content": "identical body", "type": "manual",
		"project": "prodwired", "session_id": handoffSessionID,
	})
	dedupPayload := structuredSave(t, dedupFirst)
	if dedupPayload.Status != string(domain.WriteStatusCreated) {
		t.Fatalf("dedup seed status = %q, want created", dedupPayload.Status)
	}
	dedupSecond := callTool(t, handler, map[string]any{
		"title": "Prod wiring dedup", "content": "identical body", "type": "manual",
		"project": "prodwired", "session_id": handoffSessionID,
	})
	if got, want := resultText(dedupSecond), `Memory saved: "Prod wiring dedup" (manual) [duplicate skipped]`; got != want {
		t.Fatalf("production dedup text:\n got=%q\nwant=%q", got, want)
	}
	replayed := structuredSave(t, dedupSecond)
	if replayed.Status != string(domain.WriteStatusReplayed) {
		t.Fatalf("dedup status = %q, want %q (propagated from the committing transaction)", replayed.Status, domain.WriteStatusReplayed)
	}
	if replayed.ObservationRef.LocalID == nil || dedupPayload.ObservationRef.LocalID == nil ||
		*replayed.ObservationRef.LocalID != *dedupPayload.ObservationRef.LocalID {
		t.Fatalf("dedup refs = %+v -> %+v, want the same local_id", dedupPayload.ObservationRef, replayed.ObservationRef)
	}
}

// TestCortexSaveFailureTextUsesConstantRedactedMessageOnBothChannels proves
// the fallback text and the structuredContent of a failed cortex_save carry
// the SAME constant, redacted classification. A BEFORE-INSERT trigger whose
// RAISE(ABORT, ...) message embeds SQL/path/token canary material injects a
// real driver failure through the production save path; both channels must
// stay constant and canary-free even though the raw error provably carries
// the canary (REM-SAVE-001, REM-MCP-001).
func TestCortexSaveFailureTextUsesConstantRedactedMessageOnBothChannels(t *testing.T) {
	stores, db := setupHandoffStores(t)

	const canary = "constraint failed: token=SECRET-canary-77a path=C:\\leak\\probe SQLSTATE=X"
	if _, err := db.Exec(`CREATE TRIGGER observations_leak_canary BEFORE INSERT ON observations
		BEGIN SELECT RAISE(ABORT, '` + canary + `'); END`); err != nil {
		t.Fatalf("install canary trigger: %v", err)
	}

	// Sanity: the raw channel really does carry the canary — the injection is
	// proven live, so its absence below is meaningful.
	_, rawErr := db.Exec(`INSERT INTO observations (session_id, type, title, content) VALUES (?, 'manual', 'raw', 'raw')`, handoffSessionID)
	if rawErr == nil || !strings.Contains(rawErr.Error(), "SECRET-canary-77a") {
		t.Fatalf("canary injection failed: raw error = %v", rawErr)
	}

	result := callTool(t, handleSave(stores), map[string]any{
		"title": "Leak probe", "content": "probe body", "type": "manual",
		"project": "handoff-demo", "session_id": handoffSessionID,
	})

	payload := structuredError(t, result)
	if payload.Error.Code != memorycontract.CodePersistence {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodePersistence)
	}
	const wantMessage = "write could not be persisted"
	if payload.Error.Message != wantMessage {
		t.Fatalf("structured message = %q, want the constant %q", payload.Error.Message, wantMessage)
	}

	text := resultText(result)
	if want := "Failed to save: " + wantMessage; text != want {
		t.Fatalf("fallback text = %q, want exactly %q (the same constant as structuredContent)", text, want)
	}

	for _, channel := range []string{text, payload.Error.Message} {
		for _, probe := range []string{"SECRET-canary-77a", "C:\\leak", "SQLSTATE", "RAISE", "trigger", "INSERT"} {
			if strings.Contains(channel, probe) {
				t.Fatalf("channel leaked raw error detail %q: %q", probe, channel)
			}
		}
	}
}

// TestCortexSaveLegacyTextFrozenWithStructuredAdditive pins the byte-exact
// legacy text surface of cortex_save while asserting the structured payload is
// purely additive: fresh save, dedup replay, and topic upsert (REM-SAVE-001).
func TestCortexSaveLegacyTextFrozenWithStructuredAdditive(t *testing.T) {
	stores := setupTestStores(t)
	handler := handleSave(stores)

	title, content := "Frozen text check", "frozen body"
	first := callTool(t, handler, map[string]any{"title": title, "content": content, "type": "manual", "project": "frozen"})

	// Legacy bytes: suggestion line included exactly as before (the suggestion
	// helper is deterministic and unchanged), no normalization warning for a
	// clean project name, no similarity warning (exact project matches are
	// skipped by FindSimilar).
	wantFirst := fmt.Sprintf("Memory saved: %q (%s)\nSuggested topic_key: %s",
		title, "manual", suggestTopicKey("manual", title, content))
	if got := resultText(first); got != wantFirst {
		t.Fatalf("legacy save text drifted:\n got=%q\nwant=%q", got, wantFirst)
	}
	firstPayload := structuredSave(t, first)
	if firstPayload.Status != string(domain.WriteStatusCreated) ||
		firstPayload.ObservationRef.LocalID == nil || *firstPayload.ObservationRef.LocalID <= 0 {
		t.Fatalf("structured payload = %+v, want created with positive local_id", firstPayload)
	}

	// Identical re-save (manual type): dedup replay with the exact frozen
	// duplicate-skipped suffix and no suggestion line.
	second := callTool(t, handler, map[string]any{"title": title, "content": content, "type": "manual", "project": "frozen"})
	wantSecond := fmt.Sprintf("Memory saved: %q (%s) [duplicate skipped]", title, "manual")
	if got := resultText(second); got != wantSecond {
		t.Fatalf("legacy dedup text drifted:\n got=%q\nwant=%q", got, wantSecond)
	}
	secondPayload := structuredSave(t, second)
	if secondPayload.Status != string(domain.WriteStatusReplayed) ||
		secondPayload.ObservationRef.LocalID == nil ||
		*secondPayload.ObservationRef.LocalID != *firstPayload.ObservationRef.LocalID {
		t.Fatalf("dedup payload = %+v, want replayed with the same local_id", secondPayload)
	}

	// Topic-key upsert: exact legacy text without the suggestion line, and the
	// structured classification reports the in-place update.
	upsertFirst := callTool(t, handler, map[string]any{
		"title": "Auth architecture v1", "content": "session cookies", "type": "manual", "project": "frozen", "topic_key": "architecture/auth",
	})
	if got, want := resultText(upsertFirst), `Memory saved: "Auth architecture v1" (manual)`; got != want {
		t.Fatalf("topic save text drifted:\n got=%q\nwant=%q", got, want)
	}
	upsertSecond := callTool(t, handler, map[string]any{
		"title": "Auth architecture v2", "content": "jwt with refresh", "type": "manual", "project": "frozen", "topic_key": "architecture/auth",
	})
	if got, want := resultText(upsertSecond), `Memory saved: "Auth architecture v2" (manual)`; got != want {
		t.Fatalf("topic upsert text drifted:\n got=%q\nwant=%q", got, want)
	}
	upsertPayload := structuredSave(t, upsertSecond)
	if upsertPayload.Status != string(domain.WriteStatusUpdated) {
		t.Fatalf("upsert status = %q, want %q", upsertPayload.Status, domain.WriteStatusUpdated)
	}
}
