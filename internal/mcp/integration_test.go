package mcp

import (
	"encoding/json"
	"strings"
	"testing"
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
