package server

// LIM-T05 focused oracles: pre-parse body caps (chunked included), ordinary
// 1 MiB / absolute 8 MiB foundation caps, Content-Encoding rejection,
// byte-weighted admission (16 MiB principal / 128 MiB global) with 429 +
// Retry-After, per-principal full-bundle single-flight, bounded sessions with
// idle/absolute TTL and count caps, principal binding of Mcp-Session-Id,
// cancellation cleanup, and redaction of Authorization/SQL/session internals.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lleontor705/cortex/v2/internal/config"
	"github.com/lleontor705/cortex/v2/internal/domain"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/util"
)

// opaqueReader hides the concrete reader type from httptest.NewRequest so the
// request carries no Content-Length (chunked transfer semantics).
type opaqueReader struct{ r io.Reader }

func (o opaqueReader) Read(p []byte) (int, error) { return o.r.Read(p) }

// failIfReadReader fails the test if the fast-path rejection ever reads it.
type failIfReadReader struct{ t *testing.T }

func (f failIfReadReader) Read([]byte) (int, error) {
	f.t.Fatalf("body must not be read when Content-Length already exceeds the cap")
	return 0, io.ErrUnexpectedEOF
}

func newTestGuard(perPrincipal, global int, sessionPerPrincipal int) *mcpGuard {
	return newMCPGuard(
		newMCPAdmission(mcpAdmissionLimits{PerPrincipal: perPrincipal, Global: global}),
		newMCPSessionRegistry(mcpSessionLimits{
			IdleTTL:      mcpSessionIdleTTLDefault,
			AbsoluteTTL:  mcpSessionAbsoluteTTLDefault,
			PerPrincipal: sessionPerPrincipal,
			Total:        mcpMaxSessionsTotal,
		}),
	)
}

func guardRequest(t *testing.T, method, auth string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "/mcp", body)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

func recordingNext() (*int, chan struct{}, http.Handler) {
	calls := 0
	counter := &calls
	done := make(chan struct{}, 16)
	return counter, done, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		(*counter)++
		done <- struct{}{}
		w.WriteHeader(http.StatusOK)
	})
}

func paddedBody(total int, payload string) string {
	pad := total - len(payload)
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + payload
}

// --- pre-parse absolute cap ---------------------------------------------------

// Oversize body is rejected before any JSON-RPC parse even without
// Content-Length: the body is deliberately invalid JSON, so a parse would
// surface a JSON-RPC parse error instead of the 413 transport cap.
func TestMCPGuardRejectsOversizeBeforeParseWithoutContentLength(t *testing.T) {
	guard := newTestGuard(mcpPrincipalInflightBytes, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	calls, _, next := recordingNext()

	body := strings.NewReader(strings.Repeat("X", mcpAbsoluteBodyCap+1))
	req := guardRequest(t, http.MethodPost, "Bearer tok", opaqueReader{body})
	if req.ContentLength > 0 {
		t.Fatalf("expected unknown/chunked Content-Length, got %d", req.ContentLength)
	}
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "payload_too_large") {
		t.Fatalf("missing payload_too_large: %s", rec.Body.String())
	}
	if *calls != 0 {
		t.Fatalf("transport invoked %d times on oversize body", *calls)
	}
}

// Fast path: a declared Content-Length beyond the cap rejects without reading.
func TestMCPGuardContentLengthFastPath(t *testing.T) {
	guard := newTestGuard(mcpPrincipalInflightBytes, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	_, _, next := recordingNext()

	req := guardRequest(t, http.MethodPost, "Bearer tok", failIfReadReader{t})
	req.ContentLength = mcpAbsoluteBodyCap + 1
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
}

// --- ordinary 1 MiB method cap boundaries -------------------------------------

func TestMCPGuardOrdinaryMethodCapBoundaries(t *testing.T) {
	guard := newTestGuard(mcpPrincipalInflightBytes, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	calls, _, next := recordingNext()

	exact := paddedBody(mcpOrdinaryBodyCap, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(exact))
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("exact 1MiB ordinary body: status=%d calls=%d body=%s", rec.Code, *calls, rec.Body.String())
	}

	over := paddedBody(mcpOrdinaryBodyCap+1, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req = guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(over))
	rec = httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("+1 byte ordinary body status = %d body %s", rec.Code, rec.Body.String())
	}
}

// --- large mutation classification is registry-backed -------------------------

// The 8 MiB large-mutation class may only be granted to tool names that are
// actually registered on the MCP server. The artifact names are not
// registered today, so they remain subject to the ordinary 1 MiB cap: an
// unregistered name must never unlock the large class.
func TestMCPGuardLargeMutationUnregisteredNamesRemainOrdinary(t *testing.T) {
	guard := newTestGuard(mcpPrincipalInflightBytes, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	calls, _, next := recordingNext()

	small := paddedBody(mcpOrdinaryBodyCap+512, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cortex_save"}}`)
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(small))
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ordinary tools/call over 1MiB must be 413, got %d", rec.Code)
	}

	for _, name := range []string{"cortex_project_artifact_save", "cortex_project_artifact_revision"} {
		large := paddedBody(mcpOrdinaryBodyCap+512, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q}}`, name))
		req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(large))
		rec := httptest.NewRecorder()
		guard.wrap(next).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("unregistered tool %s over 1MiB must remain 413, got %d", name, rec.Code)
		}
	}
	if *calls != 0 {
		t.Fatalf("transport invoked %d times on unregistered large names", *calls)
	}
}

// The complementary half: when the tool IS registered on a live MCP server,
// the registry-backed lookup unlocks the 8 MiB class for eligible names only.
func TestMCPGuardLargeMutationRegisteredToolUnlocksLargeCap(t *testing.T) {
	srv := mcpserver.NewMCPServer("t", "1.0.0")
	srv.AddTool(mcp.NewTool("cortex_project_artifact_save", mcp.WithDescription("test artifact save")), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})
	guard := newMCPGuardWithTools(
		newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}),
		newMCPSessionRegistry(mcpSessionLimits{IdleTTL: mcpSessionIdleTTLDefault, AbsoluteTTL: mcpSessionAbsoluteTTLDefault, PerPrincipal: mcpMaxSessionsPerPrincipal, Total: mcpMaxSessionsTotal}),
		func(name string) bool { return srv.GetTool(name) != nil },
	)
	calls, _, next := recordingNext()

	large := paddedBody(mcpOrdinaryBodyCap+512, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cortex_project_artifact_save"}}`)
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(large))
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || *calls != 1 {
		t.Fatalf("registered large mutation must pass, status=%d calls=%d", rec.Code, *calls)
	}

	// Registry-eligible but absent from the registry stays ordinary even
	// under a lookup that answers for other names.
	lookup := func(name string) bool { return name == "cortex_save" }
	if class := classifyMCPEncodedBody([]byte(`{"method":"tools/call","params":{"name":"cortex_project_artifact_save"}}`), lookup); class.Large || class.MethodCap != mcpOrdinaryBodyCap {
		t.Fatalf("unregistered large-eligible classification = %+v", class)
	}
	if class := classifyMCPEncodedBody([]byte(`{"method":"tools/call","params":{"name":"cortex_project_artifact_save"}}`), nil); class.Large || class.MethodCap != mcpOrdinaryBodyCap {
		t.Fatalf("nil-registry classification = %+v", class)
	}
	if class := classifyMCPEncodedBody([]byte(`not json`), lookup); class.Large || class.MethodCap != mcpOrdinaryBodyCap {
		t.Fatalf("unparseable body classification = %+v", class)
	}
}

// --- session reservations: accounting, release, and refusal -------------------

// A pending reservation counts against the caps between reserve and Generate;
// a request that dies before Generate returns its slot; an unreserved
// over-cap Generate refuses instead of issuing an immediately-dead ID.
func TestMCPSessionReservationAccounting(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 2})

	res := reg.reserve("A")
	if res == nil {
		t.Fatal("first reservation must succeed")
	}
	if reg.canCreate("A") {
		t.Fatal("pending reservation must count against the per-principal cap")
	}
	if second := reg.reserve("A"); second != nil {
		second.release()
		t.Fatal("second reservation for A must be denied while one is pending")
	}
	res.release()
	res.release() // double release must be a no-op
	if !reg.canCreate("A") {
		t.Fatal("released reservation must return the slot")
	}

	// Consume path: reserve, carry through the request context, Generate.
	req := guardRequest(t, http.MethodPost, "Bearer tok", nil)
	fingerprint := mcpSessionFingerprint(req)
	res2 := reg.reserve(fingerprint)
	if res2 == nil {
		t.Fatal("reservation after release must succeed")
	}
	req = req.WithContext(withMCPSessionReservation(req.Context(), res2))
	id := reg.ResolveSessionIdManager(req).Generate()
	if id == "" {
		t.Fatal("reserved initialize must issue a session")
	}
	if reg.canCreate(fingerprint) {
		t.Fatal("live session must count against the cap")
	}
	if terminated, err := reg.ResolveSessionIdManager(req).Validate(id); terminated || err != nil {
		t.Fatalf("reserved session must be alive: terminated=%v err=%v", terminated, err)
	}
	// Unreserved over-cap Generate must refuse rather than issue an
	// immediately-dead session ID.
	unreserved := mcpSessionManager{registry: reg, principal: fingerprint}
	if dead := unreserved.Generate(); dead != "" {
		t.Fatalf("over-cap unreserved Generate must refuse without issuing an ID, got %q", dead)
	}
}

// Through the real wired handler, the artifact and protocol tools stay absent.
func TestMCPServerDoesNotEnableArtifactEndpoints(t *testing.T) {
	handler := testHandler(func(context.Context) error { return nil })
	client := &mcpStreamTestClient{t: t, h: handler, token: "test-token"}
	client.initialize()
	body := client.post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`, client.session)
	for _, name := range []string{"cortex_project_artifact_save", "cortex_project_artifact_revision", "cortex_project_protocol"} {
		if strings.Contains(body, name) {
			t.Fatalf("tools/list exposed foundation-only tool %s", name)
		}
	}
}

// --- Content-Encoding rejection -----------------------------------------------

func TestMCPGuardRejectsContentEncoding(t *testing.T) {
	guard := newTestGuard(mcpPrincipalInflightBytes, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	for _, enc := range []string{"gzip", "deflate", "br", "compress"} {
		req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(`{}`))
		req.Header.Set("Content-Encoding", enc)
		rec := httptest.NewRecorder()
		guard.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType || !strings.Contains(rec.Body.String(), "unsupported_content_encoding") {
			t.Fatalf("encoding %q: status=%d body=%s", enc, rec.Code, rec.Body.String())
		}
	}

	for _, enc := range []string{"", "identity"} {
		req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
		if enc != "" {
			req.Header.Set("Content-Encoding", enc)
		}
		rec := httptest.NewRecorder()
		guard.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("encoding %q must pass, got %d %s", enc, rec.Code, rec.Body.String())
		}
	}
}

// --- admission: byte-weighted limits, growth, release -------------------------

func TestMCPAdmissionByteWeightedLimits(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 16 << 20, Global: 128 << 20})

	a := adm.acquire("p1", 15<<20)
	if a == nil {
		t.Fatal("first 15MiB acquire denied")
	}
	if b := adm.acquire("p1", 2<<20); b != nil {
		b.release()
		t.Fatal("second 2MiB for same principal must exceed 16MiB budget")
	}
	if b := adm.acquire("p2", 2<<20); b == nil {
		t.Fatal("other principal must fit global budget")
	} else {
		b.release()
	}
	a.release()
	if c := adm.acquire("p1", 16<<20); c == nil {
		t.Fatal("release did not return principal budget")
	} else {
		c.release()
	}

	// Global budget: per-principal budgets allow 64MiB each, but the shared
	// 128MiB global pool only fits two of them.
	wide := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 128 << 20, Global: 128 << 20})
	x := wide.acquire("p1", 64<<20)
	y := wide.acquire("p2", 64<<20)
	if x == nil || y == nil {
		t.Fatal("expected both 64MiB acquires to fit")
	}
	if z := wide.acquire("p3", mcpMinAdmissionCost); z != nil {
		z.release()
		t.Fatal("global 128MiB budget must be exhausted")
	}
	x.release()
	y.release()

	// grow enforces limits atomically.
	g := adm.acquire("p1", 15<<20)
	if g.grow(2 << 20) {
		t.Fatal("grow beyond principal budget must fail")
	}
	if !g.grow(1 << 20) {
		t.Fatal("grow within budget must succeed")
	}
	g.release()
}

func TestMCPAdmissionHeavySingleFlightPerPrincipal(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes})
	first := adm.acquire("p1", mcpMinAdmissionCost)
	if first == nil || !first.becomeHeavy() {
		t.Fatal("first heavy acquire must succeed")
	}
	second := adm.acquire("p1", mcpMinAdmissionCost)
	if second == nil || second.becomeHeavy() {
		second.release()
		t.Fatal("second heavy for same principal must be denied")
	}
	other := adm.acquire("p2", mcpMinAdmissionCost)
	if other == nil || !other.becomeHeavy() {
		t.Fatal("heavy for another principal must succeed")
	}
	first.release()
	again := adm.acquire("p1", mcpMinAdmissionCost)
	if again == nil || !again.becomeHeavy() {
		t.Fatal("heavy slot must return after release")
	} else {
		again.release()
	}
	other.release()
}

// 429 + Retry-After surfaces before the transport is invoked.
func TestMCPGuardRateLimitedResponse(t *testing.T) {
	guard := newTestGuard(2048, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	calls, _, next := recordingNext()

	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(strings.Repeat(" ", 4096)))
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d body %s", rec.Code, rec.Body.String())
	}
	if retry := rec.Header().Get("Retry-After"); retry != "1" {
		t.Fatalf("Retry-After = %q", retry)
	}
	if !strings.Contains(rec.Body.String(), "inflight_limit_exceeded") {
		t.Fatalf("code missing: %s", rec.Body.String())
	}
	if *calls != 0 {
		t.Fatalf("transport invoked %d times on saturated admission", *calls)
	}
}

// --- sessions: TTLs, caps, principal binding ----------------------------------

func TestMCPSessionRegistryTTLs(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 4, Total: 8})
	now := time.Unix(1_700_000_000, 0)
	reg.now = func() time.Time { return now }

	manA := mcpSessionManager{registry: reg, principal: "A"}
	id := manA.Generate()
	if terminated, err := manA.Validate(id); terminated || err != nil {
		t.Fatalf("fresh session: terminated=%v err=%v", terminated, err)
	}

	now = now.Add(2 * time.Minute) // idle expiry
	if terminated, err := manA.Validate(id); !terminated || err != nil {
		t.Fatalf("idle-expired session: terminated=%v err=%v", terminated, err)
	}

	id2 := manA.Generate()
	now = now.Add(2 * time.Hour) // absolute expiry
	if terminated, err := manA.Validate(id2); !terminated || err != nil {
		t.Fatalf("absolute-expired session: terminated=%v err=%v", terminated, err)
	}
}

func TestMCPSessionRegistryPrincipalBindingAndTerminate(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 4, Total: 8})
	manA := mcpSessionManager{registry: reg, principal: "A"}
	manB := mcpSessionManager{registry: reg, principal: "B"}
	maint := mcpSessionManager{registry: reg, maintenance: true}

	id := manA.Generate()
	if _, err := manB.Validate(id); err == nil {
		t.Fatal("session issued to A must not validate for B")
	}
	if _, err := manB.Terminate(id); err == nil {
		t.Fatal("B must not terminate A's session")
	}
	if terminated, err := manA.Validate(id); terminated || err != nil {
		t.Fatalf("A session broken by B probing: terminated=%v err=%v", terminated, err)
	}
	if notAllowed, err := manA.Terminate(id); notAllowed || err != nil {
		t.Fatalf("A terminate own session: notAllowed=%v err=%v", notAllowed, err)
	}
	if _, err := manA.Validate(id); err == nil {
		t.Fatal("terminated session must not validate")
	}

	id2 := manA.Generate()
	if notAllowed, err := maint.Terminate(id2); notAllowed || err != nil {
		t.Fatalf("maintenance terminate: notAllowed=%v err=%v", notAllowed, err)
	}
}

func TestMCPSessionRegistryCountCaps(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 2, Total: 3})
	manA := mcpSessionManager{registry: reg, principal: "A"}
	manB := mcpSessionManager{registry: reg, principal: "B"}
	manA.Generate()
	manA.Generate()
	if reg.canCreate("A") {
		t.Fatal("per-principal cap must block a third A session")
	}
	manB.Generate()
	if reg.canCreate("B") {
		t.Fatal("total cap must block further sessions")
	}
	if len(reg.sessions) != 3 {
		t.Fatalf("registry holds %d sessions, want 3", len(reg.sessions))
	}
}

// Session count cap surfaces as 429 session_limit_exceeded at initialize.
func TestMCPGuardSessionLimitExceeded(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: mcpMaxSessionsTotal})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)
	_, _, next := recordingNext()

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	key := mcpSessionFingerprint(req)

	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first initialize blocked: %d %s", rec.Code, rec.Body.String())
	}

	// The transport issued this session for the same session fingerprint.
	mcpSessionManager{registry: reg, principal: key}.Generate()

	req = guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec = httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), "session_limit_exceeded") {
		t.Fatalf("second initialize: %d %s", rec.Code, rec.Body.String())
	}
	if retry := rec.Header().Get("Retry-After"); retry == "" {
		t.Fatal("session_limit_exceeded must carry Retry-After")
	}
}

// --- cancellation releases admission ------------------------------------------

func TestMCPGuardCancellationReleasesAdmission(t *testing.T) {
	guard := newTestGuard(4096, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	releaseObserved := make(chan struct{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(releaseObserved)
		w.WriteHeader(http.StatusForbidden)
	})

	ctx, cancel := context.WithCancel(context.Background())
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		guard.wrap(next).ServeHTTP(rec, req)
		close(done)
	}()
	cancel()
	<-releaseObserved
	<-done

	// The full budget must be available again for the same principal.
	follow := guard.admission.acquire(mcpAdmissionKey(guardRequest(t, http.MethodPost, "Bearer tok", nil)), 4096)
	if follow == nil {
		t.Fatal("admission bytes were not returned after cancellation")
	}
	follow.release()
}

// --- principal binding through the real transport ------------------------------

// mcpStreamTestClient drives the fully wired handler (auth + guard + mcp-go).
type mcpStreamTestClient struct {
	t       *testing.T
	h       http.Handler
	token   string
	session string
}

func (c *mcpStreamTestClient) initialize() {
	c.t.Helper()
	body := c.post(c.t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, "")
	if !strings.Contains(body, "serverInfo") {
		c.t.Fatalf("initialize failed: %s", body)
	}
}

func (c *mcpStreamTestClient) post(t *testing.T, payload, sessionOverride string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	session := sessionOverride
	if session == "" {
		session = c.session
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if s := rec.Header().Get("Mcp-Session-Id"); s != "" {
		if c.session == "" {
			c.session = s
		}
	}
	return rec.Body.String()
}

// status version of post for binding assertions.
func (c *mcpStreamTestClient) postStatus(t *testing.T, payload string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	if s := rec.Header().Get("Mcp-Session-Id"); s != "" && c.session == "" {
		c.session = s
	}
	return rec.Code
}

func TestMCPSessionCannotBeReusedByAnotherPrincipal(t *testing.T) {
	// Two distinct verified principals through the production wiring seam:
	// requestAuthenticator installs exactly this principal context.
	protect := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var subject string
			switch r.Header.Get("Authorization") {
			case "Bearer alice-token":
				subject = "alice"
			case "Bearer bob-token":
				subject = "bob"
			default:
				writeUnauthorized(w)
				return
			}
			ctx := context.WithValue(r.Context(), principalContextKey{}, domain.Principal{Subject: subject})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	handler, _ := newHTTPHandlerWithAuth(config.Config{}, newFakeOperations(), func(context.Context) error { return nil }, protect)

	alice := &mcpStreamTestClient{t: t, h: handler, token: "alice-token"}
	alice.initialize()
	if alice.session == "" {
		t.Fatal("initialize did not issue an Mcp-Session-Id")
	}

	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	if code := alice.postStatus(t, list); code != http.StatusOK {
		t.Fatalf("owner session tools/list = %d", code)
	}

	bob := &mcpStreamTestClient{t: t, h: handler, token: "bob-token", session: alice.session}
	if code := bob.postStatus(t, list); code != http.StatusNotFound {
		t.Fatalf("cross-principal session reuse = %d, want 404", code)
	}
}

// --- redaction -----------------------------------------------------------------

func TestMCPTransportErrorsAreRedacted(t *testing.T) {
	handler := testHandler(func(context.Context) error { return nil })
	bob := &mcpStreamTestClient{t: t, h: handler, token: "test-token"}

	oversize := strings.NewReader(strings.Repeat("X", mcpAbsoluteBodyCap+1))
	req := httptest.NewRequest(http.MethodPost, "/mcp", opaqueReader{oversize})
	req.Header.Set("Authorization", "Bearer supersecret-bearer-value")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	encoded := strings.NewReader(`{}`)
	req = httptest.NewRequest(http.MethodPost, "/mcp", encoded)
	req.Header.Set("Authorization", "Bearer supersecret-bearer-value")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req)

	foreign := `{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}`
	bob.initialize()
	req3 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(foreign))
	req3.Header.Set("Authorization", "Bearer another-token")
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Mcp-Session-Id", bob.session)
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)

	for name, body := range map[string]string{
		"oversize-413": rec.Body.String(),
		"encoding-415": rec2.Body.String(),
		"session-404":  rec3.Body.String(),
	} {
		for _, canary := range []string{"supersecret-bearer-value", "another-token", "test-token", "pq:", "pgx", "sqlite", "sql:", "driver:", "map[", bob.session} {
			if bob.session != "" && strings.Contains(body, canary) {
				t.Fatalf("%s response leaks %q: %s", name, canary, body)
			}
		}
	}
}

func TestMCPRedactingLogger(t *testing.T) {
	session := "123e4567-e89b-12d3-a456-426614174000"
	inputs := []string{
		"Sweeping expired session: " + session,
		"auth failed for Authorization: Bearer abc123.def456",
		"query failed: pq: duplicate key value violates unique constraint",
		"driver: sql: connection refused pgx: pool closed",
	}
	for _, in := range inputs {
		out := redactTransportText(in)
		for _, canary := range []string{session, "abc123.def456", "pq:", "pgx:", "sql:"} {
			if canary != "" && strings.Contains(out, canary) {
				t.Fatalf("redaction failed: %q -> %q still contains %q", in, out, canary)
			}
		}
	}
	var _ util.Logger = redactingLogger{}
}

// --- concurrency: release under load -------------------------------------------

func TestMCPAdmissionConcurrentAcquireRelease(t *testing.T) {
	adm := newMCPAdmission(mcpAdmissionLimits{PerPrincipal: 1 << 20, Global: 4 << 20})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("p%d", i%4)
			if h := adm.acquire(key, 64<<10); h != nil {
				if !h.grow(64 << 10) {
					// growth denial is fine under contention
					_ = h
				}
				h.release()
				h.release() // double release must be a no-op
			}
		}(i)
	}
	wg.Wait()
	if adm.global != 0 {
		t.Fatalf("global inflight after release = %d, want 0", adm.global)
	}
	if len(adm.perPrincipal) != 0 || len(adm.heavyPerPrincipal) != 0 {
		t.Fatalf("principal maps not drained: %v %v", adm.perPrincipal, adm.heavyPerPrincipal)
	}
}

// --- FIX-MCP: unknown-length bodies are accounted before retention ------------

// gatedReader delivers its payload and then blocks on the release channel
// before reporting EOF, pinning concurrent unknown-length readers mid-body.
type gatedReader struct {
	payload []byte
	sent    int
	release chan struct{}
}

func (g *gatedReader) Read(p []byte) (int, error) {
	if g.sent < len(g.payload) {
		n := copy(p, g.payload[g.sent:])
		g.sent += n
		return n, nil
	}
	<-g.release
	return 0, io.EOF
}

func admissionGlobal(a *mcpAdmission) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.global
}

// Chunked/unknown-length/HTTP2 bodies must be charged to the inflight budget
// BEFORE bytes are retained: while two 6 MiB unknown-length bodies are pinned
// mid-stream (12 MiB held against a 16 MiB per-principal budget), a third
// unknown-length body must be denied mid-read instead of buffering for free.
func TestMCPChunkedUnknownLengthBodiesCannotExceedBudget(t *testing.T) {
	guard := newTestGuard(16<<20, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	calls, _, next := recordingNext()
	gate := make(chan struct{})
	done := make(chan int, 3)

	start := func(size int, release chan struct{}) {
		payload := paddedBody(size, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		req := guardRequest(t, http.MethodPost, "Bearer tok", opaqueReader{&gatedReader{payload: []byte(payload), release: release}})
		rec := httptest.NewRecorder()
		go func() {
			guard.wrap(next).ServeHTTP(rec, req)
			done <- rec.Code
		}()
	}

	start(6<<20, gate)
	start(6<<20, gate)
	deadline := time.Now().Add(10 * time.Second)
	for admissionGlobal(guard.admission) < 12<<20 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := admissionGlobal(guard.admission); got < 12<<20 {
		t.Fatalf("two 6MiB unknown-length bodies only pinned %d inflight bytes", got)
	}

	// The third body can never retain more than the remaining 4 MiB budget.
	thirdDone := make(chan int, 1)
	thirdRelease := make(chan struct{})
	defer close(thirdRelease)
	go func() {
		payload := paddedBody(6<<20, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
		req := guardRequest(t, http.MethodPost, "Bearer tok", opaqueReader{&gatedReader{payload: []byte(payload), release: thirdRelease}})
		rec := httptest.NewRecorder()
		guard.wrap(next).ServeHTTP(rec, req)
		thirdDone <- rec.Code
	}()
	thirdCode := <-thirdDone
	if thirdCode != http.StatusTooManyRequests {
		t.Fatalf("third unknown-length body status = %d, want 429 inflight_limit_exceeded", thirdCode)
	}

	close(gate)
	codes := map[int]int{thirdCode: 1}
	for i := 0; i < 2; i++ {
		codes[<-done]++
	}
	if codes[http.StatusTooManyRequests] != 1 {
		t.Fatalf("codes = %v, want exactly one 429", codes)
	}
	if codes[http.StatusRequestEntityTooLarge] != 2 {
		t.Fatalf("codes = %v, want the two holders to end at the 413 ordinary cap", codes)
	}
	if *calls != 0 {
		t.Fatalf("transport invoked %d times on rejected bodies", *calls)
	}
	if got := admissionGlobal(guard.admission); got != 0 {
		t.Fatalf("admission not drained after rejection: %d bytes", got)
	}
}

// --- FIX-MCP: admission identity is tenant+subject -----------------------------

func TestMCPAdmissionKeyIncludesTenantAndSubject(t *testing.T) {
	mk := func(org, subject string) *http.Request {
		req := guardRequest(t, http.MethodPost, "Bearer tok", nil)
		return req.WithContext(context.WithValue(req.Context(), principalContextKey{}, domain.Principal{Subject: subject, OrgID: org}))
	}
	stableA := mcpAdmissionKey(mk("org1", "alice"))
	stableB := mcpAdmissionKey(mk("org1", "alice"))
	if stableA != stableB {
		t.Fatal("same tenant+subject must derive a stable admission identity")
	}
	if mcpAdmissionKey(mk("org1", "alice")) == mcpAdmissionKey(mk("org2", "alice")) {
		t.Fatal("same subject in different tenants must not share admission identity")
	}
	if mcpAdmissionKey(mk("org1", "alice")) == mcpAdmissionKey(mk("org1", "bob")) {
		t.Fatal("different subjects must not share admission identity")
	}
}

// --- FIX-MCP: session binding includes the credential/grant security context ---

func TestMCPSessionBindingIncludesCredentialGrantSecurityContext(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 4, Total: 8})
	request := func(subject, grant, token string) *http.Request {
		req := guardRequest(t, http.MethodPost, "Bearer "+token, nil)
		return req.WithContext(context.WithValue(req.Context(), principalContextKey{}, domain.Principal{
			Subject: subject, OrgID: "org1", Type: "user", AuthMethod: "api_key",
			GrantDigest: grant, GrantVersion: 2,
		}))
	}

	mgrGrantA := reg.ResolveSessionIdManager(request("alice", "grant-a", "token-1"))
	id := mgrGrantA.Generate()

	// Same subject+tenant but a different credential/grant security context
	// must not reuse the session.
	if _, err := reg.ResolveSessionIdManager(request("alice", "grant-b", "token-2")).Validate(id); err == nil {
		t.Fatal("session issued under grant-a must not validate under grant-b")
	}
	// Distinct valid credentials for the same subject+grant must not share
	// the session either: rotation binding must include the credential
	// identity itself, not only the grant metadata.
	if _, err := reg.ResolveSessionIdManager(request("alice", "grant-a", "token-2")).Validate(id); err == nil {
		t.Fatal("session issued under credential token-1 must not validate under credential token-2")
	}
	// The same security context through a fresh request must reuse it.
	if terminated, err := reg.ResolveSessionIdManager(request("alice", "grant-a", "token-1")).Validate(id); terminated || err != nil {
		t.Fatalf("same security context rejected: terminated=%v err=%v", terminated, err)
	}
	// A different subject under the same grant must not reuse it.
	if _, err := reg.ResolveSessionIdManager(request("bob", "grant-a", "token-1")).Validate(id); err == nil {
		t.Fatal("session issued to alice must not validate for bob")
	}
}

// --- FIX-MCP: concurrent initialize reserves session slots atomically ----------

// Twenty-four concurrent initialize requests against the real mcp-go
// transport with a per-principal session cap of three, released from a common
// start barrier so every admission decision overlaps: at most three sessions
// may be issued, excess initialize must fail with 429 session_limit_exceeded
// BEFORE the transport runs, and every issued Mcp-Session-Id must be alive (a
// dead or immediately-invalid ID is a correctness failure, not contention).
func TestMCPConcurrentInitializeReservesSlotsAtomically(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 3, Total: mcpMaxSessionsTotal})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)
	transport := mcpserver.NewStreamableHTTPServer(newServerMCP(newFakeOperations()),
		mcpserver.WithSessionIdManagerResolver(reg),
		mcpserver.WithSessionIdleTTL(time.Minute),
	)
	handler := guard.wrap(transport)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

	const n = 24
	type outcome struct {
		code    int
		session string
		body    string
	}
	outcomes := make([]outcome, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(init))
			req.Header.Set("Authorization", "Bearer race-tok")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			<-start
			handler.ServeHTTP(rec, req)
			outcomes[i] = outcome{code: rec.Code, session: rec.Header().Get("Mcp-Session-Id"), body: rec.Body.String()}
		}(i)
	}
	close(start)
	wg.Wait()

	issued := 0
	for _, o := range outcomes {
		switch o.code {
		case http.StatusOK:
			issued++
			if o.session == "" || !strings.Contains(o.body, "serverInfo") {
				t.Fatalf("initialize 200 without a live session: %+v", o)
			}
			// The issued session must be alive: tools/list must succeed, not
			// 404 with an ID that died on arrival.
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
			req.Header.Set("Authorization", "Bearer race-tok")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Mcp-Session-Id", o.session)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("issued session is dead: tools/list = %d body %s", rec.Code, rec.Body.String())
			}
		case http.StatusTooManyRequests:
			if !strings.Contains(o.body, "session_limit_exceeded") {
				t.Fatalf("429 without session_limit_exceeded: %s", o.body)
			}
		default:
			t.Fatalf("unexpected initialize status %d: %s", o.code, o.body)
		}
	}
	if issued > 3 {
		t.Fatalf("issued %d sessions for per-principal cap 3", issued)
	}
	if issued == 0 {
		t.Fatal("no initialize succeeded under zero load")
	}
}

// --- FIX-MCP: Content-Encoding checks every field value and token --------------

func TestMCPContentEncodingValidatesEveryFieldValueAndToken(t *testing.T) {
	guard := newTestGuard(mcpPrincipalInflightBytes, mcpGlobalInflightBytes, mcpMaxSessionsPerPrincipal)
	status := func(set func(http.Header)) int {
		req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(`{}`))
		set(req.Header)
		rec := httptest.NewRecorder()
		guard.wrap(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
		return rec.Code
	}
	for name, set := range map[string]func(http.Header){
		"second-field-value": func(h http.Header) { h.Add("Content-Encoding", "identity"); h.Add("Content-Encoding", "gzip") },
		"comma-token":        func(h http.Header) { h.Set("Content-Encoding", "identity, gzip") },
		"mixed-case-comma":   func(h http.Header) { h.Set("Content-Encoding", "GZIP , deflate") },
		"br-after-identity":  func(h http.Header) { h.Set("Content-Encoding", "identity;br") },
		"unknown-token":      func(h http.Header) { h.Set("Content-Encoding", "futurecodec") },
		// Explicit empty encodings are rejected: identity is the single
		// accepted value, and an empty field value or empty list element is
		// malformed rather than equivalent to absence.
		"explicit-empty":  func(h http.Header) { h.Set("Content-Encoding", "") },
		"trailing-comma":  func(h http.Header) { h.Set("Content-Encoding", "identity,") },
		"leading-comma":   func(h http.Header) { h.Set("Content-Encoding", ", identity") },
		"whitespace-only": func(h http.Header) { h.Set("Content-Encoding", "   ") },
	} {
		if got := status(set); got != http.StatusUnsupportedMediaType {
			t.Fatalf("%s: status = %d, want 415", name, got)
		}
	}
	for name, set := range map[string]func(http.Header){
		"single-identity":      func(h http.Header) { h.Set("Content-Encoding", "identity") },
		"identity-comma-token": func(h http.Header) { h.Set("Content-Encoding", "identity, identity") },
		"identity-two-fields":  func(h http.Header) { h.Add("Content-Encoding", "identity"); h.Add("Content-Encoding", "identity") },
	} {
		if got := status(set); got != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", name, got)
		}
	}
}

// --- FIX-MCP: entire driver/database error detail becomes fixed redaction ------

func TestMCPRedactionReplacesEntireDatabaseErrorDetail(t *testing.T) {
	inputs := []string{
		"query failed: pq: duplicate key value violates unique constraint uq_sessions (SQLSTATE 23505)",
		"driver: sql: connection refused while dialing postgres@10.0.0.4:5432",
		"pgx: pool closed during SELECT id, secret FROM tokens",
		// Standalone PostgreSQL detail without any pq/pgx/sql/driver prefix
		// must collapse to the fixed redaction token as well.
		"ERROR: duplicate key value violates unique constraint uq_sessions_detail; SQLSTATE 23505",
		"writeback failed: could not serialize access due to concurrent update (SQLSTATE 40001)",
		// Punctuated and spelled-out SQLSTATE forms leak the same class of
		// detail and must collapse identically.
		"constraint check failed SQLSTATE: 23505",
		"libpq verbose: could not drop table because other objects depend on it; SQL state: 2BP01",
		"ODBC reported sql-state=42P01 relation does not exist",
	}
	for _, in := range inputs {
		out := redactTransportText(in)
		for _, leak := range []string{"duplicate", "unique constraint", "uq_sessions", "SQLSTATE", "23505", "connection refused", "10.0.0.4", "SELECT", "secret", "tokens", "pq:", "pgx:", "sql:", "driver:", "uq_sessions_detail", "serialize", "40001", "ERROR:", "SQL state", "sql-state", "2BP01", "42P01", "constraint check", "depend on it", "relation does not exist"} {
			if strings.Contains(out, leak) {
				t.Fatalf("database error detail leaked %q: %q -> %q", leak, in, out)
			}
		}
	}
	// Redaction must not swallow ordinary text that merely mentions SQL.
	for _, in := range []string{
		"sql statement: SELECT 1",
		"failed sql statement users_table locked",
	} {
		if out := redactTransportText(in); out != in {
			t.Fatalf("ordinary SQL wording must pass through: %q -> %q", in, out)
		}
	}
}

// --- FIX-MCP: canceled/failed initialize rolls back the generated session ------

// registryCounts snapshots live and pending session accounting under the
// registry lock.
func registryCounts(reg *mcpSessionRegistry) (live, pending int, pendingPrincipals int) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return len(reg.sessions), reg.pendingTotal, len(reg.pendingPerPrincipal)
}

// A canceled initialize that has already consumed its reservation through
// Generate — exactly where the upstream transport returns without writing
// anything once the request context dies — must not leave the generated
// session holding a slot the client can never use. The oracle is
// deterministic: the stand-in transport resolves the manager from the request
// (as the real transport does), calls Generate, then blocks until the request
// context is canceled and returns WITHOUT writing a status or the
// Mcp-Session-Id header.
func TestMCPCanceledInitializeAfterGenerateRollsBackSession(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 4})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)

	generated := make(chan string, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reg.ResolveSessionIdManager(r).Generate()
		generated <- id
		<-r.Context().Done() // cancel/abort after Generate, before any write
		// Deliberately no WriteHeader/Write: the session ID never issues.
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		guard.wrap(next).ServeHTTP(rec, req)
		close(done)
	}()

	if id := <-generated; id == "" {
		t.Fatal("reserved initialize must issue a session ID inside the transport")
	}
	// One live session, zero pending: the reservation was consumed.
	if live, pending, principals := registryCounts(reg); live != 1 || pending != 0 || principals != 0 {
		t.Fatalf("after Generate: live=%d pending=%d pendingPrincipals=%d, want 1/0/0", live, pending, principals)
	}

	cancel()
	<-done

	// The guard must roll the never-issued session back: live and pending
	// accounting fully drained.
	if live, pending, principals := registryCounts(reg); live != 0 || pending != 0 || principals != 0 {
		t.Fatalf("after canceled initialize: live=%d pending=%d pendingPrincipals=%d, want all zero", live, pending, principals)
	}
	// The per-principal slot must be immediately reusable: a fresh initialize
	// that behaves like the real transport (Generate, then header + status)
	// succeeds despite the aborted one.
	issued := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reg.ResolveSessionIdManager(r).Generate()
		if id == "" {
			t.Error("reused slot must admit a new reserved session")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Mcp-Session-Id", id)
		w.WriteHeader(http.StatusOK)
	})
	req2 := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec2 := httptest.NewRecorder()
	guard.wrap(issued).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("slot not reusable after rollback: initialize = %d %s", rec2.Code, rec2.Body.String())
	}
	if live, _, _ := registryCounts(reg); live != 1 {
		t.Fatalf("reused slot must hold exactly one live session, got %d", live)
	}
}

// The complementary half: when the transport DOES issue the session ID with
// the response status, the session must survive the guard — rollback only
// reclaims sessions whose ID never reached the wire.
func TestMCPIssuedInitializeSessionSurvivesGuard(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 4})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reg.ResolveSessionIdManager(r).Generate()
		w.Header().Set("Mcp-Session-Id", id)
		w.WriteHeader(http.StatusOK)
	})
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("initialize = %d %s", rec.Code, rec.Body.String())
	}
	if live, pending, _ := registryCounts(reg); live != 1 || pending != 0 {
		t.Fatalf("issued session must survive: live=%d pending=%d, want 1/0", live, pending)
	}
}

// A transport panic after Generate must not strand the generated session:
// the guard's deferred cleanup drains the registry during panic unwinding
// while the panic itself keeps propagating unchanged to the enclosing
// server machinery.
func TestMCPPanicAfterGenerateDrainsRegistryAndPropagates(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 4})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)

	generated := make(chan string, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reg.ResolveSessionIdManager(r).Generate()
		generated <- id
		panic("transport exploded after generate")
	})

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec := httptest.NewRecorder()

	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		guard.wrap(next).ServeHTTP(rec, req)
	}()

	if id := <-generated; id == "" {
		t.Fatal("reserved initialize must issue a session ID inside the transport")
	}
	if pv := <-panicked; pv != "transport exploded after generate" {
		t.Fatalf("panic must propagate unchanged, got %v", pv)
	}
	if live, pending, principals := registryCounts(reg); live != 0 || pending != 0 || principals != 0 {
		t.Fatalf("after panic: live=%d pending=%d pendingPrincipals=%d, want all zero", live, pending, principals)
	}

	// The reclaimed slot must be immediately reusable by a well-behaved
	// initialize even though the panicking one consumed Generate.
	follow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reg.ResolveSessionIdManager(r).Generate()
		if id == "" {
			t.Error("slot must be reusable after panic rollback")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Mcp-Session-Id", id)
		w.WriteHeader(http.StatusOK)
	})
	req2 := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec2 := httptest.NewRecorder()
	guard.wrap(follow).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("slot not reusable after panic rollback: initialize = %d", rec2.Code)
	}
	if live, _, _ := registryCounts(reg); live != 1 {
		t.Fatalf("reused slot must hold exactly one live session, got %d", live)
	}
}

// A Flush with the session header set sends buffered headers to the wire,
// so the session counts as issued and must survive the guard even when no
// WriteHeader/Write ever follows.
func TestMCPFlushedSessionHeaderCountsAsIssued(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 4})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reg.ResolveSessionIdManager(r).Generate()
		w.Header().Set("Mcp-Session-Id", id)
		flusher := w.(http.Flusher)
		flusher.Flush() // the only wire event: no WriteHeader/Write follows
	})

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec := httptest.NewRecorder()
	guard.wrap(next).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("initialize = %d", rec.Code)
	}
	if live, pending, _ := registryCounts(reg); live != 1 || pending != 0 {
		t.Fatalf("flushed session header must count as issued: live=%d pending=%d, want 1/0", live, pending)
	}
}

// nonFlushWriter is a minimal ResponseWriter with no Flush method.
type nonFlushWriter struct {
	header http.Header
	status int
}

func (w *nonFlushWriter) Header() http.Header         { return w.header }
func (w *nonFlushWriter) WriteHeader(status int)      { w.status = status }
func (w *nonFlushWriter) Write(p []byte) (int, error) { return len(p), nil }

// The guard must not advertise http.Flusher when the underlying writer
// lacks it: mcp-go probes for Flush support to decide whether streaming is
// possible, so a falsely-advertised Flush would steer it into an
// unsupported path.
func TestMCPGuardDoesNotAdvertiseFlusherWhenUnsupported(t *testing.T) {
	reg := newMCPSessionRegistry(mcpSessionLimits{IdleTTL: time.Minute, AbsoluteTTL: time.Hour, PerPrincipal: 1, Total: 4})
	guard := newMCPGuard(newMCPAdmission(mcpAdmissionLimits{PerPrincipal: mcpPrincipalInflightBytes, Global: mcpGlobalInflightBytes}), reg)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Error("guard must not advertise http.Flusher when the underlying writer lacks it")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// mcp-go's streaming-unsupported shape: answer 405 without
		// generating a session at all.
		http.Error(w, "Streaming unsupported", http.StatusMethodNotAllowed)
	})

	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	req := guardRequest(t, http.MethodPost, "Bearer tok", strings.NewReader(init))
	rec := &nonFlushWriter{header: make(http.Header)}
	guard.wrap(next).ServeHTTP(rec, req)

	if rec.status != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.status)
	}
	if live, pending, principals := registryCounts(reg); live != 0 || pending != 0 || principals != 0 {
		t.Fatalf("unissued initialize must fully drain: live=%d pending=%d pendingPrincipals=%d", live, pending, principals)
	}
}

// --- FIX-MCP: Authorization redaction covers every value form ------------------

// Credentials appear in logs as plain header values, quoted strings, and the
// bracketed Go header-map rendering (fmt of http.Header / map[string][]string).
// Every form must redact the COMPLETE value — no credential suffix may survive
// a partial match — while unrelated text stays untouched.
func TestMCPAuthorizationRedactionCoversAllValueForms(t *testing.T) {
	credential := "supersecret-bearer-value"
	forms := map[string]string{
		"plain":            "Authorization: Bearer " + credential,
		"equals-separator": "authorization=Bearer " + credential,
		"bare-token":       "Authorization: " + credential,
		"quoted":           `Authorization: "Bearer ` + credential + `"`,
		"go-map-bracketed": "map[Authorization:[Bearer " + credential + "]]",
		"go-map-mixed":     "map[Accept:[application/json] Authorization:[Bearer " + credential + "]]",
	}
	for name, in := range forms {
		out := redactTransportText(in)
		if strings.Contains(out, credential) {
			t.Fatalf("%s leaked credential suffix: %q -> %q", name, in, out)
		}
		if !strings.Contains(out, "<redacted>") {
			t.Fatalf("%s missing redaction marker: %q -> %q", name, in, out)
		}
	}
	// In the mixed header map only the Authorization value disappears.
	if mixed := redactTransportText(forms["go-map-mixed"]); !strings.Contains(mixed, "application/json") {
		t.Fatalf("mixed header map over-redacted a foreign value: %q", mixed)
	}
	// Unrelated text must pass through unchanged.
	for name, in := range map[string]string{
		"prose":         "the authorization workflow completed",
		"other-claim":   "authorized: true",
		"foreign-map":   "map[Accept:[application/json]]",
		"field-mention": "mentions the Authorization header in prose",
	} {
		if out := redactTransportText(in); out != in {
			t.Fatalf("%s over-redacted: %q -> %q", name, in, out)
		}
	}
}

// --- T08: transport error-response canary corpus ----------------------------

// transportCanaryCorpus holds raw internal-cause strings that must never
// appear in any MCP HTTP error response. Transport responses are constructed
// exclusively from constant coded messages, so an echo of any of these would
// prove a raw-cause regression.
var transportCanaryCorpus = []string{
	"postgres://svc:cortex-pass@10.9.8.7:5432/cortex?sslmode=disable",
	"Bearer sk-transport-canary-7",
	"Authorization: Bearer sk-transport-canary-7",
	`C:\Users\leak\cortex.db`,
	"/var/lib/cortex/secrets/token.txt",
	"http://169.254.169.254/latest/meta-data/",
	"169.254.169.254",
	`{"upstream":"secret body canary"}`,
	"SQLSTATE 23505",
	"pq: duplicate key value violates unique constraint",
}

func assertNoTransportCanaries(t *testing.T, text string) {
	t.Helper()
	for _, canary := range transportCanaryCorpus {
		if strings.Contains(text, canary) {
			t.Fatalf("canary %q leaked into transport output: %q", canary, text)
		}
	}
}

// TestMCPTransportErrorResponseCanaryCorpus drives every pre-parse transport
// rejection and asserts each response is a bounded, constant, coded error
// that carries none of the raw-cause canaries.
func TestMCPTransportErrorResponseCanaryCorpus(t *testing.T) {
	handler, _ := newHTTPHandlerWithAuth(config.Config{}, newFakeOperations(), func(context.Context) error { return nil }, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), principalContextKey{}, domain.Principal{Subject: "canary-user"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})

	responses := map[string]string{}

	unsupportedEncoding := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	unsupportedEncoding.Header.Set("Authorization", "Bearer token")
	unsupportedEncoding.Header.Set("Accept", "application/json, text/event-stream")
	unsupportedEncoding.Header.Set("Content-Type", "application/json")
	unsupportedEncoding.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unsupportedEncoding)
	responses["content-encoding"] = fmt.Sprintf("%d %s", rec.Code, rec.Body.String())

	oversize := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{`+strings.Repeat("x", 9<<20)+`}}`))
	oversize.Header.Set("Authorization", "Bearer token")
	oversize.Header.Set("Accept", "application/json, text/event-stream")
	oversize.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, oversize)
	responses["oversize"] = fmt.Sprintf("%d %s", rec.Code, rec.Body.String())

	garbage := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("not json at all"))
	garbage.Header.Set("Authorization", "Bearer token")
	garbage.Header.Set("Accept", "application/json, text/event-stream")
	garbage.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, garbage)
	responses["garbage"] = fmt.Sprintf("%d %s", rec.Code, rec.Body.String())

	unknownSession := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	unknownSession.Header.Set("Authorization", "Bearer token")
	unknownSession.Header.Set("Accept", "application/json, text/event-stream")
	unknownSession.Header.Set("Content-Type", "application/json")
	unknownSession.Header.Set("Mcp-Session-Id", "00000000-0000-0000-0000-00000000dead")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unknownSession)
	responses["unknown-session"] = fmt.Sprintf("%d %s", rec.Code, rec.Body.String())

	for name, body := range responses {
		t.Run(name, func(t *testing.T) {
			assertNoTransportCanaries(t, body)
			if name == "unknown-session" {
				// The session-not-found 404 is owned by the upstream mcp-go
				// transport: a constant plain-text body. Pin the exact
				// constant so any drift toward raw detail fails here.
				if !strings.HasSuffix(strings.TrimSpace(body), "Invalid session ID") {
					t.Fatalf("unknown-session response drifted from the constant text: %q", body)
				}
				return
			}
			if !strings.Contains(body, "error") {
				t.Fatalf("transport rejection must be a coded error response: %q", body)
			}
		})
	}
}
