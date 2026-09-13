package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/mcp/memorycontract"
)

func TestCortexPrivacyRelation_ReasoningRedactedOnCreateEdge(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "proj")
	a, b := saveObs(t, stores, "A", "proj", "s1"), saveObs(t, stores, "B", "proj", "s1")
	const canary = "canary_rel_secret_1122"

	rel := callTool(t, handleRelate(stores), map[string]any{
		"from_id": float64(a.ID), "to_id": float64(b.ID), "relation_type": "references",
		"reasoning": "Audit approved via <private>" + canary + "</private> verified",
	})
	if rel.IsError || strings.Contains(resultText(rel), canary) {
		t.Fatalf("cortex_relate error or leaked canary: %s", resultText(rel))
	}

	edges, err := stores.Graph.GetEdgesForObservation(context.Background(), a.ID)
	if err != nil || len(edges) == 0 || edges[0].Reasoning != "Audit approved via [REDACTED] verified" {
		t.Fatalf("unexpected edge or reasoning: edges=%+v, err=%v", edges, err)
	}
}
func TestCortexPrivacyRelation_MalformedReasoningOrMetadataRejectedZeroEdge(t *testing.T) {
	const canary = "canary_rel_malformed_3344"
	cases := []struct {
		name string
		args map[string]any
	}{
		{"unclosed reasoning", map[string]any{"relation_type": "references", "reasoning": "Why <private>" + canary}},
		{"stray closing reasoning", map[string]any{"relation_type": "references", "reasoning": "Why </private>"}},
		{"nested reasoning", map[string]any{"relation_type": "references", "reasoning": "Why <private>a <private>b</private></private>"}},
		{"marker in source", map[string]any{"relation_type": "references", "source": "<private>" + canary + "</private>", "reasoning": "ok"}},
		{"marker in relation_type", map[string]any{"relation_type": "<private>" + canary + "</private>", "reasoning": "ok"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stores := setupTestStores(t)
			createSession(t, stores, "s1", "proj")
			a, b := saveObs(t, stores, "A", "proj", "s1"), saveObs(t, stores, "B", "proj", "s1")
			args := map[string]any{"from_id": float64(a.ID), "to_id": float64(b.ID)}
			for k, v := range tc.args {
				args[k] = v
			}
			res := callTool(t, handleRelate(stores), args)
			if !res.IsError || strings.Contains(resultText(res), canary) {
				t.Fatalf("expected error without canary: isErr=%v, text=%s", res.IsError, resultText(res))
			}
			if count, _ := stores.Graph.CountAllEdges(context.Background()); count != 0 {
				t.Fatalf("expected 0 edges after rejection, got %d", count)
			}
		})
	}
}

func TestCortexPrivacyRelation_UnitOfWorkSupersedesAtomicity(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "proj")
	a, b := saveObs(t, stores, "A", "proj", "s1"), saveObs(t, stores, "B", "proj", "s1")
	const canary = "canary_supersedes_secret_5566"

	rel := callTool(t, handleRelate(stores), map[string]any{
		"from_id": float64(a.ID), "to_id": float64(b.ID), "relation_type": "supersedes",
		"reasoning": "Supersedes <private>" + canary + "</private> rule",
	})
	if rel.IsError || strings.Contains(resultText(rel), canary) {
		t.Fatalf("cortex_relate supersedes failed or leaked: %s", resultText(rel))
	}

	edges, err := stores.Graph.GetEdgesForObservation(context.Background(), a.ID)
	if err != nil || len(edges) == 0 || edges[0].Reasoning != "Supersedes [REDACTED] rule" {
		t.Fatalf("stored reasoning mismatch: edges=%+v, err=%v", edges, err)
	}
}

func TestCortexPrivacyGraph_BoundedRelationTraversalRedacted(t *testing.T) {
	stores := setupTestStores(t)
	createSession(t, stores, "s1", "proj")
	a, b := saveObs(t, stores, "A", "proj", "s1"), saveObs(t, stores, "B", "proj", "s1")
	const canary = "canary_graph_secret_7788"

	rel := callTool(t, handleRelate(stores), map[string]any{
		"from_id": float64(a.ID), "to_id": float64(b.ID), "relation_type": "references",
		"reasoning": "Traversed via <private>" + canary + "</private> rule",
	})
	if rel.IsError || strings.Contains(resultText(rel), canary) {
		t.Fatalf("cortex_relate error or leaked canary: %s", resultText(rel))
	}

	// 1. Verify cortex_graph_relationships returns redacted reasoning and no canary leak
	relRes := callTool(t, handleGraphRelationships(stores), map[string]any{
		"observation_id": float64(a.ID),
	})
	if relRes.IsError || strings.Contains(resultText(relRes), canary) {
		t.Fatalf("cortex_graph_relationships errored or leaked canary: %s", resultText(relRes))
	}
	if !strings.Contains(resultText(relRes), "Traversed via [REDACTED] rule") {
		t.Fatalf("cortex_graph_relationships missing redacted reasoning: %s", resultText(relRes))
	}

	// 2. Verify bounded cortex_graph traversal returns related observation without canary leak
	graphRes := callTool(t, handleGraph(stores), map[string]any{
		"observation_id": float64(a.ID),
		"depth":          float64(1),
		"max_visited":    float64(10),
		"max_results":    float64(10),
	})
	if graphRes.IsError || strings.Contains(resultText(graphRes), canary) {
		t.Fatalf("cortex_graph errored or leaked canary: %s", resultText(graphRes))
	}
	if !strings.Contains(resultText(graphRes), "Related observations for ID") || !strings.Contains(resultText(graphRes), "Content for B") {
		t.Fatalf("unexpected graph traversal result: %s", resultText(graphRes))
	}

	// 3. Verify bounded cortex_graph_path finds path without canary leak
	pathRes := callTool(t, handleGraphPath(stores), map[string]any{
		"from_id":     float64(a.ID),
		"to_id":       float64(b.ID),
		"max_depth":   float64(2),
		"max_visited": float64(10),
	})
	if pathRes.IsError || strings.Contains(resultText(pathRes), canary) {
		t.Fatalf("cortex_graph_path errored or leaked canary: %s", resultText(pathRes))
	}
}

func TestCortexPrivacyHandoff_CapabilityTupleMarkersRejectedZeroEffects(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)
	const canary = "canary_mcp_tuple_secret_9988"

	cases := []struct {
		name  string
		tuple any
	}{
		{"nested object", map[string]any{"config": map[string]any{"token": "<private>" + canary + "</private>"}}},
		{"nested array", map[string]any{"tags": []any{"ok", "<private>" + canary + "</private>"}}},
		{"object key", map[string]any{"<private>" + canary + "</private>": "val"}},
		{"unclosed marker", map[string]any{"token": "<private>" + canary}},
		{"stray closing marker", map[string]any{"token": "</private>"}},
		{"decoded nested JSON", map[string]any{"raw": `{"secret":"<private>` + canary + `</private>"}`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "k-mcp-tuple-" + tc.name
			args := handoffArgs(key, "tuple test content", nil)
			args["capability_tuple"] = tc.tuple
			res := callTool(t, handler, args)
			if !res.IsError || strings.Contains(resultText(res), canary) {
				t.Fatalf("expected error without canary leak: isError=%v text=%s", res.IsError, resultText(res))
			}
			if count, state, _ := handoffReceiptRow(t, db, "local/project:handoff-demo", key); count != 0 || state != "" {
				t.Fatalf("expected 0 receipts, got count=%d state=%s", count, state)
			}
			if got := handoffCountObservations(t, db, "tuple test content"); got != 0 {
				t.Fatalf("expected 0 observations, got %d", got)
			}
		})
	}
}

func TestCortexPrivacyHandoff_ValidCapabilityTuplePreservedAndReplays(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)
	const scope = "local/project:handoff-demo"
	const key = "k-mcp-valid-tuple"

	tuple := json.RawMessage(`{"z":1.00,"agent":"cortex-worker","scopes":["read","write"],"a":1e+03}`)
	args := handoffArgs(key, "valid tuple content", nil)
	args["capability_tuple"] = tuple

	res1 := callTool(t, handler, args)
	if res1.IsError {
		t.Fatalf("first handoff failed: %s", resultText(res1))
	}
	save1 := structuredSave(t, res1)
	if save1.Status != string(domain.WriteStatusCreated) || save1.ObservationRef.LocalID == nil {
		t.Fatalf("unexpected save1: %+v", save1)
	}
	if count, state, _ := handoffReceiptRow(t, db, scope, key); count != 1 || state != "committed" {
		t.Fatalf("expected 1 committed receipt, got count=%d state=%s", count, state)
	}
	var receiptPayload string
	var receiptHash []byte
	if err := db.QueryRow(`SELECT canonical_payload, payload_hash FROM handoff_receipts WHERE scope=? AND key=?`, scope, key).Scan(&receiptPayload, &receiptHash); err != nil {
		t.Fatalf("failed to query receipt: %v", err)
	}
	if !strings.Contains(receiptPayload, `"capability_tuple":{"a":1e+03,"agent":"cortex-worker","scopes":["read","write"],"z":1.00}`) {
		t.Fatalf("receipt payload missing capability tuple: %s", receiptPayload)
	}
	if want := sha256.Sum256([]byte(receiptPayload)); !bytes.Equal(receiptHash, want[:]) {
		t.Fatal("receipt hash does not match canonical payload")
	}
	if string(tuple) != `{"z":1.00,"agent":"cortex-worker","scopes":["read","write"],"a":1e+03}` {
		t.Fatalf("caller-owned capability tuple was mutated: %s", tuple)
	}
	if got := handoffCountObservations(t, db, "valid tuple content"); got != 1 {
		t.Fatalf("observations count = %d, want 1", got)
	}

	res2 := callTool(t, handler, args)
	if res2.IsError {
		t.Fatalf("replay handoff failed: %s", resultText(res2))
	}
	save2 := structuredSave(t, res2)
	if save2.Status != string(domain.WriteStatusReplayed) {
		t.Fatalf("status = %q, want replayed", save2.Status)
	}
	if *save2.ObservationRef.LocalID != *save1.ObservationRef.LocalID {
		t.Fatalf("local_id mismatch: %d != %d", *save2.ObservationRef.LocalID, *save1.ObservationRef.LocalID)
	}
	if count, state, _ := handoffReceiptRow(t, db, scope, key); count != 1 || state != "committed" {
		t.Fatalf("receipt count after replay = %d", count)
	}
	if got := handoffCountObservations(t, db, "valid tuple content"); got != 1 {
		t.Fatalf("observations count after replay = %d, want 1", got)
	}
}

func TestCortexPrivacyHandoff_RedactedProseAndReplay(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)
	target := saveObs(t, stores, "Target", "handoff-demo", handoffSessionID)
	const scope = "local/project:handoff-demo"
	const key = "k-mcp-redacted-handoff"
	const canaryT = "canary_mcp_title_5566"
	const canaryC = "canary_mcp_content_1122"
	const canaryR = "canary_mcp_reason_3344"

	args := handoffArgs(key, "Handoff body with <private>"+canaryC+"</private> secret", map[string]any{
		"target": map[string]any{
			"local_id": float64(target.ID),
		},
		"type":      "references",
		"reasoning": "Linking due to <private>" + canaryR + "</private> rationale",
	})
	observation := args["observation"].(map[string]any)
	originalTitle := observation["title"].(string)
	originalContent := observation["content"].(string)
	originalReasoning := args["relation"].(map[string]any)["reasoning"].(string)
	observation["title"] = "Handoff <private>" + canaryT + "</private> title"

	// 1. First execution creates observation, edge, and receipt with redactions
	res1 := callTool(t, handler, args)
	if res1.IsError || strings.Contains(resultText(res1), canaryC) || strings.Contains(resultText(res1), canaryR) {
		t.Fatalf("first handoff failed or leaked canary: isError=%v text=%s", res1.IsError, resultText(res1))
	}
	save1 := structuredSave(t, res1)
	if save1.Status != string(domain.WriteStatusCreated) || save1.ObservationRef.LocalID == nil {
		t.Fatalf("unexpected save1: %+v", save1)
	}
	obsID := *save1.ObservationRef.LocalID

	var title string
	if err := db.QueryRow("SELECT title FROM observations WHERE id = ?", obsID).Scan(&title); err != nil {
		t.Fatalf("failed to query observation title: %v", err)
	}
	if title != "Handoff [REDACTED] title" || strings.Contains(title, canaryT) {
		t.Fatalf("observation title not persisted properly: %q", title)
	}

	var content string
	if err := db.QueryRow("SELECT content FROM observations WHERE id = ?", obsID).Scan(&content); err != nil {
		t.Fatalf("failed to query observation: %v", err)
	}
	if !strings.Contains(content, "Handoff body with [REDACTED] secret") || strings.Contains(content, canaryC) {
		t.Fatalf("observation content not redacted properly: %q", content)
	}

	var reasoning string
	if err := db.QueryRow("SELECT reasoning FROM edges WHERE from_obs_id = ? AND to_obs_id = ?", obsID, target.ID).Scan(&reasoning); err != nil {
		t.Fatalf("failed to query edge: %v", err)
	}
	if !strings.Contains(reasoning, "Linking due to [REDACTED] rationale") || strings.Contains(reasoning, canaryR) {
		t.Fatalf("edge reasoning not redacted properly: %q", reasoning)
	}

	if count, state, initial := handoffReceiptRow(t, db, scope, key); count != 1 || state != "committed" || initial != "created" {
		t.Fatalf("expected 1 committed created receipt, got count=%d state=%s initial=%s", count, state, initial)
	}
	var payload string
	var payloadHash []byte
	var receiptState, receiptInitialStatus string
	var receiptObsID int64
	if err := db.QueryRow("SELECT canonical_payload, payload_hash, state, initial_status, observation_id FROM handoff_receipts WHERE scope = ? AND key = ?", scope, key).Scan(&payload, &payloadHash, &receiptState, &receiptInitialStatus, &receiptObsID); err != nil {
		t.Fatalf("failed to query receipt: %v", err)
	}
	if receiptState != "committed" || receiptInitialStatus != "created" || receiptObsID != obsID {
		t.Fatalf("receipt metadata invalid: state=%q initial=%q obsID=%d want=%d", receiptState, receiptInitialStatus, receiptObsID, obsID)
	}
	if !strings.Contains(payload, "[REDACTED]") || strings.Contains(payload, canaryC) || strings.Contains(payload, canaryR) || strings.Contains(payload, canaryT) {
		t.Fatalf("receipt payload leaked canary or missing redaction: %s", payload)
	}
	if !strings.Contains(payload, "Handoff [REDACTED] title") {
		t.Fatalf("canonical receipt missing exact protected title: %s", payload)
	}
	var canonical domain.CanonicalHandoff
	if err := json.Unmarshal([]byte(payload), &canonical); err != nil {
		t.Fatalf("failed to decode canonical receipt payload: %v", err)
	}
	if canonical.Observation.Title != "Handoff [REDACTED] title" {
		t.Fatalf("canonical receipt title mismatch: %q", canonical.Observation.Title)
	}
	if canonical.Observation.Content != "Handoff body with [REDACTED] secret" {
		t.Fatalf("canonical receipt content mismatch: %q", canonical.Observation.Content)
	}
	if canonical.Relation == nil || canonical.Relation.Reasoning != "Linking due to [REDACTED] rationale" {
		t.Fatalf("canonical receipt reasoning mismatch: %+v", canonical.Relation)
	}
	if want := sha256.Sum256([]byte(payload)); !bytes.Equal(payloadHash, want[:]) {
		t.Fatal("receipt hash does not match protected canonical payload")
	}
	if observation["title"] != "Handoff <private>canary_mcp_title_5566</private> title" || observation["content"] != originalContent || args["relation"].(map[string]any)["reasoning"] != originalReasoning {
		t.Fatalf("caller-owned handoff arguments were mutated: title=%q content=%q reasoning=%q", observation["title"], observation["content"], args["relation"].(map[string]any)["reasoning"])
	}

	// 2. Replay with identical raw arguments containing markers
	res2 := callTool(t, handler, args)
	if res2.IsError || strings.Contains(resultText(res2), canaryC) || strings.Contains(resultText(res2), canaryR) {
		t.Fatalf("replay failed or leaked canary: isError=%v text=%s", res2.IsError, resultText(res2))
	}
	save2 := structuredSave(t, res2)
	if save2.Status != string(domain.WriteStatusReplayed) || *save2.ObservationRef.LocalID != obsID {
		t.Fatalf("unexpected replay status or local_id mismatch: status=%q local_id=%v want=%d", save2.Status, save2.ObservationRef.LocalID, obsID)
	}
	if count, state, _ := handoffReceiptRow(t, db, scope, key); count != 1 || state != "committed" {
		t.Fatalf("receipt count after replay = %d", count)
	}
	if got := handoffCountObservations(t, db, "Handoff body with [REDACTED] secret"); got != 1 {
		t.Fatalf("observation count after replay = %d, want 1", got)
	}

	// 3. Replay with already-redacted text matches canonical identity
	redactedArgs := handoffArgs(key, "Handoff body with [REDACTED] secret", map[string]any{
		"target": map[string]any{
			"local_id": float64(target.ID),
		},
		"type":      "references",
		"reasoning": "Linking due to [REDACTED] rationale",
	})
	redactedArgs["observation"].(map[string]any)["title"] = "Handoff [REDACTED] title"
	res3 := callTool(t, handler, redactedArgs)
	if res3.IsError {
		t.Fatalf("redacted replay failed: %s", resultText(res3))
	}
	save3 := structuredSave(t, res3)
	if save3.Status != string(domain.WriteStatusReplayed) || *save3.ObservationRef.LocalID != obsID {
		t.Fatalf("redacted replay status=%q local_id=%v", save3.Status, save3.ObservationRef.LocalID)
	}
	if count, state, _ := handoffReceiptRow(t, db, scope, key); count != 1 || state != "committed" {
		t.Fatalf("receipt count after redacted replay = %d", count)
	}
	if got := handoffCountObservations(t, db, "Handoff body with [REDACTED] secret"); got != 1 {
		t.Fatalf("observation count after redacted replay = %d, want 1", got)
	}
	var replayPayload string
	if err := db.QueryRow("SELECT canonical_payload FROM handoff_receipts WHERE scope = ? AND key = ?", scope, key).Scan(&replayPayload); err != nil {
		t.Fatalf("failed to query replay receipt: %v", err)
	}
	if replayPayload != payload {
		t.Fatal("raw and already-protected handoffs did not retain one canonical identity")
	}
	if originalTitle != "Durable handoff" {
		t.Fatalf("unexpected fixture title: %q", originalTitle)
	}

	// 4. Conflicting handoff payload rejects with conflict code and zero effects
	const canaryConf = "canary_mcp_conf_8899"
	conflictArgs := handoffArgs(key, "Conflict body <private>"+canaryConf+"</private>", nil)
	resConf := callTool(t, handler, conflictArgs)
	if !resConf.IsError || strings.Contains(resultText(resConf), canaryConf) {
		t.Fatalf("expected conflict error without canary: isError=%v text=%s", resConf.IsError, resultText(resConf))
	}
	errConf := structuredError(t, resConf)
	if errConf.Error.Code != memorycontract.CodeConflict {
		t.Fatalf("expected CodeConflict, got %q", errConf.Error.Code)
	}
	if count, state, _ := handoffReceiptRow(t, db, scope, key); count != 1 || state != "committed" {
		t.Fatalf("receipt mutated on conflict: count=%d state=%s", count, state)
	}
	if got := handoffCountObservations(t, db, "Conflict body [REDACTED]"); got != 0 {
		t.Fatalf("conflicting observation materialized: %d", got)
	}

	// 5. Non-existent relation target rolls back atomically
	const canaryRB = "canary_mcp_rb_404"
	rbKey := "k-mcp-rb-notfound"
	rbArgs := handoffArgs(rbKey, "Rollback body <private>"+canaryRB+"</private>", map[string]any{
		"target": map[string]any{"local_id": float64(999999)},
		"type":   "references",
	})
	resRB := callTool(t, handler, rbArgs)
	if !resRB.IsError || strings.Contains(resultText(resRB), canaryRB) {
		t.Fatalf("expected rollback error without canary: isError=%v text=%s", resRB.IsError, resultText(resRB))
	}
	if count, _, _ := handoffReceiptRow(t, db, scope, rbKey); count != 0 {
		t.Fatalf("receipt materialized on rollback: %d", count)
	}
	if got := handoffCountObservations(t, db, "Rollback body [REDACTED]"); got != 0 {
		t.Fatalf("observation materialized on rollback: %d", got)
	}
}

func TestCortexPrivacyHandoff_MalformedProseAndMetadataRejectedZeroEffects(t *testing.T) {
	stores, db := setupHandoffStores(t)
	handler := handleHandoff(stores)
	target := saveObs(t, stores, "Target", "handoff-demo", handoffSessionID)
	const canary = "canary_mcp_prose_meta_1234"
	const scope = "local/project:handoff-demo"

	cases := []struct {
		name   string
		mutate func(args map[string]any)
	}{
		{"unclosed title", func(a map[string]any) { a["observation"].(map[string]any)["title"] = "Title <private>" + canary }},
		{"stray closing title", func(a map[string]any) { a["observation"].(map[string]any)["title"] = "Title </private>" }},
		{"unclosed content", func(a map[string]any) { a["observation"].(map[string]any)["content"] = "Body <private>" + canary }},
		{"nested content", func(a map[string]any) {
			a["observation"].(map[string]any)["content"] = "<private>a <private>b</private></private>"
		}},
		{"pure private title", func(a map[string]any) {
			a["observation"].(map[string]any)["title"] = "<private>" + canary + "</private>"
		}},
		{"pure private content", func(a map[string]any) {
			a["observation"].(map[string]any)["content"] = "<private>" + canary + "</private>"
		}},
		{"marker in tag", func(a map[string]any) {
			a["observation"].(map[string]any)["tags"] = []any{"<private>" + canary + "</private>"}
		}},
		{"marker in idempotency_key", func(a map[string]any) { a["idempotency_key"] = "<private>" + canary + "</private>" }},
		{"unclosed reasoning", func(a map[string]any) { a["relation"].(map[string]any)["reasoning"] = "Why <private>" + canary }},
		{"marker in relation_type", func(a map[string]any) { a["relation"].(map[string]any)["type"] = "<private>" + canary + "</private>" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "k-mcp-neg-" + tc.name
			args := handoffArgs(key, "prose neg content", map[string]any{
				"target": map[string]any{"local_id": float64(target.ID)},
				"type":   "references",
			})
			tc.mutate(args)
			res := callTool(t, handler, args)
			if !res.IsError || strings.Contains(resultText(res), canary) {
				t.Fatalf("expected error without canary leak: isError=%v text=%s", res.IsError, resultText(res))
			}
			if count, state, _ := handoffReceiptRow(t, db, scope, key); count != 0 || state != "" {
				t.Fatalf("expected 0 receipts, got count=%d state=%s", count, state)
			}
			if got := handoffCountObservations(t, db, "prose neg content"); got != 0 {
				t.Fatalf("expected 0 observations for rejected handoff, got %d", got)
			}
		})
	}
}
