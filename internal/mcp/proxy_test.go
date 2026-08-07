package mcp

import (
	"context"
	"encoding/json"
	"testing"

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
