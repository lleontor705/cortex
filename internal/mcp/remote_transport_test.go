package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	clienttransport "github.com/mark3labs/mcp-go/client/transport"
	protocol "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const transportTestToken = "ctx_transport_test_token"

// realRemoteMCP serves a genuine mcp-go Streamable HTTP server behind a bearer
// check, so the Cortex transport is exercised against the real wire protocol.
func realRemoteMCP(t *testing.T, opts ...mcpserver.StreamableHTTPOption) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	core := mcpserver.NewMCPServer("remote-cortex", "9.9.9", mcpserver.WithToolCapabilities(true), mcpserver.WithInstructions("remote protocol"))
	core.AddTool(protocol.NewTool("cortex_echo", protocol.WithString("text", protocol.Required())),
		func(_ context.Context, req protocol.CallToolRequest) (*protocol.CallToolResult, error) {
			return protocol.NewToolResultText("echo:" + req.GetString("text", "")), nil
		})
	handler := mcpserver.NewStreamableHTTPServer(core, opts...)
	var unauthorized atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+transportTestToken {
			unauthorized.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &unauthorized
}

func TestBearerTransportProxiesRealServer(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy bool
		opts   []mcpserver.StreamableHTTPOption
	}{
		{name: "modern negotiation"},
		{name: "legacy stateful session", legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, unauthorized := realRemoteMCP(t, tc.opts...)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			tr := newBearerStreamableTransport(srv.URL+"/mcp", transportTestToken, newBearerHTTPClient())
			var options []mcpclient.ClientOption
			if tc.legacy {
				options = append(options, mcpclient.WithLegacyProtocolOnly()) //nolint:staticcheck // deliberately covers legacy session servers (Mcp-Session-Id, DELETE on close)
			}
			proxy, err := openRemoteProxy(ctx, mcpclient.NewClient(tr, options...))
			if err != nil {
				t.Fatalf("openRemoteProxy: %v", err)
			}
			defer func() { _ = proxy.Close() }()

			tool := proxy.Server.GetTool("cortex_echo")
			if tool == nil {
				t.Fatal("remote tool was not proxied")
			}
			req := protocol.CallToolRequest{}
			req.Params.Name = "cortex_echo"
			req.Params.Arguments = map[string]any{"text": "hola"}
			result, err := tool.Handler(ctx, req)
			if err != nil || result.IsError {
				t.Fatalf("call: err=%v result=%+v", err, result)
			}
			if text := result.Content[0].(protocol.TextContent).Text; text != "echo:hola" {
				t.Fatalf("result = %q", text)
			}
			if tc.legacy && tr.GetSessionId() == "" {
				t.Error("legacy handshake did not record the session id")
			}
			if n := unauthorized.Load(); n != 0 {
				t.Errorf("%d request(s) reached the server without the bearer", n)
			}
		})
	}
}

func TestBearerTransportNeverForwardsTokenAcrossOrigins(t *testing.T) {
	var leaked atomic.Bool
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer attacker.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tr := newBearerStreamableTransport(redirecting.URL+"/mcp", transportTestToken, newBearerHTTPClient())
	_, err := tr.SendRequest(ctx, clienttransport.JSONRPCRequest{JSONRPC: "2.0", ID: protocol.NewRequestId(int64(1)), Method: "tools/list"})
	if err == nil {
		t.Fatal("expected the cross-origin redirect to be refused")
	}
	if err := tr.SendNotification(ctx, protocol.JSONRPCNotification{JSONRPC: "2.0", Notification: protocol.Notification{Method: "notifications/initialized"}}); err == nil {
		t.Fatal("expected the cross-origin redirect to be refused for notifications")
	}
	if leaked.Load() {
		t.Fatal("bearer token was forwarded to a different origin")
	}
}

func TestBearerTransportReportsUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	tr := newBearerStreamableTransport(srv.URL, "wrong", newBearerHTTPClient())
	_, err := tr.SendRequest(context.Background(), clienttransport.JSONRPCRequest{JSONRPC: "2.0", ID: protocol.NewRequestId(int64(1)), Method: "tools/list"})
	if !errors.Is(err, clienttransport.ErrAuthorizationRequired) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want an authorization error mentioning 401", err)
	}
}

func TestBearerTransportReadsSSEResponsesAndNotifications(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"sampling/createMessage\"}\n\n")
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\n")
		_, _ = fmt.Fprint(w, "data: \"result\":{\"ok\":true}}\n\n")
	}))
	defer srv.Close()
	tr := newBearerStreamableTransport(srv.URL, transportTestToken, newBearerHTTPClient())
	var notified atomic.Int32
	tr.SetNotificationHandler(func(protocol.JSONRPCNotification) { notified.Add(1) })

	resp, err := tr.SendRequest(context.Background(), clienttransport.JSONRPCRequest{JSONRPC: "2.0", ID: protocol.NewRequestId(int64(1)), Method: "tools/list"})
	if err != nil {
		t.Fatalf("SendRequest: %v", err)
	}
	if !strings.HasSuffix(resp.ID.String(), ":1") || !strings.Contains(string(resp.Result), `"ok":true`) {
		t.Fatalf("response = id %v result %s", resp.ID, resp.Result)
	}
	if notified.Load() != 1 {
		t.Fatalf("notifications dispatched = %d, want 1", notified.Load())
	}
}

func TestBearerTransportCloseCancelsInFlightRequests(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	tr := newBearerStreamableTransport(srv.URL, transportTestToken, newBearerHTTPClient())
	errCh := make(chan error, 1)
	go func() {
		_, err := tr.SendRequest(context.Background(), clienttransport.JSONRPCRequest{JSONRPC: "2.0", ID: protocol.NewRequestId(int64(1)), Method: "tools/list"})
		errCh <- err
	}()
	time.Sleep(50 * time.Millisecond)
	_ = tr.Close()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("in-flight request succeeded after Close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel the in-flight request")
	}
}
