package memorycontract

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/domain"
	"github.com/lleontor705/cortex/internal/transportpolicy"
)

// TestFromErrorRedactsTransportURLDetail: a *url.Error carries the full
// request URL — userinfo and query included. The structured transport message
// must be the CONSTANT text and must never echo any URL detail.
func TestFromErrorRedactsTransportURLDetail(t *testing.T) {
	err := &url.Error{
		Op:  "Post",
		URL: "https://user:super-secret@mem.example.test/mcp?token=canary-token&trace=1",
		Err: errors.New("connection refused"),
	}
	body := FromError(err)
	if body.Error.Code != CodeTransport {
		t.Fatalf("code = %q, want %q", body.Error.Code, CodeTransport)
	}
	if body.Error.Message != msgTransportFailure {
		t.Fatalf("message = %q, want the constant %q", body.Error.Message, msgTransportFailure)
	}
	if !body.Error.Retryable {
		t.Fatalf("transport failures must be retryable: %+v", body.Error)
	}
	for _, canary := range []string{"super-secret", "canary-token", "user:", "mem.example.test", "trace=1"} {
		if strings.Contains(body.Error.Message, canary) {
			t.Fatalf("transport message leaked URL detail %q: %q", canary, body.Error.Message)
		}
	}
}

// TestFromErrorWrappedPersistenceIsNeverTransport: persistence failures are
// routinely wrapped errors (SQL no-rows, constraint violations). They must
// classify as persistence with the CONSTANT message — the old generic Unwrap
// probe would have misclassified them as transport.
func TestFromErrorWrappedPersistenceIsNeverTransport(t *testing.T) {
	constraint := errors.New("UNIQUE constraint failed: handoff_receipts.scope, handoff_receipts.key")
	cases := []struct {
		name string
		err  error
	}{
		{"wrapped_sql_no_rows", fmt.Errorf("sqlite handoff: read receipt: %w", sql.ErrNoRows)},
		{"wrapped_unique_constraint", fmt.Errorf("sqlite handoff: insert receipt: %w", constraint)},
		{"plain_store_error", errors.New("memory store: insert observation: disk I/O error")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body := FromError(tc.err)
			if body.Error.Code != CodePersistence {
				t.Fatalf("code = %q, want %q", body.Error.Code, CodePersistence)
			}
			if body.Error.Message != msgWriteFailed {
				t.Fatalf("message = %q, want the constant %q", body.Error.Message, msgWriteFailed)
			}
			if strings.Contains(body.Error.Message, tc.err.Error()) {
				t.Fatalf("persistence message echoed raw error text: %q", body.Error.Message)
			}
			if !body.Error.Retryable {
				t.Fatalf("persistence failures must be retryable: %+v", body.Error)
			}
		})
	}
}

// TestFromErrorDetectsTypedTransportErrors: transport classification fires
// ONLY on explicit typed matches — *transportpolicy.Error (direct or wrapped)
// and net.Error — never on a generic Unwrap probe.
func TestFromErrorDetectsTypedTransportErrors(t *testing.T) {
	policyErr := &transportpolicy.Error{Code: transportpolicy.CodeSchemeDowngrade, Reason: "redirect would downgrade"}
	netErr := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"policy_direct", policyErr, CodeTransport},
		{"policy_wrapped", fmt.Errorf("remote MCP destination rejected: %w", policyErr), CodeTransport},
		{"net_op_error", netErr, CodeTransport},
		{"net_wrapped", fmt.Errorf("call tool: %w", netErr), CodeTransport},
		{"url_error", &url.Error{Op: "Post", URL: "https://ok.example.test/mcp", Err: errors.New("reset")}, CodeTransport},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body := FromError(tc.err)
			if body.Error.Code != tc.want {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.want)
			}
			if body.Error.Message != msgTransportFailure {
				t.Fatalf("message = %q, want the constant %q", body.Error.Message, msgTransportFailure)
			}
		})
	}
}

// TestFromErrorConstantMessagesAndTimeout: nil, generic errors, and deadlines
// reduce to their constant redacted texts; wrapping never changes the class.
func TestFromErrorConstantMessagesAndTimeout(t *testing.T) {
	if body := FromError(nil); body.Error.Code != CodePersistence || body.Error.Message != msgUnknownFailure {
		t.Fatalf("nil = %+v, want persistence/%q", body.Error, msgUnknownFailure)
	}

	secret := errors.New("boom secret=payload-fragment title=leak")
	body := FromError(secret)
	if body.Error.Code != CodePersistence || body.Error.Message != msgWriteFailed {
		t.Fatalf("generic = %+v, want persistence/%q", body.Error, msgWriteFailed)
	}
	if strings.Contains(body.Error.Message, "payload-fragment") {
		t.Fatalf("persistence message leaked error detail: %q", body.Error.Message)
	}

	if body := FromError(context.DeadlineExceeded); body.Error.Code != CodeTimeout || body.Error.Message != msgTimedOut {
		t.Fatalf("deadline = %+v, want timeout/%q", body.Error, msgTimedOut)
	}
	if body := FromError(fmt.Errorf("outer: %w", context.DeadlineExceeded)); body.Error.Code != CodeTimeout {
		t.Fatalf("wrapped deadline = %+v, want timeout", body.Error)
	}
}

// TestFromErrorHandoffTypedPassThrough: typed handoff errors keep their domain
// code, retryability, and safe pre-redacted message.
func TestFromErrorHandoffTypedPassThrough(t *testing.T) {
	conflict := FromError(fmt.Errorf("execute: %w", domain.ErrHandoffConflict))
	if conflict.Error.Code != CodeConflict || conflict.Error.Retryable {
		t.Fatalf("conflict = %+v, want conflict/non-retryable", conflict.Error)
	}
	if conflict.Error.Message != domain.ErrHandoffConflict.Message {
		t.Fatalf("message = %q, want the domain text %q", conflict.Error.Message, domain.ErrHandoffConflict.Message)
	}

	unavailable := FromError(&domain.HandoffError{Code: domain.HandoffErrorUnavailable, Message: "handoff service unavailable", Retryable: true})
	if unavailable.Error.Code != CodeUnavailable || !unavailable.Error.Retryable {
		t.Fatalf("unavailable = %+v, want unavailable/retryable", unavailable.Error)
	}
}

// TestFromErrorClassFailedIsPersistenceConstantWithoutCause: ClassFailed
// ValidationErrors wrap the REAL persistence cause (raw SQL/driver text). They
// must classify as persistence with the CONSTANT message — neither the
// ValidationError message nor the wrapped Cause may ever surface, direct or
// wrapped, and a secret canary embedded in the cause must never leak.
func TestFromErrorClassFailedIsPersistenceConstantWithoutCause(t *testing.T) {
	const canary = "SQLSTATE=42P01 token=SECRET-canary-9f2 path=C:\\leak\\probe"
	failed := domain.NewFailed(errors.New(canary), "persist failed mid-transaction")

	cases := []struct {
		name string
		err  error
	}{
		{"direct", failed},
		{"wrapped", fmt.Errorf("unitOfWork: participant: %w", failed)},
		{"double_wrapped", fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", failed))},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			body := FromError(tc.err)
			if body.Error.Code != CodePersistence {
				t.Fatalf("code = %q, want %q (ClassFailed is a persistence failure, not a validation)", body.Error.Code, CodePersistence)
			}
			if body.Error.Message != msgWriteFailed {
				t.Fatalf("message = %q, want the constant %q", body.Error.Message, msgWriteFailed)
			}
			if !body.Error.Retryable {
				t.Fatalf("persistence failures must be retryable: %+v", body.Error)
			}
			for _, canaryProbe := range []string{canary, "SECRET-canary-9f2", "persist failed mid-transaction", "SQLSTATE"} {
				if strings.Contains(body.Error.Message, canaryProbe) {
					t.Fatalf("ClassFailed message leaked %q: %q", canaryProbe, body.Error.Message)
				}
			}
		})
	}
}

// TestFromErrorRealValidationsKeepBoundedMessages: only REAL input
// validations surface their message — legacy field validation (empty Code),
// policy rejection, and dedup classification — always bounded.
func TestFromErrorRealValidationsKeepBoundedMessages(t *testing.T) {
	legacy := &domain.ValidationError{Field: "title", Message: "title is required"}
	body := FromError(legacy)
	if body.Error.Code != CodeValidation || !strings.Contains(body.Error.Message, "title is required") {
		t.Fatalf("legacy validation = %+v, want validation code with its message", body.Error)
	}

	rejected := domain.NewRejected("max_links", "too many entity links")
	body = FromError(rejected)
	if body.Error.Code != CodeValidation || !strings.Contains(body.Error.Message, "too many entity links") {
		t.Fatalf("rejected = %+v, want validation code with its rule message", body.Error)
	}
	if strings.Contains(body.Error.Message, "rejected:") {
		t.Fatalf("rejected message should be the sanitized text, got %q", body.Error.Message)
	}

	dedup := domain.NewDedupSkipped("duplicate observation skipped (normalized_hash match)")
	body = FromError(dedup)
	if body.Error.Code != CodeValidation || !strings.Contains(body.Error.Message, "duplicate observation skipped") {
		t.Fatalf("dedup classification = %+v, want validation code with its constant message", body.Error)
	}

	bounded := FromError(&domain.ValidationError{Field: "content", Message: strings.Repeat("v", MaxErrorMessageLength+80)})
	if !strings.HasSuffix(bounded.Error.Message, "…[truncated]") {
		t.Fatalf("long validation message = %q, want truncation marker", bounded.Error.Message)
	}
}

// TestValidationfBoundsMessage: formatted validation messages are bounded to
// MaxErrorMessageLength runes with an explicit truncation marker.
func TestValidationfBoundsMessage(t *testing.T) {
	body := Validationf("%s", strings.Repeat("a", MaxErrorMessageLength+50))
	runes := []rune(body.Error.Message)
	wantRunes := MaxErrorMessageLength + len([]rune("…[truncated]"))
	if len(runes) != wantRunes {
		t.Fatalf("bounded message length = %d runes, want %d", len(runes), wantRunes)
	}
	if !strings.HasSuffix(body.Error.Message, "…[truncated]") {
		t.Fatalf("message = %q, want truncation marker", body.Error.Message)
	}
}
