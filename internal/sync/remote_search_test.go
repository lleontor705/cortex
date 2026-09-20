package sync_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/domain"
	cortsync "github.com/lleontor705/cortex/v2/internal/sync"
)

func TestNewRemoteSearchClient_Validation(t *testing.T) {
	// Plain HTTP to non-loopback must be rejected by transport policy
	_, err := cortsync.NewRemoteSearchClient("http://remote-server.internal:8080", "test-token", 5*time.Second)
	if err == nil {
		t.Error("expected error for plain HTTP non-loopback destination, got nil")
	}

	// Empty token must be rejected
	_, err = cortsync.NewRemoteSearchClient("https://cortex.example.com", "", 5*time.Second)
	if err == nil {
		t.Error("expected error for empty token, got nil")
	}

	// Invalid URL must be rejected
	_, err = cortsync.NewRemoteSearchClient("://bad-url", "test-token", 5*time.Second)
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}

	// Loopback HTTP is valid
	client, err := cortsync.NewRemoteSearchClient("http://127.0.0.1:9090", "test-token", 5*time.Second)
	if err != nil {
		t.Fatalf("expected loopback HTTP to succeed, got %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	// HTTPS is valid
	clientHTTPS, err := cortsync.NewRemoteSearchClient("https://api.cortex.example.com", "test-token", 5*time.Second)
	if err != nil {
		t.Fatalf("expected HTTPS to succeed, got %v", err)
	}
	if clientHTTPS == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestRemoteSearchClient_SearchHybrid_Success(t *testing.T) {
	expectedToken := "cortex_sec_tok_123"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify method and path
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/search/hybrid" {
			t.Errorf("expected path /api/search/hybrid, got %s", r.URL.Path)
		}

		// Verify Authorization header
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+expectedToken {
			t.Errorf("expected auth Bearer %s, got %s", expectedToken, auth)
		}

		// Verify query params
		q := r.URL.Query()
		if q.Get("q") != "concurrency deadlock" {
			t.Errorf("expected q 'concurrency deadlock', got %q", q.Get("q"))
		}
		if q.Get("project") != "cortex" {
			t.Errorf("expected project 'cortex', got %q", q.Get("project"))
		}
		if q.Get("type") != "bugfix" {
			t.Errorf("expected type 'bugfix', got %q", q.Get("type"))
		}
		if q.Get("scope") != "team" {
			t.Errorf("expected scope 'team', got %q", q.Get("scope"))
		}
		if q.Get("limit") != "5" {
			t.Errorf("expected limit '5', got %q", q.Get("limit"))
		}

		// Return JSON array with mixed ID types (integer and string PublicID)
		resp := []map[string]any{
			{
				"id":         42,
				"title":      "Mutex deadlock in cache layer",
				"content":    "Fixed recursive mutex deadlock by restructuring lock acquisition",
				"type":       "bugfix",
				"project":    "cortex",
				"scope":      "team",
				"confidence": 0.95,
				"rank":       0.88,
				"created_at": "2026-09-10T12:00:00Z",
				"score_breakdown": map[string]any{
					"strategy": "hybrid",
				},
			},
			{
				"id":         "77777777-7777-7777-7777-777777777777",
				"title":      "Go routine leak in watcher",
				"content":    "Added context cancellation check in watcher loop",
				"type":       "bugfix",
				"project":    "cortex",
				"scope":      "team",
				"confidence": 1.0,
				"rank":       0.75,
				"created_at": "2026-09-11T14:30:00Z",
			},
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	client, err := cortsync.NewRemoteSearchClient(ts.URL, expectedToken, 5*time.Second)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	results, err := client.SearchHybrid(context.Background(), "concurrency deadlock", domain.SearchOptions{
		Project: "cortex",
		Type:    "bugfix",
		Scope:   "team",
		Limit:   5,
	})
	if err != nil {
		t.Fatalf("SearchHybrid failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Verify first result (integer ID)
	r0 := results[0]
	if r0.ID != 42 {
		t.Errorf("r0.ID = %d, want 42", r0.ID)
	}
	if r0.Title != "Mutex deadlock in cache layer" {
		t.Errorf("r0.Title = %q", r0.Title)
	}
	if r0.Rank != 0.88 {
		t.Errorf("r0.Rank = %f, want 0.88", r0.Rank)
	}
	if r0.ScoreBreakdown.Strategy != "hybrid" {
		t.Errorf("r0.ScoreBreakdown.Strategy = %q, want 'hybrid'", r0.ScoreBreakdown.Strategy)
	}

	// Verify second result (UUID string ID mapped to PublicID)
	r1 := results[1]
	if r1.PublicID != "77777777-7777-7777-7777-777777777777" {
		t.Errorf("r1.PublicID = %q", r1.PublicID)
	}
	if r1.Title != "Go routine leak in watcher" {
		t.Errorf("r1.Title = %q", r1.Title)
	}
	if r1.Rank != 0.75 {
		t.Errorf("r1.Rank = %f, want 0.75", r1.Rank)
	}
}

func TestRemoteSearchClient_SearchHybrid_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal database failure", http.StatusInternalServerError)
	}))
	defer ts.Close()

	client, err := cortsync.NewRemoteSearchClient(ts.URL, "tok", 5*time.Second)
	if err != nil {
		t.Fatalf("failed to construct client: %v", err)
	}

	results, err := client.SearchHybrid(context.Background(), "query", domain.SearchOptions{})
	if err == nil {
		t.Fatal("expected error on 500 response, got nil")
	}
	if results != nil {
		t.Errorf("expected nil results on error, got %v", results)
	}
}

func TestRemoteSearchClient_SearchHybrid_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	// Short timeout of 20ms
	client, err := cortsync.NewRemoteSearchClient(ts.URL, "tok", 20*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to construct client: %v", err)
	}

	_, err = client.SearchHybrid(context.Background(), "slow query", domain.SearchOptions{})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestRemoteSearchClient_SearchHybrid_Empty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	client, err := cortsync.NewRemoteSearchClient(ts.URL, "tok", 5*time.Second)
	if err != nil {
		t.Fatalf("failed to construct client: %v", err)
	}

	results, err := client.SearchHybrid(context.Background(), "nonexistent", domain.SearchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
