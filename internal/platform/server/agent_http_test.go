package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

const (
	agentProjectID = "10000000-a000-0000-0000-000000000003"
	agentWorkspace = "10000000-a000-0000-0000-000000000002"
)

type recordingAgentAnswerer struct {
	request serverAgentRequest
	answer  agentdomain.Answer
	err     error
	deltas  []string
}

type recordingAgentAuditSink struct {
	events []agentdomain.AuditEvent
	err    error
}

type nonFlushingAgentWriter struct {
	recorder *httptest.ResponseRecorder
}

func (w *nonFlushingAgentWriter) Header() http.Header         { return w.recorder.Header() }
func (w *nonFlushingAgentWriter) Write(p []byte) (int, error) { return w.recorder.Write(p) }
func (w *nonFlushingAgentWriter) WriteHeader(status int)      { w.recorder.WriteHeader(status) }

func (s *recordingAgentAuditSink) Record(_ context.Context, event agentdomain.AuditEvent) error {
	s.events = append(s.events, event)
	return s.err
}

func (a *recordingAgentAnswerer) Answer(ctx context.Context, request serverAgentRequest) (agentdomain.Answer, error) {
	a.request = request
	if a.err != nil {
		return agentdomain.Answer{}, a.err
	}
	return a.answer, ctx.Err()
}

func (a *recordingAgentAnswerer) Stream(ctx context.Context, request serverAgentRequest, callbacks agentdomain.StreamCallbacks) (agentdomain.Answer, error) {
	a.request = request
	if a.err != nil {
		return agentdomain.Answer{}, a.err
	}
	if callbacks.Meta != nil {
		if err := callbacks.Meta(a.answer.Retrieval); err != nil {
			return agentdomain.Answer{}, err
		}
	}
	deltas := a.deltas
	if len(deltas) == 0 {
		deltas = []string{a.answer.Answer}
	}
	for _, delta := range deltas {
		if err := ctx.Err(); err != nil {
			return agentdomain.Answer{}, err
		}
		if callbacks.Delta != nil {
			if err := callbacks.Delta(delta); err != nil {
				return agentdomain.Answer{}, err
			}
		}
	}
	if callbacks.Sources != nil {
		if err := callbacks.Sources(a.answer.Sources); err != nil {
			return agentdomain.Answer{}, err
		}
	}
	return a.answer, ctx.Err()
}

type waitingAgentAnswerer struct{}

func (waitingAgentAnswerer) Answer(ctx context.Context, _ serverAgentRequest) (agentdomain.Answer, error) {
	<-ctx.Done()
	return agentdomain.Answer{}, ctx.Err()
}

func (waitingAgentAnswerer) Stream(ctx context.Context, _ serverAgentRequest, _ agentdomain.StreamCallbacks) (agentdomain.Answer, error) {
	<-ctx.Done()
	return agentdomain.Answer{}, ctx.Err()
}

type cancellationAgentAnswerer struct {
	started   chan struct{}
	cancelled chan struct{}
}

type metadataScopedRetriever struct {
	result agentdomain.RetrievalResult
}

func (r metadataScopedRetriever) RetrieveScoped(context.Context, agentdomain.Scope, string, int) (agentdomain.RetrievalResult, error) {
	return r.result, nil
}

type metadataCompletion struct{}

func (metadataCompletion) Complete(ctx context.Context, request agentdomain.CompletionRequest) (agentdomain.CompletionResult, error) {
	return (firstHandleCompletion{}).Complete(ctx, request)
}

func (metadataCompletion) Stream(ctx context.Context, request agentdomain.CompletionRequest, emit func(agentdomain.CompletionClaim) error) (agentdomain.CompletionUsage, error) {
	result, err := (firstHandleCompletion{}).Complete(ctx, request)
	if err != nil {
		return agentdomain.CompletionUsage{}, err
	}
	for _, claim := range result.Claims {
		if err := emit(claim); err != nil {
			return agentdomain.CompletionUsage{}, err
		}
	}
	return result.Usage(), nil
}

func (a cancellationAgentAnswerer) Answer(ctx context.Context, _ serverAgentRequest) (agentdomain.Answer, error) {
	close(a.started)
	<-ctx.Done()
	close(a.cancelled)
	return agentdomain.Answer{}, ctx.Err()
}

func (a cancellationAgentAnswerer) Stream(ctx context.Context, _ serverAgentRequest, _ agentdomain.StreamCallbacks) (agentdomain.Answer, error) {
	return a.Answer(ctx, serverAgentRequest{})
}

func TestAgentStreamEmitsEquivalentOrderedSSEEvents(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	answer := agentdomain.Answer{
		Answer:     "Usa PostgreSQL.",
		Sources:    []agentdomain.Source{{Handle: "src_1", Type: agentdomain.EvidenceCode, Title: "store", Path: "internal/store/postgres.go", LineStart: 10, LineEnd: 12}},
		Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceHigh, Score: .9},
		Retrieval:  agentdomain.RetrievalStatus{Degraded: []string{}},
	}
	answerer := &recordingAgentAnswerer{answer: answer, deltas: []string{"Usa ", "PostgreSQL."}}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", `{"project_id":"`+agentProjectID+`","question":"¿Qué base usa?"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream; charset=utf-8" {
		t.Fatalf("content type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") || !strings.Contains(got, "no-transform") {
		t.Fatalf("cache control = %q", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q", got)
	}
	body := rec.Body.String()
	events := []string{"event: meta\n", "event: delta\n", "event: sources\n", "event: done\n"}
	previous := -1
	for _, event := range events {
		at := strings.Index(body, event)
		if at <= previous {
			t.Fatalf("events not ordered (%q at %d after %d): %s", event, at, previous, body)
		}
		previous = at
	}
	if strings.Count(body, "event: delta\n") != 2 || strings.Contains(body, "event: error\n") || !strings.Contains(body, `"text":"Usa "`) || !strings.Contains(body, `"text":"PostgreSQL."`) || !strings.Contains(body, `"handle":"src_1"`) {
		t.Fatalf("unexpected stream: %s", body)
	}
	if !strings.Contains(body, "id: 1\nevent: meta\n") {
		t.Fatalf("stream missing event id: %s", body)
	}
	canonical, err := json.Marshal(answer)
	if err != nil || !strings.Contains(body, "data: "+string(canonical)+"\n\n") {
		t.Fatalf("done is not canonical answer: %s err=%v", body, err)
	}
	if answerer.request.project.ID != agentProjectID || answerer.request.tenantID != "tenant-verified" || answerer.request.workspaceID != agentWorkspace {
		t.Fatalf("server request = %#v", answerer.request)
	}
}

func TestAgentMetadataParityAndRedaction(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	retriever := metadataScopedRetriever{result: agentdomain.RetrievalResult{
		Evidence: []agentdomain.Evidence{{Kind: agentdomain.EvidenceMemory, Title: "Safe decision", Content: "EVIDENCE_CONTENT_CANARY", Score: .9}},
		Trace: agentdomain.RetrievalTrace{
			Tier: agentdomain.RetrievalTierSemanticHybrid,
			Stages: []agentdomain.RetrievalStage{
				{Name: "lexical", Status: "ok", Count: 2},
				{Name: "lexical", Status: "degraded", Count: 9999},
				{Name: "INTERNAL_ID_CANARY", Status: "ok", Count: 99},
				{Name: "dense", Status: "PRINCIPAL_CANARY", Count: 4},
				{Name: "code", Status: "degraded", Count: -7},
				{Name: "crag", Status: "ok", Count: 99},
				{Name: "crag", Status: "degraded", Count: 1},
			},
			Degraded: []string{"dense_unavailable", "TOKEN_CANARY", "code_unavailable", "dense_unavailable"},
		},
	}}
	answerer := &serverAgentService{core: agentdomain.NewScopedService(retriever, metadataCompletion{})}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())
	body := `{"project_id":"` + agentProjectID + `","question":"QUERY_CANARY","history":[{"role":"user","content":"PROMPT_CANARY"}]}`

	jsonRec := httptest.NewRecorder()
	h.ServeHTTP(jsonRec, authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", body))
	if jsonRec.Code != http.StatusOK {
		t.Fatalf("JSON status=%d body=%s", jsonRec.Code, jsonRec.Body.String())
	}
	var jsonAnswer agentdomain.Answer
	if err := json.Unmarshal(jsonRec.Body.Bytes(), &jsonAnswer); err != nil {
		t.Fatal(err)
	}

	streamRec := httptest.NewRecorder()
	h.ServeHTTP(streamRec, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", body))
	if streamRec.Code != http.StatusOK {
		t.Fatalf("SSE status=%d body=%s", streamRec.Code, streamRec.Body.String())
	}
	metaData := agentSSEEventData(t, streamRec.Body.String(), "meta")
	doneData := agentSSEEventData(t, streamRec.Body.String(), "done")
	var meta struct {
		Retrieval agentdomain.RetrievalStatus `json:"retrieval"`
	}
	var done agentdomain.Answer
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(doneData, &done); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonAnswer.Retrieval, done.Retrieval) || !reflect.DeepEqual(meta.Retrieval, done.Retrieval) {
		t.Fatalf("metadata parity failed: JSON=%#v meta=%#v done=%#v", jsonAnswer.Retrieval, meta.Retrieval, done.Retrieval)
	}
	want := agentdomain.RetrievalStatus{
		Tier:            "semantic_hybrid",
		Stages:          []agentdomain.RetrievalStageStatus{{Name: "lexical", Status: "degraded", Count: 10000}, {Name: "code", Status: "degraded", Count: 0}, {Name: "crag", Status: "degraded", Count: 1}},
		RefinementCount: 1,
		Degraded:        []string{"code_unavailable", "dense_unavailable"},
	}
	if !reflect.DeepEqual(done.Retrieval, want) {
		t.Fatalf("canonical metadata=%#v want=%#v", done.Retrieval, want)
	}
	seenStages := make(map[string]bool, len(done.Retrieval.Stages))
	for _, stage := range done.Retrieval.Stages {
		if seenStages[stage.Name] {
			t.Fatalf("duplicate public stage %q in %#v", stage.Name, done.Retrieval.Stages)
		}
		seenStages[stage.Name] = true
	}
	if len(done.Retrieval.Stages) > 7 {
		t.Fatalf("public stage cardinality=%d", len(done.Retrieval.Stages))
	}
	combined := jsonRec.Body.String() + streamRec.Body.String()
	for _, canary := range []string{"QUERY_CANARY", "PROMPT_CANARY", "EVIDENCE_CONTENT_CANARY", "INTERNAL_ID_CANARY", "PRINCIPAL_CANARY", "TOKEN_CANARY", "tenant-verified", agentWorkspace, agentProjectID, "generation"} {
		if strings.Contains(combined, canary) {
			t.Fatalf("response leaked %q: %s", canary, combined)
		}
	}
}

func agentSSEEventData(t *testing.T, body, event string) []byte {
	t.Helper()
	prefix := "event: " + event + "\ndata: "
	start := strings.Index(body, prefix)
	if start < 0 {
		t.Fatalf("missing %s event: %s", event, body)
	}
	data := body[start+len(prefix):]
	end := strings.Index(data, "\n\n")
	if end < 0 {
		t.Fatalf("incomplete %s event: %s", event, body)
	}
	return []byte(data[:end])
}

func TestAgentStreamSanitizesProviderFailureAfterHeaders(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	answerer := &recordingAgentAnswerer{err: errors.New("upstream https://secret.invalid token=credential")}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", `{"project_id":"`+agentProjectID+`","question":"q"}`))

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "event: error\n") || !strings.Contains(rec.Body.String(), `"code":"provider_unavailable"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "secret.invalid") || strings.Contains(rec.Body.String(), "credential") || strings.Contains(rec.Body.String(), "event: done\n") {
		t.Fatalf("stream leaked upstream details or completed: %s", rec.Body.String())
	}
}

func TestAgentStreamCancellationReachesAnswererAndStopsEvents(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	answerer := cancellationAgentAnswerer{started: make(chan struct{}), cancelled: make(chan struct{})}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())
	ctx, cancel := context.WithCancel(context.Background())
	req := authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", `{"project_id":"`+agentProjectID+`","question":"q"}`).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-answerer.started:
	case <-time.After(time.Second):
		t.Fatal("answerer did not start")
	}
	cancel()
	select {
	case <-answerer.cancelled:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not reach answerer")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not stop after cancellation")
	}
	if body := rec.Body.String(); strings.Contains(body, "event: error\n") || strings.Contains(body, "event: done\n") {
		t.Fatalf("stream emitted terminal content after disconnect: %s", body)
	}
}

func TestAgentStreamRejectsQuotaBeforeSSEHeaders(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	policy := agentdomain.LimitPolicy{Tiers: map[string]agentdomain.Limits{
		"standard": {RequestsPerMinute: 1, TokensPerMinute: 100, MaxTenantConcurrent: 1, DefaultOutputTokens: 10, MaxOutputTokens: 100, JSONTimeout: time.Second, StreamTimeout: time.Second},
	}}
	sink := &recordingAgentAuditSink{}
	h := newAgentTestHandlerWithAudit(t, ops, &recordingAgentAnswerer{answer: agentdomain.Answer{
		Answer: "ok", Sources: []agentdomain.Source{}, Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceLow}, Retrieval: agentdomain.RetrievalStatus{Degraded: []string{}},
	}}, policy, agentdomain.Auditor{Sink: sink})
	body := `{"project_id":"` + agentProjectID + `","question":"q"}`
	first := httptest.NewRecorder()
	h.ServeHTTP(first, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", body))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", body))
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Content-Type") == "text/event-stream; charset=utf-8" || !strings.Contains(second.Body.String(), `"code":"quota_exceeded"`) {
		t.Fatalf("second status=%d content-type=%q body=%s", second.Code, second.Header().Get("Content-Type"), second.Body.String())
	}
}

func TestAgentProjectsAndAnswerUseVerifiedProjectMetadata(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	answerer := &recordingAgentAnswerer{answer: agentdomain.Answer{
		Answer: "Usa PostgreSQL.", Sources: []agentdomain.Source{},
		Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceHigh, Score: .9},
		Retrieval:  agentdomain.RetrievalStatus{Degraded: []string{}},
	}}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())

	projects := authenticatedAgentRequest(http.MethodGet, "/api/agent/projects", "")
	projectsRec := httptest.NewRecorder()
	h.ServeHTTP(projectsRec, projects)
	if projectsRec.Code != http.StatusOK || projectsRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("projects status=%d cache=%q body=%s", projectsRec.Code, projectsRec.Header().Get("Cache-Control"), projectsRec.Body.String())
	}
	var listed struct {
		Projects []agentProject `json:"projects"`
	}
	if err := json.Unmarshal(projectsRec.Body.Bytes(), &listed); err != nil || len(listed.Projects) != 1 || listed.Projects[0].ID != agentProjectID || listed.Projects[0].Label != "cortex" {
		t.Fatalf("projects = %#v err=%v", listed, err)
	}

	body := `{"project_id":"` + agentProjectID + `","question":"¿Qué base usa?","history":[{"role":"user","content":"Contexto"}]}`
	req := authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", body)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), "Usa PostgreSQL") {
		t.Fatalf("answer status=%d cache=%q body=%s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	if answerer.request.project.ID != agentProjectID || answerer.request.project.Label != "cortex" || answerer.request.tenantID != "tenant-verified" || answerer.request.workspaceID != agentWorkspace {
		t.Fatalf("server request = %#v", answerer.request)
	}
}

func TestAgentAnswerRejectsUngradedProjectWithoutCallingAgent(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	answerer := &recordingAgentAnswerer{}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())

	req := authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", `{"project_id":"20000000-a000-0000-0000-000000000003","question":"secret?"}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"project_not_granted"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if answerer.request.question != "" {
		t.Fatal("ungranted project reached agent")
	}
}

func TestAgentRoutesFailClosedWhenAuthorizationAuditIsUnavailable(t *testing.T) {
	for _, path := range []string{"/api/agent/answer", "/api/agent/stream"} {
		t.Run(path, func(t *testing.T) {
			ops := newFakeOperations()
			ops.agentProjects = map[string]string{agentProjectID: "cortex"}
			answerer := &recordingAgentAnswerer{}
			h := newAgentTestHandlerWithAudit(t, ops, answerer, agentdomain.DefaultLimitPolicy(), agentdomain.Auditor{})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, authenticatedAgentRequest(http.MethodPost, path, `{"project_id":"`+agentProjectID+`","question":"secret question"}`))
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"audit_unavailable"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if answerer.request.question != "" {
				t.Fatal("request reached retrieval/provider before mandatory audit")
			}
		})
	}
}

func TestAgentJSONRecordsAuthorizationAndMetadataOnlyOutcome(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	sink := &recordingAgentAuditSink{}
	h := newAgentTestHandlerWithAudit(t, ops, &recordingAgentAnswerer{answer: agentdomain.Answer{
		Answer: "safe", Sources: []agentdomain.Source{{Handle: "src_safe", Title: "Architecture"}},
		Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceHigh, Score: .9},
		Retrieval:  agentdomain.RetrievalStatus{Degraded: []string{agentdomain.DegradedCodeUnavailable}},
		Usage:      agentdomain.CompletionUsage{InputTokens: 11, OutputTokens: 7},
	}}, agentdomain.DefaultLimitPolicy(), agentdomain.Auditor{Sink: sink})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", `{"project_id":"`+agentProjectID+`","question":"do not audit this secret"}`))
	if rec.Code != http.StatusOK || len(sink.events) != 2 {
		t.Fatalf("status=%d events=%#v body=%s", rec.Code, sink.events, rec.Body.String())
	}
	if sink.events[0].Phase != agentdomain.AuditPhaseAuthorization || sink.events[1].Phase != agentdomain.AuditPhaseOutcome || sink.events[1].ResultClass != "success" || sink.events[1].SourceCount != 1 || sink.events[1].InputTokens != 11 || sink.events[1].OutputTokens != 7 {
		t.Fatalf("events=%#v", sink.events)
	}
	raw, _ := json.Marshal(sink.events)
	if strings.Contains(string(raw), "do not audit this secret") || strings.Contains(string(raw), "safe") {
		t.Fatalf("audit leaked conversational content: %s", raw)
	}
}

func TestAgentStreamRecordsAuthorizationAndMetadataOnlyOutcome(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	sink := &recordingAgentAuditSink{}
	h := newAgentTestHandlerWithAudit(t, ops, &recordingAgentAnswerer{answer: agentdomain.Answer{
		Answer: "safe", Sources: []agentdomain.Source{{Handle: "src_safe", Title: "Architecture"}},
		Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceHigh, Score: .9},
		Retrieval:  agentdomain.RetrievalStatus{Degraded: []string{}},
	}}, agentdomain.DefaultLimitPolicy(), agentdomain.Auditor{Sink: sink})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", `{"project_id":"`+agentProjectID+`","question":"do not audit this secret"}`))
	if rec.Code != http.StatusOK || len(sink.events) != 2 || sink.events[0].Transport != agentdomain.TransportStream || sink.events[1].ResultClass != "success" {
		t.Fatalf("status=%d events=%#v body=%s", rec.Code, sink.events, rec.Body.String())
	}
	raw, _ := json.Marshal(sink.events)
	if strings.Contains(string(raw), "do not audit this secret") || strings.Contains(string(raw), "safe") {
		t.Fatalf("audit leaked conversational content: %s", raw)
	}
}

func TestAgentStreamWithoutFlusherRecordsExactlyOneTerminalOutcome(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	sink := &recordingAgentAuditSink{}
	h := newAgentTestHandlerWithAudit(t, ops, &recordingAgentAnswerer{}, agentdomain.DefaultLimitPolicy(), agentdomain.Auditor{Sink: sink})
	w := &nonFlushingAgentWriter{recorder: httptest.NewRecorder()}
	h.ServeHTTP(w, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", `{"project_id":"`+agentProjectID+`","question":"q"}`))
	if w.recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", w.recorder.Code, w.recorder.Body.String())
	}
	if len(sink.events) != 2 || sink.events[0].Phase != agentdomain.AuditPhaseAuthorization || sink.events[1].Phase != agentdomain.AuditPhaseOutcome {
		t.Fatalf("events=%#v", sink.events)
	}
}

func TestAgentAnswerStableValidationAndTimeoutErrors(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	policy := agentdomain.LimitPolicy{Tiers: map[string]agentdomain.Limits{
		"standard": {RequestsPerMinute: 10, TokensPerMinute: 10000, MaxTenantConcurrent: 1, DefaultOutputTokens: 10, MaxOutputTokens: 100, JSONTimeout: time.Millisecond, StreamTimeout: time.Second},
	}}
	h := newAgentTestHandler(t, ops, waitingAgentAnswerer{}, policy)

	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"invalid json", `{`, "invalid_json", http.StatusBadRequest},
		{"client label rejected", `{"project_id":"` + agentProjectID + `","project":"forged","question":"q"}`, "invalid_json", http.StatusBadRequest},
		{"timeout", `{"project_id":"` + agentProjectID + `","question":"q"}`, "agent_timeout", http.StatusGatewayTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", tc.body)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAgentAnswerEnforcesStableQuotaResponse(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	policy := agentdomain.LimitPolicy{Tiers: map[string]agentdomain.Limits{
		"standard": {RequestsPerMinute: 1, TokensPerMinute: 100, MaxTenantConcurrent: 1, DefaultOutputTokens: 10, MaxOutputTokens: 100, JSONTimeout: time.Second, StreamTimeout: 2 * time.Second},
	}}
	sink := &recordingAgentAuditSink{}
	h := newAgentTestHandlerWithAudit(t, ops, &recordingAgentAnswerer{answer: agentdomain.Answer{
		Answer: "ok", Sources: []agentdomain.Source{}, Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceLow}, Retrieval: agentdomain.RetrievalStatus{Degraded: []string{}},
	}}, policy, agentdomain.Auditor{Sink: sink})
	body := `{"project_id":"` + agentProjectID + `","question":"q"}`
	first := httptest.NewRecorder()
	h.ServeHTTP(first, authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", body))
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	h.ServeHTTP(second, authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", body))
	if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" || !strings.Contains(second.Body.String(), `"code":"quota_exceeded"`) {
		t.Fatalf("second status=%d retry=%q body=%s", second.Code, second.Header().Get("Retry-After"), second.Body.String())
	}
	if len(sink.events) != 4 || sink.events[3].Phase != agentdomain.AuditPhaseOutcome || sink.events[3].ResultClass != string(agentdomain.ErrorQuotaExceeded) {
		t.Fatalf("quota outcome audit=%#v", sink.events)
	}
}

func TestAgentStreamClientDisconnectCancelsContextAndReleasesResources(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	answerer := &cancellationAgentAnswerer{started: started, cancelled: cancelled}

	policy := agentdomain.LimitPolicy{Tiers: map[string]agentdomain.Limits{
		"standard": {RequestsPerMinute: 10, TokensPerMinute: 1000, MaxTenantConcurrent: 2, DefaultOutputTokens: 10, MaxOutputTokens: 100, JSONTimeout: 5 * time.Second, StreamTimeout: 5 * time.Second},
	}}
	h := newAgentTestHandler(t, ops, answerer, policy)

	body := `{"project_id":"` + agentProjectID + `","question":"q"}`
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/agent/stream", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	rec := httptest.NewRecorder()
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	<-started
	cancel() // Simulate client disconnect

	select {
	case <-cancelled:
		// Successfully observed context cancellation in answerer
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for answerer cancellation")
	}

	<-done
}

func newAgentTestHandler(t *testing.T, ops *fakeOperations, answerer agentAnswerer, policy agentdomain.LimitPolicy) http.Handler {
	return newAgentTestHandlerWithAudit(t, ops, answerer, policy, agentdomain.Auditor{Sink: &recordingAgentAuditSink{}})
}

func newAgentTestHandlerWithAudit(t *testing.T, ops *fakeOperations, answerer agentAnswerer, policy agentdomain.LimitPolicy, auditor agentdomain.Auditor) http.Handler {
	t.Helper()
	cfg := config.Config{HTTP: config.HTTPConfig{Token: "test-token"}, Server: config.ServerConfig{WorkspaceID: agentWorkspace}}
	auth := requestAuthenticator{
		verifier: verifierFunc(func(_ context.Context, secret, _ string) (principal domain.Principal, err error) {
			if secret != "test-token" {
				return principal, errors.New("unknown credential")
			}
			return domain.Principal{Subject: "actor-verified", OrgID: "tenant-verified", WorkspaceIDs: []string{agentWorkspace}, Roles: []string{"viewer"}, GrantDigest: "token-provenance", RateLimitTier: "standard"}, nil
		}),
		factory: operationsFactoryFunc(func(context.Context, domain.Principal) (Operations, error) { return ops, nil }),
	}
	h, _ := newHTTPHandlerWithHybridSearch(cfg, requestOperations{}, func(context.Context) error { return nil }, auth.middleware, hybridSearchDependencies{
		agent: answerer, agentLimits: policy, agentAuditor: func(context.Context, domain.Principal) (agentdomain.Auditor, error) { return auditor, nil },
	})
	return h
}

func authenticatedAgentRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
