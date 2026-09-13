package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestExactHTTPprivacy(t *testing.T) {
	TestHTTPPrivacy_ExactProvenanceCapOracle(t)
}

func TestHTTPPrivacy_ExactProvenanceCapOracle(t *testing.T) {
	t.Run("provenancecaporacle0", func(t *testing.T) {
		srv := setupTestServer(t)
		ctx := context.Background()

		const canaryT = "canary_http_exact_t_001"
		const canaryC = "canary_http_exact_c_002"

		// 1. HTTP Protected Copies and Responses for Observation Create & Update
		_ = srv.deps.Sessions.Create(ctx, &domain.Session{ID: "s-exact", Project: "proj-exact", Directory: "."})
		createReq := `{"session_id":"s-exact","project":"proj-exact","type":"manual","title":"Create <private>` + canaryT + `</private> Title","content":"Create <private>` + canaryC + `</private> Content"}`
		w := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("POST", "/api/observations", bytes.NewBufferString(createReq)))
		if w.Code != http.StatusCreated || strings.Contains(w.Body.String(), canaryT) || strings.Contains(w.Body.String(), canaryC) || !strings.Contains(w.Body.String(), "[REDACTED]") {
			t.Fatalf("create response failed: %d %s", w.Code, w.Body.String())
		}
		var created domain.Observation
		_ = json.Unmarshal(w.Body.Bytes(), &created)

		updateReq := `{"session_id":"s-exact","project":"proj-exact","type":"manual","title":"Update <private>` + canaryT + `</private> Title","content":"Update <private>` + canaryC + `</private> Content"}`
		w = httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(w, httptest.NewRequest("PUT", fmt.Sprintf("/api/observations/%d", created.ID), bytes.NewBufferString(updateReq)))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), canaryT) || strings.Contains(w.Body.String(), canaryC) || !strings.Contains(w.Body.String(), "[REDACTED]") {
			t.Fatalf("update response failed: %d %s", w.Code, w.Body.String())
		}

		persisted, err := srv.deps.Observations.GetByID(ctx, created.ID)
		if err != nil || strings.Contains(persisted.Title, canaryT) || persisted.Title != "Update [REDACTED] Title" {
			t.Fatalf("db persisted invalid: %v, %+v", err, persisted)
		}

		var dbHash string
		if err := srv.deps.Observations.DB().QueryRowContext(ctx, "SELECT normalized_hash FROM observations WHERE id = ?", created.ID).Scan(&dbHash); err != nil {
			t.Fatalf("query normalized_hash failed: %v", err)
		}
		normExpected := strings.ToLower(strings.TrimSpace("Update [REDACTED] Content"))
		normExpected = strings.Join(strings.Fields(normExpected), " ")
		h := sha256.Sum256([]byte(normExpected))
		wantHash := fmt.Sprintf("%x", h)
		if dbHash != wantHash {
			t.Fatalf("hash oracle mismatch: got %q, want %q", dbHash, wantHash)
		}

		// 2. Cap Oracle 0: invalid whole import causes zero ImportData and zero persisted effects
		srvClean := setupTestServer(t)
		spyClean := &spyObservationImporter{ObservationStore: srvClean.deps.Observations}
		srvClean.deps.Observations = spyClean
		for _, rawJSON := range []string{
			`{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"V <private>` + canaryT + `</private>","content":"C"},{"session_id":"s1","project":"p","type":"manual","title":"","content":"C"}]}`,
			`{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"V <private>` + canaryT + `</private>","content":"C"},{"session_id":"s1","project":"p","type":"manual","title":"  ","content":"C"}]}`,
			`{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"V <private>` + canaryT + `</private>","content":"C"},{"session_id":"s1","project":"p","type":"manual","title":"T","content":""}]}`,
			`{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"V <private>` + canaryT + `</private>","content":"C"},{"session_id":"s1","project":"p","type":"manual","title":"T","content":"  "}]}`,
			`{"observations":[{"session_id":"s1","project":"p","type":"manual","title":"V <private>` + canaryT + `</private>","content":"C"},null]}`,
			`{"sessions":[null]}`,
			`{"sessions":[{"id":"s1","project":"<private>` + canaryT + `</private>","directory":"."}]}`,
			`{"prompts":[null]}`,
			`{"prompts":[{"session_id":"s1","project":"p","content":"<private>` + canaryC + `</private>"}]}`,
		} {
			spyClean.importDataCalls = 0
			spyClean.importedData = nil
			wRec := httptest.NewRecorder()
			srvClean.httpServer.Handler.ServeHTTP(wRec, httptest.NewRequest("POST", "/api/import", bytes.NewBufferString(rawJSON)))
			if wRec.Code != http.StatusBadRequest || strings.Contains(wRec.Body.String(), canaryT) || strings.Contains(wRec.Body.String(), canaryC) {
				t.Fatalf("import rejection failed: code=%d body=%s", wRec.Code, wRec.Body.String())
			}
			if spyClean.importDataCalls != 0 || len(spyClean.importedData) != 0 {
				t.Fatalf("cap oracle violated: expected 0 ImportData calls, got %d", spyClean.importDataCalls)
			}
			obs, _ := srvClean.deps.Observations.List(ctx, domain.ObservationFilter{Limit: 10})
			sess, _ := srvClean.deps.Sessions.List(ctx, "")
			var promptCount int
			if err := srvClean.deps.Observations.DB().QueryRowContext(ctx, "SELECT count(*) FROM user_prompts").Scan(&promptCount); err != nil {
				t.Fatalf("cap oracle violated: count user_prompts failed: %v", err)
			}
			prompts, _ := srvClean.deps.Prompts.List(ctx, "p", 10)
			if len(obs) != 0 || len(sess) != 0 || promptCount != 0 || len(prompts) != 0 {
				t.Fatalf("cap oracle violated: obs=%d sess=%d prompts=%d promptStore=%d", len(obs), len(sess), promptCount, len(prompts))
			}
			assertZeroPersisted(t, srvClean)
		}

		// 3. In-memory caller batch immutability
		origObs := domain.Observation{SessionID: "s1", Project: "p", Type: "manual", Title: "T <private>" + canaryT + "</private>", Content: "C", Tags: []string{"tag1"}}
		obsCopy := origObs
		obsCopy.Tags = append([]string(nil), origObs.Tags...)
		staged, err := preflightObservations([]*domain.Observation{&obsCopy, {Title: ""}})
		if err == nil || staged != nil || obsCopy.Title != origObs.Title || obsCopy.Content != origObs.Content {
			t.Fatalf("caller batch mutated or error missing: err=%v, staged=%v, copy=%+v", err, staged, obsCopy)
		}

		origSess := domain.Session{ID: "s1", Project: "p", Directory: ".", Summary: "Sum <private>" + canaryT + "</private>"}
		sessCopy := origSess
		stagedSess, sErr := preflightSessions([]*domain.Session{&sessCopy, {Project: "<private>" + canaryT + "</private>"}})
		if sErr == nil || stagedSess != nil || sessCopy.Summary != origSess.Summary {
			t.Fatalf("caller session batch mutated or error missing: err=%v, staged=%v, copy=%+v", sErr, stagedSess, sessCopy)
		}

		origPrompt := domain.Prompt{SessionID: "s1", Project: "p", Content: "Content <private>" + canaryC + "</private>"}
		promptCopy := origPrompt
		stagedPrompts, pErr := preflightPrompts([]*domain.Prompt{&promptCopy, {Content: ""}})
		if pErr == nil || stagedPrompts != nil || promptCopy.Content != origPrompt.Content {
			t.Fatalf("caller prompt batch mutated or error missing: err=%v, staged=%v, copy=%+v", pErr, stagedPrompts, promptCopy)
		}
	})
}
