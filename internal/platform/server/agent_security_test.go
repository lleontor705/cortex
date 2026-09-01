package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	agentdomain "github.com/lleontor705/cortex/v2/internal/domain/agent"
)

func TestAgentEndpointsRequireAuthentication(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	answerer := &recordingAgentAnswerer{}
	h := newAgentTestHandler(t, ops, answerer, agentdomain.DefaultLimitPolicy())

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/agent/projects", ""},
		{http.MethodPost, "/api/agent/answer", `{"project_id":"` + agentProjectID + `","question":"secret?"}`},
		{http.MethodPost, "/api/agent/stream", `{"project_id":"` + agentProjectID + `","question":"secret?"}`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
	if answerer.request.question != "" {
		t.Fatal("unauthenticated request reached the agent")
	}
}

func TestAgentReadBoundaryExposesNoMutationMethods(t *testing.T) {
	typeOfBoundary := reflect.TypeOf((*agentRetrievalOperations)(nil)).Elem()
	allowed := map[string]bool{
		"SearchAgentObservations": true,
		"GetAgentObservationByID": true,
		"ListCodeSymbols":         true,
		"GetCodeGraph":            true,
	}
	if typeOfBoundary.NumMethod() != len(allowed) {
		t.Fatalf("agent retrieval boundary has %d methods, want %d", typeOfBoundary.NumMethod(), len(allowed))
	}
	for i := 0; i < typeOfBoundary.NumMethod(); i++ {
		method := typeOfBoundary.Method(i).Name
		if !allowed[method] {
			t.Fatalf("agent retrieval boundary exposes non-read method %q", method)
		}
	}
}

func TestAgentAnswerAndStreamReturnTheSameCanonicalAnswer(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	want := agentdomain.Answer{
		Answer: "La búsqueda combina texto y vectores.",
		Sources: []agentdomain.Source{{
			Handle: "src_safe", Type: agentdomain.EvidenceMemory, Title: "Search design",
		}},
		Confidence: agentdomain.Confidence{Level: agentdomain.ConfidenceHigh, Score: .91},
		Retrieval:  agentdomain.RetrievalStatus{Degraded: []string{}},
	}
	h := newAgentTestHandler(t, ops, &recordingAgentAnswerer{answer: want}, agentdomain.DefaultLimitPolicy())
	body := `{"project_id":"` + agentProjectID + `","question":"¿Cómo busca?"}`

	jsonRec := httptest.NewRecorder()
	h.ServeHTTP(jsonRec, authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", body))
	if jsonRec.Code != http.StatusOK {
		t.Fatalf("JSON status=%d body=%s", jsonRec.Code, jsonRec.Body.String())
	}
	var jsonAnswer agentdomain.Answer
	if err := json.Unmarshal(jsonRec.Body.Bytes(), &jsonAnswer); err != nil {
		t.Fatalf("decode JSON answer: %v", err)
	}

	streamRec := httptest.NewRecorder()
	h.ServeHTTP(streamRec, authenticatedAgentRequest(http.MethodPost, "/api/agent/stream", body))
	if streamRec.Code != http.StatusOK {
		t.Fatalf("SSE status=%d body=%s", streamRec.Code, streamRec.Body.String())
	}
	donePrefix := "event: done\ndata: "
	start := strings.Index(streamRec.Body.String(), donePrefix)
	if start < 0 {
		t.Fatalf("SSE has no done event: %s", streamRec.Body.String())
	}
	data := streamRec.Body.String()[start+len(donePrefix):]
	end := strings.Index(data, "\n\n")
	if end < 0 {
		t.Fatalf("SSE done event is incomplete: %s", streamRec.Body.String())
	}
	var streamAnswer agentdomain.Answer
	if err := json.Unmarshal([]byte(data[:end]), &streamAnswer); err != nil {
		t.Fatalf("decode SSE done answer: %v", err)
	}
	if !reflect.DeepEqual(jsonAnswer, streamAnswer) || !reflect.DeepEqual(streamAnswer, want) {
		t.Fatalf("transport answers differ:\nJSON=%#v\nSSE=%#v\nwant=%#v", jsonAnswer, streamAnswer, want)
	}
}

func TestAgentAnswerSanitizesInternalProviderError(t *testing.T) {
	ops := newFakeOperations()
	ops.agentProjects = map[string]string{agentProjectID: "cortex"}
	h := newAgentTestHandler(t, ops, &recordingAgentAnswerer{
		err: &agentdomain.Error{Code: agentdomain.ErrorProviderUnavailable, Err: errors.New("upstream token=credential host=secret.invalid")},
	}, agentdomain.DefaultLimitPolicy())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, authenticatedAgentRequest(http.MethodPost, "/api/agent/answer", `{"project_id":"`+agentProjectID+`","question":"q"}`))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"code":"provider_unavailable"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "credential") || strings.Contains(rec.Body.String(), "secret.invalid") {
		t.Fatalf("JSON response leaked provider details: %s", rec.Body.String())
	}
}
