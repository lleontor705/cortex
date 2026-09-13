package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
)

func TestMCPPrivacy_SaveRedaction(t *testing.T) {
	stores := setupTestStores(t)
	const canaryT = "canary_mcp_save_title_1122"
	const canaryC = "canary_mcp_save_content_3344"

	res := callTool(t, handleSave(stores), map[string]any{
		"title":   "Save <private>" + canaryT + "</private> note",
		"content": "Body with <private>" + canaryC + "</private> secret",
		"project": "demo",
		"type":    "decision",
	})
	if res.IsError {
		t.Fatalf("cortex_save returned error: %s", resultText(res))
	}
	text := resultText(res)
	if strings.Contains(text, canaryT) || strings.Contains(text, canaryC) {
		t.Fatalf("cortex_save leaked canary: %s", text)
	}
	if !strings.Contains(text, "[REDACTED]") {
		t.Fatalf("cortex_save missing [REDACTED]: %s", text)
	}

	saveResult := structuredSave(t, res)
	if saveResult.Status != string(domain.WriteStatusCreated) || saveResult.ObservationRef.LocalID == nil {
		t.Fatalf("unexpected structured save result: %+v", saveResult)
	}

	obs, err := stores.Observations.GetByID(context.Background(), *saveResult.ObservationRef.LocalID)
	if err != nil {
		t.Fatalf("get observation: %v", err)
	}
	if strings.Contains(obs.Title, canaryT) || strings.Contains(obs.Content, canaryC) {
		t.Fatalf("database leaked canary: title=%q content=%q", obs.Title, obs.Content)
	}
	if obs.Title != "Save [REDACTED] note" || obs.Content != "Body with [REDACTED] secret" {
		t.Fatalf("unexpected persisted text: title=%q content=%q", obs.Title, obs.Content)
	}
}

func TestMCPPrivacy_SaveRejectionZeroEffects(t *testing.T) {
	const canary = "canary_mcp_save_malformed_7788"

	cases := []struct {
		name string
		args map[string]any
	}{
		{"unclosed title", map[string]any{"title": "Title <private>" + canary, "content": "body", "project": "p1"}},
		{"stray closing title", map[string]any{"title": "Title </private>", "content": "body", "project": "p1"}},
		{"unclosed content", map[string]any{"title": "Title", "content": "Body <private>" + canary, "project": "p1"}},
		{"nested content", map[string]any{"title": "Title", "content": "<private>a <private>b</private></private>", "project": "p1"}},
		{"pure private title", map[string]any{"title": "<private>" + canary + "</private>", "content": "body", "project": "p1"}},
		{"pure private content", map[string]any{"title": "Title", "content": "<private>" + canary + "</private>", "project": "p1"}},
		{"marker in project", map[string]any{"title": "Title", "content": "body", "project": "<private>" + canary + "</private>"}},
		{"marker in topic", map[string]any{"title": "Title", "content": "body", "project": "p1", "topic_key": "<private>" + canary + "</private>"}},
		{"marker in scope", map[string]any{"title": "Title", "content": "body", "project": "p1", "scope": "<private>" + canary + "</private>"}},
		{"marker in type", map[string]any{"title": "Title", "content": "body", "project": "p1", "type": "<private>" + canary + "</private>"}},
		{"marker in source", map[string]any{"title": "Title", "content": "body", "project": "p1", "source": "<private>" + canary + "</private>"}},
		{"marker in session_id", map[string]any{"title": "Title", "content": "body", "project": "p1", "session_id": "<private>" + canary + "</private>"}},
		{"marker in tags", map[string]any{"title": "Title", "content": "body", "project": "p1", "tags": "<private>" + canary + "</private>"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stores := setupTestStores(t)
			res := callTool(t, handleSave(stores), tc.args)
			if !res.IsError {
				t.Fatalf("expected error result, got success: %s", resultText(res))
			}
			text := resultText(res)
			if strings.Contains(text, canary) {
				t.Fatalf("error result leaked canary: %s", text)
			}

			// Verify ZERO observations and ZERO sessions created
			obsList, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Limit: 10})
			if err != nil {
				t.Fatalf("list observations: %v", err)
			}
			if len(obsList) != 0 {
				t.Fatalf("expected 0 observations after rejection, got %d", len(obsList))
			}
			sessions, err := stores.Sessions.List(context.Background(), "")
			if err != nil {
				t.Fatalf("list sessions: %v", err)
			}
			if len(sessions) != 0 {
				t.Fatalf("expected 0 sessions after rejection, got %d", len(sessions))
			}
		})
	}
}

func TestMCPPrivacy_SessionSummaryRejectionZeroEffects(t *testing.T) {
	const canary = "canary_mcp_summary_secret_1234"
	stores := setupTestStores(t)

	res := callTool(t, handleSessionSummary(stores), map[string]any{
		"content": "Unclosed summary <private>" + canary,
		"project": "proj",
	})
	if !res.IsError || strings.Contains(resultText(res), canary) {
		t.Fatalf("expected error without canary leak: isErr=%v text=%s", res.IsError, resultText(res))
	}

	obsList, _ := stores.Observations.List(context.Background(), domain.ObservationFilter{Limit: 10})
	if len(obsList) != 0 {
		t.Fatalf("expected 0 observations, got %d", len(obsList))
	}
	sessions, _ := stores.Sessions.List(context.Background(), "")
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestMCPPrivacy_SavePromptRejectionZeroEffects(t *testing.T) {
	const canary = "canary_mcp_prompt_secret_5678"
	stores := setupTestStores(t)

	res := callTool(t, handleSavePrompt(stores), map[string]any{
		"content": "Unclosed prompt <private>" + canary,
		"project": "proj",
	})
	if !res.IsError || strings.Contains(resultText(res), canary) {
		t.Fatalf("expected error without canary leak: isErr=%v text=%s", res.IsError, resultText(res))
	}

	sessions, _ := stores.Sessions.List(context.Background(), "")
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestMCPPrivacy_UpdateRejectionZeroEffects(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "proj")
	obs := saveObs(t, stores, "Original Title", "proj", "s1")
	const canary = "canary_mcp_update_secret_9900"

	res := callTool(t, handleUpdate(stores), map[string]any{
		"id":    float64(obs.ID),
		"title": "Bad Title <private>" + canary,
	})
	if !res.IsError || strings.Contains(resultText(res), canary) {
		t.Fatalf("expected error without canary leak: isErr=%v text=%s", res.IsError, resultText(res))
	}

	current, err := stores.Observations.GetByID(context.Background(), obs.ID)
	if err != nil {
		t.Fatalf("get observation: %v", err)
	}
	if current.Title != "Original Title" {
		t.Fatalf("observation title mutated on rejection: %q", current.Title)
	}
}

func TestMCPPrivacy_SessionStartEnd(t *testing.T) {
	stores := setupTestStores(t)
	const canary = "canary_mcp_sess_7722"

	// Start with invalid project
	resStart := callTool(t, handleSessionStart(stores), map[string]any{
		"id":      "s-start-1",
		"project": "<private>" + canary + "</private>",
	})
	if !resStart.IsError || strings.Contains(resultText(resStart), canary) {
		t.Fatalf("session start expected error without canary: %s", resultText(resStart))
	}
	sessions, _ := stores.Sessions.List(context.Background(), "")
	if len(sessions) != 0 {
		t.Fatalf("expected 0 sessions, got %d", len(sessions))
	}

	// Create valid session, then end with invalid summary
	createSession(t, stores, "s-valid", "proj")
	resEnd := callTool(t, handleSessionEnd(stores), map[string]any{
		"id":      "s-valid",
		"summary": "Bad summary <private>" + canary,
	})
	if !resEnd.IsError || strings.Contains(resultText(resEnd), canary) {
		t.Fatalf("session end expected error without canary: %s", resultText(resEnd))
	}
}

func TestMCPPrivacy_CapturePassiveRejectionZeroEffects(t *testing.T) {
	const canary = "canary_mcp_passive_bad_5566"

	cases := []struct {
		name string
		args map[string]any
	}{
		{"unclosed content", map[string]any{
			"content": "## Key Learnings:\n1. Unclosed <private>" + canary,
			"project": "demo",
		}},
		{"stray closing content", map[string]any{
			"content": "## Key Learnings:\n1. Stray </private> marker",
			"project": "demo",
		}},
		{"nested content", map[string]any{
			"content": "## Key Learnings:\n1. Nested <private>a <private>b</private></private>",
			"project": "demo",
		}},
		{"pure private content", map[string]any{
			"content": "<private>" + canary + "</private>",
			"project": "demo",
		}},
		{"unclosed marker without learnings header", map[string]any{
			"content": "Notes without header <private>" + canary,
			"project": "demo",
		}},
		{"malformed tag content", map[string]any{
			"content": "## Key Learnings:\n1. Malformed <private marker",
			"project": "demo",
		}},
		{"marker in project", map[string]any{
			"content": "## Key Learnings:\n1. Valid learning text here that is long enough",
			"project": "<private>" + canary + "</private>",
		}},
		{"marker in session_id", map[string]any{
			"content":    "## Key Learnings:\n1. Valid learning text here that is long enough",
			"project":    "demo",
			"session_id": "<private>" + canary + "</private>",
		}},
		{"marker in source", map[string]any{
			"content": "## Key Learnings:\n1. Valid learning text here that is long enough",
			"project": "demo",
			"source":  "<private>" + canary + "</private>",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stores := setupTestStores(t)
			origArgs := make(map[string]any, len(tc.args))
			for k, v := range tc.args {
				origArgs[k] = v
			}
			res := callTool(t, handleCapturePassive(stores), tc.args)
			if !res.IsError {
				t.Fatalf("expected error result, got success: %s", resultText(res))
			}
			text := resultText(res)
			if strings.Contains(text, canary) {
				t.Fatalf("error result leaked canary: %s", text)
			}

			// Verify caller argument preservation
			for k, v := range origArgs {
				if tc.args[k] != v {
					t.Fatalf("caller argument %q was mutated: got %v, want %v", k, tc.args[k], v)
				}
			}

			// Verify causal invariant: ZERO sessions and ZERO observations created
			sessions, err := stores.Sessions.List(context.Background(), "")
			if err != nil {
				t.Fatalf("list sessions: %v", err)
			}
			if len(sessions) != 0 {
				t.Fatalf("expected 0 sessions after rejection, got %d", len(sessions))
			}

			obsList, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Limit: 10})
			if err != nil {
				t.Fatalf("list observations: %v", err)
			}
			if len(obsList) != 0 {
				t.Fatalf("expected 0 observations after rejection, got %d", len(obsList))
			}
		})
	}
}

func TestMCPPrivacy_CapturePassiveRedaction(t *testing.T) {
	stores := setupTestStores(t)
	const canaryPreamble = "canary_passive_preamble_1122"
	const canaryDB = "canary_passive_db_3344"
	const canaryAuth = "canary_passive_auth_5566"

	content := "Preamble with context <private>" + canaryPreamble + "</private> info.\n\n" +
		"## Key Learnings:\n" +
		"1. Database password <private>" + canaryDB + "</private> stored in vault\n" +
		"2. Authentication header <private>" + canaryAuth + "</private> rotated on expiry\n"

	args := map[string]any{
		"content": content,
		"project": "demo",
	}
	res := callTool(t, handleCapturePassive(stores), args)
	if res.IsError {
		t.Fatalf("cortex_capture_passive returned error: %s", resultText(res))
	}
	text := resultText(res)
	if strings.Contains(text, canaryPreamble) || strings.Contains(text, canaryDB) || strings.Contains(text, canaryAuth) {
		t.Fatalf("cortex_capture_passive leaked canary: %s", text)
	}
	if !strings.Contains(text, "Passive capture complete: extracted=2 saved=2 duplicates=0 failed=0") {
		t.Fatalf("unexpected summary text: %s", text)
	}
	// Verify caller argument preservation
	if args["content"] != content || args["project"] != "demo" {
		t.Fatalf("caller-owned arguments were mutated: %+v", args)
	}

	// Verify exact persisted protected observations in store
	obsList, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(obsList) != 2 {
		t.Fatalf("expected 2 observations, got %d", len(obsList))
	}

	// Sort / verify both observations
	var dbObs, authObs *domain.Observation
	for _, o := range obsList {
		if strings.Contains(o.Title, "Database password") {
			dbObs = o
		} else if strings.Contains(o.Title, "Authentication header") {
			authObs = o
		}
	}
	if dbObs == nil || authObs == nil {
		t.Fatalf("could not find both expected observations: %+v", obsList)
	}

	// 1. Verify db observation exact values
	if strings.Contains(dbObs.Title, canaryDB) || strings.Contains(dbObs.Content, canaryDB) || strings.Contains(dbObs.TopicKey, canaryDB) {
		t.Fatalf("db observation leaked canary: title=%q content=%q topic_key=%q", dbObs.Title, dbObs.Content, dbObs.TopicKey)
	}
	wantDBTitle := "Database password [REDACTED] stored in vault"
	wantDBContent := "Database password [REDACTED] stored in vault"
	wantDBTopicKey := "learning/database-password-redacted-stored-in-vault"
	if dbObs.Title != wantDBTitle {
		t.Errorf("db title = %q, want %q", dbObs.Title, wantDBTitle)
	}
	if dbObs.Content != wantDBContent {
		t.Errorf("db content = %q, want %q", dbObs.Content, wantDBContent)
	}
	if dbObs.TopicKey != wantDBTopicKey {
		t.Errorf("db topic_key = %q, want %q", dbObs.TopicKey, wantDBTopicKey)
	}
	if dbObs.Type != "passive" || dbObs.Source != "auto" || dbObs.Scope != "project" {
		t.Errorf("db metadata mismatch: type=%q source=%q scope=%q", dbObs.Type, dbObs.Source, dbObs.Scope)
	}

	// 2. Verify auth observation exact values
	if strings.Contains(authObs.Title, canaryAuth) || strings.Contains(authObs.Content, canaryAuth) || strings.Contains(authObs.TopicKey, canaryAuth) {
		t.Fatalf("auth observation leaked canary: title=%q content=%q topic_key=%q", authObs.Title, authObs.Content, authObs.TopicKey)
	}
	wantAuthTitle := "Authentication header [REDACTED] rotated on expiry"
	wantAuthContent := "Authentication header [REDACTED] rotated on expiry"
	wantAuthTopicKey := "learning/authentication-header-redacted-rotated-on-expiry"
	if authObs.Title != wantAuthTitle {
		t.Errorf("auth title = %q, want %q", authObs.Title, wantAuthTitle)
	}
	if authObs.Content != wantAuthContent {
		t.Errorf("auth content = %q, want %q", authObs.Content, wantAuthContent)
	}
	if authObs.TopicKey != wantAuthTopicKey {
		t.Errorf("auth topic_key = %q, want %q", authObs.TopicKey, wantAuthTopicKey)
	}
	if authObs.Type != "passive" || authObs.Source != "auto" || authObs.Scope != "project" {
		t.Errorf("auth metadata mismatch: type=%q source=%q scope=%q", authObs.Type, authObs.Source, authObs.Scope)
	}

	// 3. Verify session was created
	sessions, err := stores.Sessions.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != "manual-save-demo" {
		t.Fatalf("unexpected sessions: %+v", sessions)
	}

	// 4. Replay identical content: updates existing observations in place without creating new rows
	res2 := callTool(t, handleCapturePassive(stores), args)
	if res2.IsError {
		t.Fatalf("replay returned error: %s", resultText(res2))
	}
	text2 := resultText(res2)
	if !strings.Contains(text2, "Passive capture complete: extracted=2 saved=2 duplicates=0 failed=0") {
		t.Fatalf("unexpected replay summary: %s", text2)
	}
	if args["content"] != content || args["project"] != "demo" {
		t.Fatalf("caller-owned arguments were mutated after replay: %+v", args)
	}
	obsListAfter, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil {
		t.Fatalf("list observations after replay: %v", err)
	}
	if len(obsListAfter) != 2 {
		t.Fatalf("observations count after replay = %d, want 2", len(obsListAfter))
	}
	foundDB, foundAuth := false, false
	for _, o := range obsListAfter {
		switch o.ID {
		case dbObs.ID:
			foundDB = true
		case authObs.ID:
			foundAuth = true
		default:
			t.Errorf("unexpected observation ID %d after replay", o.ID)
		}
	}
	if !foundDB || !foundAuth {
		t.Fatalf("did not find original observation IDs after replay: foundDB=%v foundAuth=%v", foundDB, foundAuth)
	}
}

func TestMCPPrivacy_CapturePassiveCallerStatePreserved(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s-caller-prior", "demo")
	priorObs := saveObs(t, stores, "Prior Title", "demo", "s-caller-prior")

	const canary = "canary_mcp_caller_state_5566"
	callerArgs := map[string]any{
		"content": "## Key Learnings:\n1. Bad learning <private>" + canary,
		"project": "demo",
	}

	// 1. Rejected capture preserves prior caller store state and caller arguments
	res := callTool(t, handleCapturePassive(stores), callerArgs)
	if !res.IsError || strings.Contains(resultText(res), canary) {
		t.Fatalf("expected error without canary leak: isError=%v text=%s", res.IsError, resultText(res))
	}
	wantContent := "## Key Learnings:\n1. Bad learning <private>" + canary
	if callerArgs["content"] != wantContent || callerArgs["project"] != "demo" {
		t.Fatalf("caller-owned arguments were mutated: %+v", callerArgs)
	}

	prior, err := stores.Observations.GetByID(context.Background(), priorObs.ID)
	if err != nil || prior.Title != "Prior Title" || prior.Content != "Content for Prior Title" {
		t.Fatalf("prior observation was mutated on rejected capture: %+v", prior)
	}
	sessions, err := stores.Sessions.List(context.Background(), "")
	if err != nil || len(sessions) != 1 || sessions[0].ID != "s-caller-prior" {
		t.Fatalf("sessions mutated or added on rejected capture: %+v", sessions)
	}
	obsList, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil || len(obsList) != 1 || obsList[0].ID != priorObs.ID {
		t.Fatalf("observations mutated or added on rejected capture: %+v", obsList)
	}

	// 2. Successful capture preserves prior caller store state alongside new observations
	validArgs := map[string]any{
		"content": "## Key Learnings:\n1. Preserved learning with <private>" + canary + "</private> secret",
		"project": "demo",
	}
	origValidContent := validArgs["content"].(string)

	resValid := callTool(t, handleCapturePassive(stores), validArgs)
	if resValid.IsError || strings.Contains(resultText(resValid), canary) {
		t.Fatalf("expected valid capture without canary leak: isError=%v text=%s", resValid.IsError, resultText(resValid))
	}
	if validArgs["content"] != origValidContent || validArgs["project"] != "demo" {
		t.Fatalf("caller-owned arguments were mutated on successful capture: %+v", validArgs)
	}

	priorAfter, err := stores.Observations.GetByID(context.Background(), priorObs.ID)
	if err != nil || priorAfter.Title != "Prior Title" || priorAfter.Content != "Content for Prior Title" {
		t.Fatalf("prior observation was mutated on successful capture: %+v", priorAfter)
	}

	obsListAfter, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil || len(obsListAfter) != 2 {
		t.Fatalf("expected 2 observations (1 prior + 1 new), got %d", len(obsListAfter))
	}

	// 3. Replay preserves prior caller store state and caller arguments
	resReplay := callTool(t, handleCapturePassive(stores), validArgs)
	if resReplay.IsError || strings.Contains(resultText(resReplay), canary) {
		t.Fatalf("expected replay without canary leak: isError=%v text=%s", resReplay.IsError, resultText(resReplay))
	}
	if validArgs["content"] != origValidContent || validArgs["project"] != "demo" {
		t.Fatalf("caller-owned arguments were mutated on replay: %+v", validArgs)
	}
	priorAfterReplay, err := stores.Observations.GetByID(context.Background(), priorObs.ID)
	if err != nil || priorAfterReplay.Title != "Prior Title" || priorAfterReplay.Content != "Content for Prior Title" {
		t.Fatalf("prior observation was mutated on replay: %+v", priorAfterReplay)
	}
	obsListAfterReplay, err := stores.Observations.List(context.Background(), domain.ObservationFilter{Project: "demo"})
	if err != nil || len(obsListAfterReplay) != 2 {
		t.Fatalf("expected 2 observations after replay, got %d", len(obsListAfterReplay))
	}
}
