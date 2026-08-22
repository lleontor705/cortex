package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/lleontor705/cortex/internal/mcp/memorycontract"
	"github.com/lleontor705/cortex/internal/transportpolicy"
	protocol "github.com/mark3labs/mcp-go/mcp"
)

type fakeRemoteToolClient struct {
	started bool
	closed  bool
	called  protocol.CallToolRequest
}

func (f *fakeRemoteToolClient) Start(context.Context) error {
	f.started = true
	return nil
}

func (f *fakeRemoteToolClient) Initialize(context.Context, protocol.InitializeRequest) (*protocol.InitializeResult, error) {
	return &protocol.InitializeResult{ServerInfo: protocol.Implementation{Name: "remote-cortex", Version: "2.0.0"}, Instructions: "Remote instructions"}, nil
}

func (f *fakeRemoteToolClient) ListTools(context.Context, protocol.ListToolsRequest) (*protocol.ListToolsResult, error) {
	return &protocol.ListToolsResult{Tools: []protocol.Tool{
		protocol.NewToolWithRawSchema("cortex_search", "Search remote memories", json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`)),
	}}, nil
}

func (f *fakeRemoteToolClient) CallTool(_ context.Context, request protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	f.called = request
	return protocol.NewToolResultText("remote result"), nil
}

func (f *fakeRemoteToolClient) Close() error {
	f.closed = true
	return nil
}

func TestRemoteProxyRegistersAndForwardsTools(t *testing.T) {
	remote := &fakeRemoteToolClient{}
	proxy, err := openRemoteProxy(context.Background(), remote)
	if err != nil {
		t.Fatal(err)
	}
	if !remote.started {
		t.Fatal("remote transport was not started")
	}
	tool := proxy.Server.GetTool("cortex_search")
	if tool == nil || tool.Tool.Description != "Search remote memories" {
		t.Fatalf("proxied tool = %+v", tool)
	}
	request := protocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "identity graph"}
	result, err := tool.Handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if remote.called.Params.Name != "cortex_search" || remote.called.GetString("query", "") != "identity graph" {
		t.Fatalf("forwarded request = %+v", remote.called.Params)
	}
	if len(result.Content) != 1 {
		t.Fatalf("forwarded result = %+v", result)
	}
	if err := proxy.Close(); err != nil || !remote.closed {
		t.Fatalf("close error = %v, closed = %v", err, remote.closed)
	}
}

func TestOpenRemoteProxyRequiresTokenEnvironment(t *testing.T) {
	t.Setenv("CORTEX_TEST_REMOTE_TOKEN", "")
	_, err := OpenRemoteProxy(context.Background(), RemoteProxyConfig{URL: "https://example.test/mcp", TokenEnv: "CORTEX_TEST_REMOTE_TOKEN"})
	if err == nil {
		t.Fatal("expected missing token error")
	}
}

// scriptedRemoteToolClient lets a test pin the remote CallTool outcome:
// a transport-style error, or a result passed through verbatim.
type scriptedRemoteToolClient struct {
	fakeRemoteToolClient
	callErr error
	result  *protocol.CallToolResult
}

func (f *scriptedRemoteToolClient) CallTool(_ context.Context, request protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	f.called = request
	if f.callErr != nil {
		return nil, f.callErr
	}
	if f.result != nil {
		return f.result, nil
	}
	return protocol.NewToolResultText("remote result"), nil
}

// proxiedSearchTool wires a scripted remote into the proxy and returns the
// forwarded cortex_search tool handler.
func proxiedSearchTool(t *testing.T, remote remoteToolClient) func(context.Context, protocol.CallToolRequest) (*protocol.CallToolResult, error) {
	t.Helper()
	proxy, err := openRemoteProxy(context.Background(), remote)
	if err != nil {
		t.Fatal(err)
	}
	tool := proxy.Server.GetTool("cortex_search")
	if tool == nil {
		t.Fatal("cortex_search was not proxied")
	}
	return tool.Handler
}

// TestOpenRemoteProxyRejectsInsecureHTTPBeforeReadingToken proves the
// REM-TRANSPORT-001 ordering: the destination gate runs BEFORE the token
// environment is consulted, so a Bearer credential is never read for — and
// never attachable to — a plain-HTTP non-loopback destination.
func TestOpenRemoteProxyRejectsInsecureHTTPBeforeReadingToken(t *testing.T) {
	t.Setenv("CORTEX_TEST_REMOTE_TOKEN", "a-very-real-token")
	_, err := OpenRemoteProxy(context.Background(), RemoteProxyConfig{
		URL: "http://remote.example.test/mcp", TokenEnv: "CORTEX_TEST_REMOTE_TOKEN",
	})
	if err == nil {
		t.Fatal("expected plain-HTTP non-loopback destination to be rejected")
	}
	if !strings.Contains(err.Error(), "destination rejected") {
		t.Fatalf("error = %v, want transport-policy destination rejection (before any token handling)", err)
	}
}

// TestOpenRemoteProxyAcceptsHTTPSAndLoopbackUntilTokenStage proves HTTPS and
// strict-loopback HTTP destinations clear the transport gate: with an empty
// token environment the failure is the token-stage error, not a policy error.
func TestOpenRemoteProxyAcceptsHTTPSAndLoopbackUntilTokenStage(t *testing.T) {
	t.Setenv("CORTEX_TEST_REMOTE_TOKEN", "")
	for _, destination := range []string{
		"https://example.test/mcp",
		"https://127.0.0.1:9443/mcp",
		"http://127.0.0.1:8080/mcp",
		"http://localhost/mcp",
	} {
		_, err := OpenRemoteProxy(context.Background(), RemoteProxyConfig{URL: destination, TokenEnv: "CORTEX_TEST_REMOTE_TOKEN"})
		if err == nil || !strings.Contains(err.Error(), "token environment variable") {
			t.Errorf("destination %s: error = %v, want token-stage failure proving the gate accepted it", destination, err)
		}
	}
	// The gate still rejects non-loopback HTTP even before that token stage.
	t.Setenv("CORTEX_TEST_REMOTE_TOKEN", "")
	if _, err := OpenRemoteProxy(context.Background(), RemoteProxyConfig{URL: "http://example.test/mcp", TokenEnv: "CORTEX_TEST_REMOTE_TOKEN"}); err == nil || strings.Contains(err.Error(), "token environment variable") {
		t.Errorf("non-loopback HTTP: error = %v, want destination rejection, not a token failure", err)
	}
}

// TestNewBearerHTTPClientAppliesTransportPolicyRedirects pins the redirect
// policy on the client every Bearer request uses: no scheme downgrade, no
// cross-origin forwarding, loopback-only plain HTTP.
func TestNewBearerHTTPClientAppliesTransportPolicyRedirects(t *testing.T) {
	client := newBearerHTTPClient()
	if client == nil || client.CheckRedirect == nil {
		t.Fatal("bearer HTTP client must install a redirect policy")
	}

	mustRequest := func(raw string) *http.Request {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{Method: http.MethodGet, URL: parsed}
	}

	cases := []struct {
		name    string
		via     string
		target  string
		wantNil bool
		wantCod string
	}{
		{"https_same_origin_allowed", "https://example.test/mcp", "https://example.test/mcp/session", true, ""},
		{"https_to_http_is_downgrade", "https://example.test/mcp", "http://example.test/mcp", false, transportpolicy.CodeSchemeDowngrade},
		{"https_cross_origin_blocked", "https://example.test/mcp", "https://evil.example.net/mcp", false, transportpolicy.CodeOriginChange},
		{"http_loopback_same_origin_allowed", "http://127.0.0.1:8080/mcp", "http://127.0.0.1:8080/other", true, ""},
		{"http_loopback_to_nonloopback_blocked", "http://127.0.0.1:8080/mcp", "http://10.0.0.5/mcp", false, transportpolicy.CodeInsecureScheme},
		{"non_http_scheme_blocked", "https://example.test/mcp", "ftp://example.net/x", false, transportpolicy.CodeUnsupportedScheme},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := client.CheckRedirect(mustRequest(tc.target), []*http.Request{mustRequest(tc.via)})
			if tc.wantNil {
				if err != nil {
					t.Fatalf("redirect %s -> %s rejected: %v", tc.via, tc.target, err)
				}
				return
			}
			var policy *transportpolicy.Error
			if !errors.As(err, &policy) || policy.Code != tc.wantCod {
				t.Fatalf("redirect %s -> %s error = %v, want policy code %s", tc.via, tc.target, err, tc.wantCod)
			}
		})
	}
}

// TestRemoteProxyClassifiesTransportFailuresAsStructuredErrors: when the
// remote transport fails and no MCP result exists, the proxy fabricates no
// success — it classifies the failure into the shared error contract with
// isError=true and never echoes a tool payload.
func TestRemoteProxyClassifiesTransportFailuresAsStructuredErrors(t *testing.T) {
	handler := proxiedSearchTool(t, &scriptedRemoteToolClient{
		callErr: &url.Error{Op: "Post", URL: "https://example.test/mcp", Err: errors.New("connection refused")},
	})

	request := protocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "anything"}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("transport failure must surface as isError, got %+v", result)
	}
	payload, ok := result.StructuredContent.(memorycontract.ErrorStructured)
	if !ok {
		t.Fatalf("structuredContent = %#v, want memorycontract.ErrorStructured", result.StructuredContent)
	}
	if payload.Error.Code != memorycontract.CodeTransport {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeTransport)
	}
	if !payload.Error.Retryable {
		t.Fatalf("transport failures must be retryable: %+v", payload.Error)
	}
	if text := resultText(result); !strings.Contains(text, payload.Error.Message) {
		t.Fatalf("text = %q, want the classified message %q", text, payload.Error.Message)
	}
}

// TestRemoteProxyClassifiesTimeoutsAsTimeoutErrors pins the deadline branch of
// the shared error contract on the proxy path.
func TestRemoteProxyClassifiesTimeoutsAsTimeoutErrors(t *testing.T) {
	handler := proxiedSearchTool(t, &scriptedRemoteToolClient{
		callErr: context.DeadlineExceeded,
	})

	request := protocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "anything"}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := result.StructuredContent.(memorycontract.ErrorStructured)
	if !ok || !result.IsError {
		t.Fatalf("result = %+v, want isError with structured error", result)
	}
	if payload.Error.Code != memorycontract.CodeTimeout {
		t.Fatalf("error code = %q, want %q", payload.Error.Code, memorycontract.CodeTimeout)
	}
}

// TestRemoteProxyPassesThroughRemoteResultsUntouched: a remote result —
// structuredContent, isError, and text — is forwarded verbatim; the proxy
// never rewrites a valid remote result.
func TestRemoteProxyPassesThroughRemoteResultsUntouched(t *testing.T) {
	structured := map[string]any{
		"observation_ref": map[string]any{"local_id": json.Number("7")},
		"status":          "created",
	}
	remoteResult := protocol.NewToolResultStructured(structured, "remote text")
	remoteResult.IsError = false
	handler := proxiedSearchTool(t, &scriptedRemoteToolClient{result: remoteResult})

	request := protocol.CallToolRequest{}
	request.Params.Arguments = map[string]any{"query": "passthrough"}
	result, err := handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("success result must not become an error: %+v", result)
	}
	if got := resultText(result); got != "remote text" {
		t.Fatalf("text = %q, want remote text untouched", got)
	}
	got, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v, want the remote payload untouched", result.StructuredContent)
	}
	ref, _ := got["observation_ref"].(map[string]any)
	if ref == nil || got["status"] != "created" {
		t.Fatalf("structuredContent = %#v, want remote structured payload untouched", got)
	}

	// isError remote results pass through equally untouched (no reclassification).
	remoteError := protocol.NewToolResultStructured(
		memorycontract.ErrorStructured{Error: memorycontract.ErrorBody{Code: memorycontract.CodeConflict, Message: "remote conflict"}},
		"remote failure",
	)
	remoteError.IsError = true
	handler = proxiedSearchTool(t, &scriptedRemoteToolClient{result: remoteError})
	result, err = handler(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("remote isError must stay isError: %+v", result)
	}
	payload, ok := result.StructuredContent.(memorycontract.ErrorStructured)
	if !ok || payload.Error.Code != memorycontract.CodeConflict {
		t.Fatalf("structuredContent = %#v, want the remote error body untouched", result.StructuredContent)
	}
}
