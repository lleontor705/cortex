package http

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestHTTPPrivacy_CreateObservationRedacted(t *testing.T) {
	srv := setupTestServer(t)
	ctx := context.Background()
	_ = srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "demo", Directory: "."})

	const canaryT = "canary_http_title_1122"
	const canaryC = "canary_http_content_3344"

	reqBody := `{"session_id":"s1","title":"HTTP <private>` + canaryT + `</private> Title","content":"Body <private>` + canaryC + `</private>","project":"demo","type":"manual"}`
	req := httptest.NewRequest("POST", "/api/observations", bytes.NewBufferString(reqBody))
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	resBody := w.Body.String()
	if strings.Contains(resBody, canaryT) || strings.Contains(resBody, canaryC) {
		t.Fatalf("response leaked canary: %s", resBody)
	}
	if !strings.Contains(resBody, "[REDACTED]") {
		t.Fatalf("response missing [REDACTED]: %s", resBody)
	}

	obsList, err := srv.deps.Observations.List(ctx, domain.ObservationFilter{Project: "demo"})
	if err != nil || len(obsList) != 1 {
		t.Fatalf("expected 1 observation, got %v (err=%v)", len(obsList), err)
	}
	if strings.Contains(obsList[0].Title, canaryT) || strings.Contains(obsList[0].Content, canaryC) {
		t.Fatalf("persisted canary leak: title=%q content=%q", obsList[0].Title, obsList[0].Content)
	}
	if obsList[0].Title != "HTTP [REDACTED] Title" || obsList[0].Content != "Body [REDACTED]" {
		t.Fatalf("unexpected persisted values: title=%q content=%q", obsList[0].Title, obsList[0].Content)
	}
}

func TestHTTPPrivacy_CreateObservationRejectionZeroEffects(t *testing.T) {
	const canary = "canary_http_malformed_5566"

	cases := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"unclosed title", `{"session_id":"s1","title":"Title <private>` + canary + `","content":"body","project":"p1"}`, "privacy_invalid_marker"},
		{"stray closing title", `{"session_id":"s1","title":"Title </private>","content":"body","project":"p1"}`, "privacy_invalid_marker"},
		{"unclosed content", `{"session_id":"s1","title":"Title","content":"Body <private>` + canary + `","project":"p1"}`, "privacy_invalid_marker"},
		{"nested content", `{"session_id":"s1","title":"Title","content":"<private>a <private>b</private></private>","project":"p1"}`, "privacy_invalid_marker"},
		{"pure private title", `{"session_id":"s1","title":"<private>` + canary + `</private>","content":"body","project":"p1"}`, "privacy_required_empty"},
		{"pure private content", `{"session_id":"s1","title":"Title","content":"<private>` + canary + `</private>","project":"p1"}`, "privacy_required_empty"},
		{"marker in project", `{"session_id":"s1","title":"Title","content":"body","project":"<private>` + canary + `</private>"}`, "privacy_invalid_marker"},
		{"marker in topic", `{"session_id":"s1","title":"Title","content":"body","project":"p1","topic_key":"<private>` + canary + `</private>"}`, "privacy_invalid_marker"},
		{"marker in scope", `{"session_id":"s1","title":"Title","content":"body","project":"p1","scope":"<private>` + canary + `</private>"}`, "privacy_invalid_marker"},
		{"marker in type", `{"session_id":"s1","title":"Title","content":"body","project":"p1","type":"<private>` + canary + `</private>"}`, "privacy_invalid_marker"},
		{"marker in source", `{"session_id":"s1","title":"Title","content":"body","project":"p1","source":"<private>` + canary + `</private>"}`, "privacy_invalid_marker"},
		{"marker in session_id", `{"session_id":"<private>` + canary + `</private>","title":"Title","content":"body","project":"p1"}`, "privacy_invalid_marker"},
		{"marker in tags", `{"session_id":"s1","title":"Title","content":"body","project":"p1","tags":["<private>` + canary + `</private>"]}`, "privacy_invalid_marker"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupTestServer(t)
			ctx := context.Background()
			_ = srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "p1", Directory: "."})
			w := httptest.NewRecorder()
			srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/observations", bytes.NewBufferString(tc.body)))
			if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), canary) || !strings.Contains(w.Body.String(), tc.wantCode) {
				t.Fatalf("unexpected response: code=%d body=%s", w.Code, w.Body.String())
			}
			if obsList, _ := srv.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 10}); len(obsList) != 0 {
				t.Fatalf("expected 0 observations after rejection, got %d", len(obsList))
			}
		})
	}
}

func TestHTTPPrivacy_CreatePromptRejectionZeroEffects(t *testing.T) {
	const canary = "canary_http_prompt_malformed_7788"
	srv := setupTestServer(t)
	_ = srv.deps.Sessions.Create(context.Background(), &domain.Session{ID: "s1", Project: "p1", Directory: "."})
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/prompts", bytes.NewBufferString(`{"session_id":"s1","project":"p1","content":"Prompt <private>`+canary+`"}`)))
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), canary) {
		t.Fatalf("expected 400 Bad Request without canary leak, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHTTPPrivacy_CreateEdgeRejectionZeroEffects(t *testing.T) {
	const canary = "canary_http_edge_malformed_9911"
	srv := setupTestServer(t)
	ctx := context.Background()
	_ = srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s1", Project: "p1", Directory: "."})
	obsA := &domain.Observation{SessionID: "s1", Title: "A", Content: "Content A", Type: "manual", Project: "p1"}
	obsB := &domain.Observation{SessionID: "s1", Title: "B", Content: "Content B", Type: "manual", Project: "p1"}
	_ = srv.deps.Observations.Save(ctx, obsA)
	_ = srv.deps.Observations.Save(ctx, obsB)

	edgeBody := fmt.Sprintf(`{"from_obs_id":%d,"to_obs_id":%d,"relation_type":"references","reasoning":"Why <private>%s"}`, obsA.ID, obsB.ID, canary)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/graph/edges", bytes.NewBufferString(edgeBody)))
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), canary) {
		t.Fatalf("expected 400 Bad Request without canary leak, got %d: %s", w.Code, w.Body.String())
	}
	if count, _ := srv.deps.Graph.CountAllEdges(ctx); count != 0 {
		t.Fatalf("expected 0 edges after rejection, got %d", count)
	}
}

func TestHTTPPrivacy_CreateSessionRejectionZeroEffects(t *testing.T) {
	const canary = "canary_http_sess_malformed_2233"
	srv := setupTestServer(t)
	w := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/sessions", bytes.NewBufferString(`{"id":"s-test","project":"<private>`+canary+`</private>","directory":"."}`)))
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), canary) {
		t.Fatalf("expected 400 Bad Request without canary leak, got %d: %s", w.Code, w.Body.String())
	}
	if sessions, _ := srv.deps.Sessions.List(context.Background(), ""); len(sessions) != 0 {
		t.Fatalf("expected 0 sessions after rejection, got %d", len(sessions))
	}
}
