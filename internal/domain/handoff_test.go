package domain_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lleontor705/cortex/internal/domain"
)

func TestObservationRefRequiresExactlyOneNamespace(t *testing.T) {
	localID := int64(42)
	publicID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	tests := []struct {
		name    string
		ref     domain.ObservationRef
		wantErr bool
	}{
		{name: "local reference", ref: domain.ObservationRef{LocalID: &localID}},
		{name: "public reference", ref: domain.ObservationRef{PublicID: &publicID}},
		{name: "neither namespace", ref: domain.ObservationRef{}, wantErr: true},
		{name: "both namespaces", ref: domain.ObservationRef{LocalID: &localID, PublicID: &publicID}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCanonicalHandoffBytesAndHash(t *testing.T) {
	targetID := int64(7)
	req := domain.HandoffRequest{
		IdempotencyKey: "delivery-17",
		Observation: domain.SaveObservationInput{
			Title: "Canonical handoff", Content: "preserve café byte-for-byte",
			Type: domain.TypeDecision, Project: "cortex", Scope: domain.ScopeProject,
			SessionID: "session-1", TopicKey: "handoff/canonical", Confidence: 0.75,
			Source: domain.SourceAI, Tags: []string{"handoff", "evidence"},
		},
		Relation: &domain.HandoffRelationInput{
			Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences,
			Weight: 2.5, Confidence: 0.8, Reasoning: "approved design",
		},
		CapabilityTuple: json.RawMessage(`{"available":true,"name":"shell"}`),
	}

	canonical, payload, hash, err := domain.CanonicalizeHandoff(req)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"observation":{"title":"Canonical handoff","content":"preserve café byte-for-byte","type":"decision","project":"cortex","scope":"project","session_id":"session-1","topic_key":"handoff/canonical","confidence":0.75,"source":"ai","tags":["handoff","evidence"]},"relation":{"target":{"local_id":7},"type":"references","weight":2.5,"confidence":0.8,"reasoning":"approved design"},"capability_tuple":{"available":true,"name":"shell"}}`)
	if !bytes.Equal(payload, want) {
		t.Fatalf("canonical payload mismatch\n got: %s\nwant: %s", payload, want)
	}
	wantHash := sha256.Sum256(want)
	if hash != wantHash {
		t.Fatalf("hash = %s, want %s", hex.EncodeToString(hash[:]), hex.EncodeToString(wantHash[:]))
	}
	if !bytes.Equal(canonical.CapabilityTuple, req.CapabilityTuple) {
		t.Fatalf("capability tuple changed: got %s want %s", canonical.CapabilityTuple, req.CapabilityTuple)
	}
}

func TestCanonicalHandoffExcludesKeyAndDistinguishesPayload(t *testing.T) {
	base := handoffRequest("key-a", "same content")
	_, first, firstHash, err := domain.CanonicalizeHandoff(base)
	if err != nil {
		t.Fatal(err)
	}

	withOtherKey := base
	withOtherKey.IdempotencyKey = "key-b"
	_, second, secondHash, err := domain.CanonicalizeHandoff(withOtherKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || firstHash != secondHash {
		t.Fatal("idempotency key must not be part of canonical payload")
	}

	changed := base
	changed.Observation.Content = "different content"
	_, third, thirdHash, err := domain.CanonicalizeHandoff(changed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, third) || firstHash == thirdHash {
		t.Fatal("different canonical payload must produce different bytes and hash")
	}
}

func TestHandoffScopedReplayAndConflict(t *testing.T) {
	authorizer := &recordingAuthorizer{scope: domain.HandoffScope("tenant-a/workspace-a")}
	executor := newRecordingExecutor()
	coordinator := domain.NewHandoffCoordinator(authorizer, executor)
	principal := domain.Principal{Subject: "user-a"}
	req := handoffRequest("shared-key", "payload-a")

	created, err := coordinator.Execute(context.Background(), principal, req)
	if err != nil || created.Status != domain.WriteStatusCreated {
		t.Fatalf("first Execute() = %+v, %v", created, err)
	}
	replayed, err := coordinator.Execute(context.Background(), principal, req)
	if err != nil || replayed.Status != domain.WriteStatusReplayed || replayed.Ref != created.Ref {
		t.Fatalf("replay Execute() = %+v, %v; created = %+v", replayed, err, created)
	}

	conflicting := req
	conflicting.Observation.Content = "payload-b"
	if _, err := coordinator.Execute(context.Background(), principal, conflicting); !errors.Is(err, domain.ErrHandoffConflict) {
		t.Fatalf("conflict error = %v, want ErrHandoffConflict", err)
	}
	if executor.materializations != 1 {
		t.Fatalf("materializations after replay/conflict = %d, want 1", executor.materializations)
	}

	authorizer.scope = domain.HandoffScope("tenant-b/workspace-b")
	otherScope, err := coordinator.Execute(context.Background(), principal, req)
	if err != nil || otherScope.Status != domain.WriteStatusCreated {
		t.Fatalf("same key in another scope = %+v, %v", otherScope, err)
	}
	if executor.materializations != 2 {
		t.Fatalf("materializations across scopes = %d, want 2", executor.materializations)
	}
}

func TestHandoffPreauthorizesCompleteRequestBeforeExecutor(t *testing.T) {
	wantErr := errors.New("relation permission denied")
	authorizer := &recordingAuthorizer{err: wantErr}
	executor := newRecordingExecutor()
	coordinator := domain.NewHandoffCoordinator(authorizer, executor)
	req := handoffRequest("denied", "must not persist")
	targetID := int64(99)
	req.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences}

	if _, err := coordinator.Execute(context.Background(), domain.Principal{Subject: "user-a"}, req); !errors.Is(err, domain.ErrHandoffUnavailable) {
		t.Fatalf("Execute() error = %v, want ErrHandoffUnavailable", err)
	}
	if authorizer.calls != 1 || authorizer.last.Relation == nil {
		t.Fatalf("authorizer did not receive complete request: calls=%d request=%+v", authorizer.calls, authorizer.last)
	}
	if executor.calls != 0 || executor.materializations != 0 {
		t.Fatalf("executor called before authorization: calls=%d materializations=%d", executor.calls, executor.materializations)
	}
}

func TestCapabilityTupleIsOpaqueAndNonExecutable(t *testing.T) {
	canaries := []struct {
		name  string
		tuple string
	}{
		{name: "command canary", tuple: `{"command":"CORTEX_CAPABILITY_COMMAND_CANARY","args":["--execute"]}`},
		{name: "approval canary", tuple: `{"approval":"approved","grant":"CORTEX_CAPABILITY_AUTHORITY_CANARY"}`},
		{name: "success canary", tuple: `{"status":"success","evidence":"CORTEX_CAPABILITY_SUCCESS_CANARY"}`},
	}

	for _, tt := range canaries {
		t.Run(tt.name, func(t *testing.T) {
			authorizer := &recordingAuthorizer{err: errors.New("independent approval missing")}
			executor := newRecordingExecutor()
			coordinator := domain.NewHandoffCoordinator(authorizer, executor)
			req := handoffRequest("opaque", "payload")
			req.CapabilityTuple = json.RawMessage(tt.tuple)

			if _, err := coordinator.Execute(context.Background(), domain.Principal{}, req); err == nil {
				t.Fatal("capability tuple must not satisfy independent authorization")
			}
			if authorizer.calls != 1 || executor.calls != 0 || executor.materializations != 0 {
				t.Fatalf("tuple gained dispatch or authority: auth=%d execute=%d materialize=%d", authorizer.calls, executor.calls, executor.materializations)
			}
			if !bytes.Equal(authorizer.last.CapabilityTuple, req.CapabilityTuple) {
				t.Fatalf("tuple was interpreted or changed: got %s want %s", authorizer.last.CapabilityTuple, req.CapabilityTuple)
			}
		})
	}
}

func TestHandoffDefensivelyCopiesRequestGraph(t *testing.T) {
	targetID := int64(9)
	req := handoffRequest("copy-key", "original-content")
	req.Observation.Tags = []string{"original-tag"}
	req.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &targetID}, Type: domain.RelationReferences, Reasoning: "original-reason"}
	req.CapabilityTuple = json.RawMessage(`{"canary":"original-capability"}`)
	principal := domain.Principal{Subject: "original-subject", Scopes: []string{"original-scope"}}
	authorizer := &mutatingAuthorizer{scope: "safe-scope"}
	executor := &capturingExecutor{result: validWriteResult(domain.WriteStatusCreated)}

	result, err := domain.NewHandoffCoordinator(authorizer, executor).Execute(context.Background(), principal, req)
	if err != nil || result.Status != domain.WriteStatusCreated {
		t.Fatalf("Execute() = %+v, %v", result, err)
	}
	if executor.key != "copy-key" || executor.scope != "safe-scope" || executor.canonical.Observation.Content != "original-content" || executor.canonical.Observation.Tags[0] != "original-tag" || executor.canonical.Relation.Reasoning != "original-reason" || string(executor.canonical.CapabilityTuple) != `{"canary":"original-capability"}` {
		t.Fatalf("executor observed mutated graph: key=%q scope=%q canonical=%+v", executor.key, executor.scope, executor.canonical)
	}
	if authorizer.subject != "original-subject" || authorizer.scopeGrant != "original-scope" || principal.Scopes[0] != "original-scope" {
		t.Fatalf("principal copy failed: authorizer=%q/%q caller=%+v", authorizer.subject, authorizer.scopeGrant, principal)
	}
	if req.Observation.Content != "original-content" || req.Observation.Tags[0] != "original-tag" || targetID != 9 || string(req.CapabilityTuple) != `{"canary":"original-capability"}` {
		t.Fatalf("caller graph was mutated: %+v", req)
	}
}

func TestCanonicalHandoffRejectsInvalidUTF8Recursively(t *testing.T) {
	bad := string([]byte{0xff})
	tests := []struct {
		name   string
		mutate func(*domain.HandoffRequest)
	}{
		{"idempotency key", func(r *domain.HandoffRequest) { r.IdempotencyKey = bad }},
		{"observation title", func(r *domain.HandoffRequest) { r.Observation.Title = bad }},
		{"nested tag", func(r *domain.HandoffRequest) { r.Observation.Tags = []string{"ok", bad} }},
		{"relation reasoning", func(r *domain.HandoffRequest) {
			id := int64(1)
			r.Relation = &domain.HandoffRelationInput{Target: domain.ObservationRef{LocalID: &id}, Type: domain.RelationReferences, Reasoning: bad}
		}},
		{"capability object key", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = json.RawMessage(append([]byte{'{', '"'}, append([]byte{0xff}, []byte(`":"value"}`)...)...))
		}},
		{"capability nested value", func(r *domain.HandoffRequest) {
			r.CapabilityTuple = json.RawMessage(append([]byte(`{"outer":{"value":"`), append([]byte{0xff}, []byte(`"}}`)...)...))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := handoffRequest("utf8", "content")
			tt.mutate(&req)
			if _, _, _, err := domain.CanonicalizeHandoff(req); !errors.Is(err, domain.ErrHandoffValidation) {
				t.Fatalf("CanonicalizeHandoff() error = %v", err)
			}
		})
	}
	if utf8.ValidString(bad) {
		t.Fatal("invalid test fixture")
	}
}

func TestCanonicalHandoffExactSizeLimit(t *testing.T) {
	req := handoffRequest("size", "x")
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	overhead := len(encoded) - 1
	req.Observation.Content = strings.Repeat("x", domain.MaxHandoffPayloadSize-overhead)
	encoded, err = json.Marshal(req)
	if err != nil || len(encoded) != domain.MaxHandoffPayloadSize {
		t.Fatalf("invalid exact-limit fixture: len=%d err=%v", len(encoded), err)
	}
	if _, _, _, err := domain.CanonicalizeHandoff(req); err != nil {
		t.Fatalf("exact limit rejected: %v", err)
	}
	req.Observation.Content += "x"
	if _, _, _, err := domain.CanonicalizeHandoff(req); !errors.Is(err, domain.ErrHandoffPayloadTooLarge) {
		t.Fatalf("limit+1 error = %v", err)
	}
}

func TestHandoffNilDependenciesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		c    *domain.HandoffCoordinator
	}{
		{"nil coordinator", nil},
		{"nil authorizer", domain.NewHandoffCoordinator(nil, newRecordingExecutor())},
		{"nil executor", domain.NewHandoffCoordinator(&recordingAuthorizer{scope: "scope"}, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.c.Execute(context.Background(), domain.Principal{}, handoffRequest("nil", "payload")); !errors.Is(err, domain.ErrHandoffUnavailable) {
				t.Fatalf("Execute() error = %v", err)
			}
		})
	}
}

func TestHandoffDependencyErrorsAreStableAndRedacted(t *testing.T) {
	const canary = "SECRET_TOKEN_CANARY"
	tests := []struct {
		name string
		c    *domain.HandoffCoordinator
		code domain.HandoffErrorCode
		op   string
	}{
		{"authorizer", domain.NewHandoffCoordinator(&recordingAuthorizer{err: errors.New("auth failed " + canary)}, newRecordingExecutor()), domain.HandoffErrorUnavailable, "authorize"},
		{"executor", domain.NewHandoffCoordinator(&recordingAuthorizer{scope: "scope"}, &capturingExecutor{err: errors.New("database failed " + canary)}), domain.HandoffErrorPersistence, "execute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.c.Execute(context.Background(), domain.Principal{}, handoffRequest("key-"+canary, "payload-"+canary))
			var got *domain.HandoffError
			if !errors.As(err, &got) || got.Code != tt.code || got.Operation != tt.op || got.Context == "" {
				t.Fatalf("error = %#v", err)
			}
			if strings.Contains(err.Error(), canary) || strings.Contains(got.Context, canary) {
				t.Fatalf("error leaked canary: %#v", got)
			}
		})
	}
}

func TestHandoffDependencyErrorsNormalizeTypedNilWithoutPanic(t *testing.T) {
	var typedNil *domain.HandoffError
	tests := []struct {
		name string
		err  error
	}{
		{name: "direct", err: domain.ErrHandoffForbidden},
		{name: "wrapped", err: errors.Join(errors.New("outer secret"), domain.ErrHandoffForbidden)},
		{name: "interface held", err: error(domain.ErrHandoffForbidden)},
		{name: "typed nil", err: error(typedNil)},
		{name: "wrapped typed nil", err: errors.Join(errors.New("outer secret"), error(typedNil))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coordinator := domain.NewHandoffCoordinator(
				&recordingAuthorizer{err: tt.err},
				newRecordingExecutor(),
			)
			_, err := coordinator.Execute(context.Background(), domain.Principal{}, handoffRequest("typed-nil", "payload"))
			var got *domain.HandoffError
			if !errors.As(err, &got) || got == nil {
				t.Fatalf("Execute() error = %#v, want non-nil *HandoffError", err)
			}
			if tt.name == "direct" || tt.name == "wrapped" || tt.name == "interface held" {
				if got.Code != domain.HandoffErrorForbidden {
					t.Fatalf("code = %q, want %q", got.Code, domain.HandoffErrorForbidden)
				}
			} else if got.Code != domain.HandoffErrorUnavailable {
				t.Fatalf("typed-nil code = %q, want fallback %q", got.Code, domain.HandoffErrorUnavailable)
			}
			if got.Operation != "authorize" || got.Context == "" || strings.Contains(err.Error(), "outer secret") {
				t.Fatalf("unstable or unredacted error: %#v", got)
			}
		})
	}
}

func TestHandoffBoundsCompleteRequestBeforeCallbacks(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.HandoffRequest, int)
	}{
		{name: "idempotency key", mutate: func(req *domain.HandoffRequest, n int) { req.IdempotencyKey = strings.Repeat("k", n) }},
		{name: "canonical payload", mutate: func(req *domain.HandoffRequest, n int) { req.Observation.Content = strings.Repeat("x", n) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findRequest := func(size int) domain.HandoffRequest {
				req := handoffRequest("bounded", "x")
				tt.mutate(&req, size)
				return req
			}
			// Find the exact accepted boundary without duplicating JSON framing rules.
			low, high := 1, domain.MaxHandoffPayloadSize+1
			for low < high {
				mid := low + (high-low+1)/2
				authorizer := &recordingAuthorizer{scope: "scope"}
				executor := &capturingExecutor{result: validWriteResult(domain.WriteStatusCreated)}
				_, err := domain.NewHandoffCoordinator(authorizer, executor).Execute(context.Background(), domain.Principal{}, findRequest(mid))
				if err == nil {
					low = mid
				} else {
					high = mid - 1
				}
			}

			authorizer := &recordingAuthorizer{scope: "scope"}
			executor := &capturingExecutor{result: validWriteResult(domain.WriteStatusCreated)}
			coordinator := domain.NewHandoffCoordinator(authorizer, executor)
			if _, err := coordinator.Execute(context.Background(), domain.Principal{}, findRequest(low)); err != nil {
				t.Fatalf("exact boundary rejected: size=%d err=%v", low, err)
			}
			if authorizer.calls != 1 {
				t.Fatalf("exact boundary authorizer calls = %d, want 1", authorizer.calls)
			}

			authorizer = &recordingAuthorizer{scope: "scope"}
			executor = &capturingExecutor{result: validWriteResult(domain.WriteStatusCreated)}
			_, err := domain.NewHandoffCoordinator(authorizer, executor).Execute(context.Background(), domain.Principal{}, findRequest(low+1))
			if !errors.Is(err, domain.ErrHandoffPayloadTooLarge) {
				t.Fatalf("boundary+1 error = %v", err)
			}
			if authorizer.calls != 0 || executor.calls != 0 {
				t.Fatalf("oversize request reached callback: auth=%d execute=%d", authorizer.calls, executor.calls)
			}
		})
	}
}

func TestHandoffIsolatesMaliciousExecutorMutation(t *testing.T) {
	req := handoffRequest("immutable-key", "immutable-content")
	req.Observation.Tags = []string{"immutable-tag"}
	req.CapabilityTuple = json.RawMessage(`{"proof":"immutable"}`)
	executor := &mutatingExecutor{delegate: newRecordingExecutor()}
	coordinator := domain.NewHandoffCoordinator(&recordingAuthorizer{scope: "scope"}, executor)

	created, err := coordinator.Execute(context.Background(), domain.Principal{}, req)
	if err != nil || created.Status != domain.WriteStatusCreated {
		t.Fatalf("first Execute() = %+v, %v", created, err)
	}
	replayed, err := coordinator.Execute(context.Background(), domain.Principal{}, req)
	if err != nil || replayed.Status != domain.WriteStatusReplayed || replayed.Ref != created.Ref {
		t.Fatalf("replay Execute() = %+v, %v; created=%+v", replayed, err, created)
	}
	if req.Observation.Content != "immutable-content" || req.Observation.Tags[0] != "immutable-tag" || string(req.CapabilityTuple) != `{"proof":"immutable"}` {
		t.Fatalf("executor mutated caller graph: %+v", req)
	}
	if executor.delegate.materializations != 1 {
		t.Fatalf("materializations = %d, want 1", executor.delegate.materializations)
	}
}

func TestHandoffRejectsMalformedExecutorResult(t *testing.T) {
	tests := []struct {
		name   string
		result domain.ObservationWriteResult
	}{
		{"missing ref", domain.ObservationWriteResult{Status: domain.WriteStatusCreated}},
		{"missing status", validWriteResult("")},
		{"unknown status", validWriteResult(domain.WriteStatus("invented"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := domain.NewHandoffCoordinator(&recordingAuthorizer{scope: "scope"}, &capturingExecutor{result: tt.result})
			if _, err := c.Execute(context.Background(), domain.Principal{}, handoffRequest("malformed", "payload")); !errors.Is(err, domain.ErrHandoffPersistence) {
				t.Fatalf("Execute() error = %v", err)
			}
		})
	}
}

func handoffRequest(key, content string) domain.HandoffRequest {
	return domain.HandoffRequest{
		IdempotencyKey: key,
		Observation: domain.SaveObservationInput{
			Title: "handoff", Content: content, Type: domain.TypeManual,
			Project: "cortex", Scope: domain.ScopeProject, Confidence: 1, Source: domain.SourceManual,
		},
	}
}

type recordingAuthorizer struct {
	scope domain.HandoffScope
	err   error
	calls int
	last  domain.HandoffRequest
}

type mutatingAuthorizer struct {
	scope      domain.HandoffScope
	subject    string
	scopeGrant string
}

func (a *mutatingAuthorizer) AuthorizeAll(_ context.Context, principal domain.Principal, req domain.HandoffRequest) (domain.HandoffScope, error) {
	a.subject = principal.Subject
	a.scopeGrant = principal.Scopes[0]
	req.Observation.Content = "mutated-content"
	req.Observation.Tags[0] = "mutated-tag"
	req.Relation.Reasoning = "mutated-reason"
	*req.Relation.Target.LocalID = 100
	req.CapabilityTuple[0] = '['
	principal.Scopes[0] = "mutated-scope"
	return a.scope, nil
}

type capturingExecutor struct {
	scope     domain.HandoffScope
	key       string
	canonical domain.CanonicalHandoff
	result    domain.ObservationWriteResult
	err       error
	calls     int
}

func (e *capturingExecutor) ExecuteHandoff(_ context.Context, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, _ [32]byte) (domain.ObservationWriteResult, error) {
	e.calls++
	e.scope, e.key, e.canonical = scope, key, canonical
	return e.result, e.err
}

type mutatingExecutor struct {
	delegate *recordingExecutor
}

func (e *mutatingExecutor) ExecuteHandoff(ctx context.Context, scope domain.HandoffScope, key string, canonical domain.CanonicalHandoff, hash [32]byte) (domain.ObservationWriteResult, error) {
	canonical.Observation.Content = "mutated-content"
	canonical.Observation.Tags[0] = "mutated-tag"
	canonical.CapabilityTuple[0] = '['
	return e.delegate.ExecuteHandoff(ctx, scope, key, canonical, hash)
}

func validWriteResult(status domain.WriteStatus) domain.ObservationWriteResult {
	id := int64(1)
	return domain.ObservationWriteResult{Ref: domain.ObservationRef{LocalID: &id}, Status: status}
}

func (a *recordingAuthorizer) AuthorizeAll(_ context.Context, _ domain.Principal, req domain.HandoffRequest) (domain.HandoffScope, error) {
	a.calls++
	a.last = req
	return a.scope, a.err
}

type receiptKey struct {
	scope domain.HandoffScope
	key   string
}

type receiptValue struct {
	hash [32]byte
	ref  domain.ObservationRef
}

type recordingExecutor struct {
	calls            int
	materializations int
	receipts         map[receiptKey]receiptValue
}

func newRecordingExecutor() *recordingExecutor {
	return &recordingExecutor{receipts: make(map[receiptKey]receiptValue)}
}

func (e *recordingExecutor) ExecuteHandoff(_ context.Context, scope domain.HandoffScope, key string, _ domain.CanonicalHandoff, hash [32]byte) (domain.ObservationWriteResult, error) {
	e.calls++
	receiptKey := receiptKey{scope: scope, key: key}
	if receipt, ok := e.receipts[receiptKey]; ok {
		if receipt.hash != hash {
			return domain.ObservationWriteResult{}, domain.ErrHandoffConflict
		}
		return domain.ObservationWriteResult{Ref: receipt.ref, Status: domain.WriteStatusReplayed}, nil
	}
	e.materializations++
	id := int64(e.materializations)
	ref := domain.ObservationRef{LocalID: &id}
	e.receipts[receiptKey] = receiptValue{hash: hash, ref: ref}
	return domain.ObservationWriteResult{Ref: ref, Status: domain.WriteStatusCreated}, nil
}
