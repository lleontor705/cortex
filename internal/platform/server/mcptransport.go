package server

// LIM-T05 — HTTP/MCP admission, body caps and inflight byte controls.
//
// This file hardens the Cortex Server Streamable HTTP MCP transport with:
//
//   - Pre-parse encoded body limits enforced while reading the actual request
//     stream, so they hold for chunked requests without Content-Length.
//   - An absolute 8 MiB encoded cap for POST /mcp plus a 1 MiB cap for every
//     ordinary JSON-RPC method. The 8 MiB "large mutation" cap is a foundation:
//     only artifact save/revision tool calls are classified large, and only
//     when such a tool is actually registered on the MCP server. No artifact
//     endpoints are enabled by this file.
//   - Rejection of compressed request bodies (Content-Encoding) before any
//     decode.
//   - Byte-weighted inflight admission semaphores: 16 MiB per principal and
//     128 MiB global by default, saturating with HTTP 429 plus Retry-After
//     before any operation executes.
//   - A per-principal single slot for full-bundle-equivalent (heavy) requests.
//   - Bounded MCP sessions with idle/absolute TTL and per-principal/total
//     count caps, bound to the verified principal so an Mcp-Session-Id issued
//     for one principal cannot be reused by another.
//   - Fixed, redacted error messages and a redacting transport logger that
//     scrub Authorization material, SQL/driver fragments, and session
//     internals.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/mark3labs/mcp-go/util"
)

// Compile-time assertions: the registry and its principal-scoped views
// satisfy the upstream streamable HTTP session contracts.
var (
	_ mcpserver.SessionIdManager         = mcpSessionManager{}
	_ mcpserver.SessionIdManagerResolver = (*mcpSessionRegistry)(nil)
	_ util.Logger                        = redactingLogger{}
)

const (
	// mcpAbsoluteBodyCap is the absolute encoded request cap for POST /mcp
	// (REQ-MCP-CAP-001). It bounds memory amplification regardless of the
	// JSON-RPC method.
	mcpAbsoluteBodyCap = 8 << 20
	// mcpOrdinaryBodyCap applies to every JSON-RPC method that is not an
	// allowed large mutation.
	mcpOrdinaryBodyCap = 1 << 20

	// Default inflight admission budgets (REQ-DOS-001).
	mcpPrincipalInflightBytes = 16 << 20
	mcpGlobalInflightBytes    = 128 << 20

	// mcpMinAdmissionCost is the floor charged to any admitted request so
	// bodyless requests cannot evade the semaphores entirely.
	mcpMinAdmissionCost = 4 << 10

	// mcpRetryAfterSeconds is the fixed Retry-After hint attached to 429s.
	mcpRetryAfterSeconds = 1

	// Session defaults: idle TTL drives the transport sweeper; the absolute
	// TTL is enforced by the registry; counts bound registry growth.
	mcpSessionIdleTTLDefault     = 30 * time.Minute
	mcpSessionAbsoluteTTLDefault = 12 * time.Hour
	mcpMaxSessionsPerPrincipal   = 16
	mcpMaxSessionsTotal          = 1024

	// mcpSessionIDHeader is the Streamable HTTP session header. It matches
	// the upstream mcp-go HeaderKeySessionID the transport issues initialize
	// responses with.
	mcpSessionIDHeader = "Mcp-Session-Id"
)

// mcpLargeMutationTools lists tool names ELIGIBLE for the full 8 MiB encoded
// cap (REQ-MCP-CAP-001). The class is only granted when the name is actually
// registered on the MCP server (see mcpGuard.registeredTool): an unregistered
// name — including these foundation artifact names — stays ordinary.
var mcpLargeMutationTools = map[string]struct{}{
	"cortex_project_artifact_save":     {},
	"cortex_project_artifact_revision": {},
}

// mcpHeavyTools lists full-bundle-equivalent tool calls. At most one such
// request may be in flight per principal (REQ-DOS-001). Like the large class,
// the heavy class requires the tool to be registered.
var mcpHeavyTools = map[string]struct{}{
	"cortex_project_protocol": {},
}

// errMCPSessionUnavailable is the only error text the session managers ever
// surface through the transport: constant, redacted, no internals.
var errMCPSessionUnavailable = errors.New("session unavailable")

// mcpEnvelopePeek is the shallow pre-parse used to classify a request without
// a full JSON-RPC decode.
type mcpEnvelopePeek struct {
	Method string `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}

// mcpRequestClass is the classification outcome for an encoded body.
type mcpRequestClass struct {
	Method      string
	Tool        string
	Initialize  bool
	Large       bool
	Heavy       bool
	MethodCap   int
	OverCapSize int // encoded size when the method cap is exceeded
}

// classifyMCPEncodedBody classifies the already-bounded encoded body. An
// unparseable body is classified ordinary; the transport produces the JSON-RPC
// parse error afterwards. The large/heavy classes additionally require the
// named tool to pass the registered lookup, so classification is
// registry-backed: unregistered names never unlock the 8 MiB cap.
func classifyMCPEncodedBody(body []byte, registered func(string) bool) mcpRequestClass {
	class := mcpRequestClass{MethodCap: mcpOrdinaryBodyCap}
	var peek mcpEnvelopePeek
	if len(body) > 0 && json.Unmarshal(body, &peek) == nil {
		class.Method = peek.Method
		class.Tool = peek.Params.Name
	}
	if peek.Method == "initialize" {
		class.Initialize = true
	}
	if peek.Method == "tools/call" && registered != nil && registered(peek.Params.Name) {
		if _, ok := mcpLargeMutationTools[peek.Params.Name]; ok {
			class.Large = true
			class.MethodCap = mcpAbsoluteBodyCap
		}
		if _, ok := mcpHeavyTools[peek.Params.Name]; ok {
			class.Heavy = true
		}
	}
	return class
}

// mcpAdmissionKey derives the accounting identity for a request. Verified
// principals are keyed by tenant+subject, so the same subject in different
// tenants never shares admission or session-slot accounting; bearer-only
// deployments are keyed by a SHA-256 of the Authorization header value. The
// raw credential is never retained or logged.
func mcpAdmissionKey(r *http.Request) string {
	if principal, ok := principalFromContext(r.Context()); ok && principal.Subject != "" {
		return "principal:" + mcpLengthDelimited(principal.OrgID, principal.Subject)
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(r.Header.Get("Authorization"))))
	return "bearer:" + hex.EncodeToString(digest[:])
}

// mcpLengthDelimited derives a stable, collision-free digest of the tenant and
// subject: length prefixes prevent separator-collision identities such as
// ("a:b","c") vs ("a","b:c").
func mcpLengthDelimited(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = fmt.Fprintf(h, "%d\x00%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// --- inflight admission semaphores ------------------------------------------

type mcpAdmissionLimits struct {
	PerPrincipal int
	Global       int
}

// mcpAdmission is a byte-weighted, non-blocking admission controller. Cost is
// the encoded request byte weight; saturation denies with 429 semantics
// instead of queueing so slow clients cannot hold slots hostage.
type mcpAdmission struct {
	mu                sync.Mutex
	limits            mcpAdmissionLimits
	global            int
	perPrincipal      map[string]int
	heavyPerPrincipal map[string]int
}

func newMCPAdmission(limits mcpAdmissionLimits) *mcpAdmission {
	return &mcpAdmission{
		limits:            limits,
		perPrincipal:      make(map[string]int),
		heavyPerPrincipal: make(map[string]int),
	}
}

// mcpAdmissionHandle releases one admitted request. grow may raise the held
// cost mid-request once the actual encoded size is known; becomeHeavy takes
// the per-principal full-bundle slot.
type mcpAdmissionHandle struct {
	admission *mcpAdmission
	principal string
	cost      int
	heavy     bool
	released  bool
}

func (a *mcpAdmission) acquire(principal string, cost int) *mcpAdmissionHandle {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.availableLocked(principal, cost) {
		return nil
	}
	a.grantLocked(principal, cost)
	return &mcpAdmissionHandle{admission: a, principal: principal, cost: cost}
}

func (a *mcpAdmission) availableLocked(principal string, cost int) bool {
	if a.global+cost > a.limits.Global {
		return false
	}
	if a.perPrincipal[principal]+cost > a.limits.PerPrincipal {
		return false
	}
	return true
}

func (a *mcpAdmission) grantLocked(principal string, cost int) {
	a.global += cost
	a.perPrincipal[principal] += cost
}

func (h *mcpAdmissionHandle) grow(delta int) bool {
	if h == nil || delta <= 0 {
		return h != nil
	}
	a := h.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	if h.released || !a.availableLocked(h.principal, delta) {
		return false
	}
	a.grantLocked(h.principal, delta)
	h.cost += delta
	return true
}

func (h *mcpAdmissionHandle) becomeHeavy() bool {
	if h == nil {
		return false
	}
	a := h.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	if h.released || h.heavy || a.heavyPerPrincipal[h.principal] >= 1 {
		return false
	}
	a.heavyPerPrincipal[h.principal]++
	h.heavy = true
	return true
}

// release returns every held byte and the heavy slot exactly once. It is safe
// against cancellation double-invocation.
func (h *mcpAdmissionHandle) release() {
	if h == nil {
		return
	}
	a := h.admission
	a.mu.Lock()
	defer a.mu.Unlock()
	if h.released {
		return
	}
	h.released = true
	a.global -= h.cost
	a.perPrincipal[h.principal] -= h.cost
	if a.perPrincipal[h.principal] <= 0 {
		delete(a.perPrincipal, h.principal)
	}
	if h.heavy {
		a.heavyPerPrincipal[h.principal]--
		if a.heavyPerPrincipal[h.principal] <= 0 {
			delete(a.heavyPerPrincipal, h.principal)
		}
	}
}

// --- bounded, principal-bound MCP sessions ----------------------------------

type mcpSessionRecord struct {
	principal  string
	createdAt  time.Time
	lastActive time.Time
	terminated bool
}

type mcpSessionLimits struct {
	IdleTTL      time.Duration
	AbsoluteTTL  time.Duration
	PerPrincipal int
	Total        int
}

// mcpSessionRegistry implements mcpserver.SessionIdManagerResolver. Every
// request-scoped manager is bound to the requesting principal; the nil-request
// manager (transport idle sweeper) is maintenance-only.
type mcpSessionRegistry struct {
	mu                  sync.Mutex
	sessions            map[string]*mcpSessionRecord
	limits              mcpSessionLimits
	now                 func() time.Time
	pendingTotal        int
	pendingPerPrincipal map[string]int
}

func newMCPSessionRegistry(limits mcpSessionLimits) *mcpSessionRegistry {
	return &mcpSessionRegistry{
		sessions:            make(map[string]*mcpSessionRecord),
		limits:              limits,
		now:                 time.Now,
		pendingPerPrincipal: make(map[string]int),
	}
}

// mcpSessionReservation is an atomically reserved session slot. The guard
// reserves it while holding the registry lock and the transport's Generate
// consumes it under the same lock, so concurrent initialize requests can
// never oversubscribe the count caps and no issued session is ever born
// over-cap (a "dead" ID handed to the client).
type mcpSessionReservation struct {
	registry   *mcpSessionRegistry
	principal  string
	sessionID  string
	consumed   bool
	released   bool
	rolledBack bool
}

type mcpSessionReservationContextKey struct{}

func withMCPSessionReservation(ctx context.Context, res *mcpSessionReservation) context.Context {
	return context.WithValue(ctx, mcpSessionReservationContextKey{}, res)
}

func mcpSessionReservationFromContext(ctx context.Context) *mcpSessionReservation {
	res, _ := ctx.Value(mcpSessionReservationContextKey{}).(*mcpSessionReservation)
	return res
}

// reserve atomically checks the count caps and records an in-flight
// reservation that counts against them. It returns nil when the caps are
// exhausted. The caller must eventually consume the reservation (via
// Generate) or release it.
func (reg *mcpSessionRegistry) reserve(principal string) *mcpSessionReservation {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.evictExpiredLocked(reg.now())
	if !reg.roomLocked(principal) {
		return nil
	}
	reg.pendingTotal++
	reg.pendingPerPrincipal[principal]++
	return &mcpSessionReservation{registry: reg, principal: principal}
}

// release returns an unconsumed reservation so a request that never reached
// Generate does not permanently hold a slot. It is a no-op after consumption
// and safe against double invocation.
func (res *mcpSessionReservation) release() {
	if res == nil {
		return
	}
	reg := res.registry
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if res.consumed || res.released {
		return
	}
	res.released = true
	reg.retireReservationLocked(res)
}

// rollbackGenerated reclaims the session a consumed reservation produced when
// its Mcp-Session-Id never successfully issued — the transport returned
// (canceled or failed) without writing the session header with a response
// status. Without this, a client that aborts initialize after Generate holds
// a slot it can never use until TTL expiry.
func (res *mcpSessionReservation) rollbackGenerated() {
	if res == nil {
		return
	}
	reg := res.registry
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if !res.consumed || res.rolledBack || res.sessionID == "" {
		return
	}
	res.rolledBack = true
	if record, ok := reg.sessions[res.sessionID]; ok && !record.terminated && record.principal == res.principal {
		record.terminated = true
		delete(reg.sessions, res.sessionID)
	}
}

// retireReservationLocked returns a pending reservation to the pool. The
// caller must hold reg.mu.
func (reg *mcpSessionRegistry) retireReservationLocked(res *mcpSessionReservation) {
	reg.pendingTotal--
	reg.pendingPerPrincipal[res.principal]--
	if reg.pendingPerPrincipal[res.principal] <= 0 {
		delete(reg.pendingPerPrincipal, res.principal)
	}
}

// ResolveSessionIdManager satisfies mcpserver.SessionIdManagerResolver. A nil
// request (sweeper) yields the maintenance manager. A request carries the
// guard's session reservation (if any) through its context.
func (reg *mcpSessionRegistry) ResolveSessionIdManager(r *http.Request) mcpserver.SessionIdManager {
	if r == nil {
		return mcpSessionManager{registry: reg, maintenance: true}
	}
	return mcpSessionManager{
		registry:    reg,
		principal:   mcpSessionFingerprint(r),
		reservation: mcpSessionReservationFromContext(r.Context()),
	}
}

// mcpSessionFingerprint binds a session to the verified principal AND the
// credential/grant security context that authenticated the request: a session
// issued under one grant must not validate for the same subject holding a
// different grant, a rotated credential, or another tenant. For verified
// principals the fingerprint includes a digest of the Authorization
// credential itself, so two distinct valid credentials sharing one grant can
// never share a session. Bearer-only deployments already digest the
// credential inside the admission key. The raw credential is never retained
// or logged.
func mcpSessionFingerprint(r *http.Request) string {
	key := mcpAdmissionKey(r)
	principal, ok := principalFromContext(r.Context())
	if !ok {
		return key
	}
	credential := sha256.Sum256([]byte(strings.TrimSpace(r.Header.Get("Authorization"))))
	return "session:" + mcpLengthDelimited(
		key,
		principal.AuthMethod,
		principal.GrantDigest,
		strconv.FormatInt(principal.GrantVersion, 10),
		hex.EncodeToString(credential[:]),
	)
}

// mcpSessionManager is a principal-scoped view over the registry. It must
// satisfy the upstream SessionIdManager interface; the assertion lives in
// tests to keep this file free of the mcp-go import cycle concerns.
type mcpSessionManager struct {
	registry    *mcpSessionRegistry
	principal   string
	maintenance bool
	reservation *mcpSessionReservation
}

// canCreate reports whether a new session may be issued to the principal,
// counting live sessions plus in-flight reservations.
func (reg *mcpSessionRegistry) canCreate(principal string) bool {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.evictExpiredLocked(reg.now())
	return reg.roomLocked(principal)
}

// roomLocked reports whether one more session for principal fits the count
// caps, counting live sessions plus pending reservations. The caller must
// hold reg.mu.
func (reg *mcpSessionRegistry) roomLocked(principal string) bool {
	per, total := 0, 0
	for _, record := range reg.sessions {
		if record.terminated {
			continue
		}
		total++
		if record.principal == principal {
			per++
		}
	}
	total += reg.pendingTotal
	per += reg.pendingPerPrincipal[principal]
	return total < reg.limits.Total && per < reg.limits.PerPrincipal
}

// Generate issues a fresh session bound to the manager's principal. When the
// guard reserved a slot for this request the reservation is consumed under
// the registry lock. Without a reservation (sweeper or unguarded callers)
// the caps are enforced atomically with creation: Generate either issues a
// session that fits the caps or refuses without creating anything, so no
// client can ever receive an immediately-invalid session ID.
func (m mcpSessionManager) Generate() string {
	reg := m.registry
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.evictExpiredLocked(reg.now())
	principal := m.principal
	if res := m.reservation; res != nil && res.registry == reg && !res.consumed && !res.released {
		res.consumed = true
		reg.retireReservationLocked(res)
		principal = res.principal
	} else if !reg.roomLocked(principal) {
		return ""
	}
	id := uuid.NewString()
	reg.sessions[id] = &mcpSessionRecord{principal: principal, createdAt: reg.now(), lastActive: reg.now()}
	if res := m.reservation; res != nil && res.consumed && res.sessionID == "" {
		res.sessionID = id
	}
	return id
}

// Validate enforces existence, binding, and both TTLs. A cross-principal or
// unknown session yields a constant error; expired sessions report
// terminated. All error text is fixed and redacted.
func (m mcpSessionManager) Validate(sessionID string) (bool, error) {
	if sessionID == "" {
		return false, errMCPSessionUnavailable
	}
	reg := m.registry
	reg.mu.Lock()
	defer reg.mu.Unlock()
	now := reg.now()
	record, ok := reg.sessions[sessionID]
	if !ok || record.terminated {
		return false, errMCPSessionUnavailable
	}
	if !m.maintenance && record.principal != m.principal {
		return false, errMCPSessionUnavailable
	}
	if now.Sub(record.createdAt) > reg.limits.AbsoluteTTL {
		record.terminated = true
		delete(reg.sessions, sessionID)
		return true, nil
	}
	if reg.limits.IdleTTL > 0 && now.Sub(record.lastActive) > reg.limits.IdleTTL {
		record.terminated = true
		delete(reg.sessions, sessionID)
		return true, nil
	}
	record.lastActive = now
	return false, nil
}

// Terminate removes the session. Principal-scoped managers may only terminate
// their own sessions; the maintenance manager terminates anything (sweeper).
func (m mcpSessionManager) Terminate(sessionID string) (bool, error) {
	if sessionID == "" {
		return false, errMCPSessionUnavailable
	}
	reg := m.registry
	reg.mu.Lock()
	defer reg.mu.Unlock()
	record, ok := reg.sessions[sessionID]
	if !ok || record.terminated {
		return false, errMCPSessionUnavailable
	}
	if !m.maintenance && record.principal != m.principal {
		return false, errMCPSessionUnavailable
	}
	record.terminated = true
	delete(reg.sessions, sessionID)
	return false, nil
}

func (reg *mcpSessionRegistry) evictExpiredLocked(now time.Time) {
	for id, record := range reg.sessions {
		if now.Sub(record.createdAt) > reg.limits.AbsoluteTTL ||
			(reg.limits.IdleTTL > 0 && now.Sub(record.lastActive) > reg.limits.IdleTTL) {
			delete(reg.sessions, id)
		}
	}
}

// --- the guard middleware ----------------------------------------------------

type mcpGuard struct {
	admission      *mcpAdmission
	sessions       *mcpSessionRegistry
	registeredTool func(string) bool
}

func newMCPGuard(admission *mcpAdmission, sessions *mcpSessionRegistry) *mcpGuard {
	return newMCPGuardWithTools(admission, sessions, nil)
}

// newMCPGuardWithTools wires the live tool registry into body classification
// so the large/heavy classes are only granted to tools actually registered on
// the MCP server. A nil lookup registers nothing: every tool name stays
// ordinary.
func newMCPGuardWithTools(admission *mcpAdmission, sessions *mcpSessionRegistry, registeredTool func(string) bool) *mcpGuard {
	return &mcpGuard{admission: admission, sessions: sessions, registeredTool: registeredTool}
}

// wrap installs the hardening chain around the transport. It must run inside
// authentication so the principal (or bearer digest) is already resolved.
func (g *mcpGuard) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Compressed request bodies are rejected before any decode
		// (REQ-DOS-003): identity is the only accepted encoding.
		if rejected := rejectContentEncoding(w, r); rejected {
			return
		}

		key := mcpAdmissionKey(r)
		var (
			class       mcpRequestClass
			body        []byte
			preCost     = mcpMinAdmissionCost
			reservation *mcpSessionReservation
		)

		if r.Method == http.MethodPost {
			// Fast path: a declared Content-Length beyond the absolute cap
			// is rejected before reading the stream.
			if r.ContentLength > mcpAbsoluteBodyCap {
				writeMCPTooLarge(w, mcpAbsoluteBodyCap)
				return
			}
			if r.ContentLength > mcpMinAdmissionCost {
				preCost = int(r.ContentLength)
			}
		}

		handle := g.admission.acquire(key, preCost)
		if handle == nil {
			writeMCPRateLimited(w)
			return
		}
		defer handle.release()

		if r.Method == http.MethodPost {
			data, ok := readBoundedBody(w, r, handle, preCost, mcpAbsoluteBodyCap)
			if !ok {
				return
			}
			body = data
			class = classifyMCPEncodedBody(body, g.registeredTool)
			if len(body) > class.MethodCap {
				writeMCPTooLarge(w, class.MethodCap)
				return
			}
			if class.Initialize {
				// Session slots are reserved atomically with the count-cap
				// check: the reservation itself (not a separate canCreate
				// peek) is the admission decision, and Generate consumes it
				// under the same registry lock. Concurrent initialize
				// requests can therefore never oversubscribe the caps, and
				// the reservation is returned when a request dies before
				// Generate runs.
				reservation = g.sessions.reserve(mcpSessionFingerprint(r))
				if reservation == nil {
					writeMCPErrorWithRetry(w, http.StatusTooManyRequests, "session_limit_exceeded", "session limit exceeded for principal")
					return
				}
				defer reservation.release()
				r = r.WithContext(withMCPSessionReservation(r.Context(), reservation))
			}
			if class.Heavy && !handle.becomeHeavy() {
				writeMCPRateLimited(w)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}

		// Cancellation: the deferred release above returns admission bytes
		// and the heavy slot when the transport returns, including on
		// client disconnect, and the guard itself spawns no goroutines.
		//
		// For initialize, the response is observed so a session whose ID
		// never issued — the transport returned, aborted, or panicked
		// without writing the Mcp-Session-Id header with a response
		// status — is rolled back rather than holding a slot until TTL
		// expiry. The rollback is deferred so panic unwinding drains it
		// too, and it never recovers: the panic keeps propagating
		// unchanged. Flush support is only advertised when the underlying
		// writer has it, so downstream Flush probes stay truthful.
		writer := w
		var observer *mcpResponseObserver
		if class.Initialize {
			observer = &mcpResponseObserver{ResponseWriter: w}
			writer = observer
			if flusher, ok := w.(http.Flusher); ok {
				flushable := &mcpFlushObserver{flusher: flusher}
				flushable.ResponseWriter = w
				writer = flushable
				observer = &flushable.mcpResponseObserver
			}
			defer func() {
				if !observer.sessionIssued {
					reservation.rollbackGenerated()
				}
			}()
		}
		next.ServeHTTP(writer, r)
	})
}

// mcpResponseObserver records whether an Mcp-Session-Id header was issued
// together with a response status line. Go writes headers to the wire at
// WriteHeader (or the first Write); a header set afterwards never reaches
// the client. A session ID therefore counts as issued only when the header
// was present at that moment. This type deliberately does NOT implement
// http.Flusher: downstream code (mcp-go) probes for Flush support, and a
// wrapper that always provided Flush would falsely advertise streaming
// support for writers without it — use mcpFlushObserver for that.
type mcpResponseObserver struct {
	http.ResponseWriter
	sessionIssued bool
	wroteHeader   bool
}

// noteWrite marks the implicit or explicit response header write. The
// session header must already be present for the ID to count as issued.
func (o *mcpResponseObserver) noteWrite() {
	if o.wroteHeader {
		return
	}
	o.wroteHeader = true
	if o.Header().Get(mcpSessionIDHeader) != "" {
		o.sessionIssued = true
	}
}

func (o *mcpResponseObserver) WriteHeader(status int) {
	o.noteWrite()
	o.ResponseWriter.WriteHeader(status)
}

func (o *mcpResponseObserver) Write(p []byte) (int, error) {
	o.noteWrite()
	return o.ResponseWriter.Write(p)
}

// mcpFlushObserver adds Flush support only for underlying writers that have
// it. The session header is marked issued BEFORE the flush: net/http sends
// buffered headers when flushing, so a session header present at flush time
// has already reached the wire.
type mcpFlushObserver struct {
	mcpResponseObserver
	flusher http.Flusher
}

func (o *mcpFlushObserver) Flush() {
	o.noteWrite()
	o.flusher.Flush()
}

// rejectContentEncoding refuses any body compression on /mcp (REQ-DOS-003).
// Header.Get only inspects the first field value, so every field value and
// every comma-separated token is validated: "identity" is the single accepted
// encoding anywhere in the header. Explicit empty encodings — an empty field
// value or an empty list element — are malformed, not equivalent to absence,
// and are rejected just like unknown codecs.
func rejectContentEncoding(w http.ResponseWriter, r *http.Request) bool {
	for _, value := range r.Header.Values("Content-Encoding") {
		for _, token := range strings.Split(value, ",") {
			token = strings.TrimSpace(strings.ToLower(token))
			if token == "identity" {
				continue
			}
			writeMCPError(w, http.StatusUnsupportedMediaType, "unsupported_content_encoding", "compressed request bodies are not supported")
			return true
		}
	}
	return false
}

// readBoundedBody reads at most cap+1 bytes from the actual stream, enforcing
// the cap even without Content-Length, and rewrites the request to the bounded
// buffer. Every retained byte is charged to the admission handle BEFORE it is
// buffered, so chunked/unknown-length bodies are denied mid-read (429) instead
// of buffering for free; the precharge (declared Content-Length or the floor)
// is credited so honest known-length requests are not double-charged.
// Over-cap requests never reach the JSON-RPC parser.
func readBoundedBody(w http.ResponseWriter, r *http.Request, handle *mcpAdmissionHandle, preCost, cap int) ([]byte, bool) {
	var data []byte
	chunk := make([]byte, 64<<10)
	charged := preCost
	for {
		n, err := r.Body.Read(chunk)
		if n > 0 {
			if delta := len(data) + n - charged; delta > 0 {
				if !handle.grow(delta) {
					writeMCPRateLimited(w)
					return nil, false
				}
				charged += delta
			}
			data = append(data, chunk[:n]...)
			if len(data) > cap {
				writeMCPTooLarge(w, cap)
				return nil, false
			}
		}
		if err == io.EOF {
			return data, true
		}
		if err != nil {
			writeMCPError(w, http.StatusBadRequest, "invalid_request", "request body could not be read")
			return nil, false
		}
	}
}

func writeMCPTooLarge(w http.ResponseWriter, cap int) {
	writeMCPError(w, http.StatusRequestEntityTooLarge, "payload_too_large", fmt.Sprintf("encoded request body exceeds the %d byte transport cap", cap))
}

func writeMCPRateLimited(w http.ResponseWriter) {
	writeMCPErrorWithRetry(w, http.StatusTooManyRequests, "inflight_limit_exceeded", "inflight request budget exhausted")
}

func writeMCPError(w http.ResponseWriter, status int, code, message string) {
	writeError(w, status, code, message)
}

func writeMCPErrorWithRetry(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Retry-After", fmt.Sprintf("%d", mcpRetryAfterSeconds))
	writeError(w, status, code, message)
}

// --- redacting transport logger ----------------------------------------------

var (
	// redactAuthorizationRe redacts the COMPLETE Authorization value in every
	// form credentials appear in transport logs: plain ("Authorization:
	// Bearer x"), separator variants (":", "="), quoted values
	// ("Authorization: \"Bearer x\""), and the bracketed Go header-map
	// rendering ("map[Authorization:[Bearer x]]"). Each value form is a
	// standalone alternative — a shared required tail would force the engine
	// to fall back to the bare-token path whenever nothing follows a quoted
	// or bracketed value, leaking the credential suffix. The ":"/"="
	// separator is REQUIRED so prose like "the authorization workflow
	// completed" or "the Authorization header" is never over-redacted.
	redactAuthorizationRe = regexp.MustCompile(`(?i)(authorization[ \t]*[:=][ \t]*)(?:"[^"]*"|\[[^\]]*\]|(?:bearer|basic|digest|token|apikey)[ \t]*[^\s,;\]]*|[^\s,;\]]+)`)
	redactDriverStartRe   = regexp.MustCompile(`(?i)\b(?:pq|pgx|sqlite3?|sql|driver)\s*:`)
	// redactSQLStateRe matches every SQLSTATE spelling that carries database
	// error detail: "SQLSTATE 23505", "SQLSTATE: 23505", "SQLSTATE=23505",
	// "SQL state: 40001", "sql-state=42P01", "sql_state 2BP01". Ordinary
	// wording like "sql statement: ..." does not match because the code
	// element must be exactly five alphanumeric characters.
	redactSQLStateRe = regexp.MustCompile(`(?i)\bsql[\s_-]*state\b\s*[:=]?\s*[0-9a-z]{5}\b`)
	redactUUIDRe     = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
)

// redactTransportText scrubs Authorization material, SQL/driver fragments,
// and UUID-shaped session/request identifiers from transport log text. When
// any driver/database error signature is present, everything from that marker
// onward is replaced with one fixed token: redacting only the marker still
// leaks schema, constraint, host, and query detail from the remainder of the
// driver error. Standalone PostgreSQL detail — an SQLSTATE signature without
// any pq/pgx/sql/driver prefix — has no trustworthy prefix to truncate at, so
// the entire text collapses to the same fixed token.
func redactTransportText(text string) string {
	text = redactAuthorizationRe.ReplaceAllString(text, "${1}<redacted>")
	if redactSQLStateRe.MatchString(text) {
		return "<db-redacted>"
	}
	if loc := redactDriverStartRe.FindStringIndex(text); loc != nil {
		return text[:loc[0]] + "<db-redacted>"
	}
	return redactUUIDRe.ReplaceAllString(text, "<session-redacted>")
}

// redactingLogger adapts util.Logger so the mcp-go transport never logs
// credentials, driver errors, or session internals in clear text.
type redactingLogger struct {
	next util.Logger
}

func (l redactingLogger) Infof(format string, v ...any) {
	l.next.Infof("%s", redactTransportText(fmt.Sprintf(format, v...)))
}

func (l redactingLogger) Errorf(format string, v ...any) {
	l.next.Errorf("%s", redactTransportText(fmt.Sprintf(format, v...)))
}
