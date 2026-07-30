// Package http — additional behavioral coverage for the REST server.
//
// This file is the G4 authoring lane for change coverage-70-and-lint (issue #46).
// It is reserved exclusively to this group; existing server_test.go remains read-only.
//
// Coverage focuses on observable HTTP contracts: request validation, error
// mapping, persistence effects, query-parameter handling, graph/scoring/import
// behavior, auth, and the small deterministic helpers (queryInt, truncateHTTP,
// mapDomainError, Addr, Shutdown). All tests are in-process (httptest) and use
// isolated per-test SQLite stores; no network listeners or external services.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/internal/domain"
	sqlitestore "github.com/lleontor705/cortex/internal/store/sqlite"
)

// --- test helpers -----------------------------------------------------------

// doJSON issues a request whose body is the JSON encoding of body (nil => no body).
// It returns the recorded response without performing any assertions.
func doJSON(t *testing.T, h http.Handler, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode request body: %v", err)
		}
		r = &buf
	}
	return doRaw(t, h, method, target, r, headers)
}

// doRaw issues a request with an optional pre-built body reader.
func doRaw(t *testing.T, h http.Handler, method, target string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rawBody builds a request body reader from a literal string.
func rawBody(s string) *bytes.Reader { return bytes.NewReader([]byte(s)) }

// decodeJSON decodes the recorder body into out, failing the test with context on error.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, out any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
}

// assertStatus fails with the response body attached when the status mismatches.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status: want %d, got %d (body=%q)", want, rec.Code, rec.Body.String())
	}
}

// createSession inserts a session directly through the store for fixture setup.
func createSession(t *testing.T, srv *Server, id, project string) {
	t.Helper()
	if err := srv.deps.Sessions.Create(context.Background(), &domain.Session{
		ID: id, Project: project, Directory: ".",
	}); err != nil {
		t.Fatalf("seed session %q: %v", id, err)
	}
}

// createObservation inserts an observation directly through the store and returns its ID.
func createObservation(t *testing.T, srv *Server, title, content, obsType, project, scope string) int64 {
	t.Helper()
	obs := &domain.Observation{
		SessionID: "s1", Title: title, Content: content,
		Type: obsType, Project: project, Scope: scope,
	}
	if err := srv.deps.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("seed observation %q: %v", title, err)
	}
	return obs.ID
}

// --- malformed / oversized bodies ------------------------------------------

func TestCreateObservation_MalformedJSON(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/observations", rawBody("{not json"), nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON' in body, got %q", rec.Body.String())
	}
}

func TestCreateObservation_OversizedBody(t *testing.T) {
	srv := setupTestServer(t)
	// Body must exceed maxRequestBodySize (1 MiB) so the LimitReader truncates
	// the JSON and the decoder fails before any store call.
	huge := strings.Repeat("a", 1<<20+256)
	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/observations",
		struct {
			Content string `json:"content"`
		}{Content: huge}, nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON' for oversized body, got %q", rec.Body.String())
	}
}

func TestCreateSession_MalformedJSON(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/sessions", rawBody("``"), nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON', got %q", rec.Body.String())
	}
}

func TestEndSession_MalformedJSON(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/sessions/s1/end", rawBody("nope"), nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON', got %q", rec.Body.String())
	}
}

func TestCreateEdge_MalformedJSON(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/graph/edges", rawBody("{"), nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON', got %q", rec.Body.String())
	}
}

func TestImport_MalformedJSON(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/import", rawBody("garbage"), nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("expected 'invalid JSON', got %q", rec.Body.String())
	}
}

// --- invalid path ids -------------------------------------------------------

func TestGetObservation_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/abc", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "invalid id") {
		t.Fatalf("expected 'invalid id', got %q", rec.Body.String())
	}
}

func TestUpdateObservation_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doJSON(t, srv.httpServer.Handler, "PUT", "/api/observations/xyz",
		domain.Observation{Title: "x", Content: "y"}, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestDeleteObservation_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "DELETE", "/api/observations/zzz", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestArchiveObservation_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/observations/nope/archive", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestRevisions_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/bad/revisions", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestGetRelated_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/graph/foo/related", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestDeleteEdge_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "DELETE", "/api/graph/edges/bar", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestGetScore_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/scores/qux", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

func TestRecalculateScore_InvalidID(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/scores/NaN/recalculate", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
}

// --- observation lifecycle & persistence ------------------------------------

func TestCreateObservation_SuccessPersists(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")

	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/observations", domain.Observation{
		SessionID: "s1", Title: "Created via HTTP", Content: "persisted body",
		Type: "decision", Project: "demo", Scope: "project",
	}, nil)
	assertStatus(t, rec, http.StatusCreated)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("expected JSON content-type, got %q", ct)
	}

	var created domain.Observation
	decodeJSON(t, rec, &created)
	if created.ID == 0 {
		t.Fatal("expected non-zero assigned ID")
	}
	if created.Title != "Created via HTTP" {
		t.Fatalf("title roundtrip: got %q", created.Title)
	}

	// Persistence: a subsequent GET returns the stored title and content.
	get := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/"+itoa(created.ID), nil, nil)
	assertStatus(t, get, http.StatusOK)
	var fetched domain.Observation
	decodeJSON(t, get, &fetched)
	if fetched.Title != "Created via HTTP" || fetched.Content != "persisted body" {
		t.Fatalf("persisted observation mismatch: %+v", fetched)
	}
}

func TestGetObservation_NotFound(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/9999", nil, nil)
	assertStatus(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("expected 'not found' in body, got %q", rec.Body.String())
	}
}

func TestUpdateObservation_SuccessPersists(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	id := createObservation(t, srv, "Original", "original body", "manual", "demo", "project")

	rec := doJSON(t, srv.httpServer.Handler, "PUT", "/api/observations/"+itoa(id), domain.Observation{
		SessionID: "s1", Title: "Updated", Content: "updated body",
		Type: "manual", Project: "demo", Scope: "project",
	}, nil)
	assertStatus(t, rec, http.StatusOK)
	var updated domain.Observation
	decodeJSON(t, rec, &updated)
	if updated.Title != "Updated" {
		t.Fatalf("update response title: got %q", updated.Title)
	}
	if updated.ID != id {
		t.Fatalf("update must preserve path id: got %d want %d", updated.ID, id)
	}

	// Persistence: GET reflects the new title.
	get := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/"+itoa(id), nil, nil)
	assertStatus(t, get, http.StatusOK)
	var fetched domain.Observation
	decodeJSON(t, get, &fetched)
	if fetched.Title != "Updated" || fetched.Content != "updated body" {
		t.Fatalf("persisted update mismatch: %+v", fetched)
	}
}

func TestDeleteObservation_SuccessPersists(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	id := createObservation(t, srv, "Doomed", "to be deleted", "manual", "demo", "project")

	rec := doRaw(t, srv.httpServer.Handler, "DELETE", "/api/observations/"+itoa(id), nil, nil)
	assertStatus(t, rec, http.StatusOK)
	var body map[string]string
	decodeJSON(t, rec, &body)
	if body["status"] != "deleted" {
		t.Fatalf("expected status=deleted, got %q", rec.Body.String())
	}

	// Persistence: soft-deleted observations are no longer retrievable.
	get := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/"+itoa(id), nil, nil)
	assertStatus(t, get, http.StatusNotFound)
}

// --- list filters, limits, offsets ------------------------------------------

func TestListObservations_FiltersLimitOffset(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	createObservation(t, srv, "Demo decision", "c1", "decision", "demo", "project")
	createObservation(t, srv, "Demo pattern", "c2", "pattern", "demo", "project")
	createObservation(t, srv, "Other personal", "c3", "manual", "other", "personal")

	cases := []struct {
		name   string
		target string
		want   int
	}{
		{"project filter", "/api/observations?project=demo", 2},
		{"other project", "/api/observations?project=other", 1},
		{"type filter", "/api/observations?type=decision", 1},
		{"scope filter", "/api/observations?scope=personal", 1},
		{"limit cap", "/api/observations?limit=1", 1},
		{"limit plus offset within range", "/api/observations?limit=1&offset=1", 1},
		{"offset past end", "/api/observations?limit=1&offset=10", 0},
		{"default returns all", "/api/observations", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doRaw(t, srv.httpServer.Handler, "GET", tc.target, nil, nil)
			assertStatus(t, rec, http.StatusOK)
			var got []*domain.Observation
			decodeJSON(t, rec, &got)
			if len(got) != tc.want {
				t.Fatalf("len: want %d, got %d", tc.want, len(got))
			}
		})
	}
}

func TestListObservations_LimitClampedToMax(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	createObservation(t, srv, "A", "c1", "manual", "demo", "project")
	// A limit far above maxLimit (100) must be accepted without error and
	// clamped internally; with one record present it returns that one record.
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations?limit=99999", nil, nil)
	assertStatus(t, rec, http.StatusOK)
	var got []*domain.Observation
	decodeJSON(t, rec, &got)
	if len(got) != 1 {
		t.Fatalf("clamped limit should return the single record, got %d", len(got))
	}
}

// --- sessions lifecycle -----------------------------------------------------

func TestCreateSession_SuccessPersists(t *testing.T) {
	srv := setupTestServer(t)
	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/sessions", domain.Session{
		ID: "http-sess", Project: "demo", Directory: "/tmp/demo",
	}, nil)
	assertStatus(t, rec, http.StatusCreated)
	var created domain.Session
	decodeJSON(t, rec, &created)
	if created.ID != "http-sess" {
		t.Fatalf("session id roundtrip: got %q", created.ID)
	}

	// Persistence: List returns the created session.
	list := doRaw(t, srv.httpServer.Handler, "GET", "/api/sessions?project=demo", nil, nil)
	assertStatus(t, list, http.StatusOK)
	var sessions []*domain.Session
	decodeJSON(t, list, &sessions)
	var found bool
	for _, s := range sessions {
		if s.ID == "http-sess" {
			found = true
		}
	}
	if !found {
		t.Fatal("created session not present in list response")
	}
}

func TestListSessions_ByProject(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	createSession(t, srv, "s2", "demo")
	createSession(t, srv, "s3", "other")

	for _, tc := range []struct {
		target string
		want   int
	}{
		{"/api/sessions?project=demo", 2},
		{"/api/sessions?project=other", 1},
		{"/api/sessions?project=missing", 0},
		{"/api/sessions", 3},
	} {
		rec := doRaw(t, srv.httpServer.Handler, "GET", tc.target, nil, nil)
		assertStatus(t, rec, http.StatusOK)
		var got []*domain.Session
		decodeJSON(t, rec, &got)
		if len(got) != tc.want {
			t.Fatalf("%s: want %d, got %d", tc.target, tc.want, len(got))
		}
	}
}

func TestEndSession_SuccessPersistsAndIdempotentBlocked(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")

	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/sessions/s1/end",
		map[string]string{"summary": "wrapped up"}, nil)
	assertStatus(t, rec, http.StatusOK)
	var body map[string]string
	decodeJSON(t, rec, &body)
	if body["status"] != "ended" {
		t.Fatalf("expected status=ended, got %q", rec.Body.String())
	}

	// Persistence: the store now reports an ended session.
	sess, err := srv.deps.Sessions.GetByID(context.Background(), "s1")
	if err != nil {
		t.Fatalf("get session after end: %v", err)
	}
	if sess.EndedAt == nil {
		t.Fatal("expected EndedAt to be set after end request")
	}
	if sess.Summary != "wrapped up" {
		t.Fatalf("expected summary persisted, got %q", sess.Summary)
	}

	// Ending an already-ended session maps to 409 Conflict.
	again := doJSON(t, srv.httpServer.Handler, "POST", "/api/sessions/s1/end",
		map[string]string{"summary": "again"}, nil)
	assertStatus(t, again, http.StatusConflict)
}

func TestEndSession_NotFound(t *testing.T) {
	srv := setupTestServer(t)
	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/sessions/ghost/end",
		map[string]string{"summary": ""}, nil)
	assertStatus(t, rec, http.StatusNotFound)
}

// --- search filters, limits, queryInt --------------------------------------

func TestSearch_MissingQuery(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/search", nil, nil)
	assertStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "'q' is required") {
		t.Fatalf("expected 'q' is required, got %q", rec.Body.String())
	}
}

func TestSearch_TypeFilter(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	createObservation(t, srv, "Alpha token", "jwt token rotation", "decision", "demo", "project")
	createObservation(t, srv, "Beta caching layer", "caching layer strategy", "pattern", "demo", "project")

	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/search?q=layer&type=pattern&project=demo", nil, nil)
	assertStatus(t, rec, http.StatusOK)
	var results []domain.SearchResult
	decodeJSON(t, rec, &results)
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 pattern result, got %d", len(results))
	}
	if results[0].Title != "Beta caching layer" {
		t.Fatalf("unexpected result title: %q", results[0].Title)
	}
}

func TestSearchHybrid_LimitClamping(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	createObservation(t, srv, "Fusion one", "vector fusion content", "decision", "demo", "project")

	// queryInt returns the parsed value or the default; the handler then clamps
	// non-positive to 10 and >50 to 50. A valid limit of 1 must be honored.
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/search/hybrid?q=fusion&limit=1", nil, nil)
	assertStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Fusion one") {
		t.Fatalf("expected fusion result, got %q", rec.Body.String())
	}
}

// --- graph create/get/delete + error mapping -------------------------------

func TestCreateEdge_SuccessAndPersistence(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	a := createObservation(t, srv, "Source", "src", "manual", "demo", "project")
	b := createObservation(t, srv, "Target", "tgt", "manual", "demo", "project")

	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/graph/edges", domain.Edge{
		FromObsID: a, ToObsID: b, RelationType: domain.RelationReferences,
	}, nil)
	assertStatus(t, rec, http.StatusCreated)
	var edge domain.Edge
	decodeJSON(t, rec, &edge)
	if edge.ID == 0 {
		t.Fatal("expected non-zero edge id")
	}
	// Default weight is applied by the service when the request omits it.
	if edge.Weight != 1.0 {
		t.Fatalf("expected default weight 1.0, got %f", edge.Weight)
	}

	// Persistence: the target is now reachable as related to the source.
	rel := doRaw(t, srv.httpServer.Handler, "GET", "/api/graph/"+itoa(a)+"/related?depth=1", nil, nil)
	assertStatus(t, rel, http.StatusOK)
	var related []*domain.Observation
	decodeJSON(t, rel, &related)
	if len(related) != 1 || related[0].ID != b {
		t.Fatalf("expected related set [target=%d], got %+v", b, related)
	}
}

func TestCreateEdge_DuplicateConflict(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	a := createObservation(t, srv, "Source", "src", "manual", "demo", "project")
	b := createObservation(t, srv, "Target", "tgt", "manual", "demo", "project")

	first := doJSON(t, srv.httpServer.Handler, "POST", "/api/graph/edges", domain.Edge{
		FromObsID: a, ToObsID: b, RelationType: domain.RelationReferences,
	}, nil)
	assertStatus(t, first, http.StatusCreated)

	// Identical (from,to,relation_type) violates the UNIQUE constraint, which
	// the store surfaces as ErrAlreadyExists -> 409 Conflict via mapDomainError.
	second := doJSON(t, srv.httpServer.Handler, "POST", "/api/graph/edges", domain.Edge{
		FromObsID: a, ToObsID: b, RelationType: domain.RelationReferences,
	}, nil)
	assertStatus(t, second, http.StatusConflict)
}

func TestCreateEdge_MissingObservations(t *testing.T) {
	srv := setupTestServer(t)
	// Both endpoints reference non-existent observations; with foreign_keys=ON
	// the FK constraint fails and the store returns a NotFoundError -> 404.
	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/graph/edges", domain.Edge{
		FromObsID: 100, ToObsID: 200, RelationType: domain.RelationReferences,
	}, nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestDeleteEdge_SuccessAndNotFound(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	a := createObservation(t, srv, "Source", "src", "manual", "demo", "project")
	b := createObservation(t, srv, "Target", "tgt", "manual", "demo", "project")

	create := doJSON(t, srv.httpServer.Handler, "POST", "/api/graph/edges", domain.Edge{
		FromObsID: a, ToObsID: b, RelationType: domain.RelationReferences,
	}, nil)
	assertStatus(t, create, http.StatusCreated)
	var edge domain.Edge
	decodeJSON(t, create, &edge)

	del := doRaw(t, srv.httpServer.Handler, "DELETE", "/api/graph/edges/"+itoa(edge.ID), nil, nil)
	assertStatus(t, del, http.StatusOK)
	var body map[string]string
	decodeJSON(t, del, &body)
	if body["status"] != "deleted" {
		t.Fatalf("expected status=deleted, got %q", del.Body.String())
	}

	// Deleting again maps the store NotFoundError to 404.
	again := doRaw(t, srv.httpServer.Handler, "DELETE", "/api/graph/edges/"+itoa(edge.ID), nil, nil)
	assertStatus(t, again, http.StatusNotFound)
}

// --- scores -----------------------------------------------------------------

func TestGetScore_Success(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	id := createObservation(t, srv, "Scored", "content", "decision", "demo", "project")
	// The importance_init trigger creates a score row on observation insert.
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/scores/"+itoa(id), nil, nil)
	assertStatus(t, rec, http.StatusOK)
	var score domain.ImportanceScore
	decodeJSON(t, rec, &score)
	if score.ObservationID != id {
		t.Fatalf("score observation id: got %d want %d", score.ObservationID, id)
	}
}

func TestGetScore_NotFound(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/scores/4242", nil, nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestRecalculateScore_SuccessPersists(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	id := createObservation(t, srv, "Decision", "important choice", "decision", "demo", "project")

	rec := doRaw(t, srv.httpServer.Handler, "POST", "/api/scores/"+itoa(id)+"/recalculate", nil, nil)
	assertStatus(t, rec, http.StatusOK)
	var resp struct {
		Score float64 `json:"score"`
	}
	decodeJSON(t, rec, &resp)
	// base(0.5) + decision type bonus(0.5) minus a negligible age penalty.
	if resp.Score < 0.9 || resp.Score > 5.0 {
		t.Fatalf("recalculated score out of expected range: %f", resp.Score)
	}

	// Persistence: the recalculated value is retrievable via GET.
	get := doRaw(t, srv.httpServer.Handler, "GET", "/api/scores/"+itoa(id), nil, nil)
	assertStatus(t, get, http.StatusOK)
	var stored domain.ImportanceScore
	decodeJSON(t, get, &stored)
	if stored.Score != resp.Score {
		t.Fatalf("persisted score %f != recalculated %f", stored.Score, resp.Score)
	}
}

// --- export / import --------------------------------------------------------

func TestExport_EmptyAndPopulated(t *testing.T) {
	srv := setupTestServer(t)

	empty := doRaw(t, srv.httpServer.Handler, "GET", "/api/export", nil, nil)
	assertStatus(t, empty, http.StatusOK)
	var data sqlitestore.ExportData
	decodeJSON(t, empty, &data)
	if data.Version != "0.1.0" {
		t.Fatalf("export version: got %q want 0.1.0", data.Version)
	}
	if len(data.Observations) != 0 {
		t.Fatalf("expected empty export, got %d observations", len(data.Observations))
	}

	createSession(t, srv, "s1", "demo")
	createObservation(t, srv, "Exported", "export content", "manual", "demo", "project")
	populated := doRaw(t, srv.httpServer.Handler, "GET", "/api/export", nil, nil)
	assertStatus(t, populated, http.StatusOK)
	decodeJSON(t, populated, &data)
	if len(data.Observations) != 1 || data.Observations[0].Title != "Exported" {
		t.Fatalf("export should contain the seeded observation, got %+v", data.Observations)
	}
}

func TestImport_SuccessPersists(t *testing.T) {
	srv := setupTestServer(t)
	payload := sqlitestore.ExportData{
		Version:    "0.1.0",
		ExportedAt: "2026-01-01T00:00:00Z",
		Sessions:   []*domain.Session{{ID: "imp-sess", Project: "demo", Directory: "."}},
		Observations: []*domain.Observation{
			{SessionID: "imp-sess", Title: "Imported", Content: "imported body",
				Type: "manual", Project: "demo", Scope: "project",
				CreatedAt: mustParseTime("2026-01-01T00:00:00Z"),
				UpdatedAt: mustParseTime("2026-01-01T00:00:00Z")},
		},
	}

	rec := doJSON(t, srv.httpServer.Handler, "POST", "/api/import", payload, nil)
	assertStatus(t, rec, http.StatusOK)
	var result sqlitestore.SyncImportResult
	decodeJSON(t, rec, &result)
	if result.SessionsImported != 1 || result.ObservationsImported != 1 {
		t.Fatalf("import counts: sessions=%d observations=%d", result.SessionsImported, result.ObservationsImported)
	}

	// Persistence: the imported observation is visible through the list endpoint.
	list := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations?project=demo", nil, nil)
	assertStatus(t, list, http.StatusOK)
	var got []*domain.Observation
	decodeJSON(t, list, &got)
	var found bool
	for _, o := range got {
		if o.Title == "Imported" {
			found = true
		}
	}
	if !found {
		t.Fatal("imported observation not persisted")
	}
}

// --- revision limits --------------------------------------------------------

func TestRevisions_LimitParam(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	obs := &domain.Observation{
		SessionID: "s1", Title: "V1", Content: "first",
		Type: "manual", Project: "demo", Scope: "project",
	}
	if err := srv.deps.Observations.Save(context.Background(), obs); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Two updates capture two revision snapshots.
	obs.Title, obs.Content = "V2", "second"
	if err := srv.deps.Observations.Update(context.Background(), obs); err != nil {
		t.Fatalf("update 1: %v", err)
	}
	obs.Title, obs.Content = "V3", "third"
	if err := srv.deps.Observations.Update(context.Background(), obs); err != nil {
		t.Fatalf("update 2: %v", err)
	}

	// Default limit returns both snapshots.
	def := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/"+itoa(obs.ID)+"/revisions", nil, nil)
	assertStatus(t, def, http.StatusOK)
	var all []observationRevisionPayload
	decodeJSON(t, def, &all)
	if len(all) != 2 {
		t.Fatalf("default revisions: want 2 snapshots, got %d", len(all))
	}

	// limit=1 constrains the payload length via loadObservationRevisionPayloads.
	lim := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/"+itoa(obs.ID)+"/revisions?limit=1", nil, nil)
	assertStatus(t, lim, http.StatusOK)
	var one []observationRevisionPayload
	decodeJSON(t, lim, &one)
	if len(one) != 1 {
		t.Fatalf("limited revisions: want 1 snapshot, got %d", len(one))
	}
}

func TestRevisions_NotFound(t *testing.T) {
	srv := setupTestServer(t)
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/7777/revisions", nil, nil)
	assertStatus(t, rec, http.StatusNotFound)
}

func TestRevisions_NilRepository(t *testing.T) {
	srv := setupTestServer(t)
	createSession(t, srv, "s1", "demo")
	id := createObservation(t, srv, "HasRevs", "c", "manual", "demo", "project")
	// With no snapshot repository wired, the handler must return an empty 200
	// list rather than erroring.
	srv.deps.TemporalSnapshots = nil
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations/"+itoa(id)+"/revisions", nil, nil)
	assertStatus(t, rec, http.StatusOK)
	if rec.Body.String() != "null\n" && !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "[") {
		t.Fatalf("expected empty/json-array body, got %q", rec.Body.String())
	}
}

// --- auth (additional oracles) ---------------------------------------------

func TestAuth_HealthRemainsPublic(t *testing.T) {
	srv := setupTestServerWithOptions(t, Options{AuthToken: "secret-token"})
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/health", nil, nil)
	assertStatus(t, rec, http.StatusOK)
}

func TestAuth_UnauthenticatedSetsChallengeHeader(t *testing.T) {
	srv := setupTestServerWithOptions(t, Options{AuthToken: "secret-token"})
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations", nil, nil)
	assertStatus(t, rec, http.StatusUnauthorized)
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("expected WWW-Authenticate Bearer challenge, got %q", got)
	}
}

func TestAuth_ConfiguredTokenIsTrimmed(t *testing.T) {
	// withAuth trims the configured token; a bearer matching the trimmed value
	// must authorize even though the Options value carried surrounding spaces.
	srv := setupTestServerWithOptions(t, Options{AuthToken: "  secret-token  "})
	auth := map[string]string{"Authorization": "Bearer secret-token"}
	rec := doRaw(t, srv.httpServer.Handler, "GET", "/api/observations", nil, auth)
	assertStatus(t, rec, http.StatusOK)
}

// --- deterministic helpers (no listeners) ----------------------------------

func TestAddr_ReturnsConfiguredAddress(t *testing.T) {
	srv := setupTestServer(t) // constructed with ":0"
	if got := srv.Addr(); got != ":0" {
		t.Fatalf("Addr: want %q, got %q", ":0", got)
	}
}

func TestShutdown_NeverServedReturnsNil(t *testing.T) {
	srv := setupTestServer(t)
	// Shutdown on a server that never called ListenAndServe is a safe no-op.
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: expected nil error, got %v", err)
	}
}

// --- pure helper unit tests -------------------------------------------------

func TestQueryInt(t *testing.T) {
	cases := []struct {
		name   string
		target string
		key    string
		def    int
		want   int
	}{
		{"absent returns default", "/x", "limit", 20, 20},
		{"valid integer", "/x?limit=7", "limit", 20, 7},
		{"zero is honored", "/x?limit=0", "limit", 20, 0},
		{"negative is honored", "/x?offset=-3", "offset", 0, -3},
		{"non-numeric falls back to default", "/x?limit=abc", "limit", 20, 20},
		{"empty value falls back to default", "/x?limit=", "limit", 20, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			if got := queryInt(req, tc.key, tc.def); got != tc.want {
				t.Fatalf("queryInt(%q,%d): want %d, got %d", tc.key, tc.def, tc.want, got)
			}
		})
	}
}

func TestTruncateHTTP(t *testing.T) {
	if got := truncateHTTP("short", 10); got != "short" {
		t.Fatalf("short string should be unchanged, got %q", got)
	}
	if got := truncateHTTP("exactly10!", 10); got != "exactly10!" {
		t.Fatalf("boundary-length string should be unchanged, got %q", got)
	}
	if got := truncateHTTP("over the limit", 5); got != "over ..." {
		t.Fatalf("truncation mismatch: got %q", got)
	}

	// Rune-aware truncation: a multi-byte string must not be split mid-rune.
	unicodeIn := strings.Repeat("é", 200) // 400 bytes, 200 runes
	got := truncateHTTP(unicodeIn, 150)
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected '...' suffix, got %q", got[:len(got)-1])
	}
	// 150 truncated runes + the 3-byte ellipsis marker runes ("...").
	if runeCount := utf8RuneCount(got); runeCount != 153 {
		t.Fatalf("expected 153 runes after truncation, got %d (body=%q)", runeCount, got)
	}
	// The truncated prefix must remain valid UTF-8 (no corrupted bytes).
	prefix := strings.TrimSuffix(got, "...")
	if !isValidUTF8(prefix) {
		t.Fatalf("truncated prefix is not valid UTF-8: %q", prefix)
	}
}

func TestMapDomainError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found -> 404", &domain.NotFoundError{Type: "observation", ID: 1}, http.StatusNotFound},
		{"validation -> 400", &domain.ValidationError{Field: "title", Message: "required"}, http.StatusBadRequest},
		{"conflict -> 409", &domain.ConflictError{Entity: "x", Reason: "dup"}, http.StatusConflict},
		{"already exists sentinel -> 409", domain.ErrAlreadyExists, http.StatusConflict},
		{"session ended -> 409", domain.ErrSessionEnded, http.StatusConflict},
		{"unknown -> 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			mapDomainError(rec, tc.err)
			assertStatus(t, rec, tc.want)
		})
	}
}

// --- small local utilities --------------------------------------------------

// itoa formats an int64 path id for URL construction.
func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// mustParseTime parses an RFC3339 timestamp for fixture construction.
func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic("mustParseTime: " + err.Error())
	}
	return t
}

// utf8RuneCount counts runes in s.
func utf8RuneCount(s string) int { return len([]rune(s)) }

// isValidUTF8 reports whether s is a valid UTF-8 encoding (no replacement runes).
func isValidUTF8(s string) bool {
	return strings.ToValidUTF8(s, "") == s
}
