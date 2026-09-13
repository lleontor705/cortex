package domain_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/lleontor705/cortex/v2/internal/domain/privacy"
)

func TestDomainPrivacyHandoff_CanonicalPayloadAndHashRedaction(t *testing.T) {
	const canaryT, canaryC, canaryR = "canary_t_11", "canary_c_22", "canary_r_33"
	targetID := int64(42)
	req := domain.HandoffRequest{
		IdempotencyKey: "idem-key-priv-1",
		Observation: domain.SaveObservationInput{
			Title: "Cluster <private>" + canaryT + "</private> note", Content: "Data <private>" + canaryC + "</private> end",
			Type: domain.TypeDecision, Project: "cortex", Scope: domain.ScopeProject, SessionID: "sess-1", TopicKey: "t/1",
		},
		Relation: &domain.HandoffRelationInput{
			Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences,
			Reasoning: "Based on <private>" + canaryR + "</private> fact",
		},
	}
	origT, origC, origR := req.Observation.Title, req.Observation.Content, req.Relation.Reasoning

	canonical, payload, hash, err := domain.CanonicalizeHandoff(req)
	if err != nil {
		t.Fatalf("CanonicalizeHandoff failed: %v", err)
	}
	if canonical.Observation.Title != "Cluster [REDACTED] note" || canonical.Observation.Content != "Data [REDACTED] end" || canonical.Relation.Reasoning != "Based on [REDACTED] fact" {
		t.Fatalf("unexpected canonical prose/reasoning: %+v", canonical)
	}
	for _, canary := range []string{canaryT, canaryC, canaryR} {
		if strings.Contains(string(payload), canary) {
			t.Fatalf("canonical payload leaked canary %q", canary)
		}
	}
	if hash != sha256.Sum256(payload) {
		t.Fatal("hash does not match sha256 of canonical payload")
	}
	if req.Observation.Title != origT || req.Observation.Content != origC || req.Relation.Reasoning != origR {
		t.Fatal("caller-owned request was mutated")
	}
}

func TestDomainPrivacyHandoff_ReplayEquivalence(t *testing.T) {
	const canary = "canary_replay_sec_7788"
	targetID := int64(10)
	rawReq := domain.HandoffRequest{
		IdempotencyKey: "replay-key-1",
		Observation: domain.SaveObservationInput{
			Title: "Title <private>" + canary + "</private>", Content: "Body <private>" + canary + "</private>",
			Type: domain.TypeManual, Project: "cortex", Scope: domain.ScopeProject, SessionID: "sess-1",
		},
		Relation: &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: "references", Reasoning: "Why <private>" + canary + "</private>"},
	}
	redactedReq := domain.HandoffRequest{
		IdempotencyKey: "replay-key-1",
		Observation: domain.SaveObservationInput{
			Title: "Title [REDACTED]", Content: "Body [REDACTED]",
			Type: domain.TypeManual, Project: "cortex", Scope: domain.ScopeProject, SessionID: "sess-1",
		},
		Relation: &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: "references", Reasoning: "Why [REDACTED]"},
	}

	can1, pay1, hash1, err1 := domain.CanonicalizeHandoff(rawReq)
	can2, pay2, hash2, err2 := domain.CanonicalizeHandoff(redactedReq)
	if err1 != nil || err2 != nil || !bytes.Equal(pay1, pay2) || hash1 != hash2 || can1.Observation.Title != can2.Observation.Title {
		t.Fatalf("replay equivalence failed: err1=%v err2=%v pay1=%s pay2=%s", err1, err2, pay1, pay2)
	}
}

func TestDomainPrivacyHandoff_MalformedAndRequiredEmptyRejections(t *testing.T) {
	const canary = "canary_reject_secret_9900"
	targetID := int64(5)
	base := func() domain.HandoffRequest {
		return domain.HandoffRequest{
			IdempotencyKey: "valid-key",
			Observation:    domain.SaveObservationInput{Title: "Title", Content: "Content", Type: "manual", Project: "p", Scope: "project", SessionID: "s1"},
			Relation:       &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: "references", Reasoning: "Reason"},
		}
	}
	cases := []struct {
		name     string
		mutate   func(*domain.HandoffRequest)
		wantCode privacy.ErrorCode
	}{
		{"unclosed marker in title", func(r *domain.HandoffRequest) { r.Observation.Title = "Title <private>" + canary }, privacy.ErrCodeInvalidMarker},
		{"stray closing in title", func(r *domain.HandoffRequest) { r.Observation.Title = "Title </private>" }, privacy.ErrCodeInvalidMarker},
		{"unclosed marker in content", func(r *domain.HandoffRequest) { r.Observation.Content = "Body <private>" + canary }, privacy.ErrCodeInvalidMarker},
		{"nested marker in content", func(r *domain.HandoffRequest) { r.Observation.Content = "<private>a <private>b</private></private>" }, privacy.ErrCodeInvalidMarker},
		{"unclosed marker in reasoning", func(r *domain.HandoffRequest) { r.Relation.Reasoning = "Why <private>" + canary }, privacy.ErrCodeInvalidMarker},
		{"pure private title fails required", func(r *domain.HandoffRequest) { r.Observation.Title = "<private>" + canary + "</private>" }, privacy.ErrCodeRequiredEmpty},
		{"pure private content fails required", func(r *domain.HandoffRequest) { r.Observation.Content = "<private>" + canary + "</private>" }, privacy.ErrCodeRequiredEmpty},
		{"whitespace residual content fails required", func(r *domain.HandoffRequest) { r.Observation.Content = " \t <private>" + canary + "</private> \n " }, privacy.ErrCodeRequiredEmpty},
		{"marker in tag rejected", func(r *domain.HandoffRequest) { r.Observation.Tags = []string{"<private>" + canary + "</private>"} }, privacy.ErrCodeInvalidMarker},
		{"marker in project rejected", func(r *domain.HandoffRequest) { r.Observation.Project = "<private>" + canary + "</private>" }, privacy.ErrCodeInvalidMarker},
		{"marker in idempotency_key rejected", func(r *domain.HandoffRequest) { r.IdempotencyKey = "<private>" + canary + "</private>" }, privacy.ErrCodeInvalidMarker},
		{"marker in relation_type rejected", func(r *domain.HandoffRequest) { r.Relation.Type = "<private>" + canary + "</private>" }, privacy.ErrCodeInvalidMarker},
		{"marker in capability_tuple value", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = []byte(`{"token":"<private>` + canary + `</private>"}`)
		}, privacy.ErrCodeInvalidMarker},
		{"marker in capability_tuple nested value", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = []byte(`{"agent":{"token":"<private>` + canary + `</private>"}}`)
		}, privacy.ErrCodeInvalidMarker},
		{"marker in capability_tuple array value", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = []byte(`{"roles":["dev","<private>` + canary + `</private>"]}`)
		}, privacy.ErrCodeInvalidMarker},
		{"marker in capability_tuple object key", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = []byte(`{"<private>` + canary + `</private>":"val"}`)
		}, privacy.ErrCodeInvalidMarker},
		{"unclosed marker in capability_tuple", func(r *domain.HandoffRequest) { r.CapabilityTuple = []byte(`{"token":"<private>` + canary + `"}`) }, privacy.ErrCodeInvalidMarker},
		{"stray closing in capability_tuple", func(r *domain.HandoffRequest) { r.CapabilityTuple = []byte(`{"token":"</private>"}`) }, privacy.ErrCodeInvalidMarker},
		{"nested marker in capability_tuple", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = []byte(`{"token":"<private>a <private>b</private></private>"}`)
		}, privacy.ErrCodeInvalidMarker},
		{"marker in capability_tuple decoded nested JSON", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = []byte(`{"raw":"{\"secret\":\"<private>` + canary + `</private>\"}"}`)
		}, privacy.ErrCodeInvalidMarker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := base()
			tc.mutate(&req)
			canonical, payload, _, err := domain.CanonicalizeHandoff(req)
			var privErr *privacy.Error
			if err == nil || !errors.As(err, &privErr) || privErr.Code != tc.wantCode {
				t.Fatalf("expected *privacy.Error with code %q, got err=%v", tc.wantCode, err)
			}
			if strings.Contains(err.Error(), canary) || payload != nil || canonical.Observation.Title != "" {
				t.Fatalf("rejected handoff leaked canary or returned data: %v", err)
			}
		})
	}
}

func TestDomainPrivacyHandoff_CapabilityTupleZeroCanonicalEffects(t *testing.T) {
	const canary = "canary_tuple_isolated_1234"
	targetID := int64(99)
	req := domain.HandoffRequest{
		IdempotencyKey: "tuple-iso-key",
		Observation: domain.SaveObservationInput{
			Title:   "Title <private>sec_t</private>",
			Content: "Content <private>sec_c</private>",
			Type:    domain.TypeDecision,
			Project: "cortex",
			Scope:   domain.ScopeProject,
		},
		Relation: &domain.HandoffRelationInput{
			Target:    domain.ObservationRef{LocalID: &targetID},
			Type:      domain.RelationReferences,
			Reasoning: "Reason <private>sec_r</private>",
		},
		CapabilityTuple: []byte(`{"nested":{"token":"<private>` + canary + `</private>"}}`),
	}

	origTitle := req.Observation.Title
	origContent := req.Observation.Content
	origReason := req.Relation.Reasoning
	origTuple := string(req.CapabilityTuple)

	canonical, payload, hash, err := domain.CanonicalizeHandoff(req)
	var privErr *privacy.Error
	if !errors.As(err, &privErr) || privErr.Code != privacy.ErrCodeInvalidMarker {
		t.Fatalf("expected ErrCodeInvalidMarker, got %v", err)
	}
	if payload != nil || hash != [32]byte{} || canonical.Observation.Title != "" || canonical.CapabilityTuple != nil {
		t.Fatalf("expected zero canonical effects on rejection, got payload=%v hash=%v canonical=%+v", payload, hash, canonical)
	}
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("error leaked canary: %v", err)
	}
	if req.Observation.Title != origTitle || req.Observation.Content != origContent || req.Relation.Reasoning != origReason || string(req.CapabilityTuple) != origTuple {
		t.Fatal("caller request was mutated")
	}

	auth := &recordingAuthorizer{scope: "scoped"}
	exec := &capturingExecutor{}
	coord := domain.NewHandoffCoordinator(auth, exec)
	if _, err := coord.Execute(t.Context(), domain.Principal{}, req); !errors.Is(err, privErr) {
		t.Fatalf("coordinator did not return privacy error: %v", err)
	}
	if auth.calls != 0 || exec.calls != 0 {
		t.Fatalf("coordinator invoked dependencies on rejected tuple: auth=%d exec=%d", auth.calls, exec.calls)
	}

	// Valid tuple behavior preserved
	validReq := req
	validReq.Observation.Title = "Clean Title"
	validReq.Observation.Content = "Clean Content"
	validReq.Relation.Reasoning = "Clean Reason"
	validReq.CapabilityTuple = []byte(`{"b":1.00,"a":1e+03}`)
	validCanonical, validPayload, validHash, err := domain.CanonicalizeHandoff(validReq)
	if err != nil {
		t.Fatalf("valid tuple rejected: %v", err)
	}
	if string(validCanonical.CapabilityTuple) != `{"a":1e+03,"b":1.00}` {
		t.Fatalf("expected canonical key sorting with numeric lexical identity, got %s", validCanonical.CapabilityTuple)
	}
	if validHash != sha256.Sum256(validPayload) {
		t.Fatal("hash does not match valid canonical payload")
	}
}
